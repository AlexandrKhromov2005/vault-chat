// The WebSocket connection to the Gateway's event stream, following the
// client rules of docs/chat/realtime.md.
//
// Events:
//   'state' {state}  'connecting' | 'live' | 'reconnecting' | 'failed' | 'stopped'
//   'ready' {userId, expiresAt}  the connection is (again) authenticated; the
//            listener catches up whatever was missed before it.
//   'event' frame    a room event; listeners ignore unknown types.

import { Backoff } from './backoff.mjs';
import { ApiError } from './http.mjs';

// The token is renewed this long before it expires on the connection.
const REAUTH_BEFORE_MS = 90_000;
// The Gateway closes connections whose auth frames come closer together.
const MIN_REAUTH_INTERVAL_MS = 10_000;
// A token sent on the connection must stay valid past the renewal point, so
// that renewing hands over a fresh token. The extra interval also covers
// expires_at, which comes from the token's exp in whole seconds and so lets
// the renewal timer fire up to a second before the session sees the token
// as due.
const TOKEN_VALIDITY_MS = REAUTH_BEFORE_MS + MIN_REAUTH_INTERVAL_MS;
// Close codes that mean the client itself is wrong; retrying cannot help.
const CLIENT_ERRORS = new Set([1003, 1008, 1009]);
const UNAUTHORIZED = 4001;

const browserTimers = {
  set: (fn, ms) => setTimeout(fn, ms),
  clear: (id) => clearTimeout(id),
  now: () => Date.now(),
};

export class Realtime extends EventTarget {
  #session;
  #url;
  #createSocket;
  #timers;
  #backoff;
  #socket = null;
  #state = 'stopped';
  #reconnectTimer = null;
  #reauthTimer = null;
  #lastAuthAt = 0;
  // #run changes on every start and stop, so that a callback of an earlier
  // run, still waiting for a refresh, does nothing once it resumes.
  #run = 0;

  constructor({
    session,
    url,
    createSocket = (target) => new WebSocket(target),
    timers = browserTimers,
    backoff = new Backoff(),
  }) {
    super();
    this.#session = session;
    this.#url = url;
    this.#createSocket = createSocket;
    this.#timers = timers;
    this.#backoff = backoff;
  }

  get state() {
    return this.#state;
  }

  start() {
    if (this.#state !== 'stopped' && this.#state !== 'failed') return;
    this.#run++;
    this.#backoff.reset();
    this.#connect();
  }

  stop() {
    this.#run++;
    this.#setState('stopped');
    this.#clearTimers();
    const socket = this.#socket;
    this.#socket = null;
    socket?.close(1000);
  }

  #connect() {
    this.#setState(this.#state === 'reconnecting' ? 'reconnecting' : 'connecting');
    const socket = this.#createSocket(this.#url);
    this.#socket = socket;
    // Every handler checks that its socket is still the current one, so a
    // replaced connection cannot act anymore.
    socket.addEventListener('open', () => socket === this.#socket && this.#authenticate(socket));
    socket.addEventListener('message', (event) => socket === this.#socket && this.#receive(event.data));
    socket.addEventListener('close', (event) => socket === this.#socket && this.#closed(event.code));
    // A failed connection, such as an upgrade refused with 429, may report
    // only an error and never close (Node's WebSocket does so). It is handled
    // as an abnormal closure; a close event that follows in browsers finds the
    // socket already replaced and is ignored.
    socket.addEventListener('error', () => {
      if (socket !== this.#socket) return;
      this.#closed(1006);
      socket.close(1000);
    });
  }

  async #authenticate(socket) {
    let token;
    try {
      token = await this.#session.validAccessToken(TOKEN_VALIDITY_MS);
    } catch {
      // A rejected refresh token signs the session out, which stops this
      // connection; a network failure is retried like a lost connection.
      if (socket === this.#socket) socket.close(1000);
      return;
    }
    if (socket !== this.#socket) return;
    this.#lastAuthAt = this.#timers.now();
    socket.send(JSON.stringify({ type: 'auth', token }));
  }

  #receive(data) {
    let frame;
    try {
      frame = JSON.parse(data);
    } catch {
      return;
    }
    if (!frame || typeof frame.type !== 'string') return;

    if (frame.type === 'ready') {
      this.#backoff.reset();
      this.#setState('live');
      this.#scheduleReauth(Date.parse(frame.expires_at));
      this.dispatchEvent(new CustomEvent('ready', { detail: { userId: frame.user_id, expiresAt: frame.expires_at } }));
      return;
    }
    this.dispatchEvent(new CustomEvent('event', { detail: frame }));
  }

  // #scheduleReauth sends a fresh token before the current one expires; the
  // Gateway answers with another ready frame.
  #scheduleReauth(expiresAt) {
    this.#timers.clear(this.#reauthTimer);
    const now = this.#timers.now();
    const at = Math.max(expiresAt - REAUTH_BEFORE_MS, this.#lastAuthAt + MIN_REAUTH_INTERVAL_MS);
    this.#reauthTimer = this.#timers.set(() => this.#authenticate(this.#socket), Math.max(0, at - now));
  }

  async #closed(code) {
    this.#socket = null;
    this.#clearTimers();
    if (this.#state === 'stopped') return;

    if (CLIENT_ERRORS.has(code)) {
      this.#setState('failed');
      return;
    }
    this.#setState('reconnecting');

    const run = this.#run;
    let delay = this.#backoff.next();
    if (code === UNAUTHORIZED) {
      try {
        await this.#session.refresh();
        delay = 0;
      } catch (err) {
        if (run === this.#run && err instanceof ApiError && err.status === 401) {
          this.#setState('stopped');
          return;
        }
      }
    }
    // Stopped, and maybe started again, while the token was refreshed.
    if (run !== this.#run) return;
    this.#reconnectTimer = this.#timers.set(() => this.#connect(), delay);
  }

  #clearTimers() {
    this.#timers.clear(this.#reconnectTimer);
    this.#timers.clear(this.#reauthTimer);
    this.#reconnectTimer = null;
    this.#reauthTimer = null;
  }

  #setState(state) {
    if (state === this.#state) return;
    this.#state = state;
    this.dispatchEvent(new CustomEvent('state', { detail: { state } }));
  }
}
