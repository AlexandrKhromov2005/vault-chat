// Reconnection delays: exponential with jitter, so that clients that lost
// the same Gateway instance do not all come back at the same moment.

export class Backoff {
  #base;
  #max;
  #random;
  #attempt = 0;

  constructor({ base = 500, max = 30_000, random = Math.random } = {}) {
    this.#base = base;
    this.#max = max;
    this.#random = random;
  }

  // next returns the delay before the next attempt, in milliseconds: between
  // half and all of base·2^attempt, at most max.
  next() {
    const full = Math.min(this.#max, this.#base * 2 ** this.#attempt);
    this.#attempt++;
    return full * (0.5 + this.#random() / 2);
  }

  reset() {
    this.#attempt = 0;
  }
}
