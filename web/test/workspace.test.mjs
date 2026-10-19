import { test } from 'node:test';
import assert from 'node:assert/strict';

import { ApiError } from '../static/app/http.mjs';
import { Workspace } from '../static/app/workspace.mjs';
import { deferred } from './fakes.mjs';
import { loadPage, settle } from './minidom.mjs';
import { click, scriptedApi, signedIn, submit, texts, type } from './ui.mjs';

const ROOMS = 'GET /api/v1/rooms?limit=100';
const dialog = {
  room_id: 'r-dialog', kind: 'direct', private: true, created_at: '2026-10-18T10:00:00Z',
  participant_ids: ['u-1', 'u-2'], last_message_at: '2026-10-18T11:00:00Z',
};
const channel = { room_id: 'r-team', kind: 'channel', name: 'Проект', owner_id: 'u-1', private: true, created_at: '2026-10-18T09:00:00Z' };

function setup(api, session = signedIn('u-1', 'alice')) {
  const document = loadPage();
  const workspace = new Workspace({ api, session, searchDelayMs: 0 });
  return { $: (id) => document.getElementById(id), workspace, session };
}

const roomNames = ($) => $('room-list').querySelectorAll('span.room-name').map((name) => name.textContent);

test('lists rooms, naming a dialog after the other participant', async () => {
  const api = scriptedApi()
    .on(ROOMS, { rooms: [dialog, channel], next_cursor: '' })
    .on('GET /api/v1/users/u-2', { user_id: 'u-2', username: 'bob' });
  const { $, workspace } = setup(api);

  await workspace.refresh();

  assert.deepEqual(roomNames($), ['bob', 'Проект']);
  assert.equal($('room').hidden, true);
  assert.equal($('no-room').hidden, false);
});

test('a room list answered after sign-out does not reach the next user', async () => {
  // Review of #62: Alice's late room list replaced Bob's sidebar and showed
  // the name of Alice's private channel.
  const late = deferred();
  const api = scriptedApi().on(ROOMS, () => late.promise);
  const { $, workspace, session } = setup(api);
  const aliceRefresh = workspace.refresh();

  workspace.reset();
  session.user = { id: 'u-2', username: 'bob' };
  api.on(ROOMS, { rooms: [{ ...channel, room_id: 'r-bob', name: 'Команда Боба', owner_id: 'u-2' }], next_cursor: '' });
  await workspace.refresh();
  late.resolve({ rooms: [{ ...channel, name: 'Секрет Алисы' }], next_cursor: '' });
  await aliceRefresh;

  assert.deepEqual(roomNames($), ['Команда Боба']);
});

test('search results answered after sign-out are dropped', async () => {
  const late = deferred();
  const api = scriptedApi().on('GET /api/v1/users?query=bo&limit=20', () => late.promise);
  const { $, workspace } = setup(api);
  await type($('user-search'), 'bo');

  workspace.reset();
  late.resolve({ users: [{ user_id: 'u-2', username: 'bob' }] });
  await settle();

  assert.deepEqual(texts($('user-results')), []);
});

test('picking a found user opens a direct dialog', async () => {
  const api = scriptedApi()
    .on('GET /api/v1/users?query=bo&limit=20', { users: [{ user_id: 'u-2', username: 'bob' }] })
    .on('POST /api/v1/rooms/direct', dialog);
  const { $ } = setup(api);

  await type($('user-search'), 'bo');
  assert.deepEqual(texts($('user-results')), ['bob']);
  await click($('user-results').querySelector('button'));

  assert.deepEqual(api.calls.at(-1), { key: 'POST /api/v1/rooms/direct', body: { peer_user_id: 'u-2' } });
  assert.deepEqual(roomNames($), ['bob']);
  assert.equal($('room').hidden, false);
  assert.equal($('room-title').textContent, 'bob');
  assert.equal($('room-kind').textContent, 'личный диалог');
  assert.equal($('user-search').value, '');
  assert.deepEqual(texts($('user-results')), []);
});

test('creates a channel and opens it', async () => {
  const api = scriptedApi().on('POST /api/v1/rooms/channels', { ...channel, name: 'Новости', room_id: 'r-news' });
  const { $ } = setup(api);
  const form = $('channel-form');
  form.closest('details').open = true;
  form.elements.name.value = '  Новости  ';

  await submit(form);

  assert.deepEqual(api.calls[0].body, { name: 'Новости', private: true });
  assert.equal($('room-title').textContent, 'Новости');
  assert.equal($('room-kind').textContent, 'закрытый канал');
  assert.equal(form.closest('details').open, false);
  assert.equal(form.elements.name.value, '');
});

test('a room that vanished from the list is closed', async () => {
  const api = scriptedApi().on(ROOMS, { rooms: [channel], next_cursor: '' });
  const { $, workspace } = setup(api);
  await workspace.refresh();
  await click($('room-list').querySelector('button'));
  assert.equal($('room').hidden, false);

  api.on(ROOMS, { rooms: [], next_cursor: '' });
  await workspace.refresh();

  assert.equal($('room').hidden, true);
  assert.match($('sidebar-error').textContent, /больше недоступна/);
});

test('a failed refresh is reported', async () => {
  const api = scriptedApi().on(ROOMS, () => {
    throw new ApiError(0, 'network', 'offline');
  });
  const { $, workspace } = setup(api);

  await workspace.refresh();

  assert.match($('sidebar-error').textContent, /Нет связи/);
});

test('reset clears the page', async () => {
  const api = scriptedApi().on(ROOMS, { rooms: [channel], next_cursor: '' });
  const { $, workspace } = setup(api);
  await workspace.refresh();
  await click($('room-list').querySelector('button'));

  workspace.reset();

  assert.deepEqual(roomNames($), []);
  assert.equal($('room').hidden, true);
  assert.equal($('no-room').hidden, false);
});
