import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Calls, HEARTBEAT_MS, RINGING_POLL_MS, SIGNAL_POLL_MS, parseCall, parseIceServers, parseSignal } from '../static/app/calls.mjs';
import { FakeTimers } from './fakes.mjs';
import { apiError, candidate, fakeMicrophone, peerConnections, routedApi } from './callfakes.mjs';
import { signedIn } from './ui.mjs';

const flush = async (rounds = 10) => {
  for (let i = 0; i < rounds; i++) await new Promise((resolve) => setImmediate(resolve));
};

// callServer plays Calls for call c-1 between u-1 (the user) and u-2.
// overrides["METHOD path"](body) may answer a request instead, or return
// undefined to let the server handle it.
function callServer({ role }) {
  const server = {
    state: null,
    version: 0,
    signals: [],
    nextSeq: 1,
    turn: { ice_servers: [{ urls: ['turn:turn.example:3478'], username: '1:c-1:u-1', credential: 'secret' }] },
    overrides: {},
    view() {
      return {
        call_id: 'c-1',
        room_id: 'r-1',
        caller_id: role === 'caller' ? 'u-1' : 'u-2',
        callee_id: role === 'caller' ? 'u-2' : 'u-1',
        state: server.state,
        state_version: server.version,
      };
    },
    move(state) {
      server.state = state;
      server.version++;
    },
    peerSignal(kind, payload) {
      const seq = server.nextSeq++;
      const signal = { signal_seq: seq, sender_device: 'd-peer', sender_seq: seq, kind, payload };
      server.signals.push(signal);
      return signal;
    },
  };
  server.handle = (method, path, body) => {
    const override = server.overrides[`${method} ${path}`];
    if (override) {
      const answer = override(body);
      if (answer !== undefined) return answer;
    }
    if (method === 'POST' && path === '/api/v1/calls') {
      server.move('ringing');
      return { call_id: 'c-1', state: 'ringing' };
    }
    if (method === 'GET' && path === '/api/v1/calls?active=true') {
      return { calls: server.state && server.state !== 'ended' ? [server.view()] : [] };
    }
    if (method === 'GET' && path === '/api/v1/calls/c-1') return server.view();
    const match = /^\/api\/v1\/calls\/c-1\/([a-z-]+)(?:\?after=(\d+))?$/.exec(path);
    if (!match) return undefined;
    const [, action, after] = match;
    if (method === 'GET' && action === 'signals') {
      return { signals: server.signals.filter((signal) => signal.signal_seq > Number(after)) };
    }
    switch (action) {
      case 'accept':
        server.move('connecting');
        return { state: 'connecting' };
      case 'reject':
      case 'cancel':
      case 'end':
        server.move('ended');
        return { state: 'ended' };
      case 'connected':
      case 'heartbeat':
        return { state: server.state };
      case 'turn':
        return server.turn;
      case 'signal': {
        const seq = server.nextSeq++;
        server.signals.push({ signal_seq: seq, sender_device: 'd-me', sender_seq: body.sender_seq, kind: body.kind, payload: body.payload });
        return { state: server.state, signal_seq: seq };
      }
      default:
        return undefined;
    }
  };
  return server;
}

function setup({ role = 'caller', micError = null, gatherOnLocal = null } = {}) {
  const server = callServer({ role });
  const api = routedApi(server.handle);
  const mic = fakeMicrophone({ error: micError });
  const pcs = peerConnections({ gatherOnLocal });
  const timers = new FakeTimers();
  let ids = 0;
  const session = signedIn('u-1', 'alice');
  const calls = new Calls({ api, session, mediaDevices: mic, createPeerConnection: pcs.create, timers, newId: () => `cmd-${++ids}` });
  const signalsSent = () => api.sent('POST', '/api/v1/calls/c-1/signal').map((r) => r.body);
  return { server, api, mic, pcs, timers, calls, signalsSent };
}

// ringingCallee sets up an incoming call that the user has not answered.
async function ringingCallee() {
  const t = setup({ role: 'callee' });
  t.server.move('ringing');
  t.calls.handleEvent({ type: 'call.ringing', call_id: 'c-1' });
  await flush();
  return t;
}

async function acceptedCallee() {
  const t = await ringingCallee();
  await t.calls.accept();
  await flush();
  return t;
}

