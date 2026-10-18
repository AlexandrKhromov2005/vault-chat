// The signed-in part of the client: user search, rooms, the open
// conversation and its members.

import {
  Outbox,
  addMember,
  catchUp,
  createChannel,
  leaveRoom,
  listAllMembers,
  listAllRooms,
  loadLatest,
  loadOlder,
  openDirect,
  removeMember,
  roomTitle,
  sendMessage,
} from './chat.mjs';
import { debounce, el, formatTime } from './dom.mjs';
import { describeError } from './errors.mjs';
import { Attachments, FileInfos, downloadFile, fileProblem, formatSize, uploadFile } from './files.mjs';
import { ApiError } from './http.mjs';
import { Timeline } from './timeline.mjs';

const $ = (id) => document.getElementById(id);
const SEARCH_DELAY_MS = 300;
// A conversation scrolled to within this distance of its end follows new
// messages; otherwise the reader's position is kept.
const STICK_TO_BOTTOM_PX = 48;

export class Workspace {
  #api;
  #session;
  #profiles;
  #rooms = []; // the room list, most recently active first
  #timelines = new Map(); // room_id → Timeline
  #current = null; // room_id of the open room
  #outbox = new Outbox();
  #membersOpen = false;
  #files;
  #attachments = new Attachments(); // uploaded files of the message being written
  #uploads = 0; // uploads in progress

  constructor({ api, session, profiles }) {
    this.#api = api;
    this.#session = session;
    this.#profiles = profiles;
    this.#files = new FileInfos(api);
    this.#bind();
  }

