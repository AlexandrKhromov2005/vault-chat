import { test } from 'node:test';
import assert from 'node:assert/strict';

import { MAX_FILE_BYTES } from '../static/app/files.mjs';
import { ApiError } from '../static/app/http.mjs';
import { Workspace } from '../static/app/workspace.mjs';
import { deferred } from './fakes.mjs';
import { FakeElement, loadPage, settle } from './minidom.mjs';
import { click, scriptedApi, signedIn, submit, texts } from './ui.mjs';

const UPLOAD = 'POST /api/v1/rooms/r-team/files';
const SEND = 'POST /api/v1/rooms/r-team/messages';
const team = { room_id: 'r-team', kind: 'channel', name: 'Проект', owner_id: 'u-1', private: true, created_at: '2026-10-18T09:00:00Z' };
const other = { ...team, room_id: 'r-other', name: 'Другой' };
const noMessages = { messages: [], next_cursor: '' };

const info = (id, filename = `${id}.txt`) => ({ file_id: id, room_id: 'r-team', filename, size: 1536, content_type: 'text/plain; charset=utf-8' });
const message = (sequence, attachmentIds, text = '') => ({
  message_id: `m-${sequence}`, room_id: 'r-team', sender_id: 'u-1', client_message_id: `c-${sequence}`,
  text, attachment_ids: attachmentIds, sequence, created_at: '2026-10-18T12:00:00Z',
});

// uploads answers each upload with the metadata of a file named like the
// uploaded one.
const uploads = (options) => info(`f-${options.body.get('file').name}`, options.body.get('file').name);

function scripted() {
  return scriptedApi()
    .on('GET /api/v1/rooms?limit=100', { rooms: [team, other], next_cursor: '' })
    .on('GET /api/v1/rooms/r-team/messages?limit=50', noMessages)
    .on('GET /api/v1/rooms/r-other/messages?limit=50', noMessages)
    .on(UPLOAD, uploads);
}

async function openTeam(api) {
  const document = loadPage();
  const $ = (id) => document.getElementById(id);
  const workspace = new Workspace({ api, session: signedIn('u-1', 'alice'), searchDelayMs: 0 });
  await workspace.refresh();
  await click($('room-list').querySelectorAll('button')[0]);
  return { $, workspace };
}

// choose picks files in the file dialog.
async function choose($, ...names) {
  const input = $('file-input');
  input.files = names.map((name) => (typeof name === 'string' ? new File(['hello'], name, { type: 'text/plain' }) : name));
  input.dispatchEvent(new Event('change'));
  await settle();
}

const submitButton = ($) => $('composer').querySelector('button[type="submit"]');

test('chosen files are uploaded, sent with the message and shown in it', async () => {
  const api = scripted().on(SEND, (options) => message(1, options.body.attachment_ids, options.body.text));
  const { $ } = await openTeam(api);

  await choose($, 'a.txt', 'b.txt');
  assert.deepEqual(texts($('pending-files')), ['📎 a.txt · 1,5 КиБ×', '📎 b.txt · 1,5 КиБ×']);
  $('message-text').value = 'Файлы';
  await submit($('composer'));

  assert.deepEqual(api.calls.find((call) => call.key === SEND).body.attachment_ids, ['f-a.txt', 'f-b.txt']);
  assert.deepEqual(texts($('pending-files')), []);
  const shown = $('message-list').querySelectorAll('button.attachment').map((button) => button.textContent);
  assert.deepEqual(shown, ['📎 a.txt · 1,5 КиБ', '📎 b.txt · 1,5 КиБ']);
});

test('files alone are sent with empty text', async () => {
  const api = scripted().on(SEND, (options) => message(1, options.body.attachment_ids));
  const { $ } = await openTeam(api);

  await choose($, 'a.txt');
  $('message-text').value = '  ';
  await submit($('composer'));

  assert.equal(api.calls.find((call) => call.key === SEND).body.text, '');
});

test('a file is detached before sending', async () => {
  const api = scripted();
  const { $ } = await openTeam(api);
  await choose($, 'a.txt', 'b.txt');

  await click($('pending-files').querySelector('button'));

  assert.deepEqual(texts($('pending-files')), ['📎 b.txt · 1,5 КиБ×']);
});

test('files over the Media limits are reported, not uploaded', async () => {
  const api = scripted();
  const { $ } = await openTeam(api);
  const big = new File([new Uint8Array(MAX_FILE_BYTES + 1)], 'big.pdf');

  await choose($, big, new File([], 'empty.txt'));

  assert.equal(api.calls.filter((call) => call.key === UPLOAD).length, 0);
  assert.match($('room-error').textContent, /пуст/);
});

test('a failed upload is reported and not attached', async () => {
  const api = scripted().on(UPLOAD, () => {
    throw new ApiError(415, 'unsupported_media_type', 'unsupported');
  });
  const { $ } = await openTeam(api);

  await choose($, 'a.exe');

  assert.match($('room-error').textContent, /^«a\.exe»: /);
  assert.deepEqual(texts($('pending-files')), []);
});

