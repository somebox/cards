// helpers.js — pure functions shared by the cards UI (no DOM access, no
// globals read). Loaded before ui.js as a classic script; also loadable by
// Node's built-in test runner (tests/js/) via the module.exports guard at the
// bottom. Keep this file dependency-free and side-effect-free: anything here
// must be callable from a unit test with plain values.

// (Rebuild P10: apiErrText was subsumed by cardsAPI.parseErrorBody in api.js —
// deleted here now that every caller flows through cardsAPI.send. Legacy
// clients: reach for cardsAPI.parseErrorBody(text, fallback).)

// ago renders an RFC3339 timestamp as a compact relative time ("just now",
// "5m ago", "3h ago", "2d ago"). `now` is injectable for tests; defaults to
// the current time.
function ago(iso, now) {
  var then = new Date(iso), ref = now ? new Date(now) : new Date();
  var s = Math.floor((ref - then) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return Math.floor(s / 60) + 'm ago';
  if (s < 86400) return Math.floor(s / 3600) + 'h ago';
  return Math.floor(s / 86400) + 'd ago';
}

// comboMatch: does an option label match the typed filter? Case-insensitive
// substring — the combobox's one matching rule (rebuild P5). Empty query
// matches everything.
function comboMatch(label, q) {
  if (!q) return true;
  return String(label).toLowerCase().indexOf(String(q).toLowerCase()) !== -1;
}

// --- $store.live reconnect decisions (rebuild P9 / hardened P4) ---
// These three pure functions are the spine of the live EventSource's
// generation-guarded reconnect. Extracted here so the invariants are
// unit-tested with plain values (tests/js/live.test.cjs) — a server-side
// keepalive test cannot exercise client reconnect semantics.

// shouldDeliver gates an SSE event by generation. $store.live bumps a
// generation counter on every open() and stop(); each EventSource's listeners
// capture the generation they opened under, so a SUPERSEDED connection
// self-silences (the "stale ES delivers after replace" bug class). Deliver iff
// the event's generation is still the current one.
function shouldDeliver(gen, currentGen) {
  return gen === currentGen;
}

// nextBackoff is the reconnect schedule: double, capped at 8s. A falsy current
// resets to the 500ms base's next step.
function nextBackoff(current) {
  return Math.min((current || 500) * 2, 8000);
}

// maxEventId is the reconnect resume cursor — the largest numeric SSE id seen
// so far, so a reconnect resumes with ?since=<id>. Non-numeric or smaller ids
// leave the cursor unchanged.
function maxEventId(current, evId) {
  var n = parseInt(evId, 10);
  return (!isNaN(n) && n > current) ? n : current;
}

// Node test-runner hook (no-op in the browser).
if (typeof module !== 'undefined' && module.exports) {
  module.exports = {
    ago: ago, comboMatch: comboMatch,
    shouldDeliver: shouldDeliver, nextBackoff: nextBackoff, maxEventId: maxEventId,
  };
}