test('parsers keep only well-formed calls, signals and ICE servers', () => {
  assert.deepEqual(parseCall({ call_id: 'c', state: 'ringing', state_version: 2, room_id: 'r', caller_id: 'a', callee_id: 'b' }),
    { id: 'c', state: 'ringing', roomId: 'r', callerId: 'a', calleeId: 'b' });
  assert.equal(parseCall({ call_id: 'c', state: 'dialing' }), null);
  assert.equal(parseCall({ call_id: '', state: 'ringing' }), null);
  assert.equal(parseCall({ call_id: 'x'.repeat(129), state: 'ringing' }), null);
  assert.deepEqual(parseCall({ call_id: 'c', state: 'active', room_id: 7, caller_id: '' }), { id: 'c', state: 'active' });

  assert.deepEqual(parseSignal({ signal_seq: 3, kind: 'answer', payload: 'v=0', sender_device: 'd' }),
    { seq: 3, kind: 'answer', payload: 'v=0' });
  for (const bad of [{ signal_seq: 0, kind: 'offer', payload: '' }, { signal_seq: 1.5, kind: 'offer', payload: '' },
    { signal_seq: 1, kind: 'bye', payload: '' }, { signal_seq: 1, kind: 'offer', payload: {} }, null]) {
    assert.equal(parseSignal(bad), null);
  }

  assert.deepEqual(parseIceServers({ ice_servers: [
    { urls: 'turn:t.example:3478', username: 'u', credential: 'p' },
    { urls: ['https://evil.example', 'stun:s.example'] },
    { urls: ['javascript:alert(1)'] },
    'junk',
  ] }), [{ urls: ['turn:t.example:3478'], username: 'u', credential: 'p' }, { urls: ['stun:s.example'] }]);
  assert.deepEqual(parseIceServers(null), []);
});

test('calls are offered only when the server lists them', async () => {
  const t = setup();
  assert.equal(t.calls.available, null);
  t.server.overrides['GET /api/v1/calls?active=true'] = () => {
    throw apiError(404, 'not_found');
  };
  await t.calls.reconcile();
  assert.equal(t.calls.available, false);
  // An outage says nothing about the API.
  t.server.overrides['GET /api/v1/calls?active=true'] = () => {
    throw apiError(503, 'unavailable');
  };
  await t.calls.reconcile();
  assert.equal(t.calls.available, false);
  delete t.server.overrides['GET /api/v1/calls?active=true'];
  await t.calls.reconcile();
  assert.equal(t.calls.available, true);
  t.calls.reset();
  assert.equal(t.calls.available, null);
});

test('a caller rings, sends its offer before its candidates and talks once the answer arrives', async () => {
  // A candidate gathered as early as possible still follows the offer.
  const t = setup({ gatherOnLocal: candidate(1) });
  await t.calls.start('r-1');

  assert.deepEqual(t.api.sent('POST', '/api/v1/calls').map((r) => r.body), [{ room_id: 'r-1', command_id: 'cmd-1' }]);
  assert.deepEqual(t.mic.opened[0].constraints, { audio: true, video: false });
  assert.equal(t.calls.view.state, 'ringing');
  assert.equal(t.calls.view.owned, true);
  assert.equal(t.calls.view.role, 'caller');
  assert.equal(t.pcs.created.length, 0, 'no connection, and so no address, before accept');

  t.server.move('connecting');
  await t.timers.advance(RINGING_POLL_MS);
  await flush();
  assert.equal(t.calls.view.state, 'connecting');
  assert.equal(t.pcs.created.length, 1);
  const pc = t.pcs.created[0];
  assert.deepEqual(pc.config.iceServers, t.server.turn.ice_servers);
  assert.deepEqual(pc.tracks, [t.mic.opened[0].track]);

  pc.gather(null);
  await flush();
  assert.deepEqual(t.signalsSent().map((s) => [s.kind, s.sender_seq]), [['offer', 1], ['candidate', 2], ['end-of-candidates', 3]]);
  assert.equal(t.signalsSent()[0].payload, 'v=0 offer');
  assert.deepEqual(JSON.parse(t.signalsSent()[1].payload), candidate(1));
  assert.equal(t.signalsSent()[2].payload, '');

  t.server.peerSignal('answer', 'v=0 answer');
  t.server.peerSignal('candidate', JSON.stringify(candidate(9)));
  await t.timers.advance(SIGNAL_POLL_MS);
  await flush();
  // The device's own offer and candidates, listed by the journal too, are
  // not applied as the peer's.
  assert.deepEqual(pc.remoteDescriptions, [{ type: 'answer', sdp: 'v=0 answer' }]);
  assert.deepEqual(pc.candidates, [candidate(9)]);

  t.server.move('active');
  pc.connect();
  await flush();
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/connected').length, 1);
  assert.equal(t.calls.view.state, 'active');
  const remote = { id: 'peer-audio' };
  pc.track(remote);
  assert.equal(t.calls.view.remote, remote);

  await t.calls.end();
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/end').length, 1);
  assert.equal(t.mic.opened[0].track.stopped, true);
  assert.equal(pc.closed, true);
  assert.equal(t.calls.view.state, 'ended');
  assert.equal(t.calls.view.remote, null);
});

