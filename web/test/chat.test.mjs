import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  Outbox,
  addMember,
  catchUp,
  createChannel,
  getRoom,
  leaveRoom,
  listAllMembers,
  listAllRooms,
  loadLatest,
  loadOlder,
  openDirect,
  removeMember,
  roomTitle,
  sendMessage,
} from '../static/app/chat.mjs';
import { Timeline } from '../static/app/timeline.mjs';

// fakeApi answers calls from a queue and records them.
function fakeApi(responses) {
  const queue = [...responses];
  const calls = [];
  return {
    calls,
    async call(method, path, options) {
      calls.push({ method, path, body: options?.body });
      if (!queue.length) throw new Error(`unexpected ${method} ${path}`);
      return queue.shift();
    },
  };
}

const msg = (sequence) => ({ message_id: `m-${sequence}`, sequence, text: `#${sequence}` });

test('listAllRooms follows the cursor to the end', async () => {
  const api = fakeApi([
    { rooms: [{ room_id: 'r-1' }], next_cursor: 'c-1' },
    { rooms: [{ room_id: 'r-2' }], next_cursor: '' },
  ]);

  const rooms = await listAllRooms(api);

  assert.deepEqual(rooms.map((r) => r.room_id), ['r-1', 'r-2']);
  // An empty cursor is never sent: the Gateway rejects noncanonical cursors.
  assert.deepEqual(api.calls.map((c) => c.path), [
    '/api/v1/rooms?limit=100',
    '/api/v1/rooms?limit=100&cursor=c-1',
  ]);
});

test('listAllMembers follows the cursor to the end', async () => {
  const api = fakeApi([
    { members: [{ user_id: 'u-1' }], next_cursor: 'u-1' },
    { members: [{ user_id: 'u-2' }], next_cursor: '' },
  ]);

  const members = await listAllMembers(api, 'r-1');

  assert.deepEqual(members.map((m) => m.user_id), ['u-1', 'u-2']);
  assert.deepEqual(api.calls.map((c) => c.path), [
    '/api/v1/rooms/r-1/members?limit=100',
    '/api/v1/rooms/r-1/members?limit=100&cursor=u-1',
  ]);
});

test('loadLatest and loadOlder page the history backwards', async () => {
  const api = fakeApi([
    { messages: [msg(4), msg(3)], next_cursor: '3' },
    { messages: [msg(2), msg(1)], next_cursor: '' },
  ]);
  const timeline = new Timeline();

  await loadLatest(api, 'r-1', timeline);
  await loadOlder(api, 'r-1', timeline);

  assert.deepEqual(timeline.messages.map((m) => m.sequence), [1, 2, 3, 4]);
  assert.deepEqual(api.calls.map((c) => c.path), [
    '/api/v1/rooms/r-1/messages?limit=50',
    '/api/v1/rooms/r-1/messages?limit=50&cursor=3',
  ]);
});

test('catchUp fetches everything after the newest message', async () => {
  const timeline = new Timeline();
  timeline.setLatest({ messages: [msg(2), msg(1)], next_cursor: '' });
  const api = fakeApi([
    { messages: [msg(3), msg(4)], next_cursor: '4' },
    { messages: [msg(5)], next_cursor: '' },
  ]);

  const appended = await catchUp(api, 'r-1', timeline);

  assert.equal(appended, 3);
  assert.equal(timeline.newest, 5);
  assert.deepEqual(api.calls.map((c) => c.path), [
    '/api/v1/rooms/r-1/messages?limit=100&after=2',
    '/api/v1/rooms/r-1/messages?limit=100&after=4',
  ]);
});

test('catchUp of an unloaded room starts at the first message', async () => {
  const api = fakeApi([{ messages: [msg(1)], next_cursor: '' }]);
  const timeline = new Timeline();

  await catchUp(api, 'r-1', timeline);

  assert.equal(api.calls[0].path, '/api/v1/rooms/r-1/messages?limit=100&after=0');
  assert.equal(timeline.newest, 1);
});

