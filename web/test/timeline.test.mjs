import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Timeline } from '../static/app/timeline.mjs';

function msg(sequence) {
  return { message_id: `m-${sequence}`, room_id: 'r-1', sequence, text: `#${sequence}` };
}

// page builds a history page: newest first, as the Gateway returns it.
function latestPage(from, to, nextCursor = '') {
  const messages = [];
  for (let s = to; s >= from; s--) messages.push(msg(s));
  return { messages, next_cursor: nextCursor };
}

const sequences = (timeline) => timeline.messages.map((m) => m.sequence);

test('a new timeline is empty and not loaded', () => {
  const timeline = new Timeline();

  assert.equal(timeline.loaded, false);
  assert.equal(timeline.newest, 0);
  assert.deepEqual(timeline.messages, []);
});

test('the latest page is shown oldest first', () => {
  const timeline = new Timeline();

  timeline.setLatest(latestPage(41, 90, '41'));

  assert.equal(timeline.loaded, true);
  assert.equal(timeline.newest, 90);
  assert.equal(timeline.olderCursor, '41');
  assert.deepEqual(sequences(timeline).slice(0, 2), [41, 42]);
  assert.equal(timeline.messages.at(-1).sequence, 90);
});

test('an empty room has nothing older', () => {
  const timeline = new Timeline();

  timeline.setLatest({ messages: [], next_cursor: '' });

  assert.equal(timeline.loaded, true);
  assert.equal(timeline.newest, 0);
  assert.equal(timeline.olderCursor, '');
});

test('older pages extend the history downwards', () => {
  const timeline = new Timeline();
  timeline.setLatest(latestPage(3, 4, '3'));

  timeline.addOlder(latestPage(1, 2, ''));

  assert.deepEqual(sequences(timeline), [1, 2, 3, 4]);
  assert.equal(timeline.olderCursor, '');
  assert.equal(timeline.newest, 4);
});

test('apply appends the next message', () => {
  const timeline = new Timeline();
  timeline.setLatest(latestPage(1, 2));

  assert.equal(timeline.apply(msg(3)), 'appended');

  assert.deepEqual(sequences(timeline), [1, 2, 3]);
  assert.equal(timeline.newest, 3);
});

test('apply recognizes duplicates', () => {
  const timeline = new Timeline();
  timeline.setLatest(latestPage(1, 2));

  assert.equal(timeline.apply(msg(2)), 'duplicate');
  assert.equal(timeline.apply(msg(3)), 'appended');
  assert.equal(timeline.apply(msg(3)), 'duplicate');

  assert.deepEqual(sequences(timeline), [1, 2, 3]);
});

test('apply reports a gap without accepting the message', () => {
  // A lost event leaves a hole; the caller fills it with catch-up after
  // newest, which also brings this message again.
  const timeline = new Timeline();
  timeline.setLatest(latestPage(1, 2));

  assert.equal(timeline.apply(msg(5)), 'gap');

  assert.deepEqual(sequences(timeline), [1, 2]);
  assert.equal(timeline.newest, 2);
});

test('a message older than the loaded window is stale', () => {
  const timeline = new Timeline();
  timeline.setLatest(latestPage(41, 90, '41'));

  assert.equal(timeline.apply(msg(10)), 'stale');

  assert.equal(timeline.messages.length, 50);
});

test('an unloaded timeline treats every message as a gap', () => {
  // The room has not been opened: only catch-up after 0 is complete.
  const timeline = new Timeline();

  assert.equal(timeline.apply(msg(1)), 'gap');
  assert.deepEqual(timeline.messages, []);
});

test('catch-up after the newest message fills a gap', () => {
  const timeline = new Timeline();
  timeline.setLatest(latestPage(1, 2));
  timeline.apply(msg(5));

  // Catch-up pages are oldest first.
  const results = [msg(3), msg(4), msg(5)].map((m) => timeline.apply(m));

  assert.deepEqual(results, ['appended', 'appended', 'appended']);
  assert.deepEqual(sequences(timeline), [1, 2, 3, 4, 5]);
});

test('messages are returned as a copy', () => {
  const timeline = new Timeline();
  timeline.setLatest(latestPage(1, 1));

  timeline.messages.pop();

  assert.equal(timeline.messages.length, 1);
});
