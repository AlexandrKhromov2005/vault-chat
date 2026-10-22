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

// flush lets pending promise callbacks run.
const flush = () => new Promise((resolve) => setImmediate(resolve));

// FakeTimers runs scheduled callbacks when a test advances its clock. set,
// clear and now stand in for the browser's timers and clock.
export class FakeTimers {
  #queue = [];
  #nextId = 1;

  constructor(now = 0) {
    this.now = now;
  }

  set = (fn, ms) => {
    const id = this.#nextId++;
    this.#queue.push({ id, at: this.now + ms, fn });
    return id;
  };

  clear = (id) => {
    this.#queue = this.#queue.filter((timer) => timer.id !== id);
  };

  async advance(ms) {
    const until = this.now + ms;
    for (;;) {
      this.#queue.sort((a, b) => a.at - b.at);
      const due = this.#queue[0];
      if (!due || due.at > until) break;
      this.#queue.shift();
      this.now = due.at;
      due.fn();
      await flush();
    }
    this.now = until;
    await flush();
  }

  get pending() {
    return this.#queue.length;
  }
}

// FakeSocket plays the Gateway's side of one WebSocket connection.
export class FakeSocket extends EventTarget {
  sent = [];
  closedWith = null;

  constructor(url) {
    super();
    this.url = url;
  }

  send(data) {
    this.sent.push(JSON.parse(data));
  }

  close(code) {
    this.closedWith = code;
  }

  open() {
    this.dispatchEvent(new Event('open'));
  }

  receive(frame) {
    this.dispatchEvent(Object.assign(new Event('message'), { data: typeof frame === 'string' ? frame : JSON.stringify(frame) }));
  }

  serverClose(code) {
    this.dispatchEvent(Object.assign(new Event('close'), { code }));
  }

  // fail reports a connection failure, such as an upgrade refused with 429.
  fail() {
    this.dispatchEvent(new Event('error'));
  }
}
