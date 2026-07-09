// helpers.js — pure functions shared by the cards UI (no DOM access, no
// globals read). Loaded before ui.js as a classic script; also loadable by
// Node's built-in test runner (tests/js/) via the module.exports guard at the
// bottom. Keep this file dependency-free and side-effect-free: anything here
// must be callable from a unit test with plain values.

// apiErrText extracts a human message from a structured core.Error JSON body
// ({message, field, ...}); falls back verbatim when the body isn't JSON.
function apiErrText(t, fallback) {
  try { var e = JSON.parse(t); return (e.message || fallback) + (e.field ? ' (' + e.field + ')' : ''); }
  catch (_) { return fallback; }
}

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

// Node test-runner hook (no-op in the browser).
if (typeof module !== 'undefined' && module.exports) {
  module.exports = { apiErrText: apiErrText, ago: ago, comboMatch: comboMatch };
}