test('a file attached while a send is under way stays in the draft', async () => {
  // Review of #63: the answer to the first send cleared the draft, losing
  // the file attached meanwhile, and the composer allowed a second send.
  const late = deferred();
  const api = scripted().on(SEND, () => late.promise);
  const { $ } = await openTeam(api);
  await choose($, 'one.txt');
  $('message-text').value = 'Первое';
  await submit($('composer'));

  await choose($, 'two.txt');
  assert.equal(submitButton($).disabled, true);
  $('message-text').value = 'Второе';
  $('message-text').dispatchEvent(Object.assign(new Event('keydown', { cancelable: true }), { key: 'Enter' }));
  await settle();
  late.resolve(message(1, ['f-one.txt'], 'Первое'));
  await settle();

  assert.equal(api.calls.filter((call) => call.key === SEND).length, 1);
  assert.deepEqual(texts($('pending-files')), ['📎 two.txt · 1,5 КиБ×']);
  assert.equal($('message-text').value, 'Второе');
  assert.equal(submitButton($).disabled, false);
});

// downloads records object URLs and the links clicked to save them while
// run() runs.
async function downloads(run) {
  const record = { blobs: [], revoked: [], saved: [] };
  const { createObjectURL, revokeObjectURL } = URL;
  const { click: realClick } = FakeElement.prototype;
  URL.createObjectURL = (blob) => `blob:${record.blobs.push(blob)}`;
  URL.revokeObjectURL = (url) => record.revoked.push(url);
  FakeElement.prototype.click = function () {
    if (this.tagName === 'A') record.saved.push([this.getAttribute('href'), this.getAttribute('download')]);
    else realClick.call(this);
  };
  try {
    await run(record);
  } finally {
    Object.assign(URL, { createObjectURL, revokeObjectURL });
    FakeElement.prototype.click = realClick;
  }
}

const withFiles = (api) => api
  .on('GET /api/v1/rooms/r-team/messages?limit=50', { messages: [message(1, ['f-1', 'f-2'])], next_cursor: '' })
  .on('GET /api/v1/files/f-1', info('f-1', 'отчёт.pdf'))
  .on('GET /api/v1/files/f-2', info('f-2', 'план.txt'));

test('an attachment is downloaded under its file name', () => downloads(async ({ blobs, revoked, saved }) => {
  const api = withFiles(scripted())
    .on('GET /api/v1/files/f-1/content', () => new Blob(['pdf']))
    .on('GET /api/v1/files/f-2/content', () => new Blob(['txt']));
  const { $ } = await openTeam(api);
  const [first, second] = $('message-list').querySelectorAll('button.attachment');

  await click(first);
  await click(second);

  assert.deepEqual(saved, [['blob:1', 'отчёт.pdf'], ['blob:2', 'план.txt']]);
  assert.equal(await blobs[0].text(), 'pdf');
  // Only the latest download keeps its content in memory.
  assert.deepEqual(revoked, ['blob:1']);
}));

test('a download answered after sign-out is not saved', () => downloads(async ({ saved }) => {
  const late = deferred();
  const api = withFiles(scripted()).on('GET /api/v1/files/f-1/content', () => late.promise);
  const { $, workspace } = await openTeam(api);
  await click($('message-list').querySelector('button.attachment'));

  workspace.reset();
  late.resolve(new Blob(['pdf']));
  await settle();

  assert.deepEqual(saved, []);
}));

test('Enter does not send while a file is uploading', async () => {
  const late = deferred();
  const api = scripted().on(UPLOAD, () => late.promise);
  const { $ } = await openTeam(api);
  await choose($, 'a.txt');
  assert.equal(submitButton($).disabled, true);

  $('message-text').value = 'С файлом';
  $('message-text').dispatchEvent(Object.assign(new Event('keydown', { cancelable: true }), { key: 'Enter' }));
  await settle();

  assert.equal(api.calls.filter((call) => call.key === SEND).length, 0);
});

test('a file the user can no longer access is marked unavailable', async () => {
  const api = scripted()
    .on('GET /api/v1/rooms/r-team/messages?limit=50', { messages: [message(1, ['f-1'])], next_cursor: '' })
    .on('GET /api/v1/files/f-1', () => {
      throw new ApiError(404, 'not_found', 'file not found');
    });
  const { $ } = await openTeam(api);

  assert.equal($('message-list').querySelector('span.attachment').textContent, 'файл недоступен');
});

test('an upload answered after sign-out does not reach the next user\'s draft', async () => {
  const late = deferred();
  const api = scripted().on(UPLOAD, () => late.promise);
  const { $, workspace } = await openTeam(api);
  await choose($, 'secret.txt', 'more.txt');

  workspace.reset();
  await workspace.refresh();
  await click($('room-list').querySelectorAll('button')[0]);
  late.resolve(info('f-secret', 'secret.txt'));
  await settle();

  assert.deepEqual(texts($('pending-files')), []);
  assert.equal(submitButton($).disabled, false);
  // The rest of the previous user's files is not uploaded with the next
  // user's token.
  assert.equal(api.calls.filter((call) => call.key === UPLOAD).length, 1);
});

test('an upload answered after another room was opened stays with its room', async () => {
  const late = deferred();
  const api = scripted().on(UPLOAD, () => late.promise);
  const { $ } = await openTeam(api);
  await choose($, 'a.txt');

  await click($('room-list').querySelectorAll('button')[1]);
  late.resolve(info('f-a', 'a.txt'));
  await settle();

  assert.equal($('room-title').textContent, 'Другой');
  assert.deepEqual(texts($('pending-files')), []);
  assert.equal(submitButton($).disabled, false);
});
