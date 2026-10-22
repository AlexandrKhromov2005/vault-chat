// Helpers for tests of the client's UI modules on the minimal DOM.

import { ApiError } from '../static/app/http.mjs';
import { settle } from './minidom.mjs';

// scriptedApi answers Api calls from handlers registered per "METHOD path".
// A handler returns the response body, throws (an ApiError), or returns a
// promise to hold the response until the test releases it. Calls are
// recorded; a call without a handler fails the test.
export function scriptedApi() {
  const handlers = new Map();
  const calls = [];
  const answer = async (key, options) => {
    calls.push({ key, body: options?.body });
    const handler = handlers.get(key);
    if (!handler) throw new Error(`unexpected ${key}`);
    return handler(options);
  };
  return {
    calls,
    on(key, handler) {
      handlers.set(key, typeof handler === 'function' ? handler : () => structuredClone(handler));
      return this;
    },
    call: (method, path, options) => answer(`${method} ${path}`, options),
    blob: (path) => answer(`GET ${path}`),
  };
}

export const notFound = () => {
  throw new ApiError(404, 'not_found', 'room not found');
};

// signedIn is a stand-in for Session with the given user, switchable in
// tests that sign another user in.
export function signedIn(id, username) {
  return { user: { id, username } };
}

// type sets an input's value and fires input, as typing does.
export async function type(input, value) {
  input.value = value;
  input.dispatchEvent(new Event('input'));
  await settle();
  await settle();
}

// submit fires a form's submit event.
export async function submit(form) {
  form.dispatchEvent(new Event('submit', { cancelable: true }));
  await settle();
  await settle();
}

export async function click(element) {
  element.click();
  await settle();
  await settle();
}

// texts returns the trimmed text of each child of a list.
export const texts = (list) => list.children.map((item) => item.textContent.trim());
