import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const base = process.env.GATEWAY_URL ?? 'http://127.0.0.1:8080';
const origin = new URL(base);
if (!['127.0.0.1', 'localhost', '::1'].includes(origin.hostname)) throw new Error('Browser acceptance is loopback-only');
const project = process.env.PROJECT;
const chrome = process.env.CHROME_BIN ?? '/usr/bin/google-chrome';
const work = await mkdtemp(join(tmpdir(), 'vault-calls-browser-'));
const browsers = [];
let traceInterval;
const forceRelay = Boolean(process.env.FORCE_RELAY);
const reconnectTest = Boolean(process.env.RECONNECT_TEST);
const turnRestartTest = Boolean(process.env.TURN_RESTART_TEST);
const androidPeer = Boolean(process.env.SKIP_CALLEE);
if (reconnectTest && androidPeer) throw new Error('RECONNECT_TEST requires a browser callee');
if (turnRestartTest && (!forceRelay || !project || androidPeer)) throw new Error('TURN_RESTART_TEST requires FORCE_RELAY, PROJECT and two browsers');
if (forceRelay && reconnectTest) throw new Error('FORCE_RELAY and RECONNECT_TEST are separate scenarios');

async function api(method, path, { token, body } = {}) {
  const response = await fetch(base + path, {
    method,
    headers: { ...(body ? { 'content-type': 'application/json' } : {}), ...(token ? { authorization: `Bearer ${token}` } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await response.json().catch(() => null);
  if (!response.ok) {
    const error = new Error(`${method} ${path}: ${response.status}`);
    error.status = response.status;
    throw error;
  }
  return data;
}

function invite(email, role) {
  const supplied = role === 'alice' ? process.env.INVITE_ALICE : process.env.INVITE_BOB;
  if (supplied) return supplied;
  if (!project) throw new Error('PROJECT or invitation environment variables are required');
  return JSON.parse(execFileSync('docker', ['exec', `${project}-auth-1`, '/usr/local/bin/invite', email], { encoding: 'utf8' })).invitation_token;
}

async function makeUser(role, suffix) {
  const email = `${role}${suffix}@example.test`;
  const username = `${role}${suffix}`.slice(0, 32);
  await api('POST', '/api/v1/auth/register', {
    body: { email, username, password: 'SyntheticPassword123', invitation_token: invite(email, role) },
  });
  const pair = await api('POST', '/api/v1/auth/login', { body: { email, password: 'SyntheticPassword123' } });
  return {
    pair,
    state: { userId: pair.user_id, username, accessToken: pair.access_token,
      refreshToken: pair.refresh_token, accessExpiresAt: Date.parse(pair.access_expires_at) },
  };
}

class Cdp {
  constructor(url) {
    this.url = url;
    this.nextId = 0;
    this.pending = new Map();
  }

  async open() {
    this.ws = new WebSocket(this.url);
    await new Promise((resolve, reject) => {
      this.ws.addEventListener('open', resolve, { once: true });
      this.ws.addEventListener('error', reject, { once: true });
    });
    this.ws.addEventListener('message', (event) => {
      const message = JSON.parse(event.data);
      if (!message.id) return;
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      if (message.error) pending.reject(new Error(`${pending.method}: ${message.error.message}`));
      else pending.resolve(message.result);
    });
  }

  call(method, params = {}, sessionId) {
    const id = ++this.nextId;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, method });
      this.ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
    });
  }

  close() { this.ws.close(); }
}

