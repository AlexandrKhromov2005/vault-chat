// Runs the client modules against a running Gateway, like tests/compose.
// Skipped unless VAULT_CHAT_WEB_GATEWAY_URL is set, e.g.:
//
//   VAULT_CHAT_WEB_GATEWAY_URL=http://127.0.0.1:8080 node --test "web/test/*.test.mjs"

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';

import { Api } from '../static/app/api.mjs';
import { requestJSON } from '../static/app/http.mjs';
import { Session } from '../static/app/session.mjs';
import { memoryStorage } from './fakes.mjs';

const base = process.env.VAULT_CHAT_WEB_GATEWAY_URL;
const skip = base ? false : 'VAULT_CHAT_WEB_GATEWAY_URL is not set';

// The browser resolves the client's relative URLs against the page; here
// they are resolved against the Gateway under test.
const fetchFn = (path, init) => fetch(new URL(path, base), init);

function newUser() {
  const id = randomUUID().slice(0, 8);
  return { email: `web-${id}@example.com`, username: `web_${id}`, password: 'Password123' };
}

test('the Gateway serves the client', { skip }, async () => {
  const page = await fetch(new URL('/', base));
  assert.equal(page.status, 200);
  assert.match(page.headers.get('content-security-policy'), /script-src 'self'/);
  assert.match(await page.text(), /<script type="module" src="\/app\/app\.mjs"><\/script>/);

  const module = await fetch(new URL('/app/session.mjs', base));
  assert.equal(module.status, 200);
  assert.equal(module.headers.get('content-type'), 'text/javascript; charset=utf-8');
});

test('a user registers, refreshes and signs out', { skip }, async () => {
  const user = newUser();
  const session = new Session({ fetchFn, storage: memoryStorage() });
  const api = new Api({ fetchFn, session });

  await session.register(user.email, user.username, user.password);
  assert.equal(session.user.username, user.username);

  const me = await api.call('GET', '/api/v1/auth/me');
  assert.equal(me.user_id, session.user.id);

  const first = session.accessToken;
  await session.refresh();
  assert.notEqual(session.accessToken, first);
  assert.equal((await api.call('GET', '/api/v1/auth/me')).username, user.username);

  await session.logout();
  assert.equal(session.user, null);
});

test('signing in with a wrong password is rejected', { skip }, async () => {
  const user = newUser();
  const session = new Session({ fetchFn, storage: memoryStorage() });
  await session.register(user.email, user.username, user.password);
  await session.logout();

  await assert.rejects(session.login(user.email, 'WrongPassword1'), { status: 401, code: 'unauthenticated' });
  assert.equal(session.user, null);
});

test('a refresh token stops working after logout', { skip }, async () => {
  const user = newUser();
  const storage = memoryStorage();
  const session = new Session({ fetchFn, storage });
  await session.register(user.email, user.username, user.password);
  const { refreshToken } = JSON.parse(storage.getItem('vault-chat.session'));

  await session.logout();

  await assert.rejects(
    requestJSON(fetchFn, 'POST', '/api/v1/auth/refresh', { body: { refresh_token: refreshToken } }),
    { status: 401 },
  );
});
