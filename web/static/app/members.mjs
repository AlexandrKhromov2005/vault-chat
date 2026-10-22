// The members panel of the open room: the list, adding and removing members
// for the channel owner, and leaving a channel.

import { addMember, leaveRoom, listAllMembers, removeMember } from './chat.mjs';
import { el } from './dom.mjs';
import { describeError } from './errors.mjs';
import { ApiError } from './http.mjs';
import { bindUserSearch } from './search.mjs';

const $ = (id) => document.getElementById(id);

export class Members {
  #api;
  #workspace;
  #room = null; // the open room
  #visible = false; // whether the user wants the panel shown

  // workspace provides scope(), me(), lostRoom(roomId) for a room the user
  // can no longer access, and leftRoom(roomId) for one the user left.
  constructor({ api, workspace, searchDelayMs }) {
    this.#api = api;
    this.#workspace = workspace;

    $('toggle-members').addEventListener('click', () => {
      this.#visible = !this.#visible;
      this.#show();
      if (this.#visible) this.#load();
    });
    $('leave-room').addEventListener('click', () => this.#leave());
    bindUserSearch({
      input: $('add-member-search'),
      list: $('add-member-results'),
      errorLine: $('members-error'),
      scope: workspace.scope,
      pick: (user) => this.#add(user),
      delayMs: searchDelayMs,
    });
  }

  // open follows the room the workspace opened; an open panel shows its
  // members.
  open(room) {
    this.#room = room;
    this.#clear();
    this.#show();
    if (this.#visible) this.#load();
  }

  // close hides the panel along with the room. reset also forgets that the
  // user wanted it shown.
  close({ reset = false } = {}) {
    this.#room = null;
    if (reset) this.#visible = false;
    this.#clear();
    this.#show();
  }

  // changed reloads the shown list after someone joined or left its room.
  async changed(roomId) {
    if (this.#visible && this.#room?.room_id === roomId) await this.#load();
  }

  #clear() {
    $('member-list').replaceChildren();
    $('add-member-results').replaceChildren();
    $('add-member-search').value = '';
    $('members-error').textContent = '';
  }

  #show() {
    $('members').hidden = !this.#room || !this.#visible;
    $('toggle-members').setAttribute('aria-pressed', String(this.#visible));
  }

  #isShown(scope, room) {
    return scope.alive && this.#room === room && this.#visible;
  }

  // #guard runs work for the shown room. A 404 means the user lost access
  // to it; other errors are shown while the room's panel is.
  async #guard(scope, room, action) {
    try {
      await action();
      return true;
    } catch (err) {
      if (!scope.alive) return false;
      if (err instanceof ApiError && err.status === 404) this.#workspace.lostRoom(room.room_id);
      else if (this.#isShown(scope, room)) $('members-error').textContent = describeError(err);
      return false;
    }
  }

  async #load() {
    const scope = this.#workspace.scope();
    const room = this.#room;
    if (!room) return;
    const me = this.#workspace.me();
    const isChannel = room.kind !== 'direct';
    const isOwner = isChannel && room.owner_id === me;
    $('add-member').hidden = !isOwner;
    $('leave-room').hidden = !isChannel || isOwner;

    await this.#guard(scope, room, async () => {
      const members = await listAllMembers(this.#api, room.room_id);
      await scope.profiles.getAll(members.map((m) => m.user_id));
      if (!this.#isShown(scope, room)) return;
      $('member-list').replaceChildren(...members.map((member) => {
        const isMe = member.user_id === me;
        return el('li', { class: 'member' },
          el('span', {}, scope.profiles.name(member.user_id) ?? 'удалённый пользователь', isMe ? ' (вы)' : ''),
          isChannel && member.user_id === room.owner_id ? el('span', { class: 'badge' }, 'владелец') : null,
          isOwner && !isMe
            ? el('button', { type: 'button', class: 'small', onclick: () => this.#remove(member.user_id) }, 'Удалить')
            : null,
        );
      }));
    });
  }

  async #add(user) {
    const scope = this.#workspace.scope();
    const room = this.#room;
    $('add-member-search').value = '';
    $('add-member-results').replaceChildren();
    if (await this.#guard(scope, room, () => addMember(this.#api, room.room_id, user.user_id)) && this.#isShown(scope, room)) {
      await this.#load();
    }
  }

  async #remove(userId) {
    const scope = this.#workspace.scope();
    const room = this.#room;
    if (await this.#guard(scope, room, () => removeMember(this.#api, room.room_id, userId)) && this.#isShown(scope, room)) {
      await this.#load();
    }
  }

  async #leave() {
    const scope = this.#workspace.scope();
    const room = this.#room;
    if (!room || !window.confirm('Покинуть канал? Вернуться можно только по приглашению владельца.')) return;
    if (await this.#guard(scope, room, () => leaveRoom(this.#api, room.room_id)) && scope.alive) {
      this.#workspace.leftRoom(room.room_id);
    }
  }
}
