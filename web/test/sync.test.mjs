import { test } from 'node:test';
import assert from 'node:assert/strict';

import { eventEffect, roomListChanges } from '../static/app/sync.mjs';

const ME = 'u-1';

test('a new message', () => {
  const message = { message_id: 'm-1', sequence: 3 };

  assert.deepEqual(eventEffect({ type: 'message.created', room_id: 'r-1', message }, ME), {
    kind: 'message',
    roomId: 'r-1',
    message,
  });
});

test('membership changes of the user add or remove the room', () => {
  assert.deepEqual(eventEffect({ type: 'member.added', room_id: 'r-1', user_id: ME }, ME), { kind: 'joined', roomId: 'r-1' });
  assert.deepEqual(eventEffect({ type: 'member.removed', room_id: 'r-1', user_id: ME }, ME), { kind: 'left', roomId: 'r-1' });
  assert.deepEqual(eventEffect({ type: 'member.left', room_id: 'r-1', user_id: ME }, ME), { kind: 'left', roomId: 'r-1' });
});

test('membership changes of others change the roster', () => {
  for (const type of ['member.added', 'member.removed', 'member.left']) {
    assert.deepEqual(eventEffect({ type, room_id: 'r-1', user_id: 'u-2' }, ME), { kind: 'members', roomId: 'r-1' });
  }
});

test('unknown or incomplete events are ignored', () => {
  // Clients must ignore unknown types and fields (docs/chat/realtime.md).
  assert.deepEqual(eventEffect({ type: 'room.renamed', room_id: 'r-1' }, ME), { kind: 'ignore' });
  assert.deepEqual(eventEffect({ type: 'message.created', room_id: 'r-1' }, ME), { kind: 'ignore' });
  assert.deepEqual(eventEffect({ type: 'member.added' }, ME), { kind: 'ignore' });
});

test('roomListChanges finds new, vanished and active rooms', () => {
  const before = [
    { room_id: 'r-1', last_message_at: '2026-10-18T10:00:00Z' },
    { room_id: 'r-2' },
    { room_id: 'r-3', last_message_at: '2026-10-18T09:00:00Z' },
  ];
  const after = [
    { room_id: 'r-4' },
    { room_id: 'r-1', last_message_at: '2026-10-18T11:00:00Z' },
    { room_id: 'r-3', last_message_at: '2026-10-18T09:00:00Z' },
  ];

  assert.deepEqual(roomListChanges(before, after), {
    added: ['r-4'],
    removed: ['r-2'],
    active: ['r-1'],
  });
});

test('a room gaining its first message is active', () => {
  assert.deepEqual(roomListChanges([{ room_id: 'r-1' }], [{ room_id: 'r-1', last_message_at: '2026-10-18T10:00:00Z' }]), {
    added: [],
    removed: [],
    active: ['r-1'],
  });
});
