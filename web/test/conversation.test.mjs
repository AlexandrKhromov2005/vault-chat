import { test } from 'node:test';
import assert from 'node:assert/strict';

import { ApiError } from '../static/app/http.mjs';
import { Workspace } from '../static/app/workspace.mjs';
import { deferred } from './fakes.mjs';
import { loadPage, settle } from './minidom.mjs';
import { click, notFound, scriptedApi, signedIn, submit } from './ui.mjs';

const ROOMS = 'GET /api/v1/rooms?limit=100';
const HISTORY = 'GET /api/v1/rooms/r-team/messages?limit=50';
const SEND = 'POST /api/v1/rooms/r-team/messages';
const team = { room_id: 'r-team', kind: 'channel', name: 'Проект', owner_id: 'u-1', private: true, created_at: '2026-10-18T09:00:00Z' };
const other = { ...team, room_id: 'r-other', name: 'Другой' };

const msg = (sequence, sender = 'u-2', text = `#${sequence}`) => ({
  message_id: `m-${sequence}`, room_id: 'r-team', sender_id: sender, client_message_id: `c-${sequence}`,
  text, sequence, created_at: '2026-10-18T12:00:00Z',
});
const page = (messages, nextCursor = '') => ({ messages: [...messages].reverse(), next_cursor: nextCursor });

function scripted() {
  return scriptedApi()
    .on(ROOMS, { rooms: [team, other], next_cursor: '' })
    .on('GET /api/v1/users/u-2', { user_id: 'u-2', username: 'bob' });
}

async function openTeam(api, session = signedIn('u-1', 'alice')) {
  const document = loadPage();
  const $ = (id) => document.getElementById(id);
  const workspace = new Workspace({ api, session, searchDelayMs: 0 });
  await workspace.refresh();
  const buttons = $('room-list').querySelectorAll('button');
  await click(buttons.find((button) => button.querySelector('span.room-name').textContent === 'Проект'));
  return { $, workspace, session };
}

const shown = ($) => $('message-list').children.map((item) => [
  item.querySelector('span.message-author').textContent,
  item.querySelector('p.message-text')?.textContent,
]);

test('opening a room shows its latest messages, oldest first', async () => {
  const api = scripted().on(HISTORY, page([msg(1), msg(2, 'u-1')]));
  const { $ } = await openTeam(api);

  assert.deepEqual(shown($), [['bob', '#1'], ['вы', '#2']]);
  assert.equal($('message-list').children[1].className, 'message own');
  assert.equal($('load-older').hidden, true);
});

test('message text is shown as text, never as markup', async () => {
  const api = scripted().on(HISTORY, page([msg(1, 'u-2', '<img src=x onerror=alert(1)>\nвторая строка')]));
  const { $ } = await openTeam(api);

  const text = $('message-list').querySelector('p.message-text');
  assert.equal(text.children.length, 0);
  assert.equal(text.textContent, '<img src=x onerror=alert(1)>\nвторая строка');
});

test('older messages load on request', async () => {
  const api = scripted()
    .on(HISTORY, page([msg(3), msg(4)], '3'))
    .on(`${HISTORY}&cursor=3`, page([msg(1), msg(2)]));
  const { $ } = await openTeam(api);
  assert.equal($('load-older').hidden, false);

  await click($('load-older'));

  assert.deepEqual(shown($).map(([, text]) => text), ['#1', '#2', '#3', '#4']);
  assert.equal($('load-older').hidden, true);
});

test('Enter sends the message and the room moves to the top', async () => {
  const api = scripted()
    .on(ROOMS, { rooms: [other, team], next_cursor: '' })
    .on(HISTORY, page([msg(1)]))
    .on(SEND, (options) => msg(2, 'u-1', options.body.text));
  const { $ } = await openTeam(api);

  $('message-text').value = 'Привет';
  $('message-text').dispatchEvent(Object.assign(new Event('keydown', { cancelable: true }), { key: 'Enter' }));
  await settle();
  await settle();

  assert.equal(api.calls.at(-1).key, SEND);
  assert.equal(api.calls.at(-1).body.text, 'Привет');
  assert.deepEqual(shown($).at(-1), ['вы', 'Привет']);
  assert.equal($('message-text').value, '');
  assert.equal($('room-list').querySelector('span.room-name').textContent, 'Проект');
});

