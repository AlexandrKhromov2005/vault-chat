import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  Attachments,
  FileInfos,
  MAX_ATTACHMENTS,
  MAX_FILE_BYTES,
  downloadFile,
  fileProblem,
  formatSize,
  uploadFile,
} from '../static/app/files.mjs';
import { ApiError } from '../static/app/http.mjs';

// fakeApi answers calls with handler(method, path) and records them.
function fakeApi(handler) {
  const calls = [];
  return {
    calls,
    async call(method, path, options) {
      calls.push({ method, path, body: options?.body });
      return handler(method, path);
    },
    async blob(path) {
      calls.push({ method: 'GET', path });
      return handler('GET', path);
    },
  };
}

const info = (id) => ({ file_id: id, filename: `${id}.txt`, size: 5, content_type: 'text/plain; charset=utf-8' });

test('uploadFile sends one multipart field named file', async () => {
  const api = fakeApi(() => info('f-1'));
  const file = new File(['hello'], 'notes.txt', { type: 'text/plain' });

  const uploaded = await uploadFile(api, 'r-1', file);

  assert.equal(uploaded.file_id, 'f-1');
  const { method, path, body } = api.calls[0];
  assert.equal(method, 'POST');
  assert.equal(path, '/api/v1/rooms/r-1/files');
  assert.deepEqual([...body.keys()], ['file']);
  assert.equal(body.get('file').name, 'notes.txt');
  assert.equal(await body.get('file').text(), 'hello');
});

test('fileProblem enforces the Media limits before uploading', () => {
  assert.equal(fileProblem(new File(['x'], 'a.txt')), null);
  assert.equal(fileProblem({ name: 'big.pdf', size: MAX_FILE_BYTES }), null);
  assert.match(fileProblem({ name: 'big.pdf', size: MAX_FILE_BYTES + 1 }), /10 МиБ/);
  assert.match(fileProblem(new File([], 'empty.txt')), /пуст/);
});

test('formatSize uses binary units', () => {
  assert.equal(formatSize(0), '0 Б');
  assert.equal(formatSize(1023), '1023 Б');
  assert.equal(formatSize(1536), '1,5 КиБ');
  assert.equal(formatSize(MAX_FILE_BYTES), '10,0 МиБ');
});

test('FileInfos loads metadata once', async () => {
  const api = fakeApi((_, path) => info(path.split('/').at(-1)));
  const infos = new FileInfos(api);

  const [a, b] = await Promise.all([infos.get('f-1'), infos.get('f-1')]);
  await infos.getAll(['f-1', 'f-2']);

  assert.equal(a.filename, 'f-1.txt');
  assert.equal(b, a);
  assert.deepEqual(api.calls.map((c) => c.path), ['/api/v1/files/f-1', '/api/v1/files/f-2']);
  assert.equal(infos.info('f-2').filename, 'f-2.txt');
});

test('a file that is no longer accessible is marked unavailable', async () => {
  // Removed members lose access to the room's files: Media answers 404.
  const api = fakeApi(() => {
    throw new ApiError(404, 'not_found', 'file not found');
  });
  const infos = new FileInfos(api);

  assert.deepEqual(await infos.get('f-1'), { file_id: 'f-1', unavailable: true });
});

test('a file that becomes accessible again is asked for again', async () => {
  // Review of #76: a 404 was cached for good, so a member invited back to
  // the room could not download the file until the page was reloaded.
  let member = false;
  const api = fakeApi((_, path) => {
    if (!member) throw new ApiError(404, 'not_found', 'file not found');
    return info(path.split('/').at(-1));
  });
  const infos = new FileInfos(api);
  await infos.get('f-1');
  assert.equal(infos.info('f-1').unavailable, true);

  member = true;

  assert.equal((await infos.get('f-1')).filename, 'f-1.txt');
  assert.equal(infos.info('f-1').filename, 'f-1.txt');
  assert.equal(api.calls.length, 2);
});

test('downloadFile fetches the content with the access token', async () => {
  const api = fakeApi(() => new Blob(['hello']));

  const blob = await downloadFile(api, 'f-1');

  assert.equal(await blob.text(), 'hello');
  assert.deepEqual(api.calls, [{ method: 'GET', path: '/api/v1/files/f-1/content' }]);
});

test('Attachments collects up to five distinct files', () => {
  const attachments = new Attachments();

  for (let i = 1; i <= MAX_ATTACHMENTS; i++) assert.equal(attachments.add(info(`f-${i}`)), true);

  assert.equal(attachments.full, true);
  assert.equal(attachments.add(info('f-6')), false);
  assert.deepEqual(attachments.ids, ['f-1', 'f-2', 'f-3', 'f-4', 'f-5']);
});

test('Attachments ignores duplicates and supports removal', () => {
  const attachments = new Attachments();
  attachments.add(info('f-1'));

  assert.equal(attachments.add(info('f-1')), false);
  attachments.remove('f-1');
  assert.deepEqual(attachments.ids, []);
});

test('removing the sent files keeps the ones attached meanwhile', () => {
  // Review of #63: the answer to a send detached a file attached while the
  // send was under way.
  const attachments = new Attachments();
  attachments.add(info('f-1'));
  attachments.add(info('f-2'));
  const sent = attachments.ids;
  attachments.add(info('f-3'));

  attachments.remove(...sent);

  assert.deepEqual(attachments.items.map((item) => item.file_id), ['f-3']);
});
