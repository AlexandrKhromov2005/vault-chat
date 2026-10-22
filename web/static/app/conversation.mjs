// The open room: its messages and the composer.

import { Outbox, catchUp, loadLatest, loadOlder, sendMessage } from './chat.mjs';
import { el, formatTime } from './dom.mjs';
import { describeError } from './errors.mjs';
import { ApiError } from './http.mjs';
import { Timeline } from './timeline.mjs';

const $ = (id) => document.getElementById(id);

// A conversation scrolled to within this distance of its end follows new
// messages; otherwise the reader's position is kept.
const STICK_TO_BOTTOM_PX = 48;

export class Conversation {
  #api;
  #workspace;
  #roomId = null; // the open room
  #outbox = new Outbox();
  #sending = false;

  // workspace provides scope(), me(), lostRoom(roomId) and
  // touch(roomId, at), which moves a room with a new message to the top.
  constructor({ api, workspace }) {
    this.#api = api;
    this.#workspace = workspace;

    $('composer').addEventListener('submit', (event) => {
      event.preventDefault();
      this.#send();
    });
    $('message-text').addEventListener('keydown', (event) => {
      if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
        event.preventDefault();
        this.#send();
      }
    });
    $('load-older').addEventListener('click', () => this.#loadOlder());
  }

  // open shows a room: its loaded messages are caught up, or its latest
  // page is read.
  async open(room) {
    const scope = this.#workspace.scope();
    const roomId = room.room_id;
    this.#roomId = roomId;
    this.#outbox = new Outbox();
    $('message-text').value = '';
    $('message-list').replaceChildren();
    $('room-error').textContent = '';

    await this.#guard(scope, roomId, async () => {
      const timeline = this.#timeline(scope, roomId);
      if (timeline.loaded) await catchUp(this.#api, roomId, timeline);
      else await loadLatest(this.#api, roomId, timeline);
      await this.#render(scope, roomId, { stick: true });
    });
    if (this.#isOpen(scope, roomId)) $('message-text').focus();
  }

  close() {
    this.#roomId = null;
    $('message-list').replaceChildren();
    $('message-text').value = '';
    $('room-error').textContent = '';
  }

  // sync catches up a room whose messages are loaded and returns how many
  // new ones arrived.
  async sync(scope, roomId) {
    const timeline = scope.timelines.get(roomId);
    if (!timeline?.loaded) return 0;
    const appended = await this.#guard(scope, roomId, () => catchUp(this.#api, roomId, timeline));
    if (appended) await this.#render(scope, roomId);
    return appended ?? 0;
  }

  #isOpen(scope, roomId) {
    return scope.alive && this.#roomId === roomId;
  }

  #timeline(scope, roomId) {
    if (!scope.timelines.has(roomId)) scope.timelines.set(roomId, new Timeline());
    return scope.timelines.get(roomId);
  }

  // #guard runs work for a room. A 404 means the user lost access to it;
  // other errors are shown while the room is open. Nothing happens once the
  // scope has ended.
  async #guard(scope, roomId, action) {
    try {
      return await action();
    } catch (err) {
      if (!scope.alive) return undefined;
      if (err instanceof ApiError && err.status === 404) this.#workspace.lostRoom(roomId);
      else if (this.#isOpen(scope, roomId)) $('room-error').textContent = describeError(err);
      return undefined;
    }
  }

  async #render(scope, roomId, { stick = false, keepOffset = false } = {}) {
    const timeline = this.#timeline(scope, roomId);
    const messages = timeline.messages;
    const me = this.#workspace.me();
    await scope.profiles.getAll(messages.map((m) => m.sender_id).filter((id) => id !== me));
    if (!this.#isOpen(scope, roomId)) return;

    const box = $('messages');
    const fromBottom = box.scrollHeight - box.scrollTop;
    const atBottom = fromBottom - box.clientHeight < STICK_TO_BOTTOM_PX;

    $('message-list').replaceChildren(...messages.map((message) => this.#messageItem(scope, message, me)));
    $('load-older').hidden = !timeline.olderCursor;

    if (keepOffset) box.scrollTop = box.scrollHeight - fromBottom;
    else if (stick || atBottom) box.scrollTop = box.scrollHeight;
  }

  #messageItem(scope, message, me) {
    const own = message.sender_id === me;
    const author = own ? 'вы' : scope.profiles.name(message.sender_id) ?? 'удалённый пользователь';
    return el('li', { class: own ? 'message own' : 'message', 'data-sequence': message.sequence },
      el('div', { class: 'message-meta' },
        el('span', { class: 'message-author' }, author),
        el('time', { datetime: message.created_at }, formatTime(message.created_at)),
      ),
      message.text ? el('p', { class: 'message-text' }, message.text) : null,
    );
  }

  async #loadOlder() {
    const scope = this.#workspace.scope();
    const roomId = this.#roomId;
    if (!roomId) return;
    await this.#guard(scope, roomId, async () => {
      await loadOlder(this.#api, roomId, this.#timeline(scope, roomId));
      await this.#render(scope, roomId, { keepOffset: true });
    });
  }

  // #send stores the composer's message. While a send is under way further
  // sends are ignored. A failed send keeps the text and its
  // client_message_id, so retrying cannot store the message twice.
  async #send() {
    const scope = this.#workspace.scope();
    const roomId = this.#roomId;
    const text = $('message-text').value;
    // The server rejects whitespace-only text.
    if (!roomId || this.#sending || !text.trim()) return;

    const outbox = this.#outbox;
    const prepared = outbox.prepare(text, []);
    this.#setSending(true);
    await this.#guard(scope, roomId, async () => {
      const message = await sendMessage(this.#api, roomId, prepared);
      if (!scope.alive) return;
      outbox.sent();
      if (this.#isOpen(scope, roomId) && $('message-text').value === text) $('message-text').value = '';
      const timeline = this.#timeline(scope, roomId);
      // Someone else wrote meanwhile: fetch what is missing first.
      if (timeline.apply(message) === 'gap') await catchUp(this.#api, roomId, timeline);
      this.#workspace.touch(roomId, message.created_at);
      await this.#render(scope, roomId, { stick: true });
    });
    this.#setSending(false);
  }

  #setSending(sending) {
    this.#sending = sending;
    $('composer').querySelector('button[type="submit"]').disabled = sending;
  }
}
