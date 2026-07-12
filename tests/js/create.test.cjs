// Unit tests for collectCreatePayload (create-card payload collection) —
// the residual acceptance item from the P4 JS-harness card: the dirty
// packages that bit us are FormData-vs-JSON coercion, multi-value absent
// vs [], tags splitting, and required tracking. Run: node --test tests/js/
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');

const { collectCreatePayload } = require(path.join(
  __dirname, '..', '..', 'internal', 'httpapi', 'templates', 'assets', 'helpers.js'));

test('routes title/status/tags to the top level and field:* into fields', () => {
  const { req, missing } = collectCreatePayload('programming-task', [
    { name: 'title', value: '  Fix the thing  ' },
    { name: 'status', value: 'todo' },
    { name: 'tags', value: ' bug, , urgent ,' },
    { name: 'field:description', value: 'd' },
  ]);
  assert.equal(missing.length, 0);
  assert.equal(req.type_id, 'programming-task');
  assert.equal(req.title, 'Fix the thing'); // trimmed
  assert.equal(req.status, 'todo');
  assert.deepEqual(req.tags, ['bug', 'urgent']); // split, trimmed, empties dropped
  assert.deepEqual(req.fields, { description: 'd' });
});

test('number kind coerces to a JSON number (the FormData-vs-JSON bug class)', () => {
  const { req } = collectCreatePayload('t', [
    { name: 'field:estimate', kind: 'number', value: '3' },
  ]);
  assert.strictEqual(req.fields.estimate, 3); // number, not "3"
});

test('multi-value: empty selection is ABSENT from fields, never []', () => {
  const { req, missing } = collectCreatePayload('t', [
    { name: 'field:platforms', kind: 'multi-enum', values: [] },
  ]);
  assert.equal(missing.length, 0);
  assert.ok(!('platforms' in req.fields)); // unset contract: absent, not []
});

test('multi-value: selections pass through as an array; blanks filtered', () => {
  const { req } = collectCreatePayload('t', [
    { name: 'field:platforms', kind: 'multi-enum', values: ['desktop', '', 'mobile'] },
  ]);
  assert.deepEqual(req.fields.platforms, ['desktop', 'mobile']);
});

test('required tracking: empty scalar and empty multi both land in missing', () => {
  const { req, missing } = collectCreatePayload('t', [
    { name: 'title', value: '   ', required: true },
    { name: 'field:platforms', kind: 'multi-user', values: [], required: true },
    { name: 'field:branch', value: '', required: false },
  ]);
  assert.deepEqual(missing, ['title', 'field:platforms']);
  assert.ok(!('title' in req));
  assert.deepEqual(req.fields, {});
});

test('empty optional scalars stay absent (no empty-string fields on the wire)', () => {
  const { req } = collectCreatePayload('t', [
    { name: 'field:branch', value: '  ' },
    { name: 'tags', value: '' },
  ]);
  assert.deepEqual(req.fields, {});
  assert.ok(!('tags' in req));
});
