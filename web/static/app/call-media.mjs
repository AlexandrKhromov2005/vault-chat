// The browser side of a call's audio: the microphone and one
// RTCPeerConnection, following the signal matrix of ADR 0005.
//
// PeerLink does not talk to the server itself. It hands outgoing signals to
// send, which queues them without waiting, and receives the peer's signals
// in signal_seq order from the caller of receive.

// Budgets of ADR 0005, enforced again here so that the client never sends
// what the server must reject.
export const MAX_SDP_BYTES = 8192;
export const MAX_CANDIDATE_BYTES = 512;
export const MAX_CANDIDATES = 32;

const encoder = new TextEncoder();
const byteLength = (text) => encoder.encode(text).length;

// MicrophoneError is a failure to open the microphone; reason is
// 'unsupported', 'denied', 'missing', 'busy' or 'failed'.
export class MicrophoneError extends Error {
  constructor(reason) {
    super(`microphone: ${reason}`);
    this.name = 'MicrophoneError';
    this.reason = reason;
  }
}

// openMicrophone asks for an audio-only stream. Calls need a secure context;
// without one the browser offers no mediaDevices at all.
export async function openMicrophone(mediaDevices) {
  if (!mediaDevices?.getUserMedia) throw new MicrophoneError('unsupported');
  try {
    return await mediaDevices.getUserMedia({ audio: true, video: false });
  } catch (err) {
    switch (err?.name) {
      case 'NotAllowedError':
      case 'SecurityError':
        throw new MicrophoneError('denied');
      case 'NotFoundError':
      case 'OverconstrainedError':
        throw new MicrophoneError('missing');
      case 'NotReadableError':
      case 'AbortError':
        throw new MicrophoneError('busy');
      default:
        throw new MicrophoneError('failed');
    }
  }
}

// stopStream releases the microphone.
export function stopStream(stream) {
  for (const track of stream?.getTracks?.() ?? []) track.stop();
}

// encodeCandidate turns an RTCIceCandidate into the text payload of a
// candidate signal, or null when it does not fit the budget.
export function encodeCandidate(candidate) {
  const payload = JSON.stringify({
    candidate: candidate.candidate,
    sdpMid: candidate.sdpMid ?? null,
    sdpMLineIndex: candidate.sdpMLineIndex ?? null,
    usernameFragment: candidate.usernameFragment ?? null,
  });
  return byteLength(payload) <= MAX_CANDIDATE_BYTES ? payload : null;
}

