// Email links remain in memory until an explicit form submission. Neither
// opening a link nor inspecting account settings consumes a one-time token.
import { requestJSON } from './http.mjs';
import { describeError } from './errors.mjs';

const $ = (id) => document.getElementById(id);
const ACTIONS = {
  'verify-email': {
    path: '/api/v1/auth/email/verification/confirm', title: 'Подтвердить email',
    description: 'Нажмите кнопку, чтобы подтвердить email аккаунта.',
    success: 'Email подтверждён. Теперь можно войти в аккаунт.',
  },
  'reset-password': {
    path: '/api/v1/auth/password/reset/confirm', title: 'Новый пароль',
    description: 'Введите новый пароль. После сохранения войдите заново на своих устройствах.',
    success: 'Пароль изменён. Войдите с новым паролем.',
  },
  'confirm-email-change': {
    path: '/api/v1/auth/email/change/confirm', title: 'Подтвердить смену email',
    description: 'Нажмите кнопку, чтобы сохранить новый email. После этого потребуется войти заново.',
    success: 'Email изменён. Войдите с новым адресом.',
  },
};
const SENT = 'Если данные подходят и отправка доступна, письмо со ссылкой придёт на указанный email.';
const BAD_LINK = 'Ссылка недействительна, уже использована или срок её действия истёк. Запросите новое письмо.';

// consumeEmailLink rejects duplicate or mixed-purpose fragments, and removes
// recognized fragments even when malformed. Tokens never enter page markup.
export function consumeEmailLink(location, history) {
  const fragment = location.hash?.slice(1) ?? '';
  const params = new URLSearchParams(fragment);
  const recognized = [...params.keys()].filter((key) => Object.hasOwn(ACTIONS, key));
  if (!recognized.length) return null;
  history.replaceState(history.state ?? null, '', `${location.pathname || '/'}${location.search || ''}`);
  const entries = [...params];
  if (entries.length !== 1) return { invalid: true };
  const [purpose, token] = entries[0];
  if (!token || token.length > 512 || /[\s\u0000-\u001f\u007f]/u.test(token)) return { invalid: true };
  return { purpose, token };
}

function failure(err, purpose = null) {
  if (err.code === 'email_verification_required') return 'Подтвердите email по ссылке из письма, затем войдите. Ниже можно запросить новое письмо.';
  if (purpose && err.code === 'invalid_email_action') return BAD_LINK;
  if (purpose === 'reset-password' && err.code === 'invalid_argument') return 'Проверьте новый пароль: минимум 8 символов, латинская буква и цифра. Если пароль длинный, сократите его.';
  if (purpose && !['network', 'rate_limited', 'unavailable', 'timeout'].includes(err.code)) return 'Не удалось подтвердить действие. Повторите позже.';
  if (err.code === 'unauthenticated') return 'Неверный email или пароль.';
  return describeError(err);
}

export class AccountSecurity {
  #api;
  #session;
  #fetchFn;
  #link;
  #proofGeneration = 0;
  #scope = 0;
  #load = 0;
  #account = null;

