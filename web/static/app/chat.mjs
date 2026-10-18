// Rooms, members and messages through the Gateway's Chat API.

const PAGE_LIMIT = 100;
const HISTORY_LIMIT = 50;

const room = (roomId) => `/api/v1/rooms/${encodeURIComponent(roomId)}`;

// pagePath appends query parameters, leaving out empty ones: the Gateway
// rejects an empty cursor.
function pagePath(path, params) {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== '' && value !== undefined) query.set(key, String(value));
  }
  return `${path}?${query}`;
}

// listAllRooms returns all of the caller's rooms, most recently active first.
export async function listAllRooms(api) {
  const rooms = [];
  let cursor = '';
  do {
    const page = await api.call('GET', pagePath('/api/v1/rooms', { limit: PAGE_LIMIT, cursor }));
    rooms.push(...page.rooms);
    cursor = page.next_cursor;
  } while (cursor);
  return rooms;
}

// listAllMembers returns the members of a room, ordered by user ID.
export async function listAllMembers(api, roomId) {
  const members = [];
  let cursor = '';
  do {
    const page = await api.call('GET', pagePath(`${room(roomId)}/members`, { limit: PAGE_LIMIT, cursor }));
    members.push(...page.members);
    cursor = page.next_cursor;
  } while (cursor);
  return members;
}

// loadLatest reads the newest history page into timeline.
export async function loadLatest(api, roomId, timeline) {
  timeline.setLatest(await api.call('GET', pagePath(`${room(roomId)}/messages`, { limit: HISTORY_LIMIT })));
}

// loadOlder reads the next older history page into timeline.
export async function loadOlder(api, roomId, timeline) {
  const cursor = timeline.olderCursor;
  if (!cursor) return;
  timeline.addOlder(await api.call('GET', pagePath(`${room(roomId)}/messages`, { limit: HISTORY_LIMIT, cursor })));
}

// catchUp fetches every message after the timeline's newest one and returns
// how many were appended.
export async function catchUp(api, roomId, timeline) {
  let appended = 0;
  let after = String(timeline.newest);
  do {
    const page = await api.call('GET', pagePath(`${room(roomId)}/messages`, { limit: PAGE_LIMIT, after }));
    // An unloaded timeline only completes itself through catch-up from 0.
    if (!timeline.loaded) timeline.setLatest({ messages: [], next_cursor: '' });
    for (const message of page.messages) {
      if (timeline.apply(message) === 'appended') appended++;
    }
    after = page.next_cursor;
  } while (after);
  return appended;
}

// sendMessage stores a message; retrying with the same clientMessageId
// returns the original instead of a copy.
export function sendMessage(api, roomId, { clientMessageId, text, attachmentIds = [] }) {
  const body = { client_message_id: clientMessageId, text };
  if (attachmentIds.length) body.attachment_ids = attachmentIds;
  return api.call('POST', `${room(roomId)}/messages`, { body });
}

// Outbox gives every message a client_message_id and keeps it while the same
// message is retried, so a send whose response was lost is not stored twice.
export class Outbox {
  #newId;
  #pending = null;

  constructor(newId = () => crypto.randomUUID()) {
    this.#newId = newId;
  }

  prepare(text, attachmentIds) {
    const key = JSON.stringify([text, attachmentIds]);
    if (this.#pending?.key !== key) this.#pending = { key, clientMessageId: this.#newId() };
    return { clientMessageId: this.#pending.clientMessageId, text, attachmentIds };
  }

  // sent ends the pending message after the server has stored it.
  sent() {
    this.#pending = null;
  }
}

export const openDirect = (api, peerUserId) =>
  api.call('POST', '/api/v1/rooms/direct', { body: { peer_user_id: peerUserId } });

export const createChannel = (api, name, isPrivate) =>
  api.call('POST', '/api/v1/rooms/channels', { body: { name, private: isPrivate } });

export const addMember = (api, roomId, userId) =>
  api.call('PUT', `${room(roomId)}/members/${encodeURIComponent(userId)}`);

export const removeMember = (api, roomId, userId) =>
  api.call('DELETE', `${room(roomId)}/members/${encodeURIComponent(userId)}`);

export const leaveRoom = (api, roomId) => api.call('DELETE', `${room(roomId)}/members/me`);

// roomTitle is the channel name, or the other participant's username for a
// direct dialog.
export function roomTitle(chatRoom, myUserId, profiles) {
  if (chatRoom.kind !== 'direct') return chatRoom.name;
  const peer = (chatRoom.participant_ids ?? []).find((id) => id !== myUserId);
  return (peer && profiles.name(peer)) || 'Личный диалог';
}
