import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Session } from '../static/app/session.mjs';
import { apiError, fakeFetch, memoryStorage } from './fakes.mjs';

const NOW = Date.parse('2026-10-18T12:00:00Z');

function tokenPair(n, accessMinutes = 15) {
  return {
    status: 200,
    body: {
      user_id: 'u-1',
      token_type: 'Bearer',
      access_token: `access-${n}`,
      refresh_token: `refresh-${n}`,
      access_expires_at: new Date(NOW + accessMinutes * 60_000).toISOString(),
      refresh_expires_at: new Date(NOW + 30 * 86_400_000).toISOString(),
    },
  };
}

const me = { status: 200, body: { user_id: 'u-1', email: 'alice@example.com', username: 'alice' } };

function newSession(responses, storage = memoryStorage()) {
  const fetchFn = fakeFetch(responses);
  const session = new Session({ fetchFn, storage, now: () => NOW });
  return { session, fetchFn, storage };
}

test('login stores the tokens and the username', async () => {
  const { session, fetchFn, storage } = newSession([tokenPair(1), me]);

  await session.login('alice@example.com', 'password1');

  assert.deepEqual(session.user, { id: 'u-1', username: 'alice' });
  assert.equal(session.accessToken, 'access-1');
  assert.deepEqual(fetchFn.calls.map((c) => `${c.method} ${c.url}`), [
    'POST /api/v1/auth/login',
    'GET /api/v1/auth/me',
  ]);
  assert.equal(fetchFn.calls[1].headers.Authorization, 'Bearer access-1');

  // Another page load in the same tab restores the session.
  const restored = new Session({ fetchFn: fakeFetch(), storage, now: () => NOW });
  assert.deepEqual(restored.user, { id: 'u-1', username: 'alice' });
  assert.equal(restored.accessToken, 'access-1');
});

test('failed login leaves the session signed out', async () => {
  const { session, storage } = newSession([apiError(401, 'unauthenticated', 'invalid credentials')]);

  await assert.rejects(session.login('alice@example.com', 'wrong'), { status: 401 });
  assert.equal(session.user, null);
  assert.equal(storage.size, 0);
});

test('register creates the account and signs in', async () => {
  const { session, fetchFn } = newSession([
    { status: 201, body: { user_id: 'u-1', email: 'alice@example.com', username: 'alice' } },
    tokenPair(1),
    me,
  ]);

  await session.register('alice@example.com', 'alice', 'password1');

  assert.deepEqual(fetchFn.calls[0].body, { email: 'alice@example.com', username: 'alice', password: 'password1' });
  assert.equal(session.user.username, 'alice');
});

test('refresh rotates the tokens', async () => {
  const { session, fetchFn } = newSession([tokenPair(1), me, tokenPair(2)]);
  await session.login('alice@example.com', 'password1');

  assert.equal(await session.refresh(), 'access-2');

  assert.deepEqual(fetchFn.calls[2].body, { refresh_token: 'refresh-1' });
  assert.equal(session.accessToken, 'access-2');
});

test('concurrent refreshes share one request', async () => {
  // Refresh tokens rotate: a second request with the same token would look
  // like theft to Auth and revoke every session.
  const { session, fetchFn } = newSession([tokenPair(1), me, tokenPair(2)]);
  await session.login('alice@example.com', 'password1');

  const results = await Promise.all([session.refresh(), session.refresh(), session.refresh()]);

  assert.deepEqual(results, ['access-2', 'access-2', 'access-2']);
  assert.equal(fetchFn.calls.length, 3);
});

test('a rejected refresh signs out', async () => {
  const { session, storage } = newSession([tokenPair(1), me, apiError(401, 'unauthenticated', 'invalid token')]);
  await session.login('alice@example.com', 'password1');
  const signedOut = new Promise((resolve) => session.addEventListener('signedout', resolve, { once: true }));

  await assert.rejects(session.refresh(), { status: 401 });

  await signedOut;
  assert.equal(session.user, null);
  assert.equal(storage.size, 0);
});

test('a network failure during refresh keeps the session', async () => {
  const { session } = newSession([tokenPair(1), me, new TypeError('Failed to fetch')]);
  await session.login('alice@example.com', 'password1');

  await assert.rejects(session.refresh(), { code: 'network' });

  assert.equal(session.accessToken, 'access-1');
});

test('validAccessToken refreshes shortly before expiry', async () => {
  let now = NOW;
  const fetchFn = fakeFetch([tokenPair(1), me, tokenPair(2)]);
  const session = new Session({ fetchFn, storage: memoryStorage(), now: () => now });
  await session.login('alice@example.com', 'password1');

  assert.equal(await session.validAccessToken(), 'access-1');
  now = NOW + 15 * 60_000 - 20_000; // 20 s before expiry
  assert.equal(await session.validAccessToken(), 'access-2');
});

test('logout ends the session on the server and locally', async () => {
  const { session, fetchFn, storage } = newSession([tokenPair(1), me, { status: 204 }]);
  await session.login('alice@example.com', 'password1');

  await session.logout();

  assert.deepEqual(fetchFn.calls[2], {
    url: '/api/v1/auth/logout',
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: { refresh_token: 'refresh-1' },
  });
  assert.equal(session.user, null);
  assert.equal(storage.size, 0);
});

test('logout signs out locally even when the server is unreachable', async () => {
  const { session } = newSession([tokenPair(1), me, new TypeError('Failed to fetch')]);
  await session.login('alice@example.com', 'password1');

  await session.logout();

  assert.equal(session.user, null);
});

test('ignores a corrupted stored session', () => {
  const session = new Session({
    fetchFn: fakeFetch(),
    storage: memoryStorage({ 'vault-chat.session': '{not json' }),
    now: () => NOW,
  });

  assert.equal(session.user, null);
});