test('a callee answers the offer after accepting, applying signals in signal_seq order', async () => {
  const t = await ringingCallee();
  assert.equal(t.calls.view.state, 'ringing');
  assert.equal(t.calls.view.role, 'callee');
  assert.equal(t.calls.view.owned, false);
  assert.equal(t.mic.requests, 0, 'the microphone stays off until the user accepts');

  await t.calls.accept();
  await flush();
  assert.deepEqual(t.api.sent('POST', '/api/v1/calls/c-1/accept').map((r) => r.body), [{ command_id: 'cmd-1' }]);
  assert.equal(t.calls.view.owned, true);
  assert.equal(t.calls.view.state, 'connecting');
  const pc = t.pcs.created[0];
  assert.deepEqual(t.signalsSent(), [], 'the callee does not offer');

  const offer = t.server.peerSignal('offer', 'v=0 offer');
  const cand = t.server.peerSignal('candidate', JSON.stringify(candidate(4)));
  // The candidate's event overtakes the offer's: the gap is caught up.
  t.calls.handleEvent({ type: 'call.signal', call_id: 'c-1', ...cand });
  await flush();
  t.calls.handleEvent({ type: 'call.signal', call_id: 'c-1', ...offer });
  t.calls.handleEvent({ type: 'call.signal', call_id: 'c-1', ...cand });
  await flush();

  assert.deepEqual(pc.remoteDescriptions, [{ type: 'offer', sdp: 'v=0 offer' }], 'each signal is applied once');
  assert.deepEqual(pc.candidates, [candidate(4)]);
  assert.deepEqual(t.signalsSent().map((s) => [s.kind, s.payload, s.sender_seq]), [['answer', 'v=0 answer', 1]]);
});

test('accepting twice opens one microphone and sends one accept', async () => {
  const t = await ringingCallee();
  const release = t.mic.hold();
  const first = t.calls.accept();
  const second = t.calls.accept();
  release();
  await Promise.all([first, second]);
  await flush();
  assert.equal(t.mic.requests, 1);
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/accept').length, 1);
  assert.equal(t.pcs.created.length, 1);
});

test('without the microphone a call is neither started nor accepted', async () => {
  const caller = setup({ micError: 'NotAllowedError' });
  await caller.calls.start('r-1');
  assert.equal(caller.api.sent('POST', '/api/v1/calls').length, 0);
  assert.equal(caller.calls.view.state, 'ended');
  assert.match(caller.calls.view.error, /микрофону/);

  const callee = setup({ role: 'callee', micError: 'NotFoundError' });
  callee.server.move('ringing');
  await callee.calls.reconcile();
  await callee.calls.accept();
  assert.equal(callee.api.sent('POST', '/api/v1/calls/c-1/accept').length, 0);
  assert.equal(callee.calls.view.state, 'ringing', 'the user can still reject');
  assert.equal(callee.calls.view.error, 'Микрофон не найден.');

  const insecure = setup();
  const calls = new Calls({ api: insecure.api, session: signedIn('u-1', 'alice'), mediaDevices: undefined, timers: insecure.timers });
  await calls.start('r-1');
  assert.match(calls.view.error, /HTTPS/);
});

test('rejecting while the browser asks for the microphone keeps it closed', async () => {
  const t = await ringingCallee();
  let answerReject;
  t.server.overrides['POST /api/v1/calls/c-1/reject'] = () => new Promise((resolve) => {
    answerReject = () => resolve({ state: 'ended' });
  });
  const release = t.mic.hold();
  const accepting = t.calls.accept();
  const rejecting = t.calls.reject();
  await flush();
  // The microphone opens while the reject is still under way.
  release();
  await accepting;
  answerReject();
  await rejecting;
  await flush();
  assert.equal(t.mic.opened[0].track.stopped, true);
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/accept').length, 0);
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/reject').length, 1);
  assert.equal(t.calls.view.state, 'ended');
});

