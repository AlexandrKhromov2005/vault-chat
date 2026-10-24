import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Api } from '../static/app/api.mjs';
import { Session } from '../static/app/session.mjs';
import { apiError, deferred, fakeFetch, memoryStorage } from './fakes.mjs';

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

  await session.register('alice@example.com', 'alice', 'password1', 'invite-token');

  assert.deepEqual(fetchFn.calls[0].body, { email: 'alice@example.com', username: 'alice', password: 'password1', invitation_token: 'invite-token' });
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

test('validAccessToken can demand a longer remaining validity', async () => {
  // The WebSocket renews 90 s before expiry and needs a new token by then,
  // not the one already on the connection.
  let now = NOW;
  const fetchFn = fakeFetch([tokenPair(1), me, tokenPair(2)]);
  const session = new Session({ fetchFn, storage: memoryStorage(), now: () => now });
  await session.login('alice@example.com', 'password1');
  now = NOW + 15 * 60_000 - 90_000;

  assert.equal(await session.validAccessToken(), 'access-1');
  assert.equal(await session.validAccessToken(90_000), 'access-2');
});

// Races between a refresh in flight and the user signing out or switching
// accounts. A late answer belongs to a session that no longer exists and
// must neither restore it nor end the new one.

const bobPair = {
  status: 200,
  body: { ...tokenPair('bob').body, user_id: 'u-2' },
};
const bobMe = { status: 200, body: { user_id: 'u-2', email: 'bob@example.com', username: 'bob' } };

test('a refresh answered after logout does not restore the session', async () => {
  const late = deferred();
  const { session, storage } = newSession([tokenPair(1), me, () => late.promise, { status: 204 }]);
  await session.login('alice@example.com', 'password1');

  const refreshing = session.refresh();
  await session.logout();
  late.resolve(tokenPair(2));

  await assert.rejects(refreshing, { status: 401 });
  assert.equal(session.user, null);
  assert.equal(storage.size, 0);
});

test('a refresh of the previous account does not replace the next one', async () => {
  const late = deferred();
  const { session, storage } = newSession([tokenPair(1), me, () => late.promise, { status: 204 }, bobPair, bobMe]);
  await session.login('alice@example.com', 'password1');

  const refreshing = session.refresh();
  await session.logout();
  await session.login('bob@example.com', 'password1');
  late.resolve(tokenPair(2));

  await assert.rejects(refreshing, { status: 401 });
  assert.deepEqual(session.user, { id: 'u-2', username: 'bob' });
  assert.equal(session.accessToken, 'access-bob');
  assert.match(storage.getItem('vault-chat.session'), /access-bob/);
});

test('a late 401 of the previous account does not sign the next one out', async () => {
  const late = deferred();
  const { session } = newSession([tokenPair(1), me, () => late.promise, { status: 204 }, bobPair, bobMe]);
  await session.login('alice@example.com', 'password1');
  const refreshing = session.refresh();
  await session.logout();
  await session.login('bob@example.com', 'password1');
  let signedOut = false;
  session.addEventListener('signedout', () => {
    signedOut = true;
  });

  late.resolve(apiError(401, 'unauthenticated', 'invalid token'));

  await assert.rejects(refreshing, { status: 401 });
  assert.equal(signedOut, false);
  assert.deepEqual(session.user, { id: 'u-2', username: 'bob' });
});

test('the next account does not share the previous account refresh', async () => {
  const late = deferred();
  const { session, fetchFn } = newSession([
    tokenPair(1), me, () => late.promise, { status: 204 }, bobPair, bobMe, { ...bobPair, body: { ...bobPair.body, access_token: 'access-bob-2' } },
  ]);
  await session.login('alice@example.com', 'password1');
  const aliceRefresh = session.refresh();
  await session.logout();
  await session.login('bob@example.com', 'password1');

  assert.equal(await session.refresh(), 'access-bob-2');
  assert.deepEqual(fetchFn.calls.at(-1).body, { refresh_token: 'refresh-bob' });

  late.resolve(tokenPair(2));
  await assert.rejects(aliceRefresh, { status: 401 });
  assert.equal(session.accessToken, 'access-bob-2');
});

test('a request of the previous account is not repeated for the next one', async () => {
  // Review of #66, end to end: Alice's message, answered 401 after Bob
  // signed in, must not be refreshed and sent again with Bob's token.
  const late = deferred();
  const { session, fetchFn } = newSession([tokenPair(1), me, () => late.promise, { status: 204 }, bobPair, bobMe]);
  const api = new Api({ fetchFn, session });
  await session.login('alice@example.com', 'password1');
  const sending = api.call('POST', '/api/v1/rooms/r-1/messages', { body: { text: 'от Alice' } });
  await new Promise((resolve) => setImmediate(resolve)); // the message is on its way

  await session.logout();
  await session.login('bob@example.com', 'password1');
  late.resolve(apiError(401, 'unauthenticated', 'invalid or expired token'));

  await assert.rejects(sending, { status: 401 });
  assert.equal(fetchFn.calls.filter((call) => call.url === '/api/v1/rooms/r-1/messages').length, 1);
  assert.equal(fetchFn.calls.filter((call) => call.url === '/api/v1/auth/refresh').length, 0);
  assert.deepEqual(session.user, { id: 'u-2', username: 'bob' });
});
