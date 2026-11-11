// Entry point of the web client: wires the page to the session and the
// signed-in workspace.

import { Api } from './api.mjs';
import { AccountSecurity } from './account-security.mjs';
import { bindCallPanel } from './call-panel.mjs';
import { Calls } from './calls.mjs';
import { Realtime } from './realtime.mjs';
import { Session } from './session.mjs';
import { Workspace } from './workspace.mjs';

const fetchFn = window.fetch.bind(window);
const session = new Session({ fetchFn, storage: window.sessionStorage });
const api = new Api({ fetchFn, session });
new AccountSecurity({ api, session, fetchFn });
const calls = new Calls({ api, session });
const workspace = new Workspace({ api, session, calls });
bindCallPanel({ calls, peerName: (view) => workspace.peerName(view) });
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
realtime.addEventListener('ready', () => {
  workspace.refresh();
  calls.reconcile();
});
realtime.addEventListener('event', (event) => {
  const frame = event.detail;
  if (typeof frame.type === 'string' && frame.type.startsWith('call.')) calls.handleEvent(frame);
  else workspace.handleEvent(frame);
});

function render() {
  const user = session.user;
  $('auth').hidden = Boolean(user);
  $('workspace').hidden = !user;
  $('account').hidden = !user;
  $('account-name').textContent = user ? user.username : '';
}

// A call ends before the session that is its device does.
$('logout').addEventListener('click', async () => {
  await calls.hangUp();
  session.logout();
});
// Closing or leaving the page releases the microphone and tells the server,
// as far as the browser lets the request finish; otherwise the server ends
// the call by its heartbeat timeout.
window.addEventListener('pagehide', () => calls.hangUp());

session.addEventListener('signedin', () => {
  render();
  workspace.refresh();
  realtime.start();
});
session.addEventListener('signedout', (event) => {
  realtime.stop();
  calls.reset();
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
