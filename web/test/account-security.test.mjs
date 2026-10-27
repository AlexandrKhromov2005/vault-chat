import { test } from 'node:test';
import assert from 'node:assert/strict';

import { AccountSecurity, consumeEmailLink } from '../static/app/account-security.mjs';
import { ApiError } from '../static/app/http.mjs';
import { apiError, deferred, fakeFetch, memoryStorage } from './fakes.mjs';
import { loadPage, settle } from './minidom.mjs';
import { click, scriptedApi, submit } from './ui.mjs';

const TOKEN = 'a'.repeat(64);
const account = { user_id: 'u-1', email: 'alice@example.invalid', username: 'alice', email_verified: false,
  verification_required: true, verification_deadline: '2026-11-02T12:00:00Z' };

class TestSession extends EventTarget {
  generation = 0;
  user = null;
  forgotten = 0;
  logins = [];
  registrations = [];
  async login(...fields) {
    this.logins.push(fields);
    if (this.loginError) throw this.loginError;
  }
  async register(...fields) {
    this.registrations.push(fields);
    return { verification_required: true };
  }
  switchUser(user) {
    this.user = user;
    this.generation++;
    this.dispatchEvent(new Event(user ? 'signedin' : 'signedout'));
  }
  forget() {
    this.forgotten++;
    this.switchUser(null);
  }
}

function setup({ hash = '', responses = [], user = null } = {}) {
  const document = loadPage();
  const location = { hash, pathname: '/', search: '?view=chat' };
  const replaced = [];
  const history = { state: null, replaceState: (...args) => { replaced.push(args); location.hash = ''; } };
  const session = new TestSession();
  session.user = user;
  const fetchFn = fakeFetch(responses);
  const api = scriptedApi();
  const storage = memoryStorage();
  const browser = Object.assign(new EventTarget(), window, {
    location, history, sessionStorage: storage, localStorage: storage,
  });
  globalThis.window = browser;
  const navigate = (nextHash) => {
    location.hash = nextHash;
    browser.dispatchEvent(new Event('hashchange'));
  };
  const security = new AccountSecurity({ api, session, fetchFn, location, history });
  return { $: (id) => document.getElementById(id), document, security, session, fetchFn, api, location, replaced, storage, navigate };
}

test('same-document email navigation clears the new fragment synchronously and waits for explicit confirmation', async () => {
  const { $, document, fetchFn, location, replaced, storage, session, navigate } = setup({
    responses: [{ status: 200, body: { user_id: 'u-1' } }],
  });
  navigate('#verify-email=' + TOKEN);
  assert.equal(location.hash, '');
  assert.deepEqual(replaced, [[null, '', '/?view=chat']]);
  assert.equal($('email-action').hidden, false);
  assert.equal($('email-action-form').hidden, false);
  assert.equal(fetchFn.calls.length, 0);
  assert.equal(document.body.textContent.includes(TOKEN), false);
  assert.equal(storage.size, 0);
  assert.equal(session.generation, 0, 'proof changes must not change the signed-in session generation');
  await submit($('email-action-form'));
  assert.deepEqual(fetchFn.calls[0].body, { token: TOKEN });
});

test('new valid and invalid links replace completed proof UI without retaining old passwords or notices', async () => {
  const { $, fetchFn, location, navigate } = setup({
    hash: '#verify-email=' + TOKEN, responses: [{ status: 200, body: { user_id: 'u-1' } }],
  });
  await submit($('email-action-form'));
  assert.ok($('email-action-notice').textContent);
  $('email-action-form').elements.password.value = 'OldPassword12';
  navigate('#reset-password=' + 'b'.repeat(64));
  assert.equal(location.hash, '');
  assert.equal($('email-action-form').hidden, false);
  assert.equal($('email-action-form').elements.password.value, '');
  assert.equal($('email-action-form').elements.password.required, true);
  assert.equal($('reset-password-field').hidden, false);
  assert.equal($('email-action-notice').textContent, '');
  assert.equal($('email-action-error').textContent, '');
  $('email-action-form').elements.password.value = 'DraftPassword12';
  navigate('#verify-email=');
  assert.equal(location.hash, '');
  assert.equal($('email-action-form').hidden, true);
  assert.equal($('email-action-form').elements.password.value, '');
  assert.equal($('email-action-form').elements.password.required, false);
  assert.equal($('reset-password-field').hidden, true);
  assert.match($('email-action-error').textContent, /Ссылка недействительна/);
  assert.equal($('email-action-description').textContent, '');
  navigate('#confirm-email-change=' + 'c'.repeat(64));
  assert.equal($('email-action-form').hidden, false);
  assert.equal($('email-action-submit').disabled, false);
  assert.equal($('email-action-error').textContent, '');
  assert.match($('email-action-title').textContent, /смену email/);
  assert.equal(fetchFn.calls.length, 1, 'navigating to each proof must not submit it');
});

