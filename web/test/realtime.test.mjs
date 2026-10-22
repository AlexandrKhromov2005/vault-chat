import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Backoff } from '../static/app/backoff.mjs';
import { ApiError } from '../static/app/http.mjs';
import { Realtime } from '../static/app/realtime.mjs';
import { FakeSocket, FakeTimers } from './fakes.mjs';
import { settle } from './minidom.mjs';

const START = Date.parse('2026-10-18T12:00:00Z');

function fakeSession({ refresh = async () => 'refreshed' } = {}) {
  let n = 0;
  return {
    refreshes: 0,
    margins: [],
    async validAccessToken(minValidityMs) {
      this.margins.push(minValidityMs);
      return `access-${++n}`;
    },
    async refresh() {
      this.refreshes++;
      return refresh();
    },
  };
}

function setup(sessionOptions) {
  const timers = new FakeTimers(START);
  const sockets = [];
  const session = fakeSession(sessionOptions);
  const realtime = new Realtime({
    session,
    url: 'ws://gateway/api/v1/ws',
    createSocket: (url) => {
      const socket = new FakeSocket(url);
      sockets.push(socket);
      return socket;
    },
    timers: { set: timers.set, clear: timers.clear, now: () => timers.now },
    backoff: new Backoff({ base: 500, max: 8000, random: () => 1 }),
  });
  const states = [];
  realtime.addEventListener('state', (event) => states.push(event.detail.state));
  const events = [];
  realtime.addEventListener('event', (event) => events.push(event.detail));
  const readies = [];
  realtime.addEventListener('ready', (event) => readies.push(event.detail));
  return { timers, sockets, session, realtime, states, events, readies };
}

const ready = (inMs) => ({ type: 'ready', user_id: 'u-1', expires_at: new Date(START + inMs).toISOString() });

async function connect(t, expiresInMs = 15 * 60_000) {
  const socket = t.sockets.at(-1);
  socket.open();
  await settle();
  socket.receive(ready(t.timers.now - START + expiresInMs));
  await settle();
  return socket;
}

test('authenticates with the first frame and goes live on ready', async () => {
  const t = setup();
  t.realtime.start();

  assert.equal(t.sockets[0].url, 'ws://gateway/api/v1/ws');
  await connect(t);

  assert.deepEqual(t.sockets[0].sent, [{ type: 'auth', token: 'access-1' }]);
  assert.equal(t.realtime.state, 'live');
  assert.deepEqual(t.states, ['connecting', 'live']);
  assert.equal(t.readies.length, 1);
});

test('forwards events and ignores malformed frames', async () => {
  const t = setup();
  t.realtime.start();
  const socket = await connect(t);

  socket.receive({ type: 'message.created', room_id: 'r-1', message: { sequence: 1 } });
  socket.receive('{not json');
  socket.receive({ no_type: true });

  assert.deepEqual(t.events, [{ type: 'message.created', room_id: 'r-1', message: { sequence: 1 } }]);
});

test('re-authenticates before the access token expires', async () => {
  const t = setup();
  t.realtime.start();
  const socket = await connect(t, 15 * 60_000);

  await t.timers.advance(15 * 60_000 - 90_000 - 1);
  assert.equal(socket.sent.length, 1);
  await t.timers.advance(1);

  assert.deepEqual(socket.sent.at(-1), { type: 'auth', token: 'access-2' });
  // The renewal asks for a token valid beyond the renewal point, i.e. a
  // fresh one, rather than resending the token already on the connection.
  // expires_at comes from the token's exp, which has whole-second precision,
  // so the timer can fire up to a second before the session sees the token
  // as due; the margin must cover that.
  assert.ok(t.session.margins.at(-1) >= 91_000);
});

test('re-authenticates at most every ten seconds', async () => {
  // The Gateway closes a connection that re-authenticates more often (1008).
  const t = setup();
  t.realtime.start();
  const socket = await connect(t, 30_000);

  await t.timers.advance(9_999);
  assert.equal(socket.sent.length, 1);
  await t.timers.advance(1);

  assert.equal(socket.sent.length, 2);
});

test('reconnects with growing delays and resets them after ready', async () => {
  const t = setup();
  t.realtime.start();
  await connect(t);

  t.sockets[0].serverClose(1001);
  await settle();
  assert.equal(t.realtime.state, 'reconnecting');
  await t.timers.advance(499);
  assert.equal(t.sockets.length, 1);
  await t.timers.advance(1);
  assert.equal(t.sockets.length, 2);

  t.sockets[1].serverClose(1006); // lost before it opened
  await settle();
  await t.timers.advance(999);
  assert.equal(t.sockets.length, 2);
  await t.timers.advance(1);
  assert.equal(t.sockets.length, 3);

  await connect(t);
  t.sockets[2].serverClose(1013);
  await settle();
  await t.timers.advance(500);
  assert.equal(t.sockets.length, 4);
  assert.equal(t.readies.length, 2);
});

