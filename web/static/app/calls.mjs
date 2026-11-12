// One-to-one audio calls in direct dialogs: the client side of the call
// control of ADR 0005 (docs/adr/0005-call-control.md).
//
// The server is authoritative. Commands go through REST and are idempotent:
// a command keeps its command_id across retries, a signal its sender_seq.
// WebSocket events only prompt the client to look; it reads the call's
// state over REST and applies the peer's signals strictly in signal_seq
// order, catching up through GET .../signals?after= after a gap. Events may
// be lost, so ringing calls are also polled, and so are the signals of a
// call that is still connecting.
//
// Only the browser tab that created a call or accepted it takes part in its
// media: it is the call's device (the Auth session the tab is signed in
// with). Other tabs of the same users see the call but do not touch the
// microphone.
//
// Calls are offered only once the server has listed the user's calls: a
// Gateway without the Calls API answers 404, and the client then hides them.
//
// Events: 'change' — the call shown to the user, or availability, changed.

import { describeError } from './errors.mjs';
import { ApiError } from './http.mjs';
import { MicrophoneError, PeerLink, openMicrophone, stopStream } from './call-media.mjs';

const STATES = new Set(['ringing', 'connecting', 'active', 'ended']);
const SIGNAL_KINDS = new Set(['offer', 'answer', 'candidate', 'end-of-candidates']);

export const HEARTBEAT_MS = 15_000; // ADR 0005: every 15 s, the call ends after 45 s without one
export const RINGING_POLL_MS = 3_000;
export const SIGNAL_POLL_MS = 1_000;
export const HANGUP_TIMEOUT_MS = 2_000;
const RETRY_DELAYS_MS = [500, 1_500];
const MAX_SIGNAL_PAGES = 20;
const MAX_REMEMBERED_ENDED = 64;
const MAX_ID_LENGTH = 128;

const callPath = (id, action) => `/api/v1/calls/${encodeURIComponent(id)}${action ? `/${action}` : ''}`;

const validId = (value) => typeof value === 'string' && value !== '' && value.length <= MAX_ID_LENGTH;

// parseCall reads a call as the server returns it. Only call_id and state
// are required; the rest is kept when present and valid. States only move
// forward, so a late answer is recognized without state_version.
export function parseCall(raw) {
  if (!raw || typeof raw !== 'object') return null;
  if (!validId(raw.call_id) || !STATES.has(raw.state)) return null;
  const call = { id: raw.call_id, state: raw.state };
  if (validId(raw.room_id)) call.roomId = raw.room_id;
  if (validId(raw.caller_id)) call.callerId = raw.caller_id;
  if (validId(raw.callee_id)) call.calleeId = raw.callee_id;
  return call;
}

// parseSignal reads one entry of the signal journal.
export function parseSignal(raw) {
  if (!raw || typeof raw !== 'object') return null;
  const { signal_seq: seq, kind, payload } = raw;
  if (!Number.isSafeInteger(seq) || seq <= 0 || !SIGNAL_KINDS.has(kind) || typeof payload !== 'string') return null;
  const signal = { seq, kind, payload };
  return signal;
}

// parseIceServers keeps the STUN and TURN servers of a POST .../turn answer.
export function parseIceServers(raw) {
  const list = Array.isArray(raw?.ice_servers) ? raw.ice_servers : [];
  const servers = [];
  for (const server of list.slice(0, 8)) {
    const urls = (Array.isArray(server?.urls) ? server.urls : [server?.urls])
      .filter((url) => typeof url === 'string' && /^(stun|turn|turns):/.test(url) && url.length <= 256);
    if (urls.length === 0) continue;
    const entry = { urls };
    if (typeof server.username === 'string' && server.username.length <= 256) entry.username = server.username;
    if (typeof server.credential === 'string' && server.credential.length <= 256) entry.credential = server.credential;
    servers.push(entry);
  }
  return servers;
}

