// api.js — cardsAPI, the ONE fetch seam for card mutations from the web UI
// (frontend-rebuild Phase 4). Version-aware fetch + structured-error parsing +
// 409/413 special-casing + the actor header live here once; components never
// hand-roll fetch/response handling. Loaded before ui.js/components.js.
//
// send() NEVER rejects: every outcome resolves to a result object —
//   { ok:true,  status, data }                                   on 2xx
//   { ok:false, status, stale:true,  message: STALE_MSG }        on 409
//   { ok:false, status, tooLarge:true, message }                 on 413
//   { ok:false, status, message, field, validOptions }           other 4xx/5xx
//   { ok:false, status:0, network:true, message }                network error
// so call sites are a single .then() with no catch-ordering bugs.
var cardsAPI = (function () {
  var STALE_MSG = 'This card changed since you opened it — reload it and try again.';

  // parseErrorBody extracts {message, field, validOptions} from a structured
  // core.Error JSON body; non-JSON bodies fall back to the given message.
  function parseErrorBody(text, fallback) {
    var out = { message: fallback, field: '', value: '', validOptions: null };
    try {
      var e = JSON.parse(text);
      if (e && typeof e === 'object') {
        if (e.message) out.message = e.message;
        if (e.field) out.field = e.field;
        if (e.value !== undefined && e.value !== null) out.value = e.value; // per-chip mirror (P6)
        if (e.valid_options) out.validOptions = e.valid_options;
      }
    } catch (_) {}
    return out;
  }

  function send(opts) {
    var headers = {};
    for (var k in (opts.headers || {})) headers[k] = opts.headers[k];
    if (typeof CARDS_ACTOR !== 'undefined' && CARDS_ACTOR) headers['X-Work-Cards-Actor'] = CARDS_ACTOR;
    var body = opts.body;
    // Raw bodies (Blob/File/FormData) are passed through untouched — fetch
    // handles the Content-Type and multipart boundaries for FormData itself;
    // JSON-stringifying a FormData would produce '{}' and silently drop every
    // field (this bit editForm's save path in P8 until we caught it).
    var isBlobLike = (typeof Blob !== 'undefined' && body instanceof Blob) ||
                     (typeof File !== 'undefined' && body instanceof File);
    var isFormData = (typeof FormData !== 'undefined' && body instanceof FormData);
    if (body !== undefined && body !== null && !isBlobLike && !isFormData) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(body);
    } else if (isBlobLike && !headers['Content-Type']) {
      headers['Content-Type'] = 'application/octet-stream';
    }
    // FormData: DO NOT set Content-Type — fetch injects
    // "multipart/form-data; boundary=..." with the correct boundary.
    return fetch(opts.url, { method: opts.method || 'GET', headers: headers, body: body })
      .then(function (r) {
        return r.text().then(function (t) {
          if (r.ok) {
            var data = null;
            try { data = JSON.parse(t); } catch (_) {}
            // .body is the raw response text (HTML fragments, non-JSON
            // payloads); .data is the parsed JSON when available.
            return { ok: true, status: r.status, data: data, body: t };
          }
          if (r.status === 409) {
            return { ok: false, status: 409, stale: true, message: STALE_MSG, body: t };
          }
          if (r.status === 413) {
            return { ok: false, status: 413, tooLarge: true, message: 'The server rejected the file as too large.', body: t };
          }
          var e = parseErrorBody(t, 'Request failed');
          return { ok: false, status: r.status, message: e.message, field: e.field, value: e.value, validOptions: e.validOptions, body: t };
        });
      })
      .catch(function () {
        return { ok: false, status: 0, network: true, message: 'Request failed (network).' };
      });
  }

  return { send: send, parseErrorBody: parseErrorBody, STALE_MSG: STALE_MSG };
})();

// Node test-runner hook (no-op in the browser).
if (typeof module !== 'undefined' && module.exports) {
  module.exports = cardsAPI;
}