test('refreshes the token after 4001 and reconnects at once', async () => {
  const t = setup();
  t.realtime.start();
  await connect(t);

  t.sockets[0].serverClose(4001);
  await settle();
  await t.timers.advance(0);

  assert.equal(t.session.refreshes, 1);
  assert.equal(t.sockets.length, 2);
});

test('stops when the session can no longer be refreshed', async () => {
  const t = setup({
    refresh: async () => {
      throw new ApiError(401, 'unauthenticated', 'invalid token');
    },
  });
  t.realtime.start();
  await connect(t);

  t.sockets[0].serverClose(4001);
  await settle();
  await t.timers.advance(60_000);

  assert.equal(t.sockets.length, 1);
  assert.equal(t.realtime.state, 'stopped');
});

test('a protocol error is not retried', async () => {
  const t = setup();
  t.realtime.start();
  await connect(t);

  t.sockets[0].serverClose(1008);
  await settle();
  await t.timers.advance(60_000);

  assert.equal(t.sockets.length, 1);
  assert.equal(t.realtime.state, 'failed');
});

test('stop closes the connection for good', async () => {
  const t = setup();
  t.realtime.start();
  const socket = await connect(t);

  t.realtime.stop();
  socket.serverClose(1000);
  await settle();
  await t.timers.advance(60 * 60_000);

  assert.equal(socket.closedWith, 1000);
  assert.equal(t.sockets.length, 1);
  assert.equal(t.realtime.state, 'stopped');
  assert.equal(t.timers.pending, 0);
});

test('events of a replaced connection are ignored', async () => {
  const t = setup();
  t.realtime.start();
  const old = await connect(t);
  old.serverClose(1001);
  await settle();
  await t.timers.advance(500);
  await connect(t);

  old.receive({ type: 'message.created', room_id: 'r-1' });

  assert.deepEqual(t.events, []);
});

test('a failed upgrade reported only as an error is retried', async () => {
  // Node's WebSocket reports a refused handshake (e.g. 429 from the rate
  // limiter) with an error event and never closes.
  const t = setup();
  t.realtime.start();

  t.sockets[0].fail();
  await settle();
  await t.timers.advance(500);

  assert.equal(t.sockets.length, 2);
  assert.equal(t.sockets[0].closedWith, 1000);
});

test('an error followed by close is handled once', async () => {
  // Browsers fire close (1006) after error; the second must not schedule
  // another reconnection.
  const t = setup();
  t.realtime.start();
  await connect(t);

  t.sockets[0].fail();
  t.sockets[0].serverClose(1006);
  await settle();
  await t.timers.advance(500);

  assert.equal(t.sockets.length, 2);
  assert.equal(t.timers.pending, 0);
});

test('a late refresh of a stopped connection leaves the next one alone', async () => {
  // Review of #79: Alice's connection closed with 4001 and its refresh was
  // held back; after sign-out Bob's connection went live, and the refresh,
  // rejected with 401, stopped Bob's connection for good.
  let failRefresh;
  const t = setup({
    refresh: () => new Promise((_, reject) => {
      failRefresh = reject;
    }),
  });
  t.realtime.start();
  await connect(t);
  t.sockets[0].serverClose(4001);
  await settle();

  t.realtime.stop();
  t.realtime.start();
  await connect(t);
  failRefresh(new ApiError(401, 'unauthenticated', 'Сессия уже завершена'));
  await settle();

  assert.equal(t.realtime.state, 'live');
  t.sockets[1].serverClose(1001);
  await settle();
  await t.timers.advance(500);
  assert.equal(t.sockets.length, 3);
});

test('a late refresh of a stopped connection does not open another one', async () => {
  // The same with a refresh that succeeds while the next connection is
  // itself reconnecting: only that connection's timer may reconnect.
  let finishRefresh;
  const t = setup({
    refresh: () => new Promise((resolve) => {
      finishRefresh = resolve;
    }),
  });
  t.realtime.start();
  await connect(t);
  t.sockets[0].serverClose(4001);
  await settle();

  t.realtime.stop();
  t.realtime.start();
  await connect(t);
  t.sockets[1].serverClose(1001);
  await settle();
  finishRefresh('access-old');
  await settle();
  await t.timers.advance(0);

  assert.equal(t.sockets.length, 2);
  await t.timers.advance(500);
  assert.equal(t.sockets.length, 3);
});
