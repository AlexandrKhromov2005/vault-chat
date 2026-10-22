// Authenticated calls to the Gateway API.

import { ApiError, requestBlob, requestJSON } from './http.mjs';

const sessionEnded = () => new ApiError(401, 'unauthenticated', 'Сессия уже завершена');

export class Api {
  #fetchFn;
  #session;

  constructor({ fetchFn, session }) {
    this.#fetchFn = fetchFn;
    this.#session = session;
  }

  // call sends an authenticated request and returns the JSON response.
  call(method, path, { body } = {}) {
    return this.#authorized((token) => requestJSON(this.#fetchFn, method, path, { body, token }));
  }

  // blob downloads an authenticated response body, such as file content.
  blob(path) {
    return this.#authorized((token) => requestBlob(this.#fetchFn, path, { token }));
  }

  // #authorized runs send with a valid access token. A 401 means the token
  // was rejected before its known expiry; it is refreshed once and the
  // request repeated. Repeating is safe: the Gateway rejects a bad token
  // before the request reaches a backend, so nothing was done yet.
  //
  // A request belongs to the session that started it, identified by
  // session.generation, which changes on every sign-in and sign-out. Once
  // that session has ended the request is abandoned: it is neither sent nor
  // refreshed and repeated, which would act as the next user.
  async #authorized(send) {
    const generation = this.#session.generation;
    const current = (value) => {
      if (this.#session.generation !== generation) throw sessionEnded();
      return value;
    };
    const token = current(await this.#session.validAccessToken());
    try {
      return await send(token);
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 401) throw err;
    }
    current();
    return send(current(await this.#session.refresh()));
  }
}