test('late success and error of an old proof cannot consume or unlock a newer pending proof', async () => {
  for (const [oldPurpose, fail] of [
    ['confirm-email-change', false], ['confirm-email-change', true],
    ['reset-password', false], ['reset-password', true],
  ]) {
    const old = deferred();
    const next = deferred();
    const { $, session, fetchFn, navigate } = setup({
      hash: '#' + oldPurpose + '=' + TOKEN, user: { id: 'u-1', username: 'alice' },
      responses: [() => old.promise, () => next.promise],
    });
    const form = $('email-action-form');
    if (oldPurpose === 'reset-password') form.elements.password.value = 'OldPassword12';
    await submit(form);
    assert.equal($('email-action-submit').disabled, true);
    navigate('#reset-password=' + 'b'.repeat(64));
    assert.equal($('email-action-submit').disabled, false, 'a replacement proof must get its own submit lifecycle');
    form.elements.password.value = 'NewPassword12';
    await submit(form);
    assert.equal($('email-action-submit').disabled, true);
    form.elements.password.value = 'NewDraftPassword12';
    old.resolve(fail ? apiError(400, 'invalid_email_action', 'old proof rejected') : { status: 200, body: { user_id: 'u-1' } });
    await settle();
    await settle();
    assert.equal(session.forgotten, 0, 'an obsolete proof result must not end the current session');
    assert.equal(session.generation, 0);
    assert.equal(form.hidden, false);
    assert.equal(form.elements.password.value, 'NewDraftPassword12', 'old finally must not clear the new form');
    assert.equal($('email-action-submit').disabled, true, 'old finally must not unlock the pending new request');
    assert.equal($('email-action-error').textContent, '');
    assert.equal($('email-action-notice').textContent, '');
    assert.deepEqual(fetchFn.calls[1].body, { token: 'b'.repeat(64), password: 'NewPassword12' });
    next.resolve({ status: 200, body: { user_id: 'u-2' } });
    await settle();
    await settle();
    assert.equal(form.hidden, true);
    assert.match($('email-action-notice').textContent, /Пароль изменён/);
  }
});

test('invalid replacement keeps its own error when the previous proof completes', async () => {
  const old = deferred();
  const { $, navigate, location } = setup({
    hash: '#verify-email=' + TOKEN, responses: [() => old.promise],
  });
  await submit($('email-action-form'));
  navigate('#reset-password=');
  old.resolve({ status: 200, body: { user_id: 'u-1' } });
  await settle();
  await settle();
  assert.equal(location.hash, '');
  assert.equal($('email-action-form').hidden, true);
  assert.match($('email-action-error').textContent, /Ссылка недействительна/);
  assert.equal($('email-action-notice').textContent, '');
});

test('proof navigation does not invalidate an unrelated pending account-settings request', async () => {
  const pending = deferred();
  const { $, session, api, navigate } = setup({ user: { id: 'u-1', username: 'alice' } });
  api.on('GET /api/v1/auth/account', { ...account, email_verified: true })
    .on('POST /api/v1/auth/email/change/request', () => pending.promise);
  await click($('account-settings-toggle'));
  const form = $('email-change-form');
  form.elements.password.value = 'CurrentPassword12';
  form.elements.new_email.value = 'new@example.invalid';
  await submit(form);
  navigate('#verify-email=' + TOKEN);
  assert.equal(session.generation, 0);
  assert.equal($('account-settings').hidden, false);
  assert.equal(form.querySelector('button[type="submit"]').disabled, true);
  pending.resolve({ message: 'generic' });
  await settle();
  await settle();
  assert.match($('settings-notice').textContent, /Если данные подходят/);
  assert.equal(form.querySelector('button[type="submit"]').disabled, false);
  assert.equal(form.elements.password.value, '');
  assert.equal($('email-action-form').hidden, false);
  assert.equal($('email-action-notice').textContent, '');
});

