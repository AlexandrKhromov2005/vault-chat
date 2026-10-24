// Runs the client modules against a running Gateway, like tests/compose.
// Skipped unless VAULT_CHAT_WEB_GATEWAY_URL is set, e.g.:
//
//   VAULT_CHAT_WEB_GATEWAY_URL=http://127.0.0.1:8080 node --test "web/test/*.test.mjs"

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';

import { Api } from '../static/app/api.mjs';
import { Backoff } from '../static/app/backoff.mjs';
import {
  Outbox,
  addMember,
  catchUp,
  createChannel,
  leaveRoom,
  listAllMembers,
  listAllRooms,
  loadLatest,
  openDirect,
  removeMember,
  roomTitle,
  sendMessage,
} from '../static/app/chat.mjs';
import { FileInfos, downloadFile, fileProblem, uploadFile } from '../static/app/files.mjs';
import { requestJSON } from '../static/app/http.mjs';
import { Profiles } from '../static/app/profiles.mjs';
import { Realtime } from '../static/app/realtime.mjs';
import { Session } from '../static/app/session.mjs';
import { Timeline } from '../static/app/timeline.mjs';
import { memoryStorage } from './fakes.mjs';

const base = process.env.VAULT_CHAT_WEB_GATEWAY_URL;
const skip = base ? false : 'VAULT_CHAT_WEB_GATEWAY_URL is not set';

// The browser resolves the client's relative URLs against the page; here
// they are resolved against the Gateway under test. Every simulated user
// shares this machine's IP and its rate limit, so the tests wait out 429
// responses as Retry-After asks instead of depending on the configured limit.
async function fetchFn(path, init) {
  for (let attempt = 0; ; attempt++) {
    const response = await fetch(new URL(path, base), init);
    if (response.status !== 429 || attempt === 10) return response;
    const seconds = Number(response.headers.get('retry-after')) || 1;
    await new Promise((resolve) => setTimeout(resolve, seconds * 1000));
  }
}

function newUser() {
  const id = randomUUID().slice(0, 8);
  return { email: `web-${id}@example.com`, username: `web_${id}`, password: 'Password123' };
}

function invitation(email) {
  const dsn = process.env.VAULT_CHAT_WEB_INVITE_DATABASE_URL;
  assert.ok(dsn, 'set VAULT_CHAT_WEB_INVITE_DATABASE_URL to issue operator test invitations');
  const output = execFileSync('go', ['run', './cmd/invite', email], {
    env: { ...process.env, AUTH_DATABASE_URL: dsn }, encoding: 'utf8',
  });
  return JSON.parse(output).invitation_token;
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

  await session.register(user.email, user.username, user.password, invitation(user.email));
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
  await session.register(user.email, user.username, user.password, invitation(user.email));
  await session.logout();

  await assert.rejects(session.login(user.email, 'WrongPassword1'), { status: 401, code: 'unauthenticated' });
  assert.equal(session.user, null);
});

test('a refresh token stops working after logout', { skip }, async () => {
  const user = newUser();
  const storage = memoryStorage();
  const session = new Session({ fetchFn, storage });
  await session.register(user.email, user.username, user.password, invitation(user.email));
  const { refreshToken } = JSON.parse(storage.getItem('vault-chat.session'));

  await session.logout();

  await assert.rejects(
    requestJSON(fetchFn, 'POST', '/api/v1/auth/refresh', { body: { refresh_token: refreshToken } }),
    { status: 401 },
  );
});

// client signs a new user in and returns what the page would hold.
async function client() {
  const user = newUser();
  const session = new Session({ fetchFn, storage: memoryStorage() });
  await session.register(user.email, user.username, user.password, invitation(user.email));
  const api = new Api({ fetchFn, session });
  return { user, session, api, profiles: new Profiles(api) };
}

