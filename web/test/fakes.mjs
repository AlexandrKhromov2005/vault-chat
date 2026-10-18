// Test doubles for the browser APIs the client modules depend on.

// fakeFetch answers requests from a queue of {status, body} responses and
// records every request. A response may be an Error to simulate a network
// failure, or a function of the request.
export function fakeFetch(responses = []) {
  const queue = [...responses];
  const calls = [];
  const fetchFn = async (url, init = {}) => {
    const request = {
      url,
      method: init.method ?? 'GET',
      headers: init.headers ?? {},
      body: init.body === undefined ? undefined : JSON.parse(init.body),
    };
    calls.push(request);
    let next = queue.shift();
    if (typeof next === 'function') next = next(request);
    if (next === undefined) throw new Error(`unexpected request ${request.method} ${url}`);
    if (next instanceof Error) throw next;
    const text = next.body === undefined ? '' : JSON.stringify(next.body);
    return {
      status: next.status,
      ok: next.status >= 200 && next.status < 300,
      text: async () => text,
    };
  };
  fetchFn.calls = calls;
  fetchFn.pending = () => queue.length;
  return fetchFn;
}

// memoryStorage implements the subset of the Web Storage API the client uses.
export function memoryStorage(initial = {}) {
  const items = new Map(Object.entries(initial));
  return {
    getItem: (key) => (items.has(key) ? items.get(key) : null),
    setItem: (key, value) => items.set(key, String(value)),
    removeItem: (key) => items.delete(key),
    get size() {
      return items.size;
    },
  };
}

export function apiError(status, code, message) {
  return { status, body: { error: { code, message } } };
}