test('a verification link clears the fragment and requires explicit submit before POST', async () => {
  const { $, document, fetchFn, location, replaced, storage } = setup({ hash: `#verify-email=${TOKEN}`,
    responses: [{ status: 200, body: { message: 'verified', user_id: 'u-1' } }] });

  assert.equal(location.hash, '');
  assert.deepEqual(replaced, [[null, '', '/?view=chat']]);
  assert.equal(fetchFn.calls.length, 0);
  assert.equal($('email-action').hidden, false);
  assert.equal($('reset-password-field').hidden, true);
  assert.equal(document.body.textContent.includes(TOKEN), false);
  assert.equal(storage.size, 0);
  await submit($('email-action-form'));

  assert.deepEqual(fetchFn.calls, [{ url: '/api/v1/auth/email/verification/confirm', method: 'POST',
    headers: { 'Content-Type': 'application/json' }, body: { token: TOKEN } }]);
  assert.equal($('email-action-form').hidden, true);
  assert.match($('email-action-notice').textContent, /Email подтверждён/);
  assert.equal(storage.size, 0);
});

test('unknown fragments are ignored; malformed, duplicate and mixed token fragments are cleared and rejected', () => {
  for (const hash of [`#verify-email=${TOKEN}&verify-email=other`, `#verify-email=${TOKEN}&reset-password=${TOKEN}`,
    '#verify-email=', '#verify-email=a%0Ab', `#verify-email=${'b'.repeat(513)}`, `#verify-email=${TOKEN}&other=ignored`]) {
    const { $, fetchFn, location } = setup({ hash });
    assert.equal(location.hash, '', hash);
    assert.equal(fetchFn.calls.length, 0, hash);
    assert.equal($('email-action-form').hidden, true, hash);
    assert.match($('email-action-error').textContent, /Ссылка недействительна/, hash);
  }
  const location = { hash: `#some-other-purpose=${TOKEN}` };
  assert.equal(consumeEmailLink(location, { replaceState: () => assert.fail('unknown fragment modified') }), null);
});

test('invalid tokens never echo server messages or tokens into the page and can be retried', async () => {
  const { $, document, fetchFn } = setup({ hash: `#verify-email=${TOKEN}`,
    responses: [apiError(400, 'invalid_email_action', `invalid token ${TOKEN}`), { status: 200, body: { user_id: 'u-1' } }] });
  await submit($('email-action-form'));
  assert.match($('email-action-error').textContent, /недействительна/);
  assert.equal(document.body.textContent.includes(TOKEN), false);
  assert.equal($('email-action-submit').disabled, false);
  await submit($('email-action-form'));
  assert.equal(fetchFn.calls.length, 2);
  assert.match($('email-action-notice').textContent, /подтверждён/);
});

test('reset requires a new password and never logs in automatically', async () => {
  const { $, session, fetchFn } = setup({ hash: `#reset-password=${TOKEN}`,
    responses: [{ status: 200, body: { user_id: 'u-1' } }] });
  assert.equal($('reset-password-field').hidden, false);
  assert.equal($('email-action-form').elements.password.required, true);
  $('email-action-form').elements.password.value = 'NewPassword12';
  await submit($('email-action-form'));
  assert.deepEqual(fetchFn.calls[0].body, { token: TOKEN, password: 'NewPassword12' });
  assert.equal(fetchFn.calls[0].url, '/api/v1/auth/password/reset/confirm');
  assert.equal($('email-action-form').elements.password.value, '');
  assert.deepEqual(session.logins, []);
  assert.equal(session.forgotten, 0);
});

test('an invalid new password gets a field hint and keeps the token for correction', async () => {
  const { $, fetchFn, document } = setup({ hash: `#reset-password=${TOKEN}`,
    responses: [apiError(400, 'invalid_argument', `password rejected ${TOKEN}`), { status: 200, body: { user_id: 'u-1' } }] });
  $('email-action-form').elements.password.value = 'weak';
  await submit($('email-action-form'));
  assert.match($('email-action-error').textContent, /пароль.*8|8.*пароль/i);
  assert.doesNotMatch($('email-action-error').textContent, /Ссылка недействительна/);
  assert.equal(document.body.textContent.includes(TOKEN), false);
  $('email-action-form').elements.password.value = 'CorrectPassword12';
  await submit($('email-action-form'));
  assert.deepEqual(fetchFn.calls.at(-1).body, { token: TOKEN, password: 'CorrectPassword12' });
  assert.equal($('email-action-form').hidden, true);
});