const MICROPHONE_TEXT = {
  unsupported: 'Браузер не даёт доступа к микрофону: звонки работают только по HTTPS.',
  denied: 'Нет доступа к микрофону. Разрешите его в настройках браузера.',
  missing: 'Микрофон не найден.',
  busy: 'Микрофон занят другим приложением.',
  failed: 'Не удалось включить микрофон.',
};

const FAILURE_TEXT = {
  ice: 'Не удалось соединиться с собеседником.',
  sdp: 'Браузер не смог подготовить звонок.',
  protocol: 'Собеседник прислал некорректные данные звонка.',
  signal: 'Не удалось передать данные звонка.',
};

function describeCallError(err) {
  if (err instanceof MicrophoneError) return MICROPHONE_TEXT[err.reason] ?? MICROPHONE_TEXT.failed;
  if (!(err instanceof ApiError)) return 'Что-то пошло не так.';
  switch (err.status) {
    case 404:
      return 'Звонок недоступен: он завершён или вы не его участник.';
    case 409:
      return 'Собеседник занят, или у вас уже есть незавершённый звонок.';
    case 429:
      return 'Слишком много вызовов. Подождите немного.';
    case 503:
      return 'Сервис звонков перегружен или недоступен. Повторите позже.';
    default:
      return describeError(err);
  }
}

const browserTimers = {
  set: (fn, ms) => setTimeout(fn, ms),
  clear: (id) => clearTimeout(id),
};

const retryable = (err, { rateLimited }) =>
  err instanceof ApiError && (err.status === 0 || (rateLimited && err.status === 429));

export class Calls extends EventTarget {
  #api;
  #session;
  #mediaDevices;
  #createPeerConnection;
  #timers;
  #newId;
  #call = null;
  #ended = []; // ids of calls that ended here, so that a stale answer cannot revive them
  #available = null; // whether the server has the Calls API; null until known
  // #run changes on every reset, so that work of an earlier user, resuming
  // after an await, does nothing.
  #run = 0;

  constructor({
    api,
    session,
    mediaDevices = globalThis.navigator?.mediaDevices,
    createPeerConnection = (config) => new RTCPeerConnection(config),
    timers = browserTimers,
    newId = () => crypto.randomUUID(),
  }) {
    super();
    this.#api = api;
    this.#session = session;
    this.#mediaDevices = mediaDevices;
    this.#createPeerConnection = createPeerConnection;
    this.#timers = timers;
    this.#newId = newId;
  }

  // available is true once the server listed the user's calls, false when
  // it has no Calls API, and null before it was asked.
  get available() {
    return this.#available;
  }

  // view is the call shown to the user, or null:
  //   state    'starting' (microphone, then create) | 'ringing' | 'connecting'
  //            | 'active' | 'ended'
  //   role     'caller' | 'callee'
  //   owned    whether this tab is the call's device and carries its audio
  //   pending  the command under way, or null
  //   error    what went wrong, as text for the user
  //   remote   the peer's MediaStream once it arrived
  get view() {
    const call = this.#call;
    if (!call) return null;
    return {
      id: call.id,
      roomId: call.roomId,
      role: call.role,
      peerId: call.role === 'caller' ? call.calleeId : call.callerId,
      state: call.state,
      owned: call.owned,
      pending: call.pending,
      error: call.error,
      remote: call.remote,
    };
  }

