import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Workspace } from '../static/app/workspace.mjs';
import { deferred } from './fakes.mjs';
import { loadPage, settle } from './minidom.mjs';
import { click, scriptedApi, signedIn } from './ui.mjs';

const ROOMS = 'GET /api/v1/rooms?limit=100';
const team = { room_id: 'r-team', kind: 'channel', name: 'Проект', owner_id: 'u-1', private: true, created_at: '2026-10-18T09:00:00Z' };
const other = { ...team, room_id: 'r-other', name: 'Другой' };
const news = { ...team, room_id: 'r-news', name: 'Новости', owner_id: 'u-2' };

const msg = (sequence, roomId = 'r-team', sender = 'u-2') => ({
  message_id: `m-${sequence}`, room_id: roomId, sender_id: sender, client_message_id: `c-${sequence}`,
  text: `#${sequence}`, sequence, created_at: `2026-10-18T12:0${sequence}:00Z`,
});
const created = (message) => ({ type: 'message.created', room_id: message.room_id, message });

function scripted() {
  return scriptedApi()
    .on(ROOMS, { rooms: [team, other], next_cursor: '' })
    .on('GET /api/v1/users/u-2', { user_id: 'u-2', username: 'bob' })
    .on('GET /api/v1/rooms/r-team/messages?limit=50', { messages: [msg(1)], next_cursor: '' })
    .on('GET /api/v1/rooms/r-other/messages?limit=50', { messages: [], next_cursor: '' })
    .on('GET /api/v1/rooms/r-team/messages?limit=100&after=1', { messages: [], next_cursor: '' });
}

async function setup(api) {
  const document = loadPage();
  const $ = (id) => document.getElementById(id);
  const workspace = new Workspace({ api, session: signedIn('u-1', 'alice'), searchDelayMs: 0 });
  await workspace.refresh();
  return { $, workspace };
}

const openRoom = ($, name) =>
  click($('room-list').querySelectorAll('button').find((button) => button.querySelector('span.room-name').textContent === name));
const rooms = ($) => $('room-list').querySelectorAll('button').map((button) =>
  [button.querySelector('span.room-name').textContent, button.className.includes('unread')]);
const shown = ($) => $('message-list').querySelectorAll('p.message-text').map((text) => text.textContent);

test('a live message appears in the open room', async () => {
  const api = scripted();
  const { $, workspace } = await setup(api);
  await openRoom($, 'Проект');

  await workspace.handleEvent(created(msg(2)));

  assert.deepEqual(shown($), ['#1', '#2']);
  assert.deepEqual(rooms($), [['Проект', false], ['Другой', false]]);
});

test('a live message in another room moves it up and marks it until opened', async () => {
  const api = scripted();
  const { $, workspace } = await setup(api);
  await openRoom($, 'Проект');

  await workspace.handleEvent(created(msg(1, 'r-other')));
  assert.deepEqual(rooms($), [['Другой', true], ['Проект', false]]);

  await openRoom($, 'Другой');
  assert.deepEqual(rooms($), [['Другой', false], ['Проект', false]]);
});

test('the user\'s own message from another tab does not mark the room', async () => {
  const api = scripted();
  const { $, workspace } = await setup(api);
  await openRoom($, 'Проект');

  await workspace.handleEvent(created(msg(1, 'r-other', 'u-1')));

  assert.deepEqual(rooms($), [['Другой', false], ['Проект', false]]);
});

test('a message after a lost event fetches what is missing', async () => {
  const api = scripted().on('GET /api/v1/rooms/r-team/messages?limit=100&after=1', { messages: [msg(2), msg(3)], next_cursor: '' });
  const { $, workspace } = await setup(api);
  await openRoom($, 'Проект');

  await workspace.handleEvent(created(msg(3)));

  assert.deepEqual(shown($), ['#1', '#2', '#3']);
});

test('a message in a room not listed yet reloads the list', async () => {
  const api = scripted();
  const { $, workspace } = await setup(api);
  api.on(ROOMS, { rooms: [news, team, other], next_cursor: '' });

  await workspace.handleEvent(created(msg(1, 'r-news')));

  assert.deepEqual(rooms($)[0], ['Новости', true]);
});

test('being added to a room lists it, marked', async () => {
  const api = scripted().on('GET /api/v1/rooms/r-news', news);
  const { $, workspace } = await setup(api);

  await workspace.handleEvent({ type: 'member.added', room_id: 'r-news', user_id: 'u-1' });

  assert.deepEqual(rooms($)[0], ['Новости', true]);
});

test('being removed closes the room', async () => {
  const api = scripted();
  const { $, workspace } = await setup(api);
  await openRoom($, 'Проект');

  await workspace.handleEvent({ type: 'member.removed', room_id: 'r-team', user_id: 'u-1' });

  assert.equal($('room').hidden, true);
  assert.deepEqual(rooms($), [['Другой', false]]);
  assert.match($('sidebar-error').textContent, /больше недоступна/);
});

test('a member change reloads the open members panel', async () => {
  const members = (...ids) => ({ members: ids.map((id) => ({ user_id: id })), next_cursor: '' });
  const api = scripted()
    .on('GET /api/v1/users/u-1', { user_id: 'u-1', username: 'alice' })
    .on('GET /api/v1/rooms/r-team/members?limit=100', members('u-1'));
  const { $, workspace } = await setup(api);
  await openRoom($, 'Проект');
  await click($('toggle-members'));

  api.on('GET /api/v1/rooms/r-team/members?limit=100', members('u-1', 'u-2'));
  await workspace.handleEvent({ type: 'member.added', room_id: 'r-team', user_id: 'u-2' });

  assert.equal($('member-list').children.length, 2);
});

test('a resync marks rooms that are new or have new messages', async () => {
  const api = scripted();
  const { $, workspace } = await setup(api);
  await openRoom($, 'Проект');
  const active = { last_message_at: '2026-10-18T12:00:00Z' };
  api.on(ROOMS, { rooms: [news, { ...other, ...active }, { ...team, ...active }], next_cursor: '' });

  await workspace.refresh();

  // The open room is being read, so it is not marked.
  assert.deepEqual(rooms($), [['Новости', true], ['Другой', true], ['Проект', false]]);
});

test('a live message handled after sign-out does not move the next user\'s rooms', async () => {
  const late = deferred();
  const api = scripted();
  const { $, workspace } = await setup(api);
  api.on(ROOMS, () => late.promise);
  const event = workspace.handleEvent(created(msg(1, 'r-news')));

  workspace.reset();
  api.on(ROOMS, { rooms: [team, news], next_cursor: '' });
  await workspace.refresh();
  late.resolve({ rooms: [news, team, other], next_cursor: '' });
  await event;

  assert.deepEqual(rooms($), [['Проект', false], ['Новости', false]]);
});

test('an added room answered after sign-out does not reach the next user', async () => {
  const late = deferred();
  const api = scripted().on('GET /api/v1/rooms/r-news', () => late.promise);
  const { $, workspace } = await setup(api);
  const event = workspace.handleEvent({ type: 'member.added', room_id: 'r-news', user_id: 'u-1' });

  workspace.reset();
  await workspace.refresh();
  late.resolve(news);
  await event;
  await settle();

  assert.deepEqual(rooms($), [['Проект', false], ['Другой', false]]);
});