test('reset and email change forget only the current owner without a logout request', async () => {
  for (const purpose of ['reset-password', 'confirm-email-change']) {
    for (const owner of ['u-1', 'u-2']) {
      const { $, session, fetchFn } = setup({ hash: `#${purpose}=${TOKEN}`,
        user: { id: 'u-1', username: 'alice' }, responses: [{ status: 200, body: { user_id: owner } }] });
      $('email-action-form').elements.password.value = 'NewPassword12';
      await submit($('email-action-form'));
      assert.equal(session.forgotten, owner === 'u-1' ? 1 : 0, `${purpose}: ${owner}`);
      assert.equal(session.user?.id ?? null, owner === 'u-1' ? null : 'u-1');
      assert.deepEqual(session.logins, []);
      assert.equal(fetchFn.calls.length, 1);
      assert.equal($('email-action-form').hidden, true);
      assert.ok($('email-action-notice').textContent);
    }
  }
});

test('a late public completion neither signs out the next account nor changes its UI', async () => {
  const late = deferred();
  const { $, session } = setup({ hash: `#confirm-email-change=${TOKEN}`, user: { id: 'u-1', username: 'alice' },
    responses: [() => late.promise] });
  await submit($('email-action-form'));
  session.switchUser({ id: 'u-2', username: 'bob' });
  late.resolve({ status: 200, body: { user_id: 'u-2' } });
  await settle();
  await settle();
  assert.equal(session.user.id, 'u-2');
  assert.equal(session.forgotten, 0);
  assert.equal($('email-action-notice').textContent, '');
});

test('pending registration explains verification, while login 403 offers password-protected resend', async () => {
  const { $, session, fetchFn } = setup({ responses: [{ status: 202, body: { message: 'generic' } }] });
  await click($('show-register'));
  const registration = $('register-form');
  Object.assign(registration.elements.email, { value: 'alice@example.invalid' });
  registration.elements.username.value = 'alice';
  registration.elements.password.value = 'Password12';
  registration.elements.invitation_token.value = TOKEN;
  await submit(registration);
  assert.match($('auth-notice').textContent, /Подтвердите email/);
  assert.equal(session.user, null);
  assert.deepEqual(session.logins, []);
  session.loginError = new ApiError(403, 'email_verification_required', 'email verification required');
  $('login-form').elements.email.value = 'alice@example.invalid';
  $('login-form').elements.password.value = 'Password12';
  await submit($('login-form'));
  assert.match($('auth-error').textContent, /Подтвердите email/);
  assert.equal($('verification-help').open, true);
  const resend = $('verification-request-form');
  assert.equal(resend.elements.email.value, 'alice@example.invalid');
  resend.elements.password.value = 'FreshPassword12';
  await submit(resend);
  assert.equal(fetchFn.calls[0].url, '/api/v1/auth/email/verification/request');
  assert.deepEqual(fetchFn.calls[0].body, { email: 'alice@example.invalid', password: 'FreshPassword12' });
  assert.equal(resend.elements.password.value, '');
  assert.match($('auth-notice').textContent, /Если данные подходят/);
});

test('password reset request gives the same generic notice without logging in', async () => {
  const { $, session, fetchFn } = setup({ responses: [{ status: 202, body: { message: 'generic' } }] });
  await click($('show-password-reset'));
  const form = $('password-reset-request-form');
  assert.equal(form.hidden, false);
  form.elements.email.value = 'unknown@example.invalid';
  await submit(form);
  assert.deepEqual(fetchFn.calls[0].body, { email: 'unknown@example.invalid' });
  assert.equal(fetchFn.calls[0].url, '/api/v1/auth/password/reset/request');
  assert.match($('auth-notice').textContent, /Если данные подходят/);
  assert.deepEqual(session.logins, []);
});

