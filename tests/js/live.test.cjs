// Unit tests for the $store.live reconnect decisions (helpers.js) — the pure
// spine of the generation-guarded EventSource reconnect. A server-side
// keepalive test cannot exercise these client invariants, so they live here.
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');

const { shouldDeliver, nextBackoff, maxEventId } = require(path.join(
  __dirname, '..', '..', 'internal', 'httpapi', 'templates', 'assets', 'helpers.js'));

test('shouldDeliver: only the current generation delivers', () => {
  assert.equal(shouldDeliver(3, 3), true);
  // A superseded EventSource (opened under an older generation) must not
  // deliver after stop()/open() bumped the counter — the "stale ES delivers
  // after replace" bug this guard exists to prevent.
  assert.equal(shouldDeliver(2, 3), false);
  assert.equal(shouldDeliver(3, 4), false);
});

test('nextBackoff: doubles, caps at 8s, resets from falsy', () => {
  assert.equal(nextBackoff(500), 1000);
  assert.equal(nextBackoff(1000), 2000);
  assert.equal(nextBackoff(4000), 8000);
  assert.equal(nextBackoff(8000), 8000); // ceiling holds
  assert.equal(nextBackoff(0), 1000);    // falsy → base's next step
  assert.equal(nextBackoff(undefined), 1000);
});

test('maxEventId: monotonic resume cursor, ignores junk/smaller', () => {
  assert.equal(maxEventId(0, '5'), 5);   // first real id
  assert.equal(maxEventId(5, '7'), 7);   // advances
  assert.equal(maxEventId(7, '3'), 7);   // out-of-order/smaller → unchanged
  assert.equal(maxEventId(7, ''), 7);    // empty → unchanged
  assert.equal(maxEventId(7, undefined), 7);
  assert.equal(maxEventId(7, 'abc'), 7); // non-numeric → unchanged
});
