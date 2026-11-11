import { test } from 'node:test';
import assert from 'node:assert/strict';

import { decodeCandidate, encodeCandidate, openMicrophone } from '../static/app/call-media.mjs';
import { candidate } from './callfakes.mjs';

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
