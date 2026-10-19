// The signed-in part of the client: the room list with user search and
// channel creation, and the open room.

import { createChannel, listAllRooms, openDirect, roomTitle } from './chat.mjs';
import { Conversation } from './conversation.mjs';
import { el, formatTime } from './dom.mjs';
import { describeError } from './errors.mjs';
import { Scope } from './scope.mjs';
import { bindUserSearch } from './search.mjs';

const $ = (id) => document.getElementById(id);

const kindName = (room) => (room.kind === 'direct' ? 'личный диалог' : room.private ? 'закрытый канал' : 'канал');

export class Workspace {
  #api;
  #session;
  #scope;
  #rooms = []; // the room list, most recently active first
  #current = null; // room_id of the open room
  #conversation;

  constructor({ api, session, searchDelayMs = 300 }) {
    this.#api = api;
    this.#session = session;
    this.#scope = new Scope(api);
    this.#conversation = new Conversation({
      api,
      workspace: {
        scope: () => this.#scope,
        me: () => this.#me,
        lostRoom: (roomId) => this.#lostRoom(roomId),
        touch: (roomId, at) => this.#touch(roomId, at),
      },
    });

    bindUserSearch({
      input: $('user-search'),
      list: $('user-results'),
      errorLine: $('sidebar-error'),
      scope: () => this.#scope,
      pick: (user) => this.#openDirectWith(user),
      delayMs: searchDelayMs,
    });
    $('channel-form').addEventListener('submit', (event) => {
      event.preventDefault();
      this.#createChannel(event.currentTarget);
    });
    $('refresh').addEventListener('click', () => this.refresh());
  }

  get #me() {
    return this.#session.user?.id;
  }

  // refresh re-reads the room list and catches up every room whose messages
  // are loaded.
  async refresh() {
    const scope = this.#scope;
    $('sidebar-error').textContent = '';
    try {
      await this.#loadRooms(scope);
    } catch (err) {
      if (scope.alive) $('sidebar-error').textContent = describeError(err);
      return;
    }
    await Promise.all([...scope.timelines.keys()].map((roomId) => this.#conversation.sync(scope, roomId)));
  }

  // reset forgets the signed-in user's data after sign-out. Work still under
  // way for that user finds its scope ended and drops its results.
  reset() {
    this.#scope.end();
    this.#scope = new Scope(this.#api);
    this.#rooms = [];
    this.#current = null;
    this.#conversation.close();
    $('room-list').replaceChildren();
    $('user-results').replaceChildren();
    $('sidebar-error').textContent = '';
    $('user-search').value = '';
    this.#showRoom(false);
  }

  // #loadPeers loads the profiles of the other participants of dialogs; the
  // user's own name is known.
  #loadPeers(scope, rooms) {
    const peers = rooms.flatMap((room) => room.participant_ids ?? []).filter((id) => id !== this.#me);
    return scope.profiles.getAll(peers);
  }

  async #loadRooms(scope) {
    const rooms = await listAllRooms(this.#api);
    await this.#loadPeers(scope, rooms);
    if (!scope.alive) return;
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

  // #addRoom puts a room returned by a create or open call at the top and
  // reports whether its scope is still alive.
  async #addRoom(scope, room) {
    await this.#loadPeers(scope, [room]);
    if (!scope.alive) return false;
    this.#rooms = [room, ...this.#rooms.filter((r) => r.room_id !== room.room_id)];
    this.#renderRooms();
    return true;
  }

  #renderRooms() {
    const profiles = this.#scope.profiles;
    $('room-list').replaceChildren(...this.#rooms.map((room) =>
      el('li', {},
        el('button', {
          type: 'button',
          class: room.room_id === this.#current ? 'room-item active' : 'room-item',
          onclick: () => this.#openRoom(room.room_id),
        },
        el('span', { class: 'room-name' }, roomTitle(room, this.#me, profiles)),
        el('span', { class: 'hint' }, kindName(room), room.last_message_at ? ` · ${formatTime(room.last_message_at)}` : ''),
        ),
      ),
    ));
  }

  // #lostRoom drops a room the user can no longer access: it was left, the
  // user was removed, or it never was theirs.
  #lostRoom(roomId) {
    this.#rooms = this.#rooms.filter((room) => room.room_id !== roomId);
    this.#scope.timelines.delete(roomId);
    if (this.#current === roomId) {
      this.#current = null;
      this.#conversation.close();
      this.#showRoom(false);
      $('sidebar-error').textContent = 'Комната больше недоступна: вы не её участник.';
    }
    this.#renderRooms();
  }

  // #touch moves a room with a new message to the top of the list.
  #touch(roomId, at) {
    const room = this.#room(roomId);
    if (!room) return;
    room.last_message_at = at;
    this.#rooms = [room, ...this.#rooms.filter((r) => r !== room)];
    this.#renderRooms();
  }

  async #openRoom(roomId) {
    const room = this.#room(roomId);
    if (!room) return;
    this.#current = roomId;
    $('room-title').textContent = roomTitle(room, this.#me, this.#scope.profiles);
    $('room-kind').textContent = kindName(room);
    this.#showRoom(true);
    this.#renderRooms();
    await this.#conversation.open(room);
  }

  #showRoom(visible) {
    $('room').hidden = !visible;
    $('no-room').hidden = visible;
  }

  async #openDirectWith(user) {
    const scope = this.#scope;
    $('user-search').value = '';
    $('user-results').replaceChildren();
    try {
      const room = await openDirect(this.#api, user.user_id);
      if (await this.#addRoom(scope, room)) await this.#openRoom(room.room_id);
    } catch (err) {
      if (scope.alive) $('sidebar-error').textContent = describeError(err);
    }
  }

  async #createChannel(form) {
    const scope = this.#scope;
    const name = form.elements.name.value.trim();
    if (!name) return;
    $('sidebar-error').textContent = '';
    try {
      const room = await createChannel(this.#api, name, form.elements.private.checked);
      if (!scope.alive) return;
      form.reset();
      form.closest('details').open = false;
      if (await this.#addRoom(scope, room)) await this.#openRoom(room.room_id);
    } catch (err) {
      if (scope.alive) $('sidebar-error').textContent = describeError(err);
    }
  }
}
