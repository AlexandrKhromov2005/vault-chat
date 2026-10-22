// Entry point of the web client: wires the page to the session and the
// signed-in workspace.

import { Api } from './api.mjs';
import { describeError } from './errors.mjs';
import { Realtime } from './realtime.mjs';
import { Session } from './session.mjs';
import { Workspace } from './workspace.mjs';

const fetchFn = window.fetch.bind(window);
const session = new Session({ fetchFn, storage: window.sessionStorage });
const workspace = new Workspace({ api: new Api({ fetchFn, session }), session });
// Same origin as the page, which the Gateway's WebSocket origin check allows.
const realtime = new Realtime({
  session,
  url: `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/api/v1/ws`,
});

const $ = (id) => document.getElementById(id);

const CONNECTION_TEXT = {
  connecting: 'подключение…',
  live: 'в сети',
  reconnecting: 'переподключение…',
  failed: 'нет связи с событиями, обновите страницу',
  stopped: 'нет связи с событиями',
};

realtime.addEventListener('state', (event) => {
  const { state } = event.detail;
  $('connection').dataset.state = state;
  $('connection').textContent = CONNECTION_TEXT[state] ?? '';
});
// Every (re)connection resynchronizes: events are not replayed.
realtime.addEventListener('ready', () => workspace.refresh());
realtime.addEventListener('event', (event) => workspace.handleEvent(event.detail));

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

session.addEventListener('signedin', () => {
  render();
  workspace.refresh();
  realtime.start();
});
session.addEventListener('signedout', (event) => {
  realtime.stop();
  workspace.reset();
  render();
  if (event.detail.reason === 'expired') {
    $('auth-error').textContent = 'Сессия истекла. Войдите снова.';
  }
});

render();
if (session.user) {
  // The room list shows at once; live events start when the socket is ready.
  workspace.refresh();
  realtime.start();
}
