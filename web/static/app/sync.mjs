// What live events and room list changes mean for the client
// (docs/chat/realtime.md).

// eventEffect classifies a WebSocket event for user me:
//   {kind: 'message', roomId, message}  a new message;
//   {kind: 'joined', roomId}            me was added to a room;
//   {kind: 'left', roomId}              me was removed or left;
//   {kind: 'members', roomId}           someone else joined or left;
//   {kind: 'ignore'}                    unknown or incomplete events.
export function eventEffect(event, me) {
  const roomId = event?.room_id;
  if (!roomId) return { kind: 'ignore' };

  switch (event.type) {
    case 'message.created':
      return event.message ? { kind: 'message', roomId, message: event.message } : { kind: 'ignore' };
    case 'member.added':
      return { kind: event.user_id === me ? 'joined' : 'members', roomId };
    case 'member.removed':
    case 'member.left':
      return { kind: event.user_id === me ? 'left' : 'members', roomId };
    default:
      return { kind: 'ignore' };
  }
}

// roomListChanges compares the room lists before and after a reload:
// rooms that are new, rooms that are gone, and rooms with a newer message.
export function roomListChanges(before, after) {
  const old = new Map(before.map((room) => [room.room_id, room]));
  const current = new Set(after.map((room) => room.room_id));
  const added = [];
  const active = [];
  for (const room of after) {
    const previous = old.get(room.room_id);
    if (!previous) added.push(room.room_id);
    else if (room.last_message_at && room.last_message_at !== previous.last_message_at) active.push(room.room_id);
  }
  const removed = before.filter((room) => !current.has(room.room_id)).map((room) => room.room_id);
  return { added, removed, active };
}
