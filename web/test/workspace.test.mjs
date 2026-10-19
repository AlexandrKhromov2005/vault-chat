import { test } from 'node:test';
import assert from 'node:assert/strict';

import { ApiError } from '../static/app/http.mjs';
import { Workspace } from '../static/app/workspace.mjs';
import { deferred } from './fakes.mjs';
import { loadPage } from './minidom.mjs';
import { click, scriptedApi, signedIn } from './ui.mjs';

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
