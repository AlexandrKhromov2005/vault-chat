import assert from 'node:assert/strict';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile } from 'node:fs/promises';

const execFileAsync = promisify(execFile);

const base = process.env.GATEWAY_URL;
const project = process.env.PROJECT;
if (!base || !project) throw new Error('GATEWAY_URL and PROJECT are required; use a disposable local stack');

async function api(method, path, { token, body } = {}) {
  const response = await fetch(base + path, {
    method,
    headers: { ...(body !== undefined ? { 'content-type': 'application/json' } : {}), ...(token ? { authorization: `Bearer ${token}` } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json().catch(() => null);
  if (!response.ok) {
    const error = new Error(`${method} ${path}: ${response.status} ${JSON.stringify(data)}`);
    error.status = response.status;
    throw error;
  }
  return { status: response.status, data };
}

function invite(email) {
  const environment = email.startsWith('alice') ? process.env.INVITE_ALICE : process.env.INVITE_BOB;
  if (environment) return environment;
  if (process.env.SKIP_DOCKER) throw new Error(`INVITE_ALICE/INVITE_BOB are required without Docker access for ${email}`);
  return JSON.parse(execFileSync('docker', ['exec', `${project}-auth-1`, '/usr/local/bin/invite', email], { encoding: 'utf8' })).invitation_token;
}

async function user(name) {
  const email = `${name}@example.test`;
  await api('POST', '/api/v1/auth/register', {
    body: { email, username: name, password: 'SyntheticPassword123', invitation_token: invite(email) },
  });
  const { data } = await api('POST', '/api/v1/auth/login', { body: { email, password: 'SyntheticPassword123' } });
  return { id: data.user_id, access: data.access_token, refresh: data.refresh_token };
}

const suffix = process.env.SUFFIX ?? Date.now();
const alice = await user(`alice${suffix}`);
const bob = await user(`bob${suffix}`);
const { data: room } = await api('POST', '/api/v1/rooms/direct', { token: alice.access, body: { peer_user_id: bob.id } });
const ws = new WebSocket(base.replace('http:', 'ws:').replace('https:', 'wss:') + '/api/v1/ws');
const events = [];
ws.addEventListener('message', (event) => events.push(JSON.parse(event.data)));
await new Promise((resolve, reject) => {
  ws.addEventListener('open', resolve, { once: true });
  ws.addEventListener('error', reject, { once: true });
});
ws.send(JSON.stringify({ type: 'auth', token: bob.access }));

async function event(type, timeout = 7000) {
  const started = Date.now();
  while (Date.now() - started < timeout) {
    const index = events.findIndex((event) => event.type === type);
    if (index >= 0) return events.splice(index, 1)[0];
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`missing ${type}`);
}

await event('ready');
const { data: created } = await api('POST', '/api/v1/calls', {
  token: alice.access, body: { room_id: room.room_id, command_id: crypto.randomUUID() },
});
assert.equal(created.state, 'ringing');
assert.equal((await event('call.created')).call_id, created.call_id);
await api('POST', `/api/v1/calls/${created.call_id}/accept`, { token: bob.access, body: { command_id: crypto.randomUUID() } });
await event('call.accepted');
const { data: turn } = await api('POST', `/api/v1/calls/${created.call_id}/turn`, { token: alice.access, body: {} });
assert.equal(turn.ttl, 600);
assert.ok(turn.ice_servers[0].username.endsWith(`:${created.call_id}:${alice.id}`));
if (!process.env.SKIP_TURN) {
  if (process.env.SKIP_DOCKER) throw new Error('TURN allocation requires Docker access; set SKIP_TURN=1 for API-only acceptance');
  execFileSync('docker', ['exec', `${project}-coturn-1`, 'turnutils_uclient', '-u', turn.ice_servers[0].username,
    '-w', turn.ice_servers[0].credential, '-n', '1', '-O', '-e', '127.0.0.1', '127.0.0.1'], { stdio: 'pipe' });
  console.log('TURN allocation passed');
}
await api('POST', `/api/v1/calls/${created.call_id}/connected`, { token: alice.access, body: { command_id: crypto.randomUUID() } });
const { data: active } = await api('POST', `/api/v1/calls/${created.call_id}/connected`, {
  token: bob.access, body: { command_id: crypto.randomUUID() },
});
assert.equal(active.state, 'active');
const { data: signaled } = await api('POST', `/api/v1/calls/${created.call_id}/signal`, {
  token: alice.access, body: { kind: 'offer', payload: 'synthetic-sdp', sender_seq: 1 },
});
assert.equal(signaled.signal_seq, 1);
assert.equal((await event('call.signal')).signal.kind, 'offer');
await api('POST', `/api/v1/calls/${created.call_id}/end`, { token: alice.access, body: { command_id: crypto.randomUUID() } });
await event('call.ended');
await assert.rejects(api('GET', `/api/v1/calls/${created.call_id}`, { token: alice.access }), /404/);

const { data: second } = await api('POST', '/api/v1/calls', {
  token: alice.access, body: { room_id: room.room_id, command_id: crypto.randomUUID() },
});
await api('POST', `/api/v1/calls/${second.call_id}/accept`, { token: bob.access, body: { command_id: crypto.randomUUID() } });
await api('POST', '/api/v1/auth/logout', { body: { refresh_token: bob.refresh } });
const deadline = Date.now() + 10000;
let revoked = false;
while (Date.now() < deadline && !revoked) {
  try {
    await api('GET', `/api/v1/calls/${second.call_id}`, { token: alice.access });
  } catch (error) {
    if (error.status === 404) revoked = true;
    else if (error.status !== 429) throw error;
  }
  if (!revoked) await new Promise((resolve) => setTimeout(resolve, 1000));
}
assert.equal(revoked, true, 'logout did not end the call');

if (process.env.DRAIN_TEST) {
  const hostRestart = process.env.CALLS_PID_FILE && process.env.CALLS_RESTART_SCRIPT;
  if (process.env.SKIP_DOCKER && !hostRestart) {
    throw new Error('DRAIN_TEST requires Docker access or CALLS_PID_FILE and CALLS_RESTART_SCRIPT');
  }
  const { data: bobLogin } = await api('POST', '/api/v1/auth/login', {
    body: { email: `bob${suffix}@example.test`, password: 'SyntheticPassword123' },
  });
  bob.access = bobLogin.access_token;
  const { data: third } = await api('POST', '/api/v1/calls', {
    token: alice.access, body: { room_id: room.room_id, command_id: crypto.randomUUID() },
  });
  await api('POST', `/api/v1/calls/${third.call_id}/accept`, { token: bob.access, body: { command_id: crypto.randomUUID() } });
  let stopping;
  if (hostRestart) {
    const pid = Number((await readFile(process.env.CALLS_PID_FILE, 'utf8')).trim());
    if (!Number.isSafeInteger(pid) || pid <= 1) throw new Error('Invalid Calls PID');
    process.kill(pid, 'SIGTERM');
    stopping = (async () => {
      const deadline = Date.now() + 16000;
      while (Date.now() < deadline) {
        try {
          const { stdout } = await execFileAsync('ps', ['-p', String(pid), '-o', 'stat=']);
          if (!stdout.trim() || /^[ZX]/.test(stdout.trim())) return;
        } catch (error) {
          if (error.code === 'ESRCH' || error.code === 1) return;
          throw error;
        }
        await new Promise((resolve) => setTimeout(resolve, 100));
      }
      throw new Error('Calls did not stop within the drain budget');
    })();
  } else {
    stopping = execFileAsync('docker', ['stop', '-t', '15', `${project}-calls-1`]);
  }
  await new Promise((resolve) => setTimeout(resolve, 500));
  await assert.rejects(api('POST', '/api/v1/calls', {
    token: alice.access, body: { room_id: room.room_id, command_id: crypto.randomUUID() },
  }), (error) => error.status === 503);
  const { data: beforeRestart } = await api('GET', `/api/v1/calls/${third.call_id}`, { token: alice.access });
  assert.equal(beforeRestart.state, 'connecting');
  await stopping;
  if (hostRestart) await execFileAsync(process.env.CALLS_RESTART_SCRIPT);
  else await execFileAsync('docker', ['start', `${project}-calls-1`]);
  let recovered = false;
  const restartDeadline = Date.now() + 30000;
  while (Date.now() < restartDeadline && !recovered) {
    try {
      const { data } = await api('GET', `/api/v1/calls/${third.call_id}`, { token: alice.access });
      assert.equal(data.state, 'connecting');
      recovered = true;
    } catch (error) {
      if (![404, 429, 503].includes(error.status)) throw error;
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  }
  assert.equal(recovered, true, 'accepted call did not recover after drain/restart');
  await api('POST', `/api/v1/calls/${third.call_id}/end`, { token: alice.access, body: { command_id: crypto.randomUUID() } });
  console.log('Calls drain and restart acceptance passed');
}

ws.close();
console.log('Calls live acceptance passed: two users, events, TURN credentials, signaling, end and revoke');
