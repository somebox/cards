// Unit tests for cardsAPI (internal/httpapi/templates/assets/api.js) — the one
// fetch seam for card mutations. Node's global fetch is stubbed per test; the
// contract under test is the result-object shape (send never rejects).
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');

const cardsAPI = require(path.join(
  __dirname, '..', '..', 'internal', 'httpapi', 'templates', 'assets', 'api.js'));

function stubFetch(status, body, capture) {
  global.fetch = (url, opts) => {
    if (capture) { capture.url = url; capture.opts = opts; }
    return Promise.resolve({ ok: status >= 200 && status < 300, status, text: () => Promise.resolve(body) });
  };
}

test('2xx: parses JSON data, ok:true', async () => {
  stubFetch(200, JSON.stringify({ id: 'card_1', version: 3 }));
  const res = await cardsAPI.send({ method: 'GET', url: '/v1/cards/card_1' });
  assert.equal(res.ok, true);
  assert.equal(res.data.version, 3);
});

test('409: stale:true with the canonical STALE_MSG', async () => {
  stubFetch(409, JSON.stringify({ error: 'version_conflict', message: 'stale' }));
  const res = await cardsAPI.send({ method: 'PATCH', url: '/x', body: { a: 1 } });
  assert.equal(res.ok, false);
  assert.equal(res.stale, true);
  assert.equal(res.message, cardsAPI.STALE_MSG);
});

test('413: tooLarge:true with the fixed message', async () => {
  stubFetch(413, '');
  const res = await cardsAPI.send({ method: 'POST', url: '/x' });
  assert.equal(res.tooLarge, true);
  assert.match(res.message, /too large/);
});

test('422: structured error surfaces message/field/valid_options', async () => {
  stubFetch(422, JSON.stringify({ error: 'unknown_enum', message: 'Unknown enum value', field: 'platforms', valid_options: ['a', 'b'] }));
  const res = await cardsAPI.send({ method: 'POST', url: '/x', body: {} });
  assert.equal(res.ok, false);
  assert.equal(res.field, 'platforms');
  assert.deepEqual(res.validOptions, ['a', 'b']);
  assert.equal(res.message, 'Unknown enum value');
});

test('network failure resolves (never rejects) with network:true', async () => {
  global.fetch = () => Promise.reject(new Error('boom'));
  const res = await cardsAPI.send({ method: 'GET', url: '/x' });
  assert.equal(res.ok, false);
  assert.equal(res.network, true);
});

test('JSON bodies get Content-Type + stringified; actor header injected', async () => {
  global.CARDS_ACTOR = 'jeremy';
  const cap = {};
  stubFetch(200, '{}', cap);
  await cardsAPI.send({ method: 'POST', url: '/x', body: { a: 1 } });
  assert.equal(cap.opts.headers['Content-Type'], 'application/json');
  assert.equal(cap.opts.headers['X-Work-Cards-Actor'], 'jeremy');
  assert.equal(cap.opts.body, '{"a":1}');
  delete global.CARDS_ACTOR;
});

test('parseErrorBody: non-JSON falls back verbatim', () => {
  const e = cardsAPI.parseErrorBody('<html>502</html>', 'Upload failed');
  assert.equal(e.message, 'Upload failed');
});