// decodeCandidate parses the peer's candidate payload. Anything else than
// the fields encodeCandidate writes, with their types, is rejected: the
// payload passed through the server unparsed.
export function decodeCandidate(payload) {
  if (typeof payload !== 'string' || byteLength(payload) > MAX_CANDIDATE_BYTES) return null;
  let value;
  try {
    value = JSON.parse(payload);
  } catch {
    return null;
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const { candidate, sdpMid = null, sdpMLineIndex = null, usernameFragment = null } = value;
  if (typeof candidate !== 'string' || candidate === '') return null;
  if (sdpMid !== null && typeof sdpMid !== 'string') return null;
  if (sdpMLineIndex !== null && !(Number.isInteger(sdpMLineIndex) && sdpMLineIndex >= 0 && sdpMLineIndex < 64)) return null;
  if (usernameFragment !== null && typeof usernameFragment !== 'string') return null;
  if (sdpMid === null && sdpMLineIndex === null) return null;
  return { candidate, sdpMid, sdpMLineIndex, usernameFragment };
}

const validSdp = (payload) => typeof payload === 'string' && payload !== '' && byteLength(payload) <= MAX_SDP_BYTES;

// PeerLink is one call's peer connection.
//
//   role          'caller' sends the first offer; 'callee' answers it.
//   send(kind, payload)  queues an outgoing signal; must not block.
//   onConnected() ICE connected for the first time.
//   onLost(failed)  a connection that was up went down: disconnected, which
//                 the browser may still repair, or failed, which only an ICE
//                 restart repairs.
//   onRestored()  the connection is up again after onLost.
//   onRemoteStream(stream)  the peer's audio arrived.
//   onFailed(reason)  the connection cannot work: 'ice', 'sdp', 'protocol'.
//
// The candidate budget covers the whole call, ICE restarts included, as the
// server counts it.
export class PeerLink {
  #pc;
  #config;
  #role;
  #send;
  #handlers;
  #closed = false;
  #connected = false;
  #lost = null; // null, 'disconnected' or 'failed' after the connection was up
  #remoteSet = false;
  #pendingCandidates = []; // the peer's candidates before its description
  #sentCandidates = 0;

  constructor({
    role, stream, createPeerConnection, iceServers = [], send,
    onConnected, onLost = () => {}, onRestored = () => {}, onRemoteStream, onFailed,
  }) {
    this.#role = role;
    this.#send = send;
    this.#handlers = { onConnected, onLost, onRestored, onRemoteStream, onFailed };
    this.#config = { iceServers, bundlePolicy: 'max-bundle', rtcpMuxPolicy: 'require' };
    this.#pc = createPeerConnection(this.#config);
    for (const track of stream.getTracks()) this.#pc.addTrack(track, stream);

    this.#pc.addEventListener('icecandidate', (event) => this.#localCandidate(event.candidate));
    this.#pc.addEventListener('track', (event) => {
      if (this.#closed) return;
      const remote = event.streams?.[0] ?? null;
      if (remote) this.#handlers.onRemoteStream(remote);
    });
    this.#pc.addEventListener('connectionstatechange', () => this.#stateChanged());
    this.#pc.addEventListener('iceconnectionstatechange', () => this.#stateChanged());
  }

  get closed() {
    return this.#closed;
  }

  // start begins negotiation: the caller makes the first offer, the callee
  // waits for it. The offer is queued before setLocalDescription, which
  // starts gathering, so that no candidate overtakes it.
  async start() {
    if (this.#role !== 'caller' || this.#closed) return;
    try {
      const offer = await this.#pc.createOffer();
      if (this.#closed) return;
      if (!validSdp(offer.sdp)) {
        this.#fail('sdp');
        return;
      }
      this.#send('offer', offer.sdp);
      await this.#pc.setLocalDescription(offer);
    } catch {
      this.#fail('sdp');
    }
  }

  // restart starts an ICE restart with a new offer, using iceServers when
  // given. It reports whether an offer went out: an offer of either side
  // still waiting for its answer comes first.
  async restart(iceServers = null) {
    if (this.#closed || this.#pc.signalingState !== 'stable') return false;
    try {
      if (iceServers) this.setIceServers(iceServers);
      const offer = await this.#pc.createOffer({ iceRestart: true });
      if (this.#closed || this.#pc.signalingState !== 'stable') return false;
      if (!validSdp(offer.sdp)) {
        this.#fail('sdp');
        return false;
      }
      this.#send('offer', offer.sdp);
      await this.#pc.setLocalDescription(offer);
      return true;
    } catch {
      this.#fail('sdp');
      return false;
    }
  }

  // setIceServers takes new TURN credentials for the next gathering.
  setIceServers(iceServers) {
    if (this.#closed) return;
    this.#config = { ...this.#config, iceServers };
    try {
      this.#pc.setConfiguration(this.#config);
    } catch {
      // The engine keeps the servers it has.
    }
  }

  // rollback withdraws a local offer that the server refused, so that the
  // peer's offer, or the next restart, can follow.
  async rollback() {
    if (this.#closed || this.#pc.signalingState !== 'have-local-offer') return;
    try {
      await this.#pc.setLocalDescription({ type: 'rollback' });
    } catch {
      this.#fail('sdp');
    }
  }

  // usesRelay reports whether the selected candidate pair goes through a
  // TURN relay of either side.
  async usesRelay() {
    if (this.#closed) return false;
    let report;
    try {
      report = await this.#pc.getStats();
    } catch {
      return false;
    }
    const stats = new Map();
    report?.forEach?.((entry) => stats.set(entry.id, entry));
    let pair = null;
    for (const entry of stats.values()) {
      if (entry.type === 'transport' && entry.selectedCandidatePairId) pair = stats.get(entry.selectedCandidatePairId) ?? pair;
    }
    if (!pair) {
      pair = [...stats.values()].find((entry) => entry.type === 'candidate-pair'
        && (entry.selected || (entry.nominated && entry.state === 'succeeded'))) ?? null;
    }
    if (!pair) return false;
    return [pair.localCandidateId, pair.remoteCandidateId].some((id) => stats.get(id)?.candidateType === 'relay');
  }

  // receive applies one of the peer's signals. The caller orders them by
  // signal_seq and runs one receive at a time.
  async receive(kind, payload) {
    if (this.#closed) return;
    try {
      switch (kind) {
        case 'offer':
          return await this.#offer(payload);
        case 'answer':
          return await this.#answer(payload);
        case 'candidate':
          return await this.#candidate(payload);
        case 'end-of-candidates':
          return await this.#endOfCandidates();
        default:
          return undefined;
      }
    } catch {
      // The browser rejected what the peer sent.
      this.#fail('protocol');
      return undefined;
    }
  }

  close() {
    if (this.#closed) return;
    this.#closed = true;
    this.#pendingCandidates = [];
    try {
      this.#pc.close();
    } catch {
      // already closed
    }
  }

  async #offer(sdp) {
    if (!validSdp(sdp)) return this.#fail('sdp');
    if (this.#pc.signalingState !== 'stable') return this.#fail('protocol');
    await this.#pc.setRemoteDescription({ type: 'offer', sdp });
    await this.#remoteDescribed();
    if (this.#closed) return undefined;
    const answer = await this.#pc.createAnswer();
    if (this.#closed) return undefined;
    if (!validSdp(answer.sdp)) return this.#fail('sdp');
    this.#send('answer', answer.sdp);
    await this.#pc.setLocalDescription(answer);
    return undefined;
  }

  async #answer(sdp) {
    if (!validSdp(sdp)) return this.#fail('sdp');
    if (this.#pc.signalingState !== 'have-local-offer') return this.#fail('protocol');
    await this.#pc.setRemoteDescription({ type: 'answer', sdp });
    await this.#remoteDescribed();
    return undefined;
  }

  async #candidate(payload) {
    const candidate = decodeCandidate(payload);
    if (!candidate) return undefined; // dropped, as a lost candidate would be
    if (!this.#remoteSet) {
      if (this.#pendingCandidates.length < MAX_CANDIDATES) this.#pendingCandidates.push(candidate);
      return undefined;
    }
    await this.#addCandidate(candidate);
    return undefined;
  }

  async #endOfCandidates() {
    if (!this.#remoteSet) return;
    try {
      await this.#pc.addIceCandidate();
    } catch {
      // Older engines do not support the end-of-candidates indication.
    }
  }

  async #remoteDescribed() {
    this.#remoteSet = true;
    const pending = this.#pendingCandidates;
    this.#pendingCandidates = [];
    for (const candidate of pending) await this.#addCandidate(candidate);
  }

  async #addCandidate(candidate) {
    if (this.#closed) return;
    try {
      await this.#pc.addIceCandidate(candidate);
    } catch {
      // A candidate the engine cannot use; others may still work.
    }
  }

  #localCandidate(candidate) {
    if (this.#closed) return;
    if (!candidate || candidate.candidate === '') {
      this.#send('end-of-candidates', '');
      return;
    }
    if (this.#sentCandidates >= MAX_CANDIDATES) return;
    const payload = encodeCandidate(candidate);
    if (payload === null) return;
    this.#sentCandidates++;
    this.#send('candidate', payload);
  }

  #stateChanged() {
    if (this.#closed) return;
    const { connectionState, iceConnectionState } = this.#pc;
    const failed = connectionState === 'failed' || iceConnectionState === 'failed';
    const up = connectionState === 'connected' || iceConnectionState === 'connected' || iceConnectionState === 'completed';
    const disconnected = connectionState === 'disconnected' || iceConnectionState === 'disconnected';
    if (!this.#connected) {
      // Before the first connection only the server's timeout helps.
      if (failed) {
        this.#fail('ice');
      } else if (up) {
        this.#connected = true;
        this.#handlers.onConnected();
      }
      return;
    }
    const lost = failed ? 'failed' : up ? null : disconnected ? 'disconnected' : this.#lost;
    if (lost === this.#lost) return;
    const was = this.#lost;
    this.#lost = lost;
    if (lost) this.#handlers.onLost(lost === 'failed');
    else if (was) this.#handlers.onRestored();
  }

  #fail(reason) {
    if (this.#closed) return undefined;
    this.#handlers.onFailed(reason);
    return undefined;
  }
}