  constructor({ api, session, fetchFn, location = window.location, history = window.history }) {
    this.#api = api;
    this.#session = session;
    this.#fetchFn = fetchFn;
    this.#readLink(location, history);
    window.addEventListener('hashchange', () => this.#readLink(location, history));
    this.#bind($('login-form'), (form) => session.login(form.elements.email.value, form.elements.password.value));
    this.#bind($('register-form'), (form) => session.register(
      form.elements.email.value, form.elements.username.value, form.elements.password.value, form.elements.invitation_token.value,
    ), (registered) => {
      if (registered?.verification_required) {
        this.#showTab('login');
        $('auth-notice').textContent = 'Аккаунт создан. Подтвердите email по ссылке из письма, затем войдите.';
        $('verification-help').open = true;
      }
    });
    this.#bind($('password-reset-request-form'), (form) => this.#public('/api/v1/auth/password/reset/request', {
      email: form.elements.email.value,
    }), () => { $('auth-notice').textContent = SENT; });
    this.#bind($('verification-request-form'), (form) => this.#public('/api/v1/auth/email/verification/request', {
      email: form.elements.email.value, password: form.elements.password.value,
    }), () => { $('auth-notice').textContent = SENT; });
    this.#bind($('settings-verify-form'), (form) => this.#public('/api/v1/auth/email/verification/request', {
      email: this.#account?.email, password: form.elements.password.value,
    }), () => { $('settings-notice').textContent = SENT; }, 'settings');
    this.#bind($('email-change-form'), (form) => api.call('POST', '/api/v1/auth/email/change/request', {
      body: { password: form.elements.password.value, new_email: form.elements.new_email.value },
    }), () => { $('settings-notice').textContent = SENT; }, 'settings');
    this.#bind($('email-action-form'), (form) => {
      if (!this.#link?.token) return Promise.reject({ code: 'invalid_email_action', status: 400 });
      const body = { token: this.#link.token };
      if (this.#link.purpose === 'reset-password') body.password = form.elements.password.value;
      return this.#public(ACTIONS[this.#link.purpose].path, body);
    }, (result) => this.#completed(result), 'email-action');
    $('show-login').addEventListener('click', () => this.#showTab('login'));
    $('show-register').addEventListener('click', () => this.#showTab('register'));
    $('show-password-reset').addEventListener('click', () => this.#showTab('reset'));
    $('account-settings-toggle').addEventListener('click', () => {
      if (!session.user) return;
      const open = $('account-settings').hidden;
      $('account-settings').hidden = !open;
      $('account-settings-toggle').setAttribute('aria-expanded', String(open));
      if (open) this.refresh();
    });
    $('settings-refresh').addEventListener('click', () => this.refresh());
    session.addEventListener('signedin', () => this.#sessionChanged());
    session.addEventListener('signedout', () => this.#sessionChanged());
  }

  #public(path, body) {
    return requestJSON(this.#fetchFn, 'POST', path, { body });
  }

  #bind(form, action, done, area = 'auth') {
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      const button = form.querySelector('button[type="submit"]');
      if (button.disabled) return;
      const scope = this.#scope;
      const generation = this.#session.generation;
      const proofGeneration = this.#proofGeneration;
      const current = () => this.#scope === scope && this.#session.generation === generation &&
        (area !== 'email-action' || this.#proofGeneration === proofGeneration);
      button.disabled = true;
      $(`${area}-error`).textContent = '';
      $(`${area}-notice`).textContent = '';
      try {
        const result = await action(form);
        if (!current()) return;
        form.reset();
        button.disabled = false;
        done?.(result);
      } catch (err) {
        if (!current()) return;
        $(`${area}-error`).textContent = failure(err, area === 'email-action' ? this.#link?.purpose ?? 'link' : null);
        if (err.code === 'email_verification_required') {
          $('verification-help').open = true;
          $('verification-request-form').elements.email.value = form.elements.email.value;
        }
      } finally {
        if (current()) {
          for (const input of form.querySelectorAll('input[type="password"]')) input.value = '';
          button.disabled = false;
        }
      }
    });
  }

  #showTab(tab) {
    $('login-form').hidden = tab !== 'login';
    $('register-form').hidden = tab !== 'register';
    $('password-reset-request-form').hidden = tab !== 'reset';
    $('show-password-reset').hidden = tab === 'reset';
    $('show-login').setAttribute('aria-selected', String(tab === 'login'));
    $('show-register').setAttribute('aria-selected', String(tab === 'register'));
    $('auth-error').textContent = '';
    $('auth-notice').textContent = '';
  }

  #readLink(location, history) {
    const link = consumeEmailLink(location, history);
    if (!link) return;
    this.#proofGeneration++;
    this.#link = link;
    this.#showLink();
  }

  #showLink() {
    if (!this.#link) return;
    $('email-action').hidden = false;
    const form = $('email-action-form');
    form.reset();
    form.hidden = false;
    form.elements.password.required = false;
    $('email-action-submit').disabled = false;
    $('reset-password-field').hidden = true;
    for (const id of ['email-action-description', 'email-action-notice', 'email-action-error']) $(id).textContent = '';
    if (this.#link.invalid) {
      $('email-action-title').textContent = 'Ссылка недействительна';
      $('email-action-error').textContent = BAD_LINK;
      $('email-action-form').hidden = true;
      return;
    }
    const action = ACTIONS[this.#link.purpose];
    $('email-action-title').textContent = action.title;
    $('email-action-description').textContent = action.description;
    $('email-action-submit').textContent = action.title;
    const reset = this.#link.purpose === 'reset-password';
    $('reset-password-field').hidden = !reset;
    $('email-action-form').elements.password.required = reset;
  }

  #completed(result) {
    const purpose = this.#link.purpose;
    this.#link = null;
    if (purpose !== 'verify-email' && result?.user_id && result.user_id === this.#session.user?.id) this.#session.forget();
    $('email-action-form').hidden = true;
    $('email-action-notice').textContent = ACTIONS[purpose].success;
    if (purpose === 'verify-email' && this.#session.user && !$('account-settings').hidden) this.refresh();
  }

  #sessionChanged() {
    this.#scope++;
    this.#load++;
    this.#account = null;
    $('account-settings').hidden = true;
    $('account-settings-toggle').setAttribute('aria-expanded', 'false');
    for (const id of ['settings-email', 'settings-status', 'settings-error', 'settings-notice', 'auth-error', 'auth-notice', 'email-action-error']) $(id).textContent = '';
    for (const id of ['login-form', 'register-form', 'password-reset-request-form', 'verification-request-form', 'settings-verify-form', 'email-change-form', 'email-action-form']) {
      $(id).reset();
      $(id).querySelector('button[type="submit"]').disabled = false;
    }
    $('settings-verify-form').hidden = true;
    $('email-change-form').hidden = true;
  }

  async refresh() {
    if (!this.#session.user || $('account-settings').hidden) return;
    const generation = this.#session.generation;
    const load = ++this.#load;
    const current = () => this.#session.generation === generation && this.#load === load;
    $('settings-error').textContent = '';
    $('settings-status').textContent = 'Загружаем состояние аккаунта…';
    try {
      const account = await this.#api.call('GET', '/api/v1/auth/account');
      if (!current()) return;
      if (account.user_id !== this.#session.user?.id) throw new Error('Не удалось загрузить состояние аккаунта.');
      this.#account = account;
      $('settings-email').textContent = account.email;
      let state = account.email_verified ? 'Email подтверждён.' : 'Email пока не подтверждён.';
      if (!account.email_verified && account.verification_required) state += ' Подтвердите email, чтобы сохранить доступ к аккаунту.';
      const deadline = Date.parse(account.verification_deadline);
      if (!account.email_verified && Number.isFinite(deadline)) state += ` Срок подтверждения: ${new Date(deadline).toLocaleString('ru-RU')}.`;
      $('settings-status').textContent = state;
      $('settings-verify-form').hidden = Boolean(account.email_verified);
      $('email-change-form').hidden = false;
    } catch (err) {
      if (current()) {
        $('settings-status').textContent = '';
        $('settings-error').textContent = failure(err);
      }
    }
  }
}
