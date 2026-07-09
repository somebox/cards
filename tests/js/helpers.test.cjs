// Unit tests for the pure UI helpers (internal/httpapi/templates/assets/helpers.js),
// run by Node's built-in test runner: `node --test tests/js/`. No dependencies.
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');

const { ago } = require(path.join(
  __dirname, '..', '..', 'internal', 'httpapi', 'templates', 'assets', 'helpers.js'));

// apiErrText was replaced by cardsAPI.parseErrorBody in P10 — see api.test.cjs
// for the equivalent structured-error unit tests.

test('ago: buckets match the data-ago contract', () => {
  const now = '2026-07-08T12:00:00Z';
  assert.equal(ago('2026-07-08T11:59:31Z', now), 'just now');       // <60s
  assert.equal(ago('2026-07-08T11:55:00Z', now), '5m ago');         // <1h
  assert.equal(ago('2026-07-08T09:00:00Z', now), '3h ago');         // <1d
  assert.equal(ago('2026-07-06T12:00:00Z', now), '2d ago');         // ≥1d
});

test('ago: defaults to the real clock when now is omitted', () => {
  assert.equal(ago(new Date().toISOString()), 'just now');
});

const { comboMatch } = require(path.join(
  __dirname, '..', '..', 'internal', 'httpapi', 'templates', 'assets', 'helpers.js'));

test('comboMatch: case-insensitive substring; empty query matches all', () => {
  assert.equal(comboMatch('Desktop', 'desk'), true);
  assert.equal(comboMatch('desktop', 'TOP'), true);
  assert.equal(comboMatch('mobile', 'desk'), false);
  assert.equal(comboMatch('anything', ''), true);
  assert.equal(comboMatch('anything', null), true);
});
