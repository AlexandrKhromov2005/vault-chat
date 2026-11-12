// Recovery of a call whose connection drops: ICE restart, TURN refresh,
// lost signals and the time limit.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  DISCONNECT_GRACE_MS, MAX_ICE_RESTARTS, MAX_RESTART_ATTEMPTS, RECOVERY_MS, RESTART_RETRY_MS, RINGING_POLL_MS, SIGNAL_POLL_MS,
  TURN_REFRESH_LEAD_MS, TURN_TTL_MS, parseTurn,
} from '../static/app/calls.mjs';
import { apiError } from './callfakes.mjs';
import { acceptedCallee, flush, setup } from './callserver.mjs';

const FRESH_TURN = { ice_servers: [{ urls: ['turn:turn.example:3478'], username: '2:c-1:u-1', credential: 'fresh' }] };

// activeCaller sets up an active call that the user placed.
async function activeCaller(options) {
  const t = setup(options);
  await t.calls.start('r-1');
  t.server.move('connecting');
  await t.timers.advance(RINGING_POLL_MS);
  const mediaAt = t.timers.now; // the connection and its first TURN credentials
  t.server.peerSignal('answer', 'v=0 answer');
  await t.timers.advance(SIGNAL_POLL_MS);
  t.server.move('active');
  const pc = t.pcs.created[0];
  pc.connect();
  await flush();
  assert.equal(t.calls.view.state, 'active');
  return { ...t, pc, mediaAt };
}

// activeCallee sets up an active call that the user answered.
async function activeCallee() {
  const t = await acceptedCallee();
  t.server.peerSignal('offer', 'v=0 offer');
  await t.timers.advance(SIGNAL_POLL_MS);
  t.server.move('active');
  const pc = t.pcs.created[0];
  pc.connect();
  await flush();
  assert.equal(t.calls.view.state, 'active');
  return { ...t, pc };
}

const offers = (t) => t.signalsSent().filter((s) => s.kind === 'offer');
const answers = (t) => t.signalsSent().filter((s) => s.kind === 'answer');
const ends = (t) => t.api.sent('POST', '/api/v1/calls/c-1/end').length;
const turns = (t) => t.api.sent('POST', '/api/v1/calls/c-1/turn').length;

test('the TURN answer may shorten the lifetime of its credentials', () => {
  assert.equal(parseTurn({ ice_servers: [] }).ttlMs, TURN_TTL_MS);
  assert.equal(parseTurn({ ice_servers: [], ttl: 300 }).ttlMs, 300_000);
  for (const ttl of [0, 60, 1.5, '300', 100_000]) assert.equal(parseTurn({ ttl }).ttlMs, TURN_TTL_MS, String(ttl));
});

test('a failed connection of an active call is restarted by the caller with fresh TURN credentials', async () => {
  const t = await activeCaller();
  t.server.turn = FRESH_TURN;
  t.pc.failIce();
  await flush();
  assert.equal(t.calls.view.state, 'active');
  assert.equal(t.calls.view.recovering, true);

  await t.timers.advance(0);
  assert.deepEqual(t.pc.offers.at(-1), { iceRestart: true });
  assert.equal(t.pc.config.iceServers[0].credential, 'fresh', 'the restart gathers with new credentials');
  assert.deepEqual(offers(t).map((s) => s.payload), ['v=0 offer', 'v=0 offer 2']);

  // The answer's event is lost: polling finds it.
  t.server.peerSignal('answer', 'v=0 answer 2');
  await t.timers.advance(SIGNAL_POLL_MS);
  assert.deepEqual(t.pc.remoteDescriptions.at(-1), { type: 'answer', sdp: 'v=0 answer 2' });
  t.pc.connect();
  await flush();
  assert.equal(t.calls.view.recovering, false);

  await t.timers.advance(RECOVERY_MS);
  assert.equal(t.calls.view.state, 'active');
  assert.equal(ends(t), 0);
  assert.equal(offers(t).length, 2);
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/connected').length, 1, 'the server is told only of the first connection');
});

test('a disconnection the browser repairs within the grace period needs no restart', async () => {
  const t = await activeCaller();
  t.pc.disconnect();
  await flush();
  assert.equal(t.calls.view.recovering, true);
  await t.timers.advance(DISCONNECT_GRACE_MS / 2);
  t.pc.connect();
  await flush();
  await t.timers.advance(RESTART_RETRY_MS * 2);
  assert.equal(t.calls.view.recovering, false);
  assert.equal(offers(t).length, 1);
  assert.equal(t.pc.offers.length, 1);
});