test('a network failure repeats a command with the same command_id', async () => {
  const t = await ringingCallee();
  let failures = 1;
  t.server.overrides['POST /api/v1/calls/c-1/accept'] = () => {
    if (failures-- > 0) throw apiError(0, 'network');
    return undefined;
  };
  const accepting = t.calls.accept();
  await flush();
  await t.timers.advance(500);
  await accepting;
  const bodies = t.api.sent('POST', '/api/v1/calls/c-1/accept').map((r) => r.body);
  assert.deepEqual(bodies, [{ command_id: 'cmd-1' }, { command_id: 'cmd-1' }]);
  assert.equal(t.calls.view.state, 'connecting');
});

test('an accept refused because another device answered leaves this tab out of the call', async () => {
  const t = await ringingCallee();
  t.server.overrides['POST /api/v1/calls/c-1/accept'] = () => {
    t.server.move('connecting');
    throw apiError(409, 'conflict');
  };
  await t.calls.accept();
  await flush();
  assert.equal(t.calls.view.owned, false);
  assert.equal(t.calls.view.state, 'connecting');
  assert.match(t.calls.view.error, /другом устройстве/);
  assert.equal(t.mic.opened[0].track.stopped, true);
  assert.equal(t.pcs.created.length, 0);
});

test('a call on another device is shown without microphone or connection', async () => {
  const t = setup({ role: 'callee' });
  t.server.state = 'active';
  await t.calls.reconcile();
  assert.equal(t.calls.view.state, 'active');
  assert.equal(t.calls.view.owned, false);
  assert.equal(t.mic.requests, 0);
  assert.equal(t.pcs.created.length, 0);
  await t.timers.advance(HEARTBEAT_MS * 2);
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/heartbeat').length, 0, 'only the call device sends heartbeats');

  // Its end is noticed without events.
  t.server.move('ended');
  await t.timers.advance(HEARTBEAT_MS);
  assert.equal(t.calls.view.state, 'ended');
});

test('the call device sends a heartbeat every 15 s until the call ends', async () => {
  const t = await acceptedCallee();
  await t.timers.advance(HEARTBEAT_MS);
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/heartbeat').length, 1);
  await t.timers.advance(HEARTBEAT_MS);
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/heartbeat').length, 2);

  t.server.move('ended');
  await t.timers.advance(HEARTBEAT_MS);
  assert.equal(t.calls.view.state, 'ended', 'the heartbeat answer carries the state');
  assert.equal(t.mic.opened[0].track.stopped, true);
  assert.equal(t.pcs.created[0].closed, true);
  const beats = t.api.sent('POST', '/api/v1/calls/c-1/heartbeat').length;
  await t.timers.advance(HEARTBEAT_MS * 4);
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/heartbeat').length, beats);
  assert.equal(t.timers.pending, 0);
});

test('call.ended stops the media; a stale answer neither reverts nor revives the call', async () => {
  const t = await acceptedCallee();

  // A late answer from before the accept.
  t.server.overrides['GET /api/v1/calls/c-1'] = () => ({ ...t.server.view(), state: 'ringing', state_version: 0 });
  t.calls.handleEvent({ type: 'call.accepted', call_id: 'c-1' });
  await flush();
  assert.equal(t.calls.view.state, 'connecting');
  delete t.server.overrides['GET /api/v1/calls/c-1'];

  t.server.move('ended');
  t.calls.handleEvent({ type: 'call.ended', call_id: 'c-1' });
  await flush();
  assert.equal(t.calls.view.state, 'ended');
  assert.equal(t.mic.opened[0].track.stopped, true);
  assert.equal(t.pcs.created[0].closed, true);

  // An active-call list read before the end lists the call as ringing.
  t.server.overrides['GET /api/v1/calls?active=true'] = () => ({ calls: [{ ...t.server.view(), state: 'ringing' }] });
  await t.calls.reconcile();
  assert.equal(t.calls.view.state, 'ended');
  // A late event about the ended call is not even looked up.
  const lists = t.api.sent('GET', '/api/v1/calls?active=true').length;
  t.calls.handleEvent({ type: 'call.ringing', call_id: 'c-1' });
  await flush();
  assert.equal(t.api.sent('GET', '/api/v1/calls?active=true').length, lists);
  assert.equal(t.calls.view.state, 'ended');
  t.calls.dismiss();
  assert.equal(t.calls.view, null);
  t.calls.handleEvent({ type: 'call.ended', call_id: 'c-1' });
  await flush();
  assert.equal(t.api.sent('GET', '/api/v1/calls?active=true').length, lists, 'nor after it was dismissed');
  assert.equal(t.mic.opened.length, 1);
});

