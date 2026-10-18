import { test } from 'node:test';
import assert from 'node:assert/strict';

import { ApiError, requestJSON } from '../static/app/http.mjs';
import { apiError, fakeFetch } from './fakes.mjs';

test('sends JSON with a bearer token and returns the parsed body', async () => {
  const fetchFn = fakeFetch([{ status: 200, body: { user_id: 'u-1' } }]);

  const body = await requestJSON(fetchFn, 'POST', '/api/v1/auth/login', {
    body: { email: 'a@b.co' },
    token: 'tok',
  });

  assert.deepEqual(body, { user_id: 'u-1' });
  assert.deepEqual(fetchFn.calls, [{
    url: '/api/v1/auth/login',
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer tok' },
    body: { email: 'a@b.co' },
  }]);
});

test('omits the body and token when there are none', async () => {
  const fetchFn = fakeFetch([{ status: 200, body: {} }]);

  await requestJSON(fetchFn, 'GET', '/api/v1/rooms');

  assert.deepEqual(fetchFn.calls[0].headers, {});
  assert.equal(fetchFn.calls[0].body, undefined);
});

test('returns null for 204 No Content', async () => {
  const fetchFn = fakeFetch([{ status: 204 }]);

  assert.equal(await requestJSON(fetchFn, 'POST', '/api/v1/auth/logout', { body: {} }), null);
});

test('turns the Gateway error envelope into ApiError', async () => {
  const fetchFn = fakeFetch([apiError(409, 'already_exists', 'email already registered')]);

  await assert.rejects(requestJSON(fetchFn, 'POST', '/api/v1/auth/register', { body: {} }), (err) => {
    assert.ok(err instanceof ApiError);
    assert.equal(err.status, 409);
    assert.equal(err.code, 'already_exists');
    assert.equal(err.message, 'email already registered');
    return true;
  });
});

test('reports a response without the envelope by its status', async () => {
  const fetchFn = fakeFetch([{ status: 502 }]);

  await assert.rejects(requestJSON(fetchFn, 'GET', '/api/v1/rooms'), {
    name: 'ApiError',
    status: 502,
    code: 'http_error',
  });
});

test('reports a network failure as status 0', async () => {
  const fetchFn = fakeFetch([new TypeError('Failed to fetch')]);

  await assert.rejects(requestJSON(fetchFn, 'GET', '/api/v1/rooms'), {
    name: 'ApiError',
    status: 0,
    code: 'network',
  });
});