test('a failed send keeps the text and retries under the same message ID', async () => {
  let fail = true;
  const api = scripted().on(HISTORY, page([])).on(SEND, (options) => {
    if (fail) throw new ApiError(0, 'network', 'offline');
    return msg(1, 'u-1', options.body.text);
  });
  const { $ } = await openTeam(api);
  $('message-text').value = 'Важное';

  await submit($('composer'));
  assert.equal($('message-text').value, 'Важное');
  assert.match($('room-error').textContent, /Нет связи/);
  fail = false;
  await submit($('composer'));

  const sends = api.calls.filter((call) => call.key === SEND);
  assert.equal(sends.length, 2);
  assert.equal(sends[1].body.client_message_id, sends[0].body.client_message_id);
  assert.equal($('message-text').value, '');
});

test('a second submit while sending is ignored', async () => {
  const late = deferred();
  const api = scripted().on(HISTORY, page([])).on(SEND, () => late.promise);
  const { $ } = await openTeam(api);
  $('message-text').value = 'Один раз';

  await submit($('composer'));
  await submit($('composer'));
  late.resolve(msg(1, 'u-1', 'Один раз'));
  await settle();

  assert.equal(api.calls.filter((call) => call.key === SEND).length, 1);
});

test('whitespace alone is not sent', async () => {
  const api = scripted().on(HISTORY, page([]));
  const { $ } = await openTeam(api);
  $('message-text').value = '  \n ';

  await submit($('composer'));

  assert.equal(api.calls.filter((call) => call.key === SEND).length, 0);
});

test('a room that answers 404 is dropped from the list', async () => {
  const api = scripted().on(HISTORY, notFound);
  const { $ } = await openTeam(api);

  assert.equal($('room').hidden, true);
  assert.deepEqual($('room-list').querySelectorAll('span.room-name').map((name) => name.textContent), ['Другой']);
  assert.match($('sidebar-error').textContent, /больше недоступна/);
});

test('history answered after sign-out is not shown', async () => {
  const late = deferred();
  const api = scripted().on(HISTORY, () => late.promise);
  const { $, workspace } = await openTeam(api);

  workspace.reset();
  late.resolve(page([msg(1, 'u-2', 'секрет Алисы')]));
  await settle();

  assert.deepEqual(shown($), []);
});

test('a late 404 for the previous user does not close the next user\'s room', async () => {
  const late = deferred();
  const api = scripted().on(HISTORY, () => late.promise.then(notFound));
  const { $, workspace } = await openTeam(api);

  workspace.reset();
  api.on(HISTORY, page([]));
  await workspace.refresh();
  await click($('room-list').querySelector('button'));
  late.resolve();
  await settle();

  assert.equal($('room-title').textContent, 'Проект');
  assert.equal($('room').hidden, false);
  assert.equal($('sidebar-error').textContent, '');
});

test('history of a room the user has left for another is not shown', async () => {
  const late = deferred();
  const api = scripted()
    .on(HISTORY, () => late.promise)
    .on('GET /api/v1/rooms/r-other/messages?limit=50', page([]));
  const { $ } = await openTeam(api);

  await click($('room-list').querySelectorAll('button')[1]);
  late.resolve(page([msg(1)]));
  await settle();

  assert.equal($('room-title').textContent, 'Другой');
  assert.deepEqual(shown($), []);
});

test('refresh catches up the open room after its newest message', async () => {
  const api = scripted()
    .on(HISTORY, page([msg(1)]))
    .on('GET /api/v1/rooms/r-team/messages?limit=100&after=1', { messages: [msg(2)], next_cursor: '' });
  const { $, workspace } = await openTeam(api);

  await workspace.refresh();

  assert.deepEqual(shown($).map(([, text]) => text), ['#1', '#2']);
});