test('two users find each other and talk in a direct dialog', { skip }, async () => {
  const alice = await client();
  const bob = await client();

  const found = await alice.profiles.search(bob.user.username);
  assert.deepEqual(found.map((u) => u.username), [bob.user.username]);

  const room = await openDirect(alice.api, found[0].user_id);
  assert.equal(roomTitle(room, alice.session.user.id, alice.profiles), bob.user.username);

  const outbox = new Outbox();
  const prepared = outbox.prepare('Привет, Боб!\nКак дела?', []);
  const sent = await sendMessage(alice.api, room.room_id, prepared);
  // A retry after a lost response returns the stored message, not a copy.
  const retried = await sendMessage(alice.api, room.room_id, outbox.prepare('Привет, Боб!\nКак дела?', []));
  assert.equal(retried.message_id, sent.message_id);

  const bobRooms = await listAllRooms(bob.api);
  assert.deepEqual(bobRooms.map((r) => r.room_id), [room.room_id]);
  const bobTimeline = new Timeline();
  await loadLatest(bob.api, room.room_id, bobTimeline);
  assert.deepEqual(bobTimeline.messages.map((m) => m.text), ['Привет, Боб!\nКак дела?']);

  const aliceTimeline = new Timeline();
  await loadLatest(alice.api, room.room_id, aliceTimeline);
  await sendMessage(bob.api, room.room_id, new Outbox().prepare('Отлично!', []));
  assert.equal(await catchUp(alice.api, room.room_id, aliceTimeline), 1);
  assert.deepEqual(aliceTimeline.messages.map((m) => m.sequence), [1, 2]);

  // Someone outside the dialog sees neither the room nor its history.
  const eve = await client();
  await assert.rejects(loadLatest(eve.api, room.room_id, new Timeline()), { status: 404 });
  assert.deepEqual(await listAllRooms(eve.api), []);
});

test('a channel owner manages members', { skip }, async () => {
  const owner = await client();
  const member = await client();
  const leaver = await client();

  const channel = await createChannel(owner.api, 'Проект', true);
  await addMember(owner.api, channel.room_id, member.session.user.id);
  await addMember(owner.api, channel.room_id, leaver.session.user.id);
  const ids = (await listAllMembers(owner.api, channel.room_id)).map((m) => m.user_id);
  assert.equal(ids.length, 3);

  // Only the owner manages; the owner cannot leave.
  await assert.rejects(removeMember(member.api, channel.room_id, leaver.session.user.id), { status: 403 });
  await assert.rejects(leaveRoom(owner.api, channel.room_id), { status: 409, code: 'failed_precondition' });

  await leaveRoom(leaver.api, channel.room_id);
  assert.deepEqual(await listAllRooms(leaver.api), []);

  await removeMember(owner.api, channel.room_id, member.session.user.id);
  await assert.rejects(listAllMembers(member.api, channel.room_id), { status: 404 });
  assert.deepEqual((await listAllMembers(owner.api, channel.room_id)).map((m) => m.user_id), [owner.session.user.id]);
});

test('two users exchange a file and a third gets no access', { skip }, async () => {
  const alice = await client();
  const bob = await client();
  const eve = await client();
  const room = await openDirect(alice.api, bob.session.user.id);

  const content = 'Отчёт за неделю\nстрока 2\n';
  const file = new File([content], 'report.txt', { type: 'text/plain' });
  assert.equal(fileProblem(file), null);
  const uploaded = await uploadFile(alice.api, room.room_id, file);
  assert.equal(uploaded.filename, 'report.txt');

  // An attachment-only message: the text may be empty.
  const sent = await sendMessage(alice.api, room.room_id, new Outbox().prepare('', [uploaded.file_id]));
  assert.deepEqual(sent.attachment_ids, [uploaded.file_id]);

  const timeline = new Timeline();
  await loadLatest(bob.api, room.room_id, timeline);
  const [received] = timeline.messages;
  assert.deepEqual(received.attachment_ids, [uploaded.file_id]);
  const info = await new FileInfos(bob.api).get(uploaded.file_id);
  assert.equal(info.size, Buffer.byteLength(content));
  assert.equal(await (await downloadFile(bob.api, uploaded.file_id)).text(), content);

  const eveFiles = new FileInfos(eve.api);
  assert.deepEqual(await eveFiles.get(uploaded.file_id), { file_id: uploaded.file_id, unavailable: true });
  await assert.rejects(downloadFile(eve.api, uploaded.file_id), { status: 404 });
  await assert.rejects(uploadFile(eve.api, room.room_id, file), { status: 404 });
});