test('a disconnection that turns into a failure is restarted at once', async () => {
  const t = await activeCaller();
  t.pc.disconnect();
  await flush();
  t.pc.failIce();
  await t.timers.advance(0);
  assert.equal(offers(t).length, 2);
});

test('the callee waits for the restart offer and answers it with fresh TURN credentials', async () => {
  const t = await activeCallee();
  t.server.turn = FRESH_TURN;
  t.pc.failIce();
  await t.timers.advance(RESTART_RETRY_MS * 2);
  assert.equal(offers(t).length, 0, 'only the caller restarts, so that offers do not cross');
  assert.equal(t.calls.view.recovering, true);

  t.server.peerSignal('offer', 'v=0 offer 2');
  await t.timers.advance(SIGNAL_POLL_MS);
  assert.deepEqual(t.pc.remoteDescriptions.at(-1), { type: 'offer', sdp: 'v=0 offer 2' });
  assert.equal(t.pc.configurations.at(-1).iceServers[0].credential, 'fresh');
  assert.equal(answers(t).length, 2);
  t.pc.connect();
  await flush();
  assert.equal(t.calls.view.recovering, false);
  assert.equal(t.calls.view.state, 'active');
});

test('a connection not back within 30 s ends the call and releases the microphone', async () => {
  const t = await activeCaller();
  t.pc.failIce();
  await t.timers.advance(RECOVERY_MS - 1);
  assert.equal(t.calls.view.state, 'active');
  // Without the peer's answer the restart offer stays the only one.
  assert.equal(offers(t).length, 2);

  await t.timers.advance(1);
  assert.equal(t.calls.view.state, 'ended');
  assert.equal(t.calls.view.error, 'Связь с собеседником потеряна.');
  assert.equal(ends(t), 1);
  assert.equal(t.mic.opened[0].track.stopped, true);
  assert.equal(t.pc.closed, true);
  assert.equal(t.timers.pending, 0);
});

test('one loss gets at most three restarts, 8 s apart', async () => {
  const t = await activeCaller();
  t.pc.failIce();
  // The peer answers every restart, but the connection does not come back.
  for (let step = 0; step < 4; step++) {
    await t.timers.advance(step === 0 ? 0 : RESTART_RETRY_MS);
    // Polling applies the answer before the next attempt.
    if (t.pc.signalingState === 'have-local-offer') t.server.peerSignal('answer', `v=0 answer ${step + 2}`);
  }
  assert.equal(offers(t).length, 1 + MAX_RESTART_ATTEMPTS);
  await t.timers.advance(RECOVERY_MS);
  assert.equal(t.calls.view.state, 'ended');
});

test('a call ended by the server during recovery stops recovering', async () => {
  const t = await activeCallee();
  t.pc.failIce();
  await flush();
  t.server.move('ended');
  t.calls.handleEvent({ type: 'call.ended', call_id: 'c-1' });
  await flush();
  assert.equal(t.calls.view.state, 'ended');
  assert.equal(t.calls.view.recovering, false);
  assert.equal(t.timers.pending, 0);
  assert.equal(ends(t), 0);
});

test('an offline browser restarts as soon as it is back online', async () => {
  let online = true;
  const t = await activeCaller({ isOnline: () => online });
  online = false;
  t.pc.failIce();
  await t.timers.advance(RESTART_RETRY_MS * 2);
  assert.equal(offers(t).length, 1, 'no offer while offline');

  online = true;
  t.calls.online();
  await t.timers.advance(0);
  assert.equal(offers(t).length, 2);
});

test('a restart offer lost to the network is sent again with its sender_seq, not as a new offer', async () => {
  const t = await activeCaller();
  let failing = true;
  t.server.overrides['POST /api/v1/calls/c-1/signal'] = (body) => {
    if (body.kind === 'offer' && failing) throw apiError(0, 'network');
    return undefined;
  };
  t.pc.failIce();
  await t.timers.advance(2_000); // the restart and its two retries
  assert.equal(offers(t).length, 4);
  assert.equal(t.calls.view.state, 'active', 'a lost description does not end a call that was up');

  failing = false;
  await t.timers.advance(RESTART_RETRY_MS);
  const restart = offers(t).slice(1);
  assert.equal(restart.length, 4);
  assert.equal(new Set(restart.map((s) => `${s.sender_seq} ${s.payload}`)).size, 1, 'the same signal each time');
  assert.equal(t.pc.offers.length, 2, 'no second restart');
  assert.equal(t.server.signals.filter((s) => s.kind === 'offer' && s.sender_device === 'd-me').length, 2,
    'the first offer and the restart offer');
});