test('a device sends at most 32 candidates and no end-of-candidates once active', async () => {
  const t = setup();
  await t.calls.start('r-1');
  t.server.move('connecting');
  await t.timers.advance(RINGING_POLL_MS);
  await flush();
  const pc = t.pcs.created[0];
  for (let n = 1; n <= 40; n++) pc.gather(candidate(n));
  await flush(60);
  assert.equal(t.signalsSent().filter((s) => s.kind === 'candidate').length, 32);

  t.server.move('active');
  pc.connect();
  await flush();
  assert.equal(t.calls.view.state, 'active');
  pc.gather(null);
  await flush();
  assert.equal(t.signalsSent().filter((s) => s.kind === 'end-of-candidates').length, 0);
});

test('a signal stored although its answer was lost is not applied as the peer signal', async () => {
  const t = setup();
  await t.calls.start('r-1');
  t.server.move('connecting');
  await t.timers.advance(RINGING_POLL_MS);
  await flush();
  const pc = t.pcs.created[0];
  t.server.overrides['POST /api/v1/calls/c-1/signal'] = (body) => {
    if (body.kind !== 'candidate') return undefined;
    if (!t.server.signals.some((s) => s.sender_device === 'd-me' && s.sender_seq === body.sender_seq)) {
      t.server.signals.push({ signal_seq: t.server.nextSeq++, sender_device: 'd-me', sender_seq: body.sender_seq, kind: body.kind, payload: body.payload });
    }
    throw apiError(0, 'network');
  };
  pc.gather(candidate(2));
  await flush();
  await t.timers.advance(2_000);
  t.server.peerSignal('answer', 'v=0 answer');
  await t.timers.advance(SIGNAL_POLL_MS);
  await flush();
  assert.equal(t.signalsSent().filter((s) => s.kind === 'candidate').length, 3, 'tried three times');
  assert.deepEqual(pc.remoteDescriptions, [{ type: 'answer', sdp: 'v=0 answer' }]);
  assert.deepEqual(pc.candidates, [], 'its own candidate is not applied');
  assert.equal(t.calls.view.state, 'connecting');
});

test('a malformed candidate is dropped and an oversized offer ends the call', async () => {
  const t = await acceptedCallee();
  const pc = t.pcs.created[0];
  t.server.peerSignal('offer', 'v=0 offer');
  t.server.peerSignal('candidate', '{"candidate": 5}');
  t.server.peerSignal('candidate', 'not json');
  t.server.peerSignal('candidate', JSON.stringify({ ...candidate(1), sdpMLineIndex: -1 }));
  await t.timers.advance(SIGNAL_POLL_MS);
  await flush();
  assert.deepEqual(pc.candidates, []);
  assert.equal(t.calls.view.state, 'connecting');

  const t2 = await acceptedCallee();
  t2.server.peerSignal('offer', 'a'.repeat(8193));
  await t2.timers.advance(SIGNAL_POLL_MS);
  await flush();
  assert.equal(t2.pcs.created[0].remoteDescriptions.length, 0);
  assert.equal(t2.calls.view.state, 'ended');
  assert.equal(t2.api.sent('POST', '/api/v1/calls/c-1/end').length, 1);
  assert.equal(t2.mic.opened[0].track.stopped, true);
});

test('an answer before any offer is rejected as a protocol error', async () => {
  const t = await acceptedCallee();
  t.server.peerSignal('answer', 'v=0 answer');
  await t.timers.advance(SIGNAL_POLL_MS);
  await flush();
  assert.equal(t.calls.view.state, 'ended');
  assert.match(t.calls.view.error, /некорректные/);
});

test('an ICE failure ends the call on the server', async () => {
  const t = await acceptedCallee();
  t.pcs.created[0].failIce();
  await flush();
  assert.equal(t.calls.view.state, 'ended');
  assert.equal(t.calls.view.error, 'Не удалось соединиться с собеседником.');
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/end').length, 1);
});

test('without TURN the call tries direct connections', async () => {
  const t = await ringingCallee();
  t.server.overrides['POST /api/v1/calls/c-1/turn'] = () => {
    throw apiError(503, 'unavailable');
  };
  await t.calls.accept();
  await flush();
  assert.deepEqual(t.pcs.created[0].config.iceServers, []);
});