async function launchBrowser(name, { fakeUi = true } = {}) {
  const profile = join(work, name);
  const process = spawn(chrome, [
    '--headless=new', '--disable-gpu', '--disable-background-networking', '--disable-extensions',
    '--no-first-run', '--no-default-browser-check', '--remote-debugging-port=0',
    ...(globalThis.process.env.ALLOW_ALL_WEBRTC_IP ? [] : ['--force-webrtc-ip-handling-policy=default_public_interface_only']),
    `--user-data-dir=${profile}`, ...(fakeUi ? ['--use-fake-ui-for-media-stream'] : []),
    '--use-fake-device-for-media-stream', '--autoplay-policy=no-user-gesture-required', 'about:blank',
  ], { stdio: ['ignore', 'ignore', 'pipe'] });
  browsers.push(process);
  const url = await new Promise((resolve, reject) => {
    let output = '';
    const timer = setTimeout(() => reject(new Error(`Chromium ${name} startup timed out`)), 15000);
    process.once('exit', (code) => { clearTimeout(timer); reject(new Error(`Chromium ${name} exited: ${code}`)); });
    process.stderr.on('data', (chunk) => {
      output += chunk.toString();
      const match = output.match(/DevTools listening on (ws:\/\/[^\s]+)/);
      if (match) { clearTimeout(timer); resolve(match[1]); }
    });
  });
  const cdp = new Cdp(url);
  await cdp.open();
  const { targetId } = await cdp.call('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await cdp.call('Target.attachToTarget', { targetId, flatten: true });
  const page = { cdp, sessionId };
  await cdp.call('Page.enable', {}, sessionId);
  await cdp.call('Runtime.enable', {}, sessionId);
  return page;
}

async function evaluate(page, expression) {
  const result = await page.cdp.call('Runtime.evaluate', {
    expression, awaitPromise: true, returnByValue: true,
  }, page.sessionId);
  if (result.exceptionDetails) throw new Error('Browser evaluation failed');
  return result.result?.value;
}

async function waitFor(page, expression, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await evaluate(page, expression)) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Browser condition timed out: ${expression.slice(0, 120)}`);
}

async function openWorkspace(page, state) {
  await page.cdp.call('Page.addScriptToEvaluateOnNewDocument', {
    source: `
      sessionStorage.setItem('vault-chat.session', ${JSON.stringify(JSON.stringify(state))});
      window.__callPeers = [];
      window.__callSockets = [];
      window.__callReady = 0;
      window.__candidateAdds = 0;
      window.__endCandidateAdds = 0;
      window.__callIceLoss = 0;
      window.__callTrace = [];
      const NativePeer = RTCPeerConnection;
      window.RTCPeerConnection = class extends NativePeer {
        constructor(...args) {
          if (${forceRelay} && args[0]) args[0] = { ...args[0], iceTransportPolicy: 'relay' };
          if (${reconnectTest} && args[0]) args[0] = { ...args[0], iceServers: [] };
          super(...args);
          window.__callPeers.push(this);
          if (${forceRelay}) {
            const configure = this.setConfiguration.bind(this);
            this.setConfiguration = (config) => configure({ ...config, iceTransportPolicy: 'relay' });
          }
          this.addEventListener('iceconnectionstatechange', () => {
            if (this.iceConnectionState === 'disconnected' || this.iceConnectionState === 'failed') window.__callIceLoss++;
          });
          if (${Boolean(process.env.TRACE_ICE)}) {
            const state = () => window.__callTrace.push({ at: performance.now(), connection: this.connectionState,
              ice: this.iceConnectionState, gathering: this.iceGatheringState });
            this.addEventListener('connectionstatechange', state);
            this.addEventListener('iceconnectionstatechange', state);
            this.addEventListener('icegatheringstatechange', state);
            this.addEventListener('icecandidate', (event) => {
              window.__callTrace.push({ candidate: event.candidate?.candidate?.match(/typ\s+(\S+)/)?.[1] ?? 'end' });
            });
          }
          const add = this.addIceCandidate.bind(this);
          this.addIceCandidate = (...args) => {
            window.__candidateAdds++;
            if (args.length === 0 || args[0] === undefined) window.__endCandidateAdds++;
            return add(...args);
          };
        }
      };
      const NativeSocket = WebSocket;
      window.WebSocket = class extends NativeSocket {
        constructor(...args) {
          super(...args);
          window.__callSockets.push(this);
          this.addEventListener('message', (event) => {
            try { if (JSON.parse(event.data).type === 'ready') window.__callReady++; } catch {}
          });
        }
      };
    `,
  }, page.sessionId);
  await page.cdp.call('Page.navigate', { url: base }, page.sessionId);
  await waitFor(page, `document.readyState === 'complete' && !document.getElementById('workspace').hidden`);
  await waitFor(page, `Boolean(document.querySelector('.room-item'))`);
  await evaluate(page, `document.querySelector('.room-item').click()`);
  await waitFor(page, `!document.getElementById('room').hidden`);
}

async function iceDiagnostics(page) {
  return evaluate(page, `(async () => {
    const pc = window.__callPeers[0];
    const report = await pc?.getStats().catch(() => null);
    const localTypes = {};
    const remoteTypes = {};
    const pairStates = {};
    report?.forEach((entry) => {
      if (entry.type === 'local-candidate') localTypes[entry.candidateType] = (localTypes[entry.candidateType] ?? 0) + 1;
      if (entry.type === 'remote-candidate') remoteTypes[entry.candidateType] = (remoteTypes[entry.candidateType] ?? 0) + 1;
      if (entry.type === 'candidate-pair') pairStates[entry.state] = (pairStates[entry.state] ?? 0) + 1;
    });
    return { state: document.getElementById('call').dataset.state,
      error: document.getElementById('call-error').textContent,
      connection: pc?.connectionState, ice: pc?.iceConnectionState,
      gathering: pc?.iceGatheringState, localTypes, remoteTypes, pairStates };
  })()`);
}

async function activeCall(token) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    const { calls } = await api('GET', '/api/v1/calls?active=true', { token });
    if (calls.length === 1) return calls[0];
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error('Expected exactly one active call');
}

async function mediaBytes(page) {
  return evaluate(page, `(async () => {
    const pc = window.__callPeers[0];
    if (!pc) return null;
    const report = await pc.getStats();
    let sent = 0, received = 0, selected = null, selectedId = null;
    const entries = new Map();
    report.forEach((entry) => {
      entries.set(entry.id, entry);
      if (entry.type === 'outbound-rtp' && entry.kind === 'audio') sent = entry.bytesSent ?? 0;
      if (entry.type === 'inbound-rtp' && entry.kind === 'audio') received = entry.bytesReceived ?? 0;
      if (entry.type === 'transport' && entry.selectedCandidatePairId) selectedId = entry.selectedCandidatePairId;
    });
    if (selectedId) selected = entries.get(selectedId);
    if (!selected) report.forEach((entry) => {
      if (entry.type === 'candidate-pair' && (entry.selected || (entry.nominated && entry.state === 'succeeded'))) selected = entry;
    });
    const local = selected ? entries.get(selected.localCandidateId) : null;
    const remote = selected ? entries.get(selected.remoteCandidateId) : null;
    return { sent, received, localType: local?.candidateType, remoteType: remote?.candidateType,
      relay: local?.candidateType === 'relay' || remote?.candidateType === 'relay' };
  })()`);
}

async function growingMedia(page, before) {
  const deadline = Date.now() + 10000;
  let current = before;
  while (Date.now() < deadline) {
    current = await mediaBytes(page);
    if (current.sent > before.sent && current.received > before.received) return current;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  return current;
}

async function endActiveCalls(token) {
  try {
    const { calls } = await api('GET', '/api/v1/calls?active=true', { token });
    for (const call of calls) {
      await api('POST', `/api/v1/calls/${call.call_id}/end`, {
        token, body: { command_id: crypto.randomUUID() },
      }).catch(() => {});
    }
  } catch {}
}

let saved = null;
if (process.env.STATE_FILE) {
  try {
    saved = JSON.parse(await readFile(process.env.STATE_FILE, 'utf8'));
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
}
const suffix = process.env.SUFFIX ?? saved?.suffix ?? Date.now();
let alice;
let bob;
try {
  let room;
  if (saved) {
    const refreshed = await api('POST', '/api/v1/auth/refresh', {
      body: { refresh_token: saved.alice.refresh_token },
    });
    saved.alice = refreshed;
    await writeFile(process.env.STATE_FILE, JSON.stringify(saved), { mode: 0o600 });
    alice = { pair: saved.alice, state: { userId: saved.alice.user_id, username: `alice${suffix}`,
      accessToken: saved.alice.access_token, refreshToken: saved.alice.refresh_token,
      accessExpiresAt: Date.parse(saved.alice.access_expires_at) } };
    bob = { pair: saved.bob };
    room = saved.room;
  } else {
    alice = await makeUser('alice', suffix);
    bob = await makeUser('bob', suffix);
    room = await api('POST', '/api/v1/rooms/direct', {
      token: alice.pair.access_token, body: { peer_user_id: bob.pair.user_id },
    });
    if (process.env.STATE_FILE) {
      await writeFile(process.env.STATE_FILE, JSON.stringify({ suffix, alice: alice.pair, bob: bob.pair, room }), { mode: 0o600 });
    }
  }
  assert.ok(room.room_id);

  const caller = await launchBrowser('caller');
  const callee = androidPeer ? null : await launchBrowser('callee');
  await openWorkspace(caller, alice.state);
  if (callee) await openWorkspace(callee, bob.state);
  await waitFor(caller, `!document.getElementById('call-start').hidden`);

  await evaluate(caller, `document.getElementById('call-start').click()`);
  if (process.env.TRACE_ICE) {
    traceInterval = setInterval(() => {
      for (const [name, page] of [['caller', caller], ['callee', callee]]) {
        if (page) iceDiagnostics(page).then((value) => console.log(name, JSON.stringify(value))).catch(() => {});
      }
    }, 2000);
  }
  if (callee) {
    await waitFor(callee, `!document.getElementById('call-accept').hidden`);
    await evaluate(callee, `document.getElementById('call-accept').click(); document.getElementById('call-accept').click()`);
  }
  const call = await activeCall(alice.pair.access_token);
  if (reconnectTest) {
    const socketsBefore = await evaluate(callee, `window.__callSockets.length`);
    const readyBefore = await evaluate(callee, `window.__callReady`);
    const candidatesBefore = await evaluate(callee, `window.__endCandidateAdds`);
    await evaluate(callee, `window.__callSockets.at(-1).close()`);
    await waitFor(callee, `window.__callSockets.at(-1).readyState === WebSocket.CLOSED`);
    let signals = [];
    for (let attempt = 0; attempt < 50; attempt++) {
      const response = await api('GET', `/api/v1/calls/${call.call_id}/signals?after=0`, { token: alice.pair.access_token });
      signals = response.signals ?? [];
      if (signals.some((signal) => signal.kind === 'offer')) break;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    const offer = signals.find((signal) => signal.kind === 'offer');
    assert.ok(offer);
    const senderSeq = Math.max(...signals.filter((signal) => signal.sender_device === offer.sender_device).map((signal) => signal.sender_seq));
    await api('POST', `/api/v1/calls/${call.call_id}/signal`, {
      token: alice.pair.access_token,
      body: { kind: 'end-of-candidates', payload: '', sender_seq: senderSeq + 100 },
    });
    await waitFor(callee, `window.__callSockets.length > ${socketsBefore} && window.__callReady > ${readyBefore}`);
    await waitFor(callee, `window.__endCandidateAdds === ${candidatesBefore + 1}`);
    assert.equal(await evaluate(callee, `window.__callPeers.length`), 1);
  }
  try {
    await waitFor(caller, `document.getElementById('call').dataset.state === 'active'`, Number(process.env.CALL_WAIT_MS ?? 20000));
  } catch (error) {
    const diagnostics = await iceDiagnostics(caller);
    console.error('Caller ICE diagnostics', diagnostics);
    if (process.env.TRACE_ICE) {
      console.error('Caller event trace', await evaluate(caller, 'window.__callTrace'));
      if (callee) console.error('Callee event trace', await evaluate(callee, 'window.__callTrace'));
    }
    throw error;
  }
  if (callee) await waitFor(callee, `document.getElementById('call').dataset.state === 'active'`);
  await waitFor(caller, `document.getElementById('call-audio').srcObject?.getAudioTracks().length === 1`);
  if (callee) await waitFor(callee, `document.getElementById('call-audio').srcObject?.getAudioTracks().length === 1`);
  assert.equal(await evaluate(caller, `window.__callPeers.length`), 1);
  if (callee) assert.equal(await evaluate(callee, `window.__callPeers.length`), 1);

  assert.equal((await activeCall(alice.pair.access_token)).state, 'active');
  const beforeCaller = await mediaBytes(caller);
  const beforeCallee = callee ? await mediaBytes(callee) : null;
  await new Promise((resolve) => setTimeout(resolve, Number(process.env.HOLD_ACTIVE_MS ?? 1500)));
  const afterCaller = await growingMedia(caller, beforeCaller);
  const afterCallee = callee ? await growingMedia(callee, beforeCallee) : null;
  assert.ok(afterCaller.sent > beforeCaller.sent, 'caller produced no audio packets');
  assert.ok(afterCaller.received > beforeCaller.received, 'caller received no audio packets');
  if (callee) {
    assert.ok(afterCallee.sent > beforeCallee.sent, 'callee produced no audio packets');
    assert.ok(afterCallee.received > beforeCallee.received, 'callee received no audio packets');
  }
  if (forceRelay) {
    console.log('Relay stats', { caller: afterCaller, callee: afterCallee });
    assert.equal(afterCaller.relay, true, 'caller did not select a TURN relay');
    if (callee) assert.equal(afterCallee.relay, true, 'callee did not select a TURN relay');
  }

  if (turnRestartTest) {
    const lossBefore = await evaluate(caller, 'window.__callIceLoss');
    const calleeLossBefore = await evaluate(callee, 'window.__callIceLoss');
    execFileSync('docker', ['restart', `${project}-coturn-1`], { stdio: 'pipe' });
    await waitFor(caller, `window.__callIceLoss > ${lossBefore}`, 30000);
    await waitFor(callee, `window.__callIceLoss > ${calleeLossBefore}`, 30000);
    await waitFor(caller, `document.getElementById('call').dataset.state === 'active' && ['connected', 'completed'].includes(window.__callPeers[0].iceConnectionState)`, 60000);
    await waitFor(callee, `document.getElementById('call').dataset.state === 'active' && ['connected', 'completed'].includes(window.__callPeers[0].iceConnectionState)`, 60000);
    const recoveredCaller = await growingMedia(caller, afterCaller);
    const recoveredCallee = await growingMedia(callee, afterCallee);
    assert.ok(recoveredCaller.sent > afterCaller.sent && recoveredCaller.received > afterCaller.received, 'caller RTP did not recover');
    assert.ok(recoveredCallee.sent > afterCallee.sent && recoveredCallee.received > afterCallee.received, 'callee RTP did not recover');
    assert.equal(recoveredCaller.relay, true, 'caller recovery left TURN');
    assert.equal(recoveredCallee.relay, true, 'callee recovery left TURN');
    assert.equal(await evaluate(caller, 'window.__callPeers.length'), 1);
    assert.equal(await evaluate(callee, 'window.__callPeers.length'), 1);
  }

  if (reconnectTest) {
    await evaluate(callee, `window.__callSockets.at(-1).close()`);
    await waitFor(callee, `window.__callSockets.at(-1).readyState === WebSocket.CLOSED`);
  }

  if (androidPeer) {
    await waitFor(caller, `document.getElementById('call').dataset.state === 'ended'`, Number(process.env.CALL_WAIT_MS ?? 20000));
  } else {
    await evaluate(caller, `document.getElementById('call-end').click(); document.getElementById('call-end').click()`);
    await waitFor(caller, `document.getElementById('call').dataset.state === 'ended'`);
  }
  if (callee) await waitFor(callee, `document.getElementById('call').dataset.state === 'ended'`);
  await waitFor(caller, `document.getElementById('call-audio').srcObject === null`);
  if (callee) await waitFor(callee, `document.getElementById('call-audio').srcObject === null`);
  for (const page of callee ? [caller, callee] : [caller]) {
    const cleanup = await evaluate(page, `(() => {
      const pc = window.__callPeers[0];
      return pc.connectionState === 'closed' && pc.getSenders().every((sender) => sender.track?.readyState === 'ended');
    })()`);
    assert.equal(cleanup, true, 'microphone or peer connection remained active');
  }
  await assert.rejects(api('GET', `/api/v1/calls/${call.call_id}`, { token: alice.pair.access_token }), /404/);

  const denied = await launchBrowser('denied', { fakeUi: false });
  await denied.cdp.call('Browser.setPermission', {
    origin: base, permission: { name: 'microphone' }, setting: 'denied',
  });
  await openWorkspace(denied, alice.state);
  await waitFor(denied, `!document.getElementById('call-start').hidden`);
  await evaluate(denied, `document.getElementById('call-start').click()`);
  await waitFor(denied, `document.getElementById('call-error').textContent.includes('микрофон')`);
  assert.equal((await api('GET', '/api/v1/calls?active=true', { token: alice.pair.access_token })).calls.length, 0);
  assert.equal(await evaluate(denied, `window.__callPeers.length`), 0, 'permission denial created a peer connection');

  caller.cdp.close();
  callee?.cdp.close();
  denied.cdp.close();
  console.log(`Two-browser audio acceptance passed: real bidirectional RTP, one peer per device, end cleanup, permission denial${forceRelay ? ', forced TURN relay' : ''}${reconnectTest ? ', WebSocket signal/end reconciliation' : ''}${turnRestartTest ? ', coturn restart and RTP recovery' : ''}`);
} finally {
  clearInterval(traceInterval);
  if (alice) await endActiveCalls(alice.pair.access_token);
  await Promise.all(browsers.map(async (process) => {
    if (process.exitCode !== null) return;
    const exited = new Promise((resolve) => process.once('exit', resolve));
    process.kill('SIGTERM');
    await Promise.race([exited, new Promise((resolve) => setTimeout(resolve, 5000))]);
    if (process.exitCode === null) {
      process.kill('SIGKILL');
      await exited;
    }
  }));
  await new Promise((resolve) => setTimeout(resolve, 500));
  await rm(work, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
}
