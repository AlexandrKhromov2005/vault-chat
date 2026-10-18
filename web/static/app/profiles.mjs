// Public profiles (user ID and username), cached for the page's lifetime.

import { ApiError } from './http.mjs';

// The Gateway requires at least two characters for a prefix search.
const MIN_QUERY_LENGTH = 2;
const SEARCH_LIMIT = 20;

export class Profiles {
  #api;
  #known = new Map();
  #loading = new Map();

  constructor(api) {
    this.#api = api;
  }

  // name returns the cached username, or null when it is unknown or the
  // account no longer exists.
  name(userId) {
    return this.#known.get(userId)?.username ?? null;
  }

  // get returns the profile of userId, loading it once. An account that no
  // longer exists resolves with username null.
  get(userId) {
    if (this.#known.has(userId)) return Promise.resolve(this.#known.get(userId));
    if (!this.#loading.has(userId)) {
      const loading = this.#load(userId).finally(() => this.#loading.delete(userId));
      this.#loading.set(userId, loading);
    }
    return this.#loading.get(userId);
  }

  // getAll loads the profiles of several users.
  getAll(userIds) {
    return Promise.all([...new Set(userIds)].map((id) => this.get(id)));
  }

  // search finds users whose name starts with query, excluding the caller.
  async search(query) {
    const prefix = query.trim();
    if (prefix.length < MIN_QUERY_LENGTH) return [];
    const params = new URLSearchParams({ query: prefix, limit: String(SEARCH_LIMIT) });
    const { users } = await this.#api.call('GET', `/api/v1/users?${params}`);
    for (const user of users) this.#known.set(user.user_id, user);
    return users;
  }

  async #load(userId) {
    let profile;
    try {
      profile = await this.#api.call('GET', `/api/v1/users/${encodeURIComponent(userId)}`);
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 404) throw err;
      profile = { user_id: userId, username: null };
    }
    this.#known.set(userId, profile);
    return profile;
  }
}
