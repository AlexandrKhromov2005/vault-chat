import { test } from 'node:test';
import assert from 'node:assert/strict';

import { PeerLink, decodeCandidate, encodeCandidate, openMicrophone } from '../static/app/call-media.mjs';
import { FakePeerConnection, candidate } from './callfakes.mjs';

test('candidates round-trip and malformed ones are refused', () => {
  const payload = encodeCandidate(candidate(1));
  assert.deepEqual(decodeCandidate(payload), candidate(1));
  assert.equal(encodeCandidate({ ...candidate(1), candidate: 'x'.repeat(600) }), null, 'over 512 bytes');
  for (const bad of [
    'null', '[]', '"text"', '{}', '{"candidate":""}', '{"candidate":"c"}',
    JSON.stringify({ ...candidate(1), sdpMid: 5 }),
    JSON.stringify({ ...candidate(1), sdpMLineIndex: 1.5 }),
    JSON.stringify({ ...candidate(1), usernameFragment: [] }),
    `{"candidate":"${'x'.repeat(520)}","sdpMid":"0"}`,
    42,
  ]) {
    assert.equal(decodeCandidate(bad), null, String(bad).slice(0, 40));
  }
});

test('microphone failures are told apart', async () => {
  const failing = (name) => ({ getUserMedia: async () => { throw Object.assign(new Error('x'), { name }); } });
  const reason = (devices) => openMicrophone(devices).then(() => 'opened', (err) => err.reason);
  assert.equal(await reason(undefined), 'unsupported');
  assert.equal(await reason(failing('NotAllowedError')), 'denied');
  assert.equal(await reason(failing('SecurityError')), 'denied');
  assert.equal(await reason(failing('NotFoundError')), 'missing');
  assert.equal(await reason(failing('NotReadableError')), 'busy');
  assert.equal(await reason(failing('TypeError')), 'failed');
});

function link(pc, handlers = {}) {
  return new PeerLink({
    role: 'caller',
    stream: { getTracks: () => [] },
    createPeerConnection: () => pc,
    send: () => {},
    onConnected: () => {},
    onRemoteStream: () => {},
    onFailed: () => {},
    ...handlers,
  });
}

test('a relay is recognized in the selected pair of either browser stats shape', async () => {
  const pc = new FakePeerConnection({});
  const peer = link(pc);
  assert.equal(await peer.usesRelay(), false, 'no stats yet');
  pc.relay('local');
  assert.equal(await peer.usesRelay(), true);
  pc.relay('remote');
  assert.equal(await peer.usesRelay(), true);
  pc.relay('none');
  assert.equal(await peer.usesRelay(), false);
  // Firefox marks the pair itself instead of naming it in the transport.
  pc.stats = new Map([
    ['p', { id: 'p', type: 'candidate-pair', selected: true, localCandidateId: 'l', remoteCandidateId: 'r' }],
    ['q', { id: 'q', type: 'candidate-pair', localCandidateId: 'x', remoteCandidateId: 'r' }],
    ['l', { id: 'l', type: 'local-candidate', candidateType: 'host' }],
    ['x', { id: 'x', type: 'local-candidate', candidateType: 'relay' }],
    ['r', { id: 'r', type: 'remote-candidate', candidateType: 'host' }],
  ]);
  assert.equal(await peer.usesRelay(), false, 'only the selected pair counts');
  pc.getStats = async () => { throw new Error('closed'); };
  assert.equal(await peer.usesRelay(), false);
});

test('an ICE restart waits for the answer to an earlier offer', async () => {
  const pc = new FakePeerConnection({});
  const sent = [];
  const peer = link(pc, { send: (kind, payload) => sent.push([kind, payload]) });
  await peer.start();
  assert.equal(await peer.restart(), false);
  await peer.receive('answer', 'v=0 answer');
  assert.equal(await peer.restart([{ urls: ['turn:t.example'] }]), true);
  assert.deepEqual(pc.offers, [{}, { iceRestart: true }]);
  assert.deepEqual(pc.config.iceServers, [{ urls: ['turn:t.example'] }]);
  assert.equal(pc.config.bundlePolicy, 'max-bundle', 'the rest of the configuration stays');
  assert.deepEqual(sent.map(([kind]) => kind), ['offer', 'offer']);
  await peer.rollback();
  assert.equal(pc.signalingState, 'stable');
});

test('a connection that was up reports its loss and its return, and fails only before', async () => {
  const pc = new FakePeerConnection({});
  const seen = [];
  link(pc, {
    onConnected: () => seen.push('connected'),
    onLost: (failed) => seen.push(failed ? 'failed' : 'disconnected'),
    onRestored: () => seen.push('restored'),
    onFailed: (reason) => seen.push(`fail:${reason}`),
  });
  pc.connect();
  pc.disconnect();
  pc.disconnect();
  pc.failIce();
  pc.checking();
  pc.connect();
  pc.connect();
  assert.deepEqual(seen, ['connected', 'disconnected', 'failed', 'restored']);
});

test('local offer failures are reported instead of being swallowed', async () => {
  for (const method of ['createOffer', 'setLocalDescription']) {
    const pc = new FakePeerConnection({});
    pc[method] = async () => { throw new Error('browser rejected the local description'); };
    let failure = '';
    const link = new PeerLink({
      role: 'caller',
      stream: { getTracks: () => [] },
      createPeerConnection: () => pc,
      send: () => {},
      onConnected: () => {},
      onRemoteStream: () => {},
      onFailed: (reason) => { failure = reason; },
    });

    await link.start();

    assert.equal(failure, 'sdp', method);
  }
});
