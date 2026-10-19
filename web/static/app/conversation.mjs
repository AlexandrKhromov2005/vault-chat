// The open room: its messages with their attachments, and the composer.

import { Outbox, catchUp, loadLatest, loadOlder, sendMessage } from './chat.mjs';
import { el, formatTime } from './dom.mjs';
import { describeError } from './errors.mjs';
import { Attachments, downloadFile, fileProblem, formatSize, uploadFile } from './files.mjs';
import { ApiError } from './http.mjs';
import { Timeline } from './timeline.mjs';

const $ = (id) => document.getElementById(id);

// A conversation scrolled to within this distance of its end follows new
// messages; otherwise the reader's position is kept.
const STICK_TO_BOTTOM_PX = 48;

// A draft is the message being written in the open room: its outbox, the
// files attached to it and the number of uploads under way. Files belong to
// the room they were uploaded to, so every opened room starts a new draft;
// files never sent stay on the server unreferenced (docs/media/files.md).
const newDraft = () => ({ outbox: new Outbox(), attachments: new Attachments(), uploads: 0 });

export class Conversation {
  #api;
  #workspace;
  #roomId = null; // the open room
  #draft = newDraft();
  #sending = false;
  #downloadUrl = null; // object URL of the latest download

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
    $('attach').addEventListener('click', () => $('file-input').click());
    $('file-input').addEventListener('change', (event) => {
      const files = [...event.currentTarget.files];
      event.currentTarget.value = '';
      this.#upload(files);
    });
  }

  // open shows a room: its loaded messages are caught up, or its latest
  // page is read.
  async open(room) {
    const scope = this.#workspace.scope();
    const roomId = room.room_id;
    this.#roomId = roomId;
    this.#clear();

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
    this.#clear();
  }

  #clear() {
    this.#draft = newDraft();
    this.#renderDraft();
    this.#revokeDownload();
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
    try {
      await scope.files.getAll(messages.flatMap((m) => m.attachment_ids ?? []));
    } catch {
      // Attachments without metadata are still shown and downloadable.
    }
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
      message.attachment_ids?.length
        ? el('ul', { class: 'attachments' }, message.attachment_ids.map((id) => el('li', {}, this.#attachmentItem(scope, id))))
        : null,
    );
  }

  #attachmentItem(scope, fileId) {
    const info = scope.files.info(fileId);
    if (info?.unavailable) return el('span', { class: 'attachment unavailable' }, 'файл недоступен');
    const label = info ? `${info.filename} · ${formatSize(info.size)}` : 'вложение';
    return el('button', {
      type: 'button',
      class: 'attachment',
      title: 'Скачать',
      onclick: () => this.#download(fileId, info?.filename ?? 'attachment'),
    }, `📎 ${label}`);
  }

  // #download saves a file. The content is fetched with the access token and
  // offered as a download; untrusted files are never rendered in the page.
  async #download(fileId, filename) {
    const scope = this.#workspace.scope();
    const roomId = this.#roomId;
    $('room-error').textContent = '';
    await this.#guard(scope, roomId, async () => {
      const blob = await downloadFile(this.#api, fileId);
      if (!this.#isOpen(scope, roomId)) return;
      this.#revokeDownload();
      this.#downloadUrl = URL.createObjectURL(blob);
      const link = el('a', { href: this.#downloadUrl, download: filename, hidden: true });
      document.body.append(link);
      link.click();
      link.remove();
    });
  }

  // #revokeDownload frees the content of the previous download, which the
  // browser has saved by then.
  #revokeDownload() {
    if (this.#downloadUrl) URL.revokeObjectURL(this.#downloadUrl);
    this.#downloadUrl = null;
  }

  // #upload stores the chosen files in the open room one after another and
  // attaches them to its draft. A failed upload is reported, not retried:
  // uploads have no idempotency key.
  async #upload(files) {
    const scope = this.#workspace.scope();
    const roomId = this.#roomId;
    const draft = this.#draft;
    if (!roomId) return;
    $('room-error').textContent = '';
    for (const file of files) {
      // The rest is dropped with the draft: the room was closed or the user
      // signed out, and the next user's token must not upload these files.
      if (this.#draft !== draft) return;
      const problem = fileProblem(file);
      if (problem || draft.attachments.full) {
        $('room-error').textContent = problem ?? 'К сообщению можно прикрепить не больше 5 файлов.';
        continue;
      }
      draft.uploads++;
      this.#renderDraft();
      try {
        const info = await uploadFile(this.#api, roomId, file);
        scope.files.remember(info);
        draft.attachments.add(info);
      } catch (err) {
        if (this.#draft === draft) $('room-error').textContent = `«${file.name}»: ${describeError(err)}`;
      } finally {
        draft.uploads--;
        this.#renderDraft();
      }
    }
  }

  // #renderDraft shows the open room's draft. Sending waits for uploads to
  // finish and for the previous send to be answered.
  #renderDraft() {
    const draft = this.#draft;
    const items = draft.attachments.items.map((info) =>
      el('li', {},
        `📎 ${info.filename} · ${formatSize(info.size)}`,
        el('button', {
          type: 'button',
          class: 'small',
          title: 'Убрать',
          onclick: () => {
            draft.attachments.remove(info.file_id);
            this.#renderDraft();
          },
        }, '×'),
      ),
    );
    if (draft.uploads) items.push(el('li', { class: 'hint' }, 'Загрузка…'));
    $('pending-files').replaceChildren(...items);
    $('composer').querySelector('button[type="submit"]').disabled = this.#sending || draft.uploads > 0;
    $('attach').disabled = draft.attachments.full;
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
  // sends are ignored, but the draft can still change: the answer removes
  // only what was sent. A failed send keeps the draft and its
  // client_message_id, so retrying cannot store the message twice.
  async #send() {
    const scope = this.#workspace.scope();
    const roomId = this.#roomId;
    const draft = this.#draft;
    const attachmentIds = draft.attachments.ids;
    const typed = $('message-text').value;
    // The server rejects whitespace-only text; with attachments the text may
    // be empty instead.
    const text = typed.trim() ? typed : '';
    if (!roomId || this.#sending || draft.uploads || (!text && !attachmentIds.length)) return;

    const prepared = draft.outbox.prepare(text, attachmentIds);
    this.#setSending(true);
    await this.#guard(scope, roomId, async () => {
      const message = await sendMessage(this.#api, roomId, prepared);
      if (!scope.alive) return;
      draft.outbox.sent();
      draft.attachments.remove(...attachmentIds);
      if (this.#draft === draft) {
        if ($('message-text').value === typed) $('message-text').value = '';
        this.#renderDraft();
      }
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
    this.#renderDraft();
  }
}
