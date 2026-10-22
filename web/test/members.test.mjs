import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Workspace } from '../static/app/workspace.mjs';
import { deferred } from './fakes.mjs';
import { loadPage, settle } from './minidom.mjs';
import { click, notFound, scriptedApi, signedIn, texts, type } from './ui.mjs';

const ROOMS = 'GET /api/v1/rooms?limit=100';
const MEMBERS = 'GET /api/v1/rooms/r-team/members?limit=100';
const team = { room_id: 'r-team', kind: 'channel', name: 'Проект', owner_id: 'u-1', private: true, created_at: '2026-10-18T09:00:00Z' };
const other = { ...team, room_id: 'r-other', name: 'Другой' };
const dialog = {
  room_id: 'r-dialog', kind: 'direct', private: true, created_at: '2026-10-18T10:00:00Z', participant_ids: ['u-1', 'u-2'],
};
const members = (...ids) => ({ members: ids.map((id) => ({ user_id: id, joined_at: '2026-10-18T09:00:00Z' })), next_cursor: '' });
const noMessages = { messages: [], next_cursor: '' };

function scripted() {
  return scriptedApi()
    .on(ROOMS, { rooms: [team, other, dialog], next_cursor: '' })
    .on('GET /api/v1/users/u-1', { user_id: 'u-1', username: 'alice' })
    .on('GET /api/v1/users/u-2', { user_id: 'u-2', username: 'bob' })
    .on('GET /api/v1/users/u-3', { user_id: 'u-3', username: 'carol' })
    .on('GET /api/v1/rooms/r-team/messages?limit=50', noMessages)
    .on('GET /api/v1/rooms/r-other/messages?limit=50', noMessages)
    .on('GET /api/v1/rooms/r-dialog/messages?limit=50', noMessages);
}

async function setup(api, { me = 'u-1', confirmAnswer = true } = {}) {
  const document = loadPage({ confirmAnswer });
  const $ = (id) => document.getElementById(id);
  const workspace = new Workspace({ api, session: signedIn(me, 'me'), searchDelayMs: 0 });
  await workspace.refresh();
  return { $, workspace };
}

const openRoom = ($, name) =>
  click($('room-list').querySelectorAll('button').find((button) => button.querySelector('span.room-name').textContent === name));

const memberNames = ($) => $('member-list').children.map((item) => item.querySelector('span').textContent);

test('the owner sees the members with the owner marked and can manage them', async () => {
  const api = scripted().on(MEMBERS, members('u-1', 'u-2'));
  const { $ } = await setup(api);
  await openRoom($, 'Проект');
  assert.equal($('members').hidden, true);

  await click($('toggle-members'));

  assert.equal($('members').hidden, false);
  assert.equal($('toggle-members').getAttribute('aria-pressed'), 'true');
  assert.deepEqual(memberNames($), ['alice (вы)', 'bob']);
  assert.equal($('member-list').children[0].querySelector('span.badge').textContent, 'владелец');
  assert.equal($('member-list').children[0].querySelector('button'), null);
  assert.equal($('member-list').children[1].querySelector('button').textContent, 'Удалить');
  assert.equal($('add-member').hidden, false);
  assert.equal($('leave-room').hidden, true);
});

test('a member who is not the owner can only leave', async () => {
  const api = scripted().on(MEMBERS, members('u-1', 'u-2'));
  const { $ } = await setup(api, { me: 'u-2' });
  await openRoom($, 'Проект');

  await click($('toggle-members'));

  assert.deepEqual(memberNames($), ['alice', 'bob (вы)']);
  assert.equal($('member-list').querySelectorAll('button').length, 0);
  assert.equal($('add-member').hidden, true);
  assert.equal($('leave-room').hidden, false);
});

test('a dialog lists both participants and cannot be left', async () => {
  const api = scripted().on('GET /api/v1/rooms/r-dialog/members?limit=100', members('u-1', 'u-2'));
  const { $ } = await setup(api);
  await openRoom($, 'bob');

  await click($('toggle-members'));

  assert.deepEqual(memberNames($), ['alice (вы)', 'bob']);
  assert.equal($('member-list').querySelectorAll('span.badge').length, 0);
  assert.equal($('add-member').hidden, true);
  assert.equal($('leave-room').hidden, true);
});

test('the panel follows the open room until it is hidden', async () => {
  const api = scripted()
    .on(MEMBERS, members('u-1', 'u-2'))
    .on('GET /api/v1/rooms/r-other/members?limit=100', members('u-1', 'u-3'));
  const { $ } = await setup(api);
  await openRoom($, 'Проект');
  await click($('toggle-members'));

  await openRoom($, 'Другой');
  assert.deepEqual(memberNames($), ['alice (вы)', 'carol']);

  await click($('toggle-members'));
  assert.equal($('members').hidden, true);
  await openRoom($, 'Проект');
  assert.equal(api.calls.filter((call) => call.key === MEMBERS).length, 1);
});

test('the owner adds a found user', async () => {
  const api = scripted()
    .on(MEMBERS, members('u-1'))
    .on('GET /api/v1/users?query=ca&limit=20', { users: [{ user_id: 'u-3', username: 'carol' }] })
    .on('PUT /api/v1/rooms/r-team/members/u-3', { user_id: 'u-3' });
  const { $ } = await setup(api);
  await openRoom($, 'Проект');
  await click($('toggle-members'));

  await type($('add-member-search'), 'ca');
  assert.deepEqual(texts($('add-member-results')), ['carol']);
  api.on(MEMBERS, members('u-1', 'u-3'));
  await click($('add-member-results').querySelector('button'));

  assert.ok(api.calls.some((call) => call.key === 'PUT /api/v1/rooms/r-team/members/u-3'));
  assert.deepEqual(memberNames($), ['alice (вы)', 'carol']);
  assert.equal($('add-member-search').value, '');
  assert.deepEqual(texts($('add-member-results')), []);
});

