// Test doubles for the browser APIs the client modules depend on.

// fakeFetch answers requests from a queue of {status, body, bytes} responses
// and records every request. A response may be an Error to simulate a
// network failure, or a function of the request, which may return a promise
// to delay the response. JSON request bodies are recorded parsed; other
// bodies (FormData) as they are.
export function fakeFetch(responses = []) {
  const queue = [...responses];
  const calls = [];
  const fetchFn = async (url, init = {}) => {
    const request = {
      url,
      method: init.method ?? 'GET',
      headers: init.headers ?? {},
      body: typeof init.body === 'string' ? JSON.parse(init.body) : init.body,
    };
    calls.push(request);
    let next = queue.shift();
    if (typeof next === 'function') next = await next(request);
    if (next === undefined) throw new Error(`unexpected request ${request.method} ${url}`);
    if (next instanceof Error) throw next;
    const text = next.body === undefined ? '' : JSON.stringify(next.body);
    return {
      status: next.status,
      ok: next.status >= 200 && next.status < 300,
      text: async () => text,
      blob: async () => new Blob([next.bytes ?? text]),
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

// deferred returns a promise with its resolve function, to hold a response
// until the test releases it.
export function deferred() {
  let resolve;
  const promise = new Promise((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
