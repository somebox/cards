// Unit tests for the pure UI helpers (internal/httpapi/templates/assets/helpers.js),
// run by Node's built-in test runner: `node --test tests/js/`. No dependencies.
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');

const { apiErrText, ago } = require(path.join(
  __dirname, '..', '..', 'internal', 'httpapi', 'templates', 'assets', 'helpers.js'));

test('apiErrText: structured core.Error → message (field)', () => {
  const body = JSON.stringify({ error: 'validation_failed', message: 'Unknown enum value', field: 'priority' });
  assert.equal(apiErrText(body, 'Save failed'), 'Unknown enum value (priority)');
});

test('apiErrText: message without field has no suffix', () => {
  assert.equal(apiErrText(JSON.stringify({ message: 'Nope' }), 'x'), 'Nope');
});

test('apiErrText: non-JSON body falls back verbatim', () => {
  assert.equal(apiErrText('<html>502</html>', 'Save failed'), 'Save failed');
});

test('apiErrText: JSON without message uses fallback but keeps field', () => {
  assert.equal(apiErrText(JSON.stringify({ field: 'tags' }), 'Save failed'), 'Save failed (tags)');
});

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
