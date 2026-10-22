// The loaded messages of one room.
//
// Messages are numbered per room 1, 2, 3, ... without gaps (see
// docs/chat/messages.md). The timeline holds a contiguous block of them,
// ending at `newest`, and classifies every incoming message against it, so
// that live events, sends and catch-up pages can all go through apply().

export class Timeline {
  #bySequence = new Map();
  #newest = 0;
  #loaded = false;
  // olderCursor is the history cursor of the next older page; empty when
  // the first message of the room is loaded.
  olderCursor = '';

  // loaded reports whether the latest page has been read.
  get loaded() {
    return this.#loaded;
  }

  // newest is the highest sequence up to which the block is complete; it is
  // the `after` position for catch-up.
  get newest() {
    return this.#newest;
  }

  // messages are the loaded messages, oldest first.
  get messages() {
    return [...this.#bySequence.values()].sort((a, b) => a.sequence - b.sequence);
  }

  // setLatest replaces the timeline with the newest history page.
  setLatest(page) {
    this.#bySequence.clear();
    this.#newest = 0;
    this.#addPage(page);
    this.#newest = Math.max(0, ...page.messages.map((m) => m.sequence));
    this.#loaded = true;
  }

  // addOlder adds the next older history page.
  addOlder(page) {
    this.#addPage(page);
  }

  // apply adds a message that arrived after the latest page was read: from a
  // send, a live event or a catch-up page. It returns
  //   'appended'  the message directly follows the newest one;
  //   'duplicate' the message is already loaded;
  //   'gap'       messages before it are missing; it is not added, and the
  //               caller should catch up after `newest`, which returns it too;
  //   'stale'     it is older than the loaded block and not shown.
  apply(message) {
    const { sequence } = message;
    if (this.#bySequence.has(sequence)) return 'duplicate';
    if (!this.#loaded || sequence > this.#newest + 1) return 'gap';
    if (sequence <= this.#newest) return 'stale';
    this.#bySequence.set(sequence, message);
    this.#newest = sequence;
    return 'appended';
  }

  #addPage(page) {
    for (const message of page.messages) {
      this.#bySequence.set(message.sequence, message);
    }
    this.olderCursor = page.next_cursor ?? '';
  }
}
