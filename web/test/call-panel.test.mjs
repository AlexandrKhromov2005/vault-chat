import { test } from 'node:test';
import assert from 'node:assert/strict';

import { bindCallPanel } from '../static/app/call-panel.mjs';
import { Workspace } from '../static/app/workspace.mjs';
import { loadPage } from './minidom.mjs';
import { click, scriptedApi, signedIn } from './ui.mjs';

// StubCalls is a Calls whose view the test sets.
class StubCalls extends EventTarget {
  view = null;
  available = true;
  actions = [];

  show(view) {
    this.view = view ? { role: 'callee', owned: false, pending: null, error: '', remote: null, roomId: 'r-dialog', ...view } : null;
    this.dispatchEvent(new Event('change'));
  }

  accept() { this.actions.push('accept'); }
  reject() { this.actions.push('reject'); }
  cancel() { this.actions.push('cancel'); }
  end() { this.actions.push('end'); }
  dismiss() { this.actions.push('dismiss'); }
  start(roomId) { this.actions.push(['start', roomId]); }
}

function panel() {
  const document = loadPage();
  const $ = (id) => document.getElementById(id);
  const calls = new StubCalls();
  let plays = 0;
  $('call-audio').play = () => {
    plays++;
    return Promise.resolve();
  };
  bindCallPanel({ calls, peerName: () => 'bob' });
  const visible = () => ['call-accept', 'call-reject', 'call-cancel', 'call-end', 'call-dismiss'].filter((id) => !$(id).hidden);
  return { $, calls, visible, plays: () => plays };
}

test('the call bar offers what the call state allows', async () => {
  const { $, calls, visible } = panel();
  assert.equal($('call').hidden, true);

  calls.show({ state: 'ringing' });
  assert.equal($('call').hidden, false);
  assert.equal($('call-title').textContent, 'bob');
  assert.equal($('call-status').textContent, 'Входящий звонок от bob');
  assert.deepEqual(visible(), ['call-accept', 'call-reject']);
  await click($('call-accept'));
  await click($('call-reject'));
  assert.deepEqual(calls.actions, ['accept', 'reject']);

  calls.show({ state: 'ringing', pending: 'accept' });
  assert.equal($('call-accept').disabled, true, 'no second accept while one is under way');
  assert.equal($('call-status').textContent, 'Подключаемся…');

  calls.show({ state: 'ringing', role: 'caller', owned: true });
  assert.deepEqual(visible(), ['call-cancel']);
  assert.equal($('call-status').textContent, 'Вызываем bob…');

  calls.show({ state: 'ringing', role: 'caller', owned: false });
  assert.deepEqual(visible(), []);

  calls.show({ state: 'active', owned: true });
  assert.deepEqual(visible(), ['call-end']);

  calls.show({ state: 'ended', error: 'Микрофон не найден.' });
  assert.deepEqual(visible(), ['call-dismiss']);
  assert.equal($('call-status').textContent, 'Звонок завершён');
  assert.equal($('call-error').textContent, 'Микрофон не найден.');

  calls.show(null);
  assert.equal($('call').hidden, true);
});

test('the peer is heard only on the call device and only until the call ends', () => {
  const { $, calls, plays } = panel();
  const remote = { id: 'peer' };

  calls.show({ state: 'active', owned: false, remote });
  assert.equal($('call-audio').srcObject ?? null, null);
  assert.equal($('call-status').textContent, 'Звонок идёт на другом устройстве');

  calls.show({ state: 'active', owned: true, remote });
  assert.equal($('call-audio').srcObject, remote);
  assert.equal(plays(), 1);
  calls.show({ state: 'active', owned: true, remote });
  assert.equal(plays(), 1, 'the same stream is not restarted');

  calls.show({ state: 'ended', owned: true, remote });
  assert.equal($('call-audio').srcObject, null);
});

test('a direct dialog can be called; a channel cannot', async () => {
  const dialog = { room_id: 'r-dialog', kind: 'direct', private: true, created_at: '2026-10-18T10:00:00Z', participant_ids: ['u-1', 'u-2'] };
  const channel = { room_id: 'r-team', kind: 'channel', name: 'Проект', owner_id: 'u-1', private: true, created_at: '2026-10-18T09:00:00Z' };
  const api = scriptedApi()
    .on('GET /api/v1/rooms?limit=100', { rooms: [dialog, channel], next_cursor: '' })
    .on('GET /api/v1/users/u-2', { user_id: 'u-2', username: 'bob' })
    .on('GET /api/v1/rooms/r-dialog/messages?limit=50', { messages: [], next_cursor: '' })
    .on('GET /api/v1/rooms/r-team/messages?limit=50', { messages: [], next_cursor: '' })
    .on('GET /api/v1/rooms/r-dialog/members?limit=100', { members: [], next_cursor: '' })
    .on('GET /api/v1/rooms/r-team/members?limit=100', { members: [], next_cursor: '' });
  const document = loadPage();
  const $ = (id) => document.getElementById(id);
  const calls = new StubCalls();
  const workspace = new Workspace({ api, session: signedIn('u-1', 'alice'), calls, searchDelayMs: 0 });
  await workspace.refresh();
  const open = (name) => click($('room-list').querySelectorAll('button')
    .find((button) => button.querySelector('span.room-name').textContent === name));

  assert.equal($('call-start').hidden, true);
  await open('Проект');
  assert.equal($('call-start').hidden, true);
  await open('bob');
  assert.equal($('call-start').hidden, false);
  await click($('call-start'));
  assert.deepEqual(calls.actions, [['start', 'r-dialog']]);
  assert.equal(workspace.peerName({ roomId: 'r-dialog' }), 'bob');

  calls.show({ state: 'ringing', role: 'caller', owned: true });
  assert.equal($('call-start').disabled, true, 'one call at a time');
  calls.show({ state: 'ended' });
  assert.equal($('call-start').disabled, false);

  calls.available = false;
  calls.show(null);
  assert.equal($('call-start').hidden, true, 'hidden while the server has no calls');
  calls.available = true;
  calls.show(null);
  assert.equal($('call-start').hidden, false);

  workspace.reset();
  assert.equal($('call-start').hidden, true);
});