test('account settings show email/grace state and require fresh password for resend or change', async () => {
  const { $, api, fetchFn } = setup({ user: { id: 'u-1', username: 'alice' },
    responses: [{ status: 202, body: { message: 'generic' } }] });
  api.on('GET /api/v1/auth/account', account)
    .on('POST /api/v1/auth/email/change/request', { message: 'generic' });
  await click($('account-settings-toggle'));
  assert.equal($('account-settings').hidden, false);
  assert.equal($('settings-email').textContent, account.email);
  assert.match($('settings-status').textContent, /пока не подтверждён/);
  assert.match($('settings-status').textContent, /Срок подтверждения/);
  const resend = $('settings-verify-form');
  resend.elements.password.value = 'CurrentPassword12';
  await submit(resend);
  assert.deepEqual(fetchFn.calls[0].body, { email: account.email, password: 'CurrentPassword12' });
  assert.equal(resend.elements.password.value, '');
  const change = $('email-change-form');
  change.elements.new_email.value = 'new@example.invalid';
  change.elements.password.value = 'FreshPassword12';
  await submit(change);
  assert.deepEqual(api.calls.at(-1).body, { new_email: 'new@example.invalid', password: 'FreshPassword12' });
  assert.equal(change.elements.password.value, '');
  assert.equal($('settings-email').textContent, account.email);
  api.on('GET /api/v1/auth/account', { ...account, email_verified: true });
  await click($('settings-refresh'));
  assert.equal($('settings-verify-form').hidden, true);
  assert.equal($('settings-status').textContent, 'Email подтверждён.');
});

test('late settings results and errors do not display the previous account data', async () => {
  const late = deferred();
  const { $, session, api } = setup({ user: { id: 'u-1', username: 'alice' } });
  api.on('GET /api/v1/auth/account', () => late.promise);
  await click($('account-settings-toggle'));
  session.switchUser({ id: 'u-2', username: 'bob' });
  api.on('GET /api/v1/auth/account', { ...account, user_id: 'u-2', email: 'bob@example.invalid', email_verified: true });
  await click($('account-settings-toggle'));
  late.resolve(account);
  await settle();
  assert.equal($('settings-email').textContent, 'bob@example.invalid');
  assert.equal($('settings-status').textContent, 'Email подтверждён.');
  assert.equal($('settings-error').textContent, '');
});

test('late settings mutation success or error leaves the next account form and notice alone', async () => {
  for (const fail of [false, true]) {
    const late = deferred();
    const { $, session, api } = setup({ user: { id: 'u-1', username: 'alice' } });
    api.on('GET /api/v1/auth/account', account)
      .on('POST /api/v1/auth/email/change/request', async () => {
        await late.promise;
        if (fail) throw new ApiError(503, 'unavailable', 'offline');
        return { message: 'generic' };
      });
    await click($('account-settings-toggle'));
    const change = $('email-change-form');
    change.elements.new_email.value = 'alice-new@example.invalid';
    change.elements.password.value = 'AlicePassword12';
    await submit(change);
    session.switchUser({ id: 'u-2', username: 'bob' });
    api.on('GET /api/v1/auth/account', { ...account, user_id: 'u-2', email: 'bob@example.invalid' });
    await click($('account-settings-toggle'));
    change.elements.new_email.value = 'bob-new@example.invalid';
    change.elements.password.value = 'BobPassword12';
    late.resolve();
    await settle();
    await settle();
    assert.equal(change.elements.new_email.value, 'bob-new@example.invalid');
    assert.equal(change.elements.password.value, 'BobPassword12');
    assert.equal($('settings-error').textContent, '');
    assert.equal($('settings-notice').textContent, '');
  }
});

test('a dependency outage allows a retry and simultaneous submits make one request', async () => {
  const late = deferred();
  const { $, fetchFn } = setup({ hash: `#verify-email=${TOKEN}`,
    responses: [() => late.promise, { status: 200, body: { user_id: 'u-1' } }] });
  const form = $('email-action-form');
  form.dispatchEvent(new Event('submit', { cancelable: true }));
  form.dispatchEvent(new Event('submit', { cancelable: true }));
  assert.equal(fetchFn.calls.length, 1);
  late.resolve(apiError(503, 'unavailable', 'mailer offline'));
  await settle();
  await settle();
  assert.match($('email-action-error').textContent, /временно недоступен/);
  assert.equal($('email-action-submit').disabled, false);
  await submit(form);
  assert.equal(fetchFn.calls.length, 2);
});