  // start calls the other participant of a direct room.
  async start(roomId) {
    if (this.#call && this.#call.state !== 'ended') return;
    const run = this.#run;
    const call = this.#record({ roomId, role: 'caller', callerId: this.#me, state: 'starting', owned: true, pending: 'start' });
    this.#call = call;
    this.#changed();

    let stream;
    try {
      stream = await openMicrophone(this.#mediaDevices);
    } catch (err) {
      if (this.#is(call, run)) this.#finish(call, describeCallError(err));
      return;
    }
    if (!this.#is(call, run)) {
      stopStream(stream);
      return;
    }
    call.stream = stream;

    const commandId = this.#newId();
    let result;
    try {
      result = await this.#retrying(run, () =>
        this.#api.call('POST', '/api/v1/calls', { body: { room_id: roomId, command_id: commandId } }));
    } catch (err) {
      if (!this.#is(call, run)) return;
      this.#finish(call, describeCallError(err));
      // Busy may mean a call of this user that the client does not show yet.
      if (err instanceof ApiError && err.status === 409) this.reconcile();
      return;
    }
    if (!this.#is(call, run)) return;
    const created = parseCall({ state: 'ringing', ...result });
    if (!created) {
      this.#finish(call, 'Сервер вернул неожиданный ответ.');
      return;
    }
    call.id = created.id;
    call.pending = null;
    this.#apply(call, created);
    this.#changed();
  }

  // accept answers the ringing call on this tab, which becomes its device.
  async accept() {
    const call = this.#call;
    if (!call?.id || call.state !== 'ringing' || call.role !== 'callee' || call.pending) return;
    const run = this.#run;
    call.pending = 'accept';
    call.error = '';
    this.#changed();

    let stream;
    try {
      stream = await openMicrophone(this.#mediaDevices);
    } catch (err) {
      if (!this.#is(call, run)) return;
      call.pending = null;
      call.error = describeCallError(err);
      this.#changed();
      return;
    }
    // Rejected, ended or signed out while the browser asked for the microphone.
    if (!this.#is(call, run) || call.state !== 'ringing' || call.pending !== 'accept') {
      stopStream(stream);
      return;
    }
    call.stream = stream;

    const commandId = this.#newId();
    let result;
    try {
      result = await this.#retrying(run, () =>
        this.#api.call('POST', callPath(call.id, 'accept'), { body: { command_id: commandId } }));
    } catch (err) {
      if (!this.#is(call, run)) return;
      this.#release(call);
      call.pending = null;
      if (err instanceof ApiError && err.status === 409) {
        call.error = 'Звонок уже принят на другом устройстве или завершён.';
        this.#refresh(call);
      } else if (err instanceof ApiError && err.status === 404) {
        this.#finish(call, 'Звонок уже завершён.');
        return;
      } else {
        call.error = describeCallError(err);
      }
      this.#changed();
      return;
    }
    if (!this.#is(call, run)) return;
    if (call.pending !== 'accept') {
      // Hung up while the accept was under way: end what it connected.
      this.#command(run, call, 'end').catch(() => {});
      this.#finish(call);
      return;
    }
    call.pending = null;
    call.owned = true;
    this.#apply(call, { state: 'connecting', ...parseCall({ call_id: call.id, ...result }) });
    this.#changed();
  }

  reject() {
    return this.#hangUp('reject');
  }

  cancel() {
    return this.#hangUp('cancel');
  }

  end() {
    return this.#hangUp('end');
  }

  // hangUp ends this tab's call in whatever way its state allows, for
  // logout and closing the page. A call merely shown here, such as an
  // incoming one that another tab may still answer, is left alone. It waits
  // for the server at most HANGUP_TIMEOUT_MS.
  async hangUp() {
    const call = this.#call;
    if (!call || call.state === 'ended') return;
    if (call.state === 'starting') {
      this.#finish(call);
      return;
    }
    if (!call.owned && call.pending !== 'accept') return;
    let action = 'end';
    if (call.state === 'ringing') action = call.role === 'caller' ? 'cancel' : 'reject';
    await Promise.race([this.#hangUp(action), new Promise((resolve) => this.#timers.set(resolve, HANGUP_TIMEOUT_MS))]);
  }

  // dismiss hides an ended call.
  dismiss() {
    if (this.#call?.state !== 'ended') return;
    this.#call = null;
    this.#changed();
  }

  // handleEvent takes a call.* WebSocket event. Events are hints: the call
  // is then read from the server.
  handleEvent(frame) {
    if (typeof frame?.type !== 'string' || !frame.type.startsWith('call.')) return;
    const id = frame.call_id;
    if (!validId(id)) return;
    const call = this.#call;
    if (call?.id === id) {
      if (call.state === 'ended') return;
      if (frame.type === 'call.signal') {
        const signal = parseSignal(frame.signal ?? frame);
        this.#enqueue(call, async () => {
          if (!signal || !(await this.#ingest(call, [signal], false))) await this.#catchUp(call);
        });
        return;
      }
      this.#refresh(call);
      return;
    }
    if (this.#ended.includes(id)) return;
    this.reconcile();
  }

  // reconcile reads the user's unfinished calls, after every (re)connection
  // of the event stream and whenever an event names an unknown call.
  async reconcile() {
    const run = this.#run;
    let result;
    try {
      result = await this.#api.call('GET', '/api/v1/calls?active=true');
    } catch (err) {
      if (run === this.#run && err instanceof ApiError && err.status === 404 && this.#available !== false) {
        this.#available = false;
        this.#changed();
      }
      return; // otherwise the next connection tries again
    }
    if (run !== this.#run) return;
    if (this.#available !== true) {
      this.#available = true;
      this.#changed();
    }
    const listed = (Array.isArray(result?.calls) ? result.calls : [])
      .map(parseCall)
      .filter((call) => call && call.state !== 'ended' && !this.#ended.includes(call.id));

    const current = this.#call;
    if (current && current.state !== 'ended') {
      if (!current.id) return; // still being created
      const seen = listed.find((call) => call.id === current.id);
      if (seen) this.#apply(current, seen);
      else this.#refresh(current); // gone from the list: most likely ended
      if (current.link) this.#enqueue(current, () => this.#catchUp(current));
      this.#changed();
      return;
    }

    const me = this.#me;
    for (const found of listed) {
      const role = found.callerId === me ? 'caller' : found.calleeId === me ? 'callee' : null;
      if (!role) continue;
      const call = this.#record({ ...found, role, state: found.state, owned: false });
      this.#call = call;
      this.#schedule(call);
      this.#changed();
      return;
    }
  }

  // reset drops everything after sign-out. The microphone is released; the
  // server ends a call this way by its timeouts.
  reset() {
    this.#run++;
    if (this.#call) this.#release(this.#call);
    this.#clearTimers(this.#call);
    this.#call = null;
    this.#ended = [];
    this.#available = null;
    this.#changed();
  }

  get #me() {
    return this.#session.user?.id;
  }

  #record(fields) {
    return {
      id: null,
      roomId: undefined,
      callerId: undefined,
      calleeId: undefined,
      role: null,
      owned: false,
      pending: null,
      error: '',
      stream: null,
      link: null,
      remote: null,
      mediaStarted: false,
      iceUp: false,
      queue: Promise.resolve(),
      senderSeq: 0,
      cursor: 0,
      own: new Set(), // signal_seq of this device's signals
      unsure: new Set(), // kind and payload of signals whose answer was lost
      poll: null,
      beat: null,
      ...fields,
    };
  }

  #is(call, run = this.#run) {
    return run === this.#run && this.#call === call;
  }

  #changed() {
    this.dispatchEvent(new CustomEvent('change'));
  }

  // #apply takes the server's view of the call. An ended call stays ended.
  #apply(call, view) {
    if (call.state === 'ended' || !view) return;
    for (const key of ['roomId', 'callerId', 'calleeId']) if (view[key] !== undefined) call[key] = view[key];
    if (!STATES.has(view.state)) return;

    if (view.state === 'ended') {
      this.#finish(call, call.error);
      return;
    }
    // A state does not go back: a late answer cannot undo a transition.
    const order = ['ringing', 'connecting', 'active'];
    if (order.indexOf(view.state) > order.indexOf(call.state) || call.state === 'starting') call.state = view.state;
    if (call.owned && call.state !== 'ringing' && !call.mediaStarted) this.#beginMedia(call);
    this.#schedule(call);
  }

  async #refresh(call) {
    const run = this.#run;
    if (!call.id) return;
    let result;
    try {
      result = await this.#api.call('GET', callPath(call.id));
    } catch (err) {
      if (this.#is(call, run) && err instanceof ApiError && err.status === 404) this.#finish(call, call.error);
      return;
    }
    if (!this.#is(call, run)) return;
    this.#apply(call, parseCall({ call_id: call.id, ...result }));
    this.#changed();
  }

  async #hangUp(action) {
    const call = this.#call;
    if (!call?.id || call.state === 'ended' || call.pending === action) return;
    if (action === 'reject' && !(call.state === 'ringing' && call.role === 'callee')) return;
    if (action === 'cancel' && !(call.state === 'ringing' && call.role === 'caller')) return;
    const run = this.#run;
    const owned = call.owned || action === 'reject';
    const accepting = call.pending === 'accept';
    call.pending = action;
    call.error = '';
    // The microphone goes off at once, whatever the server answers.
    this.#release(call);
    this.#changed();

    try {
      await this.#command(run, call, action);
    } catch (err) {
      if (!this.#is(call, run)) return;
      const conflict = err instanceof ApiError && err.status === 409;
      if (conflict && action === 'cancel') {
        // Accepted meanwhile: end what is now a connecting call.
        try {
          await this.#command(run, call, 'end');
        } catch {
          // The server's timeouts end it.
        }
      } else if (conflict && action === 'reject' && !accepting) {
        // Answered on another device: the call goes on there.
        call.pending = null;
        call.error = 'Звонок принят на другом устройстве.';
        this.#refresh(call);
        this.#changed();
        return;
      } else if (!owned) {
        // Another device's call: it goes on there.
        call.pending = null;
        call.error = conflict || (err instanceof ApiError && err.status === 404)
          ? 'Этот звонок идёт на другом устройстве.'
          : describeCallError(err);
        this.#changed();
        return;
      }
    }
    if (this.#is(call, run)) this.#finish(call);
  }

  #command(run, call, action) {
    const commandId = this.#newId();
    return this.#retrying(run, () => this.#api.call('POST', callPath(call.id, action), { body: { command_id: commandId } }));
  }

  // #retrying repeats an idempotent request after a network failure, and a
  // signal also after 429, a few times.
  async #retrying(run, send, { rateLimited = false } = {}) {
    for (let attempt = 0; ; attempt++) {
      if (run !== this.#run) throw new ApiError(401, 'unauthenticated', 'Сессия уже завершена');
      try {
        return await send();
      } catch (err) {
        if (!retryable(err, { rateLimited }) || attempt >= RETRY_DELAYS_MS.length || run !== this.#run) throw err;
      }
      await new Promise((resolve) => this.#timers.set(resolve, RETRY_DELAYS_MS[attempt]));
    }
  }

  // #beginMedia sets up the peer connection once the call is accepted.
  async #beginMedia(call) {
    call.mediaStarted = true;
    const run = this.#run;
    if (!call.stream) {
      this.#fail(call, 'sdp');
      return;
    }
    let iceServers = [];
    try {
      iceServers = parseIceServers(await this.#api.call('POST', callPath(call.id, 'turn'), { body: {} }));
    } catch {
      // Without TURN only direct connections work (ADR 0005, coturn outage).
    }
    if (!this.#is(call, run) || call.state === 'ended') return;
    try {
      call.link = new PeerLink({
        role: call.role,
        stream: call.stream,
        createPeerConnection: this.#createPeerConnection,
        iceServers,
        send: (kind, payload) => this.#sendSignal(call, run, kind, payload),
        onConnected: () => this.#connected(call, run),
        onRemoteStream: (stream) => {
          if (!this.#is(call, run)) return;
          call.remote = stream;
          this.#changed();
        },
        onFailed: (reason) => this.#is(call, run) && this.#fail(call, reason),
      });
    } catch {
      this.#fail(call, 'sdp');
      return;
    }
    this.#enqueue(call, () => call.link?.start(), run);
    // Signals sent before the connection existed, such as the caller's offer.
    this.#enqueue(call, () => this.#catchUp(call), run);
    this.#schedule(call);
  }

  // #enqueue runs fn after the call's earlier signal work. Sending and
  // applying signals share one queue, so that the answer to a signal of
  // this device has arrived, and its signal_seq is known as its own, before
  // the next signals are applied.
  #enqueue(call, fn, run = this.#run) {
    call.queue = call.queue
      .then(() => (this.#is(call, run) && call.state !== 'ended' ? fn() : undefined))
      .catch(() => {});
  }

  #sendSignal(call, run, kind, payload) {
    const senderSeq = ++call.senderSeq;
    this.#enqueue(call, async () => {
      // ADR 0005 allows end-of-candidates only while connecting.
      if (kind === 'end-of-candidates' && call.state === 'active') return;
      let result;
      try {
        result = await this.#retrying(run, () => this.#api.call('POST', callPath(call.id, 'signal'), {
          body: { kind, payload, sender_seq: senderSeq },
        }), { rateLimited: true });
      } catch (err) {
        if (!this.#is(call, run)) return;
        // The signal may have been stored all the same; it must not come
        // back as the peer's.
        if (err instanceof ApiError && err.status === 0) call.unsure.add(`${kind}
${payload}`);
        // A lost candidate is tolerable; a lost description is not.
        if (kind === 'candidate' || kind === 'end-of-candidates') return;
        if (err instanceof ApiError && (err.status === 404 || err.status === 409)) this.#refresh(call);
        else this.#fail(call, 'signal');
        return;
      }
      if (!this.#is(call, run)) return;
      const seq = result?.signal_seq;
      if (Number.isSafeInteger(seq) && seq > 0) {
        call.own.add(seq);
        this.#advance(call);
      }
    }, run);
  }

  #advance(call) {
    while (call.own.has(call.cursor + 1)) call.cursor++;
  }

  #ownSignal(call, signal) {
    return call.own.has(signal.seq) || call.unsure.has(`${signal.kind}
${signal.payload}`);
  }

  // #ingest applies signals in signal_seq order and reports whether the
  // list could be applied. A live event must be the next signal; a
  // catch-up page from the server is complete and may skip numbers.
  async #ingest(call, signals, complete) {
    if (!call.link) return complete; // applied once the connection exists
    const run = this.#run;
    const sorted = signals.filter((signal) => signal.seq > call.cursor).sort((a, b) => a.seq - b.seq);
    for (const signal of sorted) {
      this.#advance(call);
      if (signal.seq <= call.cursor) continue;
      if (!complete && signal.seq !== call.cursor + 1) return false;
      if (this.#ownSignal(call, signal)) {
        call.own.add(signal.seq);
      } else {
        await call.link.receive(signal.kind, signal.payload);
        // The signal may have failed the call.
        if (!this.#is(call, run) || !call.link) return true;
      }
      call.cursor = signal.seq;
    }
    this.#advance(call);
    return true;
  }

  async #catchUp(call) {
    if (!call.link) return;
    const run = this.#run;
    for (let page = 0; page < MAX_SIGNAL_PAGES; page++) {
      let result;
      try {
        result = await this.#api.call('GET', `${callPath(call.id, 'signals')}?after=${call.cursor}`);
      } catch {
        return; // the next poll, heartbeat or event tries again
      }
      if (!this.#is(call, run)) return;
      const signals = (Array.isArray(result?.signals) ? result.signals : []).map(parseSignal).filter(Boolean);
      const before = call.cursor;
      await this.#ingest(call, signals, true);
      if (!this.#is(call, run) || call.cursor === before) return;
    }
  }

  async #connected(call, run) {
    if (!this.#is(call, run)) return;
    call.iceUp = true;
    this.#schedule(call);
    try {
      const result = await this.#command(run, call, 'connected');
      if (this.#is(call, run)) this.#apply(call, parseCall({ call_id: call.id, ...result }));
    } catch {
      // Without connected from both devices the server ends the call after
      // 30 s; the heartbeat then reports it.
    }
    if (this.#is(call, run)) this.#changed();
  }

  async #heartbeat(call, run) {
    call.beat = null;
    if (!this.#is(call, run) || !(call.state === 'connecting' || call.state === 'active')) return;
    try {
      const result = await this.#api.call('POST', callPath(call.id, 'heartbeat'), { body: { command_id: this.#newId() } });
      if (this.#is(call, run)) this.#apply(call, parseCall({ call_id: call.id, ...result }));
    } catch (err) {
      if (!this.#is(call, run)) return;
      if (err instanceof ApiError && err.status === 404) this.#finish(call, 'Звонок завершён сервером.');
      else if (err instanceof ApiError && err.status === 409) this.#refresh(call);
    }
    if (!this.#is(call, run) || call.state === 'ended') return;
    this.#enqueue(call, () => this.#catchUp(call), run);
    this.#schedule(call);
    this.#changed();
  }

  // #schedule keeps the call's timers: polling while ringing or while the
  // connection is not up yet, and the heartbeat of the call's device.
  #schedule(call) {
    this.#timers.clear(call.poll);
    call.poll = null;
    if (!this.#is(call) || call.state === 'ended') return;
    const run = this.#run;
    let delay = 0;
    let work = null;
    if (call.state === 'ringing') {
      delay = RINGING_POLL_MS;
      work = () => this.#refresh(call);
    } else if (call.state === 'connecting' && call.link && !call.iceUp) {
      delay = SIGNAL_POLL_MS;
      work = () => this.#enqueue(call, () => this.#catchUp(call), run);
    } else if (!call.owned) {
      // Another device's call: its end is noticed even without events.
      delay = HEARTBEAT_MS;
      work = () => this.#refresh(call);
    }
    if (work) {
      call.poll = this.#timers.set(async () => {
        call.poll = null;
        await work();
        if (this.#is(call, run)) this.#schedule(call);
      }, delay);
    }
    if (call.owned && call.beat === null && (call.state === 'connecting' || call.state === 'active')) {
      call.beat = this.#timers.set(() => this.#heartbeat(call, run), HEARTBEAT_MS);
    }
  }

  #fail(call, reason) {
    const run = this.#run;
    const message = FAILURE_TEXT[reason] ?? FAILURE_TEXT.signal;
    this.#release(call);
    if (call.id && call.owned && call.state !== 'ended') {
      this.#command(run, call, 'end').catch(() => {});
    }
    this.#finish(call, message);
  }

  // #release stops the microphone and closes the peer connection.
  #release(call) {
    call.link?.close();
    call.link = null;
    stopStream(call.stream);
    call.stream = null;
    call.remote = null;
  }

  #clearTimers(call) {
    if (!call) return;
    this.#timers.clear(call.poll);
    this.#timers.clear(call.beat);
    call.poll = null;
    call.beat = null;
  }

  #finish(call, error = '') {
    this.#release(call);
    this.#clearTimers(call);
    call.state = 'ended';
    call.pending = null;
    call.error = error;
    if (call.id && !this.#ended.includes(call.id)) {
      this.#ended.push(call.id);
      if (this.#ended.length > MAX_REMEMBERED_ENDED) this.#ended.shift();
    }
    if (this.#call === call) this.#changed();
  }
}
