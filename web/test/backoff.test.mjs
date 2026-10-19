import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Backoff } from '../static/app/backoff.mjs';

test('delays grow exponentially up to the maximum', () => {
  const backoff = new Backoff({ base: 500, max: 4000, random: () => 1 });

  assert.deepEqual([1, 2, 3, 4, 5].map(() => backoff.next()), [500, 1000, 2000, 4000, 4000]);
});

test('jitter spreads delays between half and the full value', () => {
  // Clients that lost the same Gateway must not all reconnect at once.
  assert.equal(new Backoff({ base: 1000, random: () => 0 }).next(), 500);
  assert.equal(new Backoff({ base: 1000, random: () => 0.5 }).next(), 750);
});

test('reset starts over after a successful connection', () => {
  const backoff = new Backoff({ base: 500, random: () => 1 });
  backoff.next();
  backoff.next();

  backoff.reset();

  assert.equal(backoff.next(), 500);
});
