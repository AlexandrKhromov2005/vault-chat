// A Calls server for one call, c-1, and a Calls client wired to it with
// fake timers, microphone and peer connections.

import { Calls } from '../static/app/calls.mjs';
import { FakeTimers } from './fakes.mjs';
import { fakeMicrophone, peerConnections, routedApi } from './callfakes.mjs';
import { signedIn } from './ui.mjs';

export const flush = async (rounds = 10) => {
  for (let i = 0; i < rounds; i++) await new Promise((resolve) => setImmediate(resolve));
};

// callServer plays Calls for call c-1 between u-1 (the user) and u-2.
// overrides["METHOD path"](body) may answer a request instead, or return
// undefined to let the server handle it.
export function callServer({ role }) {
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
        // A repeated sender_seq returns the signal_seq it got first.
        const known = server.signals.find((s) => s.sender_device === 'd-me' && s.sender_seq === body.sender_seq);
        if (known) return { state: server.state, signal_seq: known.signal_seq };
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

export function setup({ role = 'caller', micError = null, gatherOnLocal = null, isOnline } = {}) {
  const server = callServer({ role });
  const api = routedApi(server.handle);
  const mic = fakeMicrophone({ error: micError });
  const pcs = peerConnections({ gatherOnLocal });
  const timers = new FakeTimers();
  let ids = 0;
  const session = signedIn('u-1', 'alice');
  const calls = new Calls({ api, session, mediaDevices: mic, createPeerConnection: pcs.create, timers, newId: () => `cmd-${++ids}`, isOnline });
  const signalsSent = () => api.sent('POST', '/api/v1/calls/c-1/signal').map((r) => r.body);
  return { server, api, mic, pcs, timers, calls, signalsSent };
}

// ringingCallee sets up an incoming call that the user has not answered.
export async function ringingCallee() {
  const t = setup({ role: 'callee' });
  t.server.move('ringing');
  t.calls.handleEvent({ type: 'call.ringing', call_id: 'c-1' });
  await flush();
  return t;
}

export async function acceptedCallee() {
  const t = await ringingCallee();
  await t.calls.accept();
  await flush();
  return t;
}
