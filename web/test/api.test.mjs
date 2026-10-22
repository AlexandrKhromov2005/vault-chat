import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Api } from '../static/app/api.mjs';
import { apiError, fakeFetch } from './fakes.mjs';

// fakeSession stands in for Session: it hands out access-<n> tokens and
// counts refreshes. onToken and onRefresh run while a token is handed out or
// refreshed, e.g. to sign another user in meanwhile; signing in or out
// changes the generation.
function fakeSession({ refreshFails = false, onToken, onRefresh } = {}) {
  let n = 1;
  return {
    generation: 0,
    refreshes: 0,
    async validAccessToken() {
      onToken?.(this);
      return `access-${n}`;
    },
    async refresh() {
      this.refreshes++;
      onRefresh?.(this);
      if (refreshFails) throw Object.assign(new Error('invalid token'), { status: 401 });
      n++;
      return `access-${n}`;
    },
  };
}

test('calls the API with the current access token', async () => {
  const fetchFn = fakeFetch([{ status: 200, body: { rooms: [] } }]);
  const api = new Api({ fetchFn, session: fakeSession() });

  assert.deepEqual(await api.call('GET', '/api/v1/rooms'), { rooms: [] });
  assert.equal(fetchFn.calls[0].headers.Authorization, 'Bearer access-1');
});

test('refreshes once and retries after 401', async () => {
  const session = fakeSession();
  const fetchFn = fakeFetch([
    apiError(401, 'unauthenticated', 'invalid or expired token'),
    { status: 200, body: { rooms: [] } },
  ]);
  const api = new Api({ fetchFn, session });

  assert.deepEqual(await api.call('GET', '/api/v1/rooms'), { rooms: [] });
  assert.equal(session.refreshes, 1);
  assert.equal(fetchFn.calls[1].headers.Authorization, 'Bearer access-2');
});

test('does not retry twice', async () => {
  const session = fakeSession();
  const fetchFn = fakeFetch([
    apiError(401, 'unauthenticated', 'invalid or expired token'),
    apiError(401, 'unauthenticated', 'invalid or expired token'),
  ]);
  const api = new Api({ fetchFn, session });

  await assert.rejects(api.call('GET', '/api/v1/rooms'), { status: 401 });
  assert.equal(session.refreshes, 1);
});

test('a failed refresh is reported to the caller', async () => {
  const fetchFn = fakeFetch([apiError(401, 'unauthenticated', 'invalid or expired token')]);
  const api = new Api({ fetchFn, session: fakeSession({ refreshFails: true }) });

  await assert.rejects(api.call('GET', '/api/v1/rooms'), { status: 401 });
  assert.equal(fetchFn.pending(), 0);
});

test('other errors are not retried', async () => {
  const session = fakeSession();
  const fetchFn = fakeFetch([apiError(404, 'not_found', 'room not found')]);
  const api = new Api({ fetchFn, session });

  await assert.rejects(api.call('GET', '/api/v1/rooms/x'), { status: 404 });
  assert.equal(session.refreshes, 0);
});

test('blob downloads with the access token and refreshes once after 401', async () => {
  const session = fakeSession();
  const fetchFn = fakeFetch([
    apiError(401, 'unauthenticated', 'invalid or expired token'),
    { status: 200, bytes: 'content' },
  ]);
  const api = new Api({ fetchFn, session });

  const blob = await api.blob('/api/v1/files/f-1/content');

  assert.equal(await blob.text(), 'content');
  assert.equal(session.refreshes, 1);
  assert.equal(fetchFn.calls[1].headers.Authorization, 'Bearer access-2');
});

const unauthenticated = apiError(401, 'unauthenticated', 'invalid or expired token');

test('a 401 answered after another user signed in is not repeated', async () => {
  // Review of #66: Alice's message, answered 401 after Bob signed in, was
  // sent again with Bob's token and stored as Bob's.
  const session = fakeSession();
  const fetchFn = fakeFetch([
    () => {
      session.generation++;
      return unauthenticated;
    },
  ]);
  const api = new Api({ fetchFn, session });

  await assert.rejects(api.call('POST', '/api/v1/rooms/r-1/messages', { body: { text: 'от Alice' } }), { status: 401 });
  assert.equal(session.refreshes, 0);
  assert.equal(fetchFn.calls.length, 1);
});

test('a session that ends while the token is handed out sends nothing', async () => {
  const session = fakeSession({ onToken: (s) => s.generation++ });
  const fetchFn = fakeFetch([]);
  const api = new Api({ fetchFn, session });

  await assert.rejects(api.call('POST', '/api/v1/rooms/r-1/messages', { body: { text: 'от Alice' } }), { status: 401 });
  assert.equal(fetchFn.calls.length, 0);
});

test('a session that ends during the refresh is not repeated', async () => {
  const session = fakeSession({ onRefresh: (s) => s.generation++ });
  const fetchFn = fakeFetch([unauthenticated]);
  const api = new Api({ fetchFn, session });

  await assert.rejects(api.blob('/api/v1/files/f-1/content'), { status: 401 });
  assert.equal(fetchFn.calls.length, 1);
});
