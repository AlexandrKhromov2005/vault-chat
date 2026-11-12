// Test doubles for calls: the browser's microphone and RTCPeerConnection,
// and an API answering from per-request handlers.

import { ApiError } from '../static/app/http.mjs';

// fakeMicrophone stands in for navigator.mediaDevices. With error set,
// getUserMedia fails with a DOMException of that name; with hold set, it
// waits until release() is called.
export function fakeMicrophone({ error = null } = {}) {
  const opened = [];
  let held = null;
  const media = {
    opened,
    requests: 0,
    hold() {
      let release;
      held = new Promise((resolve) => {
        release = resolve;
      });
      return () => release();
    },
    async getUserMedia(constraints) {
      media.requests++;
      if (held) await held;
      if (error) throw Object.assign(new Error('denied'), { name: error });
      const track = { kind: 'audio', stopped: false, stop() { this.stopped = true; } };
      const stream = { constraints, getTracks: () => [track], track };
      opened.push(stream);
      return stream;
    },
  };
  return media;
}

export class FakePeerConnection extends EventTarget {
  constructor(config) {
    super();
    this.config = config;
    this.tracks = [];
    this.local = null;
    this.remote = null;
    this.candidates = [];
    this.signalingState = 'stable';
    this.connectionState = 'new';
    this.iceConnectionState = 'new';
    this.closed = false;
    this.offerSdp = 'v=0 offer';
    this.answerSdp = 'v=0 answer';
    this.remoteDescriptions = [];
    this.offers = []; // the options of each createOffer
    this.configurations = []; // each setConfiguration
    this.stats = new Map();
  }

  addTrack(track) {
    this.tracks.push(track);
  }

  async createOffer(options = {}) {
    this.offers.push(options);
    return { type: 'offer', sdp: this.offers.length === 1 ? this.offerSdp : `${this.offerSdp} ${this.offers.length}` };
  }

  setConfiguration(config) {
    this.config = config;
    this.configurations.push(config);
  }

  async getStats() {
    return this.stats;
  }

  // relay makes the selected candidate pair go through a TURN relay.
  relay(side = 'local') {
    this.stats = new Map([
      ['t', { id: 't', type: 'transport', selectedCandidatePairId: 'p' }],
      ['p', { id: 'p', type: 'candidate-pair', localCandidateId: 'l', remoteCandidateId: 'r' }],
      ['l', { id: 'l', type: 'local-candidate', candidateType: side === 'local' ? 'relay' : 'host' }],
      ['r', { id: 'r', type: 'remote-candidate', candidateType: side === 'remote' ? 'relay' : 'srflx' }],
    ]);
  }

  async createAnswer() {
    return { type: 'answer', sdp: this.answerSdp };
  }

  // With gatherOnLocal set, a candidate is gathered within
  // setLocalDescription, earlier than browsers do.
  async setLocalDescription(description) {
    if (description.type === 'rollback') {
      if (this.signalingState !== 'have-local-offer') throw new Error('nothing to roll back');
      this.rolledBack = (this.rolledBack ?? 0) + 1;
      this.signalingState = 'stable';
      return;
    }
    this.local = description;
    this.signalingState = description.type === 'offer' ? 'have-local-offer' : 'stable';
    if (this.gatherOnLocal) this.gather(this.gatherOnLocal);
  }

  async setRemoteDescription(description) {
    if (this.rejectRemote) throw new Error('invalid description');
    this.remote = description;
    this.remoteDescriptions.push(description);
    this.signalingState = description.type === 'offer' ? 'have-remote-offer' : 'stable';
  }

  async addIceCandidate(candidate) {
    this.candidates.push(candidate ?? null);
  }

  close() {
    this.closed = true;
  }

  gather(candidate) {
    this.dispatchEvent(Object.assign(new Event('icecandidate'), { candidate }));
  }

  connect() {
    this.connectionState = 'connected';
    this.dispatchEvent(new Event('connectionstatechange'));
  }

  failIce() {
    this.connectionState = 'failed';
    this.dispatchEvent(new Event('connectionstatechange'));
  }

  disconnect() {
    this.connectionState = 'disconnected';
    this.dispatchEvent(new Event('connectionstatechange'));
  }

  // checking is the state of a connection while an ICE restart runs.
  checking() {
    this.connectionState = 'connecting';
    this.dispatchEvent(new Event('connectionstatechange'));
  }

  track(stream) {
    this.dispatchEvent(Object.assign(new Event('track'), { streams: [stream] }));
  }
}

export function peerConnections({ gatherOnLocal = null } = {}) {
  const created = [];
  const create = (config) => {
    const pc = new FakePeerConnection(config);
    pc.gatherOnLocal = gatherOnLocal;
    created.push(pc);
    return pc;
  };
  return { created, create };
}

// routedApi answers Api calls through handle(method, path, body), which
// returns a body, throws, or returns a promise. Unhandled requests fail.
export function routedApi(handle) {
  const requests = [];
  return {
    requests,
    sent: (method, path) => requests.filter((r) => r.method === method && r.path === path),
    call: async (method, path, options) => {
      const request = { method, path, body: options?.body };
      requests.push(request);
      const result = await handle(method, path, options?.body, request);
      if (result === undefined) throw new Error(`unexpected ${method} ${path}`);
      return structuredClone(result);
    },
  };
}

export const apiError = (status, code = 'error') => new ApiError(status, code, code);

export const candidate = (n) => ({
  candidate: `candidate:${n} 1 udp 2122260223 192.0.2.${n} 5000${n} typ host`,
  sdpMid: '0',
  sdpMLineIndex: 0,
  usernameFragment: 'abcd',
});