  get #me() {
    return this.#session.user?.id;
  }

  // refresh re-reads the room list and catches up the open room.
  async refresh() {
    $('sidebar-error').textContent = '';
    try {
      await this.#loadRooms();
      if (this.#current) await this.#syncRoom(this.#current);
    } catch (err) {
      $('sidebar-error').textContent = describeError(err);
    }
  }

  // reset forgets everything after sign-out.
  reset() {
    this.#rooms = [];
    this.#timelines.clear();
    this.#current = null;
    this.#membersOpen = false;
    this.#outbox = new Outbox();
    this.#files = new FileInfos(this.#api);
    this.#attachments.clear();
    for (const id of ['room-list', 'user-results', 'message-list', 'member-list', 'add-member-results', 'pending-files']) {
      $(id).replaceChildren();
    }
    for (const id of ['sidebar-error', 'room-error', 'members-error']) $(id).textContent = '';
    $('user-search').value = '';
    this.#showRoom(false);
  }

  // ---- rooms -------------------------------------------------------------

  async #loadRooms() {
    const rooms = await listAllRooms(this.#api);
    await this.#profiles.getAll(rooms.flatMap((room) => room.participant_ids ?? []));
    this.#rooms = rooms;
    if (this.#current && !this.#room(this.#current)) {
      this.#lostRoom(this.#current);
      return;
    }
    this.#renderRooms();
  }

  #room(roomId) {
    return this.#rooms.find((room) => room.room_id === roomId);
  }

  // #addRoom puts a room returned by a create or open call at the top.
  async #addRoom(room) {
    await this.#profiles.getAll(room.participant_ids ?? []);
    this.#rooms = [room, ...this.#rooms.filter((r) => r.room_id !== room.room_id)];
    this.#renderRooms();
  }

  #renderRooms() {
    $('room-list').replaceChildren(
      ...this.#rooms.map((room) =>
        el('li', {},
          el('button', {
            type: 'button',
            class: room.room_id === this.#current ? 'room-item active' : 'room-item',
            onclick: () => this.#openRoom(room.room_id),
          },
          el('span', { class: 'room-name' }, roomTitle(room, this.#me, this.#profiles)),
          el('span', { class: 'hint' },
            room.kind === 'direct' ? 'диалог' : room.private ? 'закрытый канал' : 'канал',
            room.last_message_at ? ` · ${formatTime(room.last_message_at)}` : ''),
          ),
        ),
      ),
    );
  }

  // #lostRoom handles a room the user can no longer access: it was left,
  // the user was removed, or it never was theirs.
  #lostRoom(roomId) {
    this.#rooms = this.#rooms.filter((room) => room.room_id !== roomId);
    this.#timelines.delete(roomId);
    if (this.#current === roomId) {
      this.#current = null;
      this.#showRoom(false);
      $('sidebar-error').textContent = 'Комната больше недоступна: вы не её участник.';
    }
    this.#renderRooms();
  }

  // #guard runs a room operation and turns "not found" into a lost room.
  async #guard(roomId, errorLine, action) {
    $(errorLine).textContent = '';
    try {
      return await action();
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) this.#lostRoom(roomId);
      else $(errorLine).textContent = describeError(err);
      return undefined;
    }
  }

  // ---- conversation ------------------------------------------------------

  async #openRoom(roomId) {
    const room = this.#room(roomId);
    if (!room) return;
    this.#current = roomId;
    this.#outbox = new Outbox();
    // Files belong to the room they were uploaded to; unsent ones stay on
    // the server unreferenced (docs/media/files.md).
    this.#attachments.clear();
    this.#renderPending();
    $('message-text').value = '';
    $('room-title').textContent = roomTitle(room, this.#me, this.#profiles);
    $('room-kind').textContent = room.kind === 'direct' ? 'личный диалог' : room.private ? 'закрытый канал' : 'канал';
    $('message-list').replaceChildren();
    this.#showRoom(true);
    this.#renderRooms();

    await this.#guard(roomId, 'room-error', async () => {
      const timeline = this.#timeline(roomId);
      if (timeline.loaded) await catchUp(this.#api, roomId, timeline);
      else await loadLatest(this.#api, roomId, timeline);
      await this.#renderMessages({ stick: true });
    });
    if (this.#membersOpen) await this.#loadMembers();
    $('message-text').focus();
  }

  async #syncRoom(roomId) {
    await this.#guard(roomId, 'room-error', async () => {
      const timeline = this.#timeline(roomId);
      if (!timeline.loaded) await loadLatest(this.#api, roomId, timeline);
      else if ((await catchUp(this.#api, roomId, timeline)) === 0) return;
      if (roomId === this.#current) await this.#renderMessages();
    });
  }

  #timeline(roomId) {
    if (!this.#timelines.has(roomId)) this.#timelines.set(roomId, new Timeline());
    return this.#timelines.get(roomId);
  }

  #showRoom(visible) {
    $('room').hidden = !visible;
    $('no-room').hidden = visible;
    $('members').hidden = !visible || !this.#membersOpen;
    $('toggle-members').setAttribute('aria-pressed', String(this.#membersOpen));
  }

  async #renderMessages({ stick = false, keepOffset = false } = {}) {
    const roomId = this.#current;
    const timeline = this.#timeline(roomId);
    const messages = timeline.messages;
    await this.#profiles.getAll(messages.map((m) => m.sender_id));
    try {
      await this.#files.getAll(messages.flatMap((m) => m.attachment_ids ?? []));
    } catch {
      // Attachments without metadata are still shown and downloadable.
    }
    if (roomId !== this.#current) return;

    const box = $('messages');
    const fromBottom = box.scrollHeight - box.scrollTop;
    const atBottom = fromBottom - box.clientHeight < STICK_TO_BOTTOM_PX;

    $('message-list').replaceChildren(...messages.map((message) => this.#messageItem(message)));
    $('load-older').hidden = !timeline.olderCursor;

    if (keepOffset) box.scrollTop = box.scrollHeight - fromBottom;
    else if (stick || atBottom) box.scrollTop = box.scrollHeight;
  }

  #messageItem(message) {
    const own = message.sender_id === this.#me;
    const author = this.#profiles.name(message.sender_id) ?? 'удалённый пользователь';
    return el('li', { class: own ? 'message own' : 'message', 'data-sequence': message.sequence },
      el('div', { class: 'message-meta' },
        el('span', { class: 'message-author' }, own ? 'вы' : author),
        el('time', { datetime: message.created_at }, formatTime(message.created_at)),
      ),
      message.text ? el('p', { class: 'message-text' }, message.text) : null,
      message.attachment_ids?.length
        ? el('ul', { class: 'attachments' }, message.attachment_ids.map((id) => el('li', {}, this.#attachmentItem(id))))
        : null,
    );
  }

  #attachmentItem(fileId) {
    const info = this.#files.info(fileId);
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
    $('room-error').textContent = '';
    try {
      const url = URL.createObjectURL(await downloadFile(this.#api, fileId));
      const link = el('a', { href: url, download: filename, hidden: true });
      document.body.append(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 60_000);
    } catch (err) {
      $('room-error').textContent = describeError(err);
    }
  }

  // #upload stores the chosen files in the open room one after another and
  // attaches them to the message being written. A failed upload is reported,
  // not retried: uploads have no idempotency key.
  async #upload(files) {
    const roomId = this.#current;
    $('room-error').textContent = '';
    for (const file of files) {
      const problem = fileProblem(file);
      if (problem || this.#attachments.full) {
        $('room-error').textContent = problem ?? 'К сообщению можно прикрепить не больше 5 файлов.';
        continue;
      }
      this.#uploads++;
      this.#renderPending();
      try {
        const info = await uploadFile(this.#api, roomId, file);
        this.#files.remember(info);
        if (roomId === this.#current) this.#attachments.add(info);
      } catch (err) {
        if (roomId === this.#current) $('room-error').textContent = `«${file.name}»: ${describeError(err)}`;
      } finally {
        this.#uploads--;
        this.#renderPending();
      }
    }
  }

  #renderPending() {
    const items = this.#attachments.items.map((info) =>
      el('li', {},
        `📎 ${info.filename} · ${formatSize(info.size)}`,
        el('button', {
          type: 'button',
          class: 'small',
          title: 'Убрать',
          onclick: () => {
            this.#attachments.remove(info.file_id);
            this.#renderPending();
          },
        }, '×'),
      ),
    );
    if (this.#uploads) items.push(el('li', { class: 'hint' }, 'Загрузка…'));
    $('pending-files').replaceChildren(...items);
    $('composer').querySelector('button[type="submit"]').disabled = this.#uploads > 0;
    $('attach').disabled = this.#attachments.full;
  }

  async #send() {
    const roomId = this.#current;
    const attachmentIds = this.#attachments.ids;
    let text = $('message-text').value;
    // The server rejects whitespace-only text; with attachments the text may
    // be empty instead.
    if (!text.trim()) {
      if (!attachmentIds.length) return;
      text = '';
    }
    if (!roomId || this.#uploads) return;

    const prepared = this.#outbox.prepare(text, attachmentIds);
    const button = $('composer').querySelector('button[type="submit"]');
    button.disabled = true;
    await this.#guard(roomId, 'room-error', async () => {
      const message = await sendMessage(this.#api, roomId, prepared);
      this.#outbox.sent();
      if (roomId === this.#current) {
        $('message-text').value = '';
        this.#attachments.clear();
        this.#renderPending();
      }
      const timeline = this.#timeline(roomId);
      if (timeline.apply(message) === 'gap') await catchUp(this.#api, roomId, timeline);
      this.#touch(roomId, message.created_at);
      if (roomId === this.#current) await this.#renderMessages({ stick: true });
    });
    button.disabled = this.#uploads > 0;
  }

  // #touch moves a room with a new message to the top of the list.
  #touch(roomId, at) {
    const room = this.#room(roomId);
    if (!room) return;
    room.last_message_at = at;
    this.#rooms = [room, ...this.#rooms.filter((r) => r !== room)];
    this.#renderRooms();
  }

  // ---- members -----------------------------------------------------------

  async #loadMembers() {
    const roomId = this.#current;
    const room = this.#room(roomId);
    if (!room) return;
    const isChannel = room.kind !== 'direct';
    const isOwner = isChannel && room.owner_id === this.#me;
    $('add-member').hidden = !isOwner;
    $('leave-room').hidden = !isChannel || isOwner;

    await this.#guard(roomId, 'members-error', async () => {
      const members = await listAllMembers(this.#api, roomId);
      await this.#profiles.getAll(members.map((m) => m.user_id));
      if (roomId !== this.#current) return;
      $('member-list').replaceChildren(...members.map((member) => {
        const name = this.#profiles.name(member.user_id) ?? 'удалённый пользователь';
        const isMe = member.user_id === this.#me;
        return el('li', { class: 'member' },
          el('span', {}, name, isMe ? ' (вы)' : ''),
          member.user_id === room.owner_id ? el('span', { class: 'badge' }, 'владелец') : null,
          isOwner && !isMe
            ? el('button', { type: 'button', class: 'small', onclick: () => this.#removeMember(member.user_id) }, 'Удалить')
            : null,
        );
      }));
    });
  }

  async #removeMember(userId) {
    const roomId = this.#current;
    await this.#guard(roomId, 'members-error', () => removeMember(this.#api, roomId, userId));
    await this.#loadMembers();
  }

  async #addMember(user) {
    const roomId = this.#current;
    await this.#guard(roomId, 'members-error', () => addMember(this.#api, roomId, user.user_id));
    $('add-member-search').value = '';
    $('add-member-results').replaceChildren();
    await this.#loadMembers();
  }

  async #leave() {
    const roomId = this.#current;
    if (!roomId || !window.confirm('Покинуть канал? Вернуться можно только по приглашению владельца.')) return;
    const left = await this.#guard(roomId, 'members-error', async () => {
      await leaveRoom(this.#api, roomId);
      return true;
    });
    if (left) {
      this.#lostRoom(roomId);
      // Leaving was intended; no need to explain the missing room.
      $('sidebar-error').textContent = '';
    }
  }

  // ---- search and creation -----------------------------------------------

  // #searchInto shows users matching input's value in list; pick runs on a
  // click. Answers to outdated queries are dropped.
  #searchInto(input, list, errorLine, pick) {
    let latest = 0;
    const search = debounce(async () => {
      const query = ++latest;
      $(errorLine).textContent = '';
      try {
        const users = await this.#profiles.search(input.value);
        if (query !== latest) return;
        list.replaceChildren(...users.map((user) =>
          el('li', {}, el('button', { type: 'button', class: 'user-item', onclick: () => pick(user) }, user.username)),
        ));
      } catch (err) {
        if (query === latest) $(errorLine).textContent = describeError(err);
      }
    }, SEARCH_DELAY_MS);
    input.addEventListener('input', search);
  }

  async #openDirectWith(user) {
    $('user-search').value = '';
    $('user-results').replaceChildren();
    try {
      const room = await openDirect(this.#api, user.user_id);
      await this.#addRoom(room);
      await this.#openRoom(room.room_id);
    } catch (err) {
      $('sidebar-error').textContent = describeError(err);
    }
  }

  async #createChannel(form) {
    const name = form.elements.name.value.trim();
    if (!name) return;
    $('sidebar-error').textContent = '';
    try {
      const room = await createChannel(this.#api, name, form.elements.private.checked);
      form.reset();
      form.closest('details').open = false;
      await this.#addRoom(room);
      await this.#openRoom(room.room_id);
    } catch (err) {
      $('sidebar-error').textContent = describeError(err);
    }
  }

  // ---- wiring ------------------------------------------------------------

  #bind() {
    this.#searchInto($('user-search'), $('user-results'), 'sidebar-error', (user) => this.#openDirectWith(user));
    this.#searchInto($('add-member-search'), $('add-member-results'), 'members-error', (user) => this.#addMember(user));

    $('channel-form').addEventListener('submit', (event) => {
      event.preventDefault();
      this.#createChannel(event.currentTarget);
    });
    $('refresh').addEventListener('click', () => this.refresh());

    $('composer').addEventListener('submit', (event) => {
      event.preventDefault();
      this.#send();
    });
    $('attach').addEventListener('click', () => $('file-input').click());
    $('file-input').addEventListener('change', (event) => {
      const files = [...event.currentTarget.files];
      event.currentTarget.value = '';
      this.#upload(files);
    });
    $('message-text').addEventListener('keydown', (event) => {
      if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
        event.preventDefault();
        this.#send();
      }
    });
    $('load-older').addEventListener('click', async () => {
      const roomId = this.#current;
      await this.#guard(roomId, 'room-error', async () => {
        await loadOlder(this.#api, roomId, this.#timeline(roomId));
        if (roomId === this.#current) await this.#renderMessages({ keepOffset: true });
      });
    });

    $('toggle-members').addEventListener('click', () => {
      this.#membersOpen = !this.#membersOpen;
      this.#showRoom(Boolean(this.#current));
      if (this.#membersOpen) this.#loadMembers();
    });
    $('leave-room').addEventListener('click', () => this.#leave());
  }
}