test('a removed member loses access to the channel files', { skip }, async () => {
  const owner = await client();
  const member = await client();
  const channel = await createChannel(owner.api, 'Документы', true);
  await addMember(owner.api, channel.room_id, member.session.user.id);
  const plan = new File(['План встречи\n'], 'plan.txt', { type: 'text/plain' });
  const uploaded = await uploadFile(owner.api, channel.room_id, plan);
  assert.equal(await (await downloadFile(member.api, uploaded.file_id)).text(), 'План встречи\n');

  await removeMember(owner.api, channel.room_id, member.session.user.id);

  await assert.rejects(downloadFile(member.api, uploaded.file_id), { status: 404 });
});

test('the server rejects content outside the allowlist', { skip }, async () => {
  const alice = await client();
  const bob = await client();
  const room = await openDirect(alice.api, bob.session.user.id);
  const html = new File(['<html><script>alert(1)</script></html>'], 'page.html', { type: 'text/html' });

  await assert.rejects(uploadFile(alice.api, room.room_id, html), { status: 400 });
});

// waitFor resolves with the first event of type that satisfies match.
function waitFor(target, type, match = () => true, timeoutMs = 10_000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`no ${type} within ${timeoutMs} ms`)), timeoutMs);
    target.addEventListener(type, function listener(event) {
      if (!match(event.detail)) return;
      clearTimeout(timer);
      target.removeEventListener(type, listener);
      resolve(event.detail);
    });
  });
}

test('live events arrive and a dropped connection catches up', { skip }, async () => {
  const alice = await client();
  const bob = await client();
  const room = await openDirect(alice.api, bob.session.user.id);

  const sockets = [];
  const realtime = new Realtime({
    session: bob.session,
    url: new URL('/api/v1/ws', base.replace(/^http/, 'ws')).href,
    createSocket: (url) => {
      const socket = new WebSocket(url);
      sockets.push(socket);
      return socket;
    },
    backoff: new Backoff({ base: 200, max: 1000 }),
  });
  try {
    realtime.start();
    // Upgrades count against the shared per-IP rate limit; after a 429 the
    // client keeps retrying with backoff, so connecting may take a while.
    await waitFor(realtime, 'ready', undefined, 60_000);
    assert.equal(realtime.state, 'live');

    const timeline = new Timeline();
    await loadLatest(bob.api, room.room_id, timeline);

    const live = waitFor(realtime, 'event', (e) => e.type === 'message.created' && e.message.text === 'Ты тут?');
    await sendMessage(alice.api, room.room_id, new Outbox().prepare('Ты тут?', []));
    assert.equal(timeline.apply((await live).message), 'appended');

    // Drop the connection and miss two messages meanwhile.
    const reconnected = waitFor(realtime, 'ready', undefined, 60_000);
    sockets.at(-1).close();
    await sendMessage(alice.api, room.room_id, new Outbox().prepare('Пропущенное 1', []));
    await sendMessage(alice.api, room.room_id, new Outbox().prepare('Пропущенное 2', []));
    await reconnected;
    assert.ok(sockets.length >= 2, 'a new connection was opened');

    // What a ready frame triggers in the page: catch up after the newest.
    await catchUp(bob.api, room.room_id, timeline);
    assert.deepEqual(timeline.messages.map((m) => [m.sequence, m.text]), [
      [1, 'Ты тут?'],
      [2, 'Пропущенное 1'],
      [3, 'Пропущенное 2'],
    ]);
  } finally {
    realtime.stop();
  }
});
