// The signed-in user's tokens.
//
// Tokens live in sessionStorage: they survive a page reload but not the tab,
// and every tab signs in on its own, which lets one browser show several
// users side by side. Refresh tokens rotate on every use, so a duplicated tab
// (which copies sessionStorage) and its original must not both refresh; the
// second refresh would be taken for token theft and end both sessions.

import { ApiError, requestJSON } from './http.mjs';

const STORAGE_KEY = 'vault-chat.session';

// Access tokens are renewed this long before they expire, so that requests
// and the WebSocket never present an expired one.
const RENEW_BEFORE_MS = 60_000;

// sessionEnded is the error of a request made for a session that ended
// before it was answered.
const sessionEnded = () => new ApiError(401, 'unauthenticated', 'Сессия уже завершена');

export class Session extends EventTarget {
  #fetchFn;
  #storage;
  #now;
  #state;
  #refreshing = null;
  // #generation identifies the current session. It changes whenever a session
  // starts or ends, so that the answer to a request made for an earlier
  // session is dropped instead of restoring or ending the current one.
  #generation = 0;

  constructor({ fetchFn, storage, now = () => Date.now() }) {
    super();
    this.#fetchFn = fetchFn;
    this.#storage = storage;
    this.#now = now;
    this.#state = load(storage);
  }

  // generation identifies the current session; Api uses it to abandon the
  // requests of a session that has ended.
  get generation() {
    return this.#generation;
  }

  // user is {id, username} of the signed-in user, or null.
  get user() {
    return this.#state ? { id: this.#state.userId, username: this.#state.username } : null;
  }

  get accessToken() {
    return this.#state?.accessToken ?? null;
  }

  // accessExpiresAt is the expiry of the access token in milliseconds.
  get accessExpiresAt() {
    return this.#state?.accessExpiresAt ?? 0;
  }

  async register(email, username, password) {
    await requestJSON(this.#fetchFn, 'POST', '/api/v1/auth/register', {
      body: { email, username, password },
    });
    await this.login(email, password);
  }

  async login(email, password) {
    const generation = this.#generation;
    const pair = await requestJSON(this.#fetchFn, 'POST', '/api/v1/auth/login', {
      body: { email, password },
    });
    const me = await requestJSON(this.#fetchFn, 'GET', '/api/v1/auth/me', { token: pair.access_token });
    if (generation !== this.#generation) throw sessionEnded();
    this.#generation++;
    this.#refreshing = null;
    this.#save({ ...fromPair(pair), username: me.username });
    this.dispatchEvent(new Event('signedin'));
  }

  // refresh exchanges the refresh token for a new pair and returns the new
  // access token. Concurrent calls share one request. A rejected refresh
  // token signs the user out; a network failure keeps the session.
  refresh() {
    if (!this.#refreshing) {
      const refreshing = this.#doRefresh().finally(() => {
        if (this.#refreshing === refreshing) this.#refreshing = null;
      });
      this.#refreshing = refreshing;
    }
    return this.#refreshing;
  }

  async #doRefresh() {
    const state = this.#state;
    const generation = this.#generation;
    if (!state) throw new ApiError(401, 'unauthenticated', 'Нужно войти заново');
    let pair;
    try {
      pair = await requestJSON(this.#fetchFn, 'POST', '/api/v1/auth/refresh', {
        body: { refresh_token: state.refreshToken },
      });
    } catch (err) {
      if (generation !== this.#generation) throw sessionEnded();
      if (err instanceof ApiError && err.status === 401) this.#signOut('expired');
      throw err;
    }
    if (generation !== this.#generation) throw sessionEnded();
    this.#save({ ...fromPair(pair), username: state.username });
    return this.#state.accessToken;
  }

  // validAccessToken returns an access token that stays valid for more than
  // minValidityMs, refreshing it first if needed.
  async validAccessToken(minValidityMs = RENEW_BEFORE_MS) {
    if (!this.#state) throw new ApiError(401, 'unauthenticated', 'Нужно войти');
    if (this.#state.accessExpiresAt - this.#now() > minValidityMs) return this.#state.accessToken;
    return this.refresh();
  }

  // logout forgets the session at once and then ends it on the server when
  // that can be reached. Ending it locally first also drops a refresh that is
  // answered while the logout request is under way.
  async logout() {
    const state = this.#state;
    this.#signOut('logout');
    if (!state) return;
    try {
      await requestJSON(this.#fetchFn, 'POST', '/api/v1/auth/logout', {
        body: { refresh_token: state.refreshToken },
      });
    } catch {
      // An unreachable server only means the refresh token stays valid until
      // it expires.
    }
  }

  #save(state) {
    this.#state = state;
    this.#storage.setItem(STORAGE_KEY, JSON.stringify(state));
  }

  #signOut(reason) {
    this.#generation++;
    this.#refreshing = null;
    this.#state = null;
    this.#storage.removeItem(STORAGE_KEY);
    this.dispatchEvent(new CustomEvent('signedout', { detail: { reason } }));
  }
}

function fromPair(pair) {
  return {
    userId: pair.user_id,
    accessToken: pair.access_token,
    refreshToken: pair.refresh_token,
    accessExpiresAt: Date.parse(pair.access_expires_at),
  };
}

function load(storage) {
  try {
    const state = JSON.parse(storage.getItem(STORAGE_KEY));
    if (state && state.userId && state.accessToken && state.refreshToken) return state;
  } catch {
    // A corrupted entry is treated as signed out.
  }
  return null;
}
