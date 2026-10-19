// Entry point of the web client: wires the page to the session.

import { describeError } from './errors.mjs';
import { Session } from './session.mjs';

const fetchFn = window.fetch.bind(window);
const session = new Session({ fetchFn, storage: window.sessionStorage });

const $ = (id) => document.getElementById(id);

function render() {
  const user = session.user;
  $('auth').hidden = Boolean(user);
  $('workspace').hidden = !user;
  $('account').hidden = !user;
  $('account-name').textContent = user ? user.username : '';
}

function showAuthTab(tab) {
  const login = tab === 'login';
  $('login-form').hidden = !login;
  $('register-form').hidden = login;
  $('show-login').setAttribute('aria-selected', String(login));
  $('show-register').setAttribute('aria-selected', String(!login));
  $('auth-error').textContent = '';
}

// submitWith runs action for a form, disabling it while the request runs and
// reporting failures in the auth error line.
function submitWith(form, action) {
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const fields = Object.fromEntries(new FormData(form));
    const button = form.querySelector('button[type="submit"]');
    button.disabled = true;
    $('auth-error').textContent = '';
    try {
      await action(fields);
      form.reset();
    } catch (err) {
      $('auth-error').textContent =
        err.code === 'unauthenticated' ? 'Неверный email или пароль.' : describeError(err);
    } finally {
      button.disabled = false;
    }
  });
}

submitWith($('login-form'), ({ email, password }) => session.login(email, password));
submitWith($('register-form'), ({ email, username, password }) => session.register(email, username, password));
$('show-login').addEventListener('click', () => showAuthTab('login'));
$('show-register').addEventListener('click', () => showAuthTab('register'));
$('logout').addEventListener('click', () => session.logout());

session.addEventListener('signedin', render);
session.addEventListener('signedout', (event) => {
  render();
  if (event.detail.reason === 'expired') {
    $('auth-error').textContent = 'Сессия истекла. Войдите снова.';
  }
});

render();