test('the owner removes a member', async () => {
  const api = scripted()
    .on(MEMBERS, members('u-1', 'u-2'))
    .on('DELETE /api/v1/rooms/r-team/members/u-2', undefined);
  const { $ } = await setup(api);
  await openRoom($, 'Проект');
  await click($('toggle-members'));

  api.on(MEMBERS, members('u-1'));
  await click($('member-list').children[1].querySelector('button'));

  assert.ok(api.calls.some((call) => call.key === 'DELETE /api/v1/rooms/r-team/members/u-2'));
  assert.deepEqual(memberNames($), ['alice (вы)']);
});

test('leaving a channel drops it without an error', async () => {
  const api = scripted()
    .on(MEMBERS, members('u-1', 'u-2'))
    .on('DELETE /api/v1/rooms/r-team/members/me', undefined);
  const { $ } = await setup(api, { me: 'u-2' });
  await openRoom($, 'Проект');
  await click($('toggle-members'));

  await click($('leave-room'));

  assert.ok(api.calls.some((call) => call.key === 'DELETE /api/v1/rooms/r-team/members/me'));
  assert.equal($('room').hidden, true);
  assert.equal($('members').hidden, true);
  assert.equal($('sidebar-error').textContent, '');
  assert.ok(!$('room-list').querySelectorAll('span.room-name').some((name) => name.textContent === 'Проект'));
});

test('leaving waits for confirmation', async () => {
  const api = scripted().on(MEMBERS, members('u-1', 'u-2'));
  const { $ } = await setup(api, { me: 'u-2', confirmAnswer: false });
  await openRoom($, 'Проект');
  await click($('toggle-members'));

  await click($('leave-room'));

  assert.ok(!api.calls.some((call) => call.key.startsWith('DELETE')));
  assert.equal($('room').hidden, false);
});

test('members of a room the user lost access to drop the room', async () => {
  const api = scripted().on(MEMBERS, notFound);
  const { $ } = await setup(api);
  await openRoom($, 'Проект');

  await click($('toggle-members'));

  assert.equal($('room').hidden, true);
  assert.equal($('members').hidden, true);
  assert.match($('sidebar-error').textContent, /больше недоступна/);
});

test('members answered after sign-out are not shown', async () => {
  const late = deferred();
  const api = scripted().on(MEMBERS, () => late.promise);
  const { $, workspace } = await setup(api);
  await openRoom($, 'Проект');
  await click($('toggle-members'));

  workspace.reset();
  late.resolve(members('u-1', 'u-2'));
  await settle();
  await settle();

  assert.deepEqual(memberNames($), []);
  assert.equal($('members').hidden, true);
  // The next user starts with the panel hidden.
  assert.equal($('toggle-members').getAttribute('aria-pressed'), 'false');
});

test('members of a room the user has left for another are not shown', async () => {
  const late = deferred();
  const api = scripted()
    .on(MEMBERS, () => late.promise)
    .on('GET /api/v1/rooms/r-other/members?limit=100', members('u-1', 'u-3'));
  const { $ } = await setup(api);
  await openRoom($, 'Проект');
  await click($('toggle-members'));

  await openRoom($, 'Другой');
  late.resolve(members('u-1', 'u-2'));
  await settle();
  await settle();

  assert.deepEqual(memberNames($), ['alice (вы)', 'carol']);
  assert.equal($('add-member').hidden, false);
});

test('an added member answered after the owner moved on does not reload the list', async () => {
  const late = deferred();
  const api = scripted()
    .on(MEMBERS, members('u-1'))
    .on('GET /api/v1/rooms/r-other/members?limit=100', members('u-1', 'u-3'))
    .on('GET /api/v1/users?query=bo&limit=20', { users: [{ user_id: 'u-2', username: 'bob' }] })
    .on('PUT /api/v1/rooms/r-team/members/u-2', () => late.promise);
  const { $ } = await setup(api);
  await openRoom($, 'Проект');
  await click($('toggle-members'));
  await type($('add-member-search'), 'bo');
  await click($('add-member-results').querySelector('button'));

  await openRoom($, 'Другой');
  late.resolve({ user_id: 'u-2' });
  await settle();
  await settle();

  assert.deepEqual(memberNames($), ['alice (вы)', 'carol']);
  assert.equal(api.calls.filter((call) => call.key === MEMBERS).length, 1);
  assert.equal(api.calls.filter((call) => call.key.startsWith('GET /api/v1/rooms/r-other/members')).length, 1);
});

test('a late 404 for the previous user does not close the next user\'s room', async () => {
  const late = deferred();
  const api = scripted().on(MEMBERS, () => late.promise.then(notFound));
  const { $, workspace } = await setup(api);
  await openRoom($, 'Проект');
  await click($('toggle-members'));

  workspace.reset();
  await workspace.refresh();
  await openRoom($, 'Проект');
  late.resolve();
  await settle();
  await settle();

  assert.equal($('room').hidden, false);
  assert.equal($('sidebar-error').textContent, '');
});