test('sendMessage posts the text with its client message id', async () => {
  const api = fakeApi([msg(3)]);

  const sent = await sendMessage(api, 'r-1', { clientMessageId: 'c-1', text: 'hi' });

  assert.equal(sent.sequence, 3);
  assert.deepEqual(api.calls[0], {
    method: 'POST',
    path: '/api/v1/rooms/r-1/messages',
    body: { client_message_id: 'c-1', text: 'hi' },
  });
});

test('sendMessage includes attachments only when there are some', async () => {
  const api = fakeApi([msg(3)]);

  await sendMessage(api, 'r-1', { clientMessageId: 'c-1', text: '', attachmentIds: ['f-1'] });

  assert.deepEqual(api.calls[0].body, { client_message_id: 'c-1', text: '', attachment_ids: ['f-1'] });
});

test('Outbox reuses the id for a retry of the same message', () => {
  let n = 0;
  const outbox = new Outbox(() => `id-${++n}`);

  const first = outbox.prepare('hello', []);
  const retry = outbox.prepare('hello', []);
  const edited = outbox.prepare('hello!', []);

  assert.equal(first.clientMessageId, 'id-1');
  assert.equal(retry.clientMessageId, 'id-1');
  assert.equal(edited.clientMessageId, 'id-2');
});

test('Outbox starts a new message after a successful send', () => {
  let n = 0;
  const outbox = new Outbox(() => `id-${++n}`);

  outbox.prepare('hello', []);
  outbox.sent();

  assert.equal(outbox.prepare('hello', []).clientMessageId, 'id-2');
});

test('Outbox treats different attachments as a different message', () => {
  let n = 0;
  const outbox = new Outbox(() => `id-${++n}`);

  outbox.prepare('', ['f-1']);

  assert.equal(outbox.prepare('', ['f-2']).clientMessageId, 'id-2');
});

test('room management calls', async () => {
  const api = fakeApi([{ room_id: 'r-1' }, { room_id: 'r-2' }, null, null, null]);

  await openDirect(api, 'u-2');
  await createChannel(api, 'Team', true);
  await addMember(api, 'r-2', 'u-3');
  await removeMember(api, 'r-2', 'u-3');
  await leaveRoom(api, 'r-2');

  assert.deepEqual(api.calls, [
    { method: 'POST', path: '/api/v1/rooms/direct', body: { peer_user_id: 'u-2' } },
    { method: 'POST', path: '/api/v1/rooms/channels', body: { name: 'Team', private: true } },
    { method: 'PUT', path: '/api/v1/rooms/r-2/members/u-3', body: undefined },
    { method: 'DELETE', path: '/api/v1/rooms/r-2/members/u-3', body: undefined },
    { method: 'DELETE', path: '/api/v1/rooms/r-2/members/me', body: undefined },
  ]);
});

test('path segments are encoded', async () => {
  const api = fakeApi([null]);

  await removeMember(api, 'r/1', 'u?2');

  assert.equal(api.calls[0].path, '/api/v1/rooms/r%2F1/members/u%3F2');
});

test('roomTitle names a dialog after the other participant', () => {
  const profiles = { name: (id) => ({ 'u-2': 'bob' })[id] ?? null };

  assert.equal(roomTitle({ kind: 'channel', name: 'Team' }, 'u-1', profiles), 'Team');
  assert.equal(roomTitle({ kind: 'direct', participant_ids: ['u-1', 'u-2'] }, 'u-1', profiles), 'bob');
  assert.equal(roomTitle({ kind: 'direct', participant_ids: ['u-1', 'u-9'] }, 'u-1', profiles), 'Личный диалог');
});

test('getRoom reads the metadata of one room', async () => {
  const api = fakeApi([{ room_id: 'r-1', kind: 'channel' }]);

  assert.equal((await getRoom(api, 'r-1')).kind, 'channel');
  assert.deepEqual(api.calls[0], { method: 'GET', path: '/api/v1/rooms/r-1', body: undefined });
});
