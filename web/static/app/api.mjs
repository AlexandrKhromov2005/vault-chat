// Authenticated calls to the Gateway API.

import { ApiError, requestJSON } from './http.mjs';

export class Api {
  #fetchFn;
  #session;

  constructor({ fetchFn, session }) {
    this.#fetchFn = fetchFn;
    this.#session = session;
  }

  // call sends an authenticated request. A 401 means the access token was
  // rejected before its known expiry; it is refreshed once and the request
  // repeated. Requests that change data are safe to repeat: message sends are
  // idempotent by client_message_id and member changes by their target.
  async call(method, path, { body } = {}) {
    const token = await this.#session.validAccessToken();
    try {
      return await requestJSON(this.#fetchFn, method, path, { body, token });
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 401) throw err;
    }
    const fresh = await this.#session.refresh();
    return requestJSON(this.#fetchFn, method, path, { body, token: fresh });
  }
}