test('a description stored although its answer was lost is not stored twice', async () => {
  const t = await activeCallee();
  let lose = true;
  t.server.overrides['POST /api/v1/calls/c-1/signal'] = (body) => {
    if (body.kind !== 'answer' || !lose) return undefined;
    t.server.handle('POST', '/api/v1/calls/c-1/signal', body);
    throw apiError(0, 'network');
  };
  t.server.peerSignal('offer', 'v=0 offer 2');
  await t.timers.advance(SIGNAL_POLL_MS);
  await t.timers.advance(2_000);
  assert.equal(t.calls.view.state, 'active');

  lose = false;
  await t.timers.advance(15_000); // the heartbeat sends it again
  const stored = t.server.signals.filter((s) => s.kind === 'answer' && s.sender_device === 'd-me');
  assert.equal(stored.length, 2, 'the first answer and the restart answer');
  assert.equal(new Set(answers(t).slice(1).map((s) => s.sender_seq)).size, 1);
});

test('a restart offer refused because the peer offered first is withdrawn and the peer offer answered', async () => {
  const t = await activeCaller();
  t.server.peerSignal('offer', 'v=0 peer offer');
  t.server.overrides['POST /api/v1/calls/c-1/signal'] = (body) => {
    if (body.kind === 'offer') throw apiError(409, 'conflict');
    return undefined;
  };
  t.pc.failIce();
  await t.timers.advance(0);
  assert.equal(t.pc.rolledBack, 1);
  await t.timers.advance(SIGNAL_POLL_MS);
  assert.deepEqual(t.pc.remoteDescriptions.at(-1), { type: 'offer', sdp: 'v=0 peer offer' });
  assert.equal(answers(t).length, 1);
  assert.equal(t.calls.view.state, 'active');
});

test('a relayed connection is restarted before its TURN credentials expire', async () => {
  const t = await activeCaller();
  t.server.turn = { ...FRESH_TURN, ttl: 300 };
  // The credentials of the first request last the default 10 minutes.
  t.pc.relay('remote');
  const refreshAt = t.mediaAt + TURN_TTL_MS - TURN_REFRESH_LEAD_MS;
  await t.timers.advance(refreshAt - 1 - t.timers.now);
  assert.equal(offers(t).length, 1);
  await t.timers.advance(1);
  assert.equal(offers(t).length, 2);
  assert.equal(turns(t), 2);
  assert.equal(t.pc.config.iceServers[0].credential, 'fresh');

  // The next ones last as long as the answer says.
  t.server.peerSignal('answer', 'v=0 answer 2');
  await t.timers.advance(240_000);
  assert.equal(offers(t).length, 3);
});

test('a direct connection keeps its connection when TURN credentials expire', async () => {
  const t = await activeCaller();
  t.pc.relay('none');
  await t.timers.advance(TURN_TTL_MS * 2);
  assert.equal(offers(t).length, 1);
  assert.equal(t.calls.view.state, 'active');
});

test('ICE restarts are bounded per call', async () => {
  const t = await activeCaller();
  for (let i = 0; i <= MAX_ICE_RESTARTS; i++) {
    t.pc.failIce();
    await t.timers.advance(0);
    if (t.pc.signalingState === 'have-local-offer') {
      t.server.peerSignal('answer', `v=0 answer ${i + 2}`);
      await t.timers.advance(SIGNAL_POLL_MS);
    }
    t.pc.connect();
    await flush();
  }
  assert.equal(offers(t).length, 1 + MAX_ICE_RESTARTS);
  t.pc.failIce();
  await t.timers.advance(RECOVERY_MS);
  assert.equal(t.calls.view.state, 'ended');
  assert.equal(offers(t).length, 1 + MAX_ICE_RESTARTS);
});

test('a restart offer seen again after the event stream reconnects is applied once', async () => {
  const t = await activeCallee();
  t.pc.failIce();
  await flush();
  const offer = t.server.peerSignal('offer', 'v=0 offer 2');
  t.calls.handleEvent({ type: 'call.signal', call_id: 'c-1', ...offer });
  await flush();
  // The socket reconnects and the client reconciles.
  await t.calls.reconcile();
  await flush();
  t.calls.handleEvent({ type: 'call.signal', call_id: 'c-1', ...offer });
  await t.timers.advance(SIGNAL_POLL_MS * 3);
  assert.deepEqual(t.pc.remoteDescriptions.filter((d) => d.type === 'offer').map((d) => d.sdp), ['v=0 offer', 'v=0 offer 2']);
  assert.equal(answers(t).length, 2);
});
