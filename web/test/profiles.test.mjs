import { test } from 'node:test';
import assert from 'node:assert/strict';

import { ApiError } from '../static/app/http.mjs';
import { Profiles } from '../static/app/profiles.mjs';

// fakeApi answers calls with handler(method, path) and records them.
function fakeApi(handler) {
  const calls = [];
  return {
    calls,
    async call(method, path, options) {
      calls.push({ method, path, body: options?.body });
      return handler(method, path);
    },
  };
}

test('get loads a profile once and caches it', async () => {
  const api = fakeApi(() => ({ user_id: 'u-2', username: 'bob' }));
  const profiles = new Profiles(api);

  const [a, b] = await Promise.all([profiles.get('u-2'), profiles.get('u-2')]);
  const c = await profiles.get('u-2');

  assert.deepEqual([a, b, c].map((p) => p.username), ['bob', 'bob', 'bob']);
  assert.deepEqual(api.calls, [{ method: 'GET', path: '/api/v1/users/u-2', body: undefined }]);
  assert.equal(profiles.name('u-2'), 'bob');
});

test('a missing account resolves without a username', async () => {
  const api = fakeApi(() => {
    throw new ApiError(404, 'not_found', 'user not found');
  });
  const profiles = new Profiles(api);

  assert.deepEqual(await profiles.get('u-9'), { user_id: 'u-9', username: null });
  assert.equal(profiles.name('u-9'), null);
});

test('other errors are not cached', async () => {
  let fail = true;
  const api = fakeApi(() => {
    if (fail) throw new ApiError(0, 'network', 'offline');
    return { user_id: 'u-2', username: 'bob' };
  });
  const profiles = new Profiles(api);

  await assert.rejects(profiles.get('u-2'), { code: 'network' });
  fail = false;
  assert.equal((await profiles.get('u-2')).username, 'bob');
});

test('search queries by prefix and remembers the results', async () => {
  const api = fakeApi(() => ({ users: [{ user_id: 'u-2', username: 'bob' }] }));
  const profiles = new Profiles(api);

  const users = await profiles.search('  bo  ');

  assert.deepEqual(users, [{ user_id: 'u-2', username: 'bob' }]);
  assert.deepEqual(api.calls.map((c) => c.path), ['/api/v1/users?query=bo&limit=20']);
  assert.equal(profiles.name('u-2'), 'bob');
});

test('search encodes the query', async () => {
  const api = fakeApi(() => ({ users: [] }));
  const profiles = new Profiles(api);

  await profiles.search('a&b=c');

  assert.equal(api.calls[0].path, '/api/v1/users?query=a%26b%3Dc&limit=20');
});

test('search needs at least two characters', async () => {
  const api = fakeApi(() => ({ users: [] }));
  const profiles = new Profiles(api);

  assert.deepEqual(await profiles.search(' b '), []);
  assert.equal(api.calls.length, 0);
});
