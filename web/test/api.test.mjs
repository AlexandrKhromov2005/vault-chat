import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Api } from '../static/app/api.mjs';
import { apiError, fakeFetch } from './fakes.mjs';

// fakeSession stands in for Session: it hands out access-<n> tokens and
// counts refreshes.
function fakeSession({ refreshFails = false } = {}) {
  let n = 1;
  return {
    refreshes: 0,
    async validAccessToken() {
      return `access-${n}`;
    },
    async refresh() {
      this.refreshes++;
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