test('a busy callee ends the attempt and the user\'s calls are reconciled', async () => {
  const t = setup();
  t.server.overrides['POST /api/v1/calls'] = () => {
    throw apiError(409, 'busy');
  };
  await t.calls.start('r-1');
  await flush();
  assert.equal(t.calls.view.state, 'ended');
  assert.match(t.calls.view.error, /занят/);
  assert.equal(t.mic.opened[0].track.stopped, true);
  assert.equal(t.api.sent('GET', '/api/v1/calls?active=true').length, 1);
});

test('signing out while the microphone opens neither creates the call nor keeps the microphone', async () => {
  const t = setup();
  const release = t.mic.hold();
  const starting = t.calls.start('r-1');
  t.calls.reset();
  release();
  await starting;
  await flush();
  assert.equal(t.mic.opened[0].track.stopped, true);
  assert.equal(t.api.sent('POST', '/api/v1/calls').length, 0);
  assert.equal(t.calls.view, null);
});

test('signing out during a call releases it and ignores its late answers', async () => {
  const t = await acceptedCallee();
  let answerList;
  t.server.overrides['GET /api/v1/calls?active=true'] = () => new Promise((resolve) => {
    answerList = () => resolve({ calls: [t.server.view()] });
  });
  const reconciling = t.calls.reconcile();
  await flush();
  t.calls.reset();
  assert.equal(t.mic.opened[0].track.stopped, true);
  assert.equal(t.pcs.created[0].closed, true);
  assert.equal(t.timers.pending, 0);
  // The previous user's call list arrives after sign-out.
  answerList();
  await reconciling;
  await flush();
  assert.equal(t.calls.view, null);
});

test('hanging up before logout cancels a ringing outgoing call within the time limit', async () => {
  const t = setup();
  await t.calls.start('r-1');
  let release;
  t.server.overrides['POST /api/v1/calls/c-1/cancel'] = () => new Promise((resolve) => {
    release = resolve;
  });
  const hanging = t.calls.hangUp();
  await flush();
  assert.equal(t.mic.opened[0].track.stopped, true, 'the microphone goes off at once');
  await t.timers.advance(2_000);
  await hanging;
  release({ state: 'ended' });
  await flush();
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/cancel').length, 1);
});

test('a cancel that lost the race with accept ends the call instead', async () => {
  const t = setup();
  await t.calls.start('r-1');
  t.server.overrides['POST /api/v1/calls/c-1/cancel'] = () => {
    t.server.move('connecting');
    throw apiError(409, 'conflict');
  };
  await t.calls.cancel();
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/end').length, 1);
  assert.equal(t.calls.view.state, 'ended');
});

test('closing a tab that only shows an incoming call does not reject it for the other tabs', async () => {
  const t = await ringingCallee();
  await t.calls.hangUp();
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/reject').length, 0);
  assert.equal(t.calls.view.state, 'ringing');
});

test('a reject that lost the race with accept on another device leaves that call alone', async () => {
  const t = await ringingCallee();
  t.server.overrides['POST /api/v1/calls/c-1/reject'] = () => {
    t.server.move('connecting');
    throw apiError(409, 'conflict');
  };
  await t.calls.reject();
  await flush();
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/end').length, 0);
  assert.equal(t.calls.view.state, 'connecting');
  assert.equal(t.calls.view.owned, false);
  assert.match(t.calls.view.error, /другом устройстве/);
});

test('hanging up while this tab accepts ends the call the accept connected', async () => {
  const t = await ringingCallee();
  let answerAccept;
  t.server.overrides['POST /api/v1/calls/c-1/accept'] = () => new Promise((resolve) => {
    answerAccept = () => {
      t.server.move('connecting');
      resolve({ state: 'connecting' });
    };
  });
  t.server.overrides['POST /api/v1/calls/c-1/reject'] = () => {
    throw apiError(409, 'conflict');
  };
  const accepting = t.calls.accept();
  await flush();
  const hanging = t.calls.hangUp();
  await flush();
  answerAccept();
  await accepting;
  await hanging;
  await flush();
  assert.equal(t.api.sent('POST', '/api/v1/calls/c-1/end').length, 1);
  assert.equal(t.calls.view.state, 'ended');
  assert.equal(t.mic.opened[0].track.stopped, true);
  assert.equal(t.pcs.created.length, 0);
});
