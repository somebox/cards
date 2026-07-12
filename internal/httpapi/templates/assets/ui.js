// ui.js — the cards web UI's client behavior (extracted verbatim from
// layout.html in frontend-rebuild Phase 0; served embedded, cache-busted by
// ?v=<assetStamp>). Division of labor (DESIGN.md): Go templates render all
// server data and the first paint; this file adds interactivity only.
//
// swapHTML is the ONE seam through which server-rendered HTML enters the live
// DOM: it swaps innerHTML, boots Alpine components inside the fresh subtree
// (Alpine.initTree — never on document or a persistent root, which would
// double-bind), and re-renders relative times. A docaudit guard test pins
// that no other .innerHTML assignment exists in JS or templates.
function swapHTML(container, html) {
  container.innerHTML = html;
  if (window.Alpine && window.Alpine.initTree) window.Alpine.initTree(container);
  if (typeof refreshAgo === 'function') refreshAgo();
}

  // ---- Toasts ----
  function toast(msg, kind) {
    var c = document.getElementById('toasts');
    var t = document.createElement('div');
    t.className = 'toast' + (kind === 'err' ? ' toast--err' : '');
    var span = document.createElement('span'); span.textContent = msg;
    var btn = document.createElement('button'); btn.setAttribute('aria-label', 'dismiss'); btn.textContent = '×';
    btn.onclick = function(){ t.remove(); };
    t.appendChild(span); t.appendChild(btn);
    c.appendChild(t);
    setTimeout(function(){ if (t.parentNode) t.remove(); }, 6000);
  }

  // ---- Definitions reload failure banner (P3b --watch) ----
  // Shown on definition_reload_failed; cleared on definition_reloaded (or dismiss).
  // Last-good generation stays serving — this is feedback, not a hard stop.
  function showDefReloadBanner(message) {
    var el = document.getElementById('def-reload-banner');
    if (!el) return;
    el.replaceChildren();
    var msg = document.createElement('div');
    msg.className = 'def-reload-banner__msg';
    var strong = document.createElement('strong');
    strong.textContent = 'Definitions reload failed';
    msg.appendChild(strong);
    msg.appendChild(document.createTextNode(' — last-good definitions still serving. '));
    msg.appendChild(document.createTextNode(message || 'validation failed'));
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'def-reload-banner__dismiss';
    btn.setAttribute('aria-label', 'dismiss');
    btn.textContent = '×';
    btn.onclick = function () { clearDefReloadBanner(); };
    el.appendChild(msg);
    el.appendChild(btn);
    el.hidden = false;
  }
  function clearDefReloadBanner() {
    var el = document.getElementById('def-reload-banner');
    if (!el) return;
    el.hidden = true;
    el.replaceChildren();
  }

  // ---- Modal ----
  var modal = document.getElementById('card-modal');
  function openModal(html) {
    // swapHTML runs Alpine.initTree on the fresh subtree; every editable
    // field is an editField/editForm component, so click-to-edit, dirty-
    // gating, and save are wired declaratively — no wire*() fan-out.
    swapHTML(modal, html);
    // Label the dialog from the card title so a screen reader announces what
    // opened (a11y). The native dialog handles focus: showModal() moves focus
    // into the dialog, and close() restores it to the trigger (the card link).
    var titleEl = modal.querySelector('.modal__head-main .card-title__view, .modal__head-main, .card-title__view');
    if (titleEl) {
      if (!titleEl.id) titleEl.id = 'card-modal-title';
      modal.setAttribute('aria-labelledby', titleEl.id);
    } else {
      modal.removeAttribute('aria-labelledby');
    }
    if (!modal.open) modal.showModal();
    refreshAgo(); // convert injected <time data-ago> to relative text
  }
  // Card interactivity is now Alpine components (components.js): comments,
  // entries, artifacts (P4), combobox (P5), multiselect + tags (P6), create
  // forms (P7), and editField/editForm — the click-to-edit + dirty-gated
  // save that replaced the old wire fan-out (P8). Their fetches go through
  // cardsAPI; the open modal's identity lives in Alpine.store('card').
  // reloadModal re-fetches the modal after a NON-PATCH mutation (upload today;
  // the follow-on comments/entries sprint reuses this). The board grid is
  // SSE-live; the open modal is not, so it is refreshed explicitly. Defined
  // edge behavior: if the user closed the modal or switched cards mid-request
  // we do NOT clobber it, and a failed refetch surfaces an error and keeps the
  // (now-stale) modal rather than blanking it or pretending it is current.
  function reloadModal(cardID) {
    return fetch('/ui/cards/' + cardID + '/modal', {headers:{'X-Cards-Partial':'true'}})
      .then(function(r){ if (!r.ok) throw new Error(''+r.status); return r.text(); })
      .then(function(html){
        // Superseded-guard: only overwrite if the modal is still showing
        // the same card. $store.card.id is set by editForm.init() when the
        // fragment mounts — the seam we standardized on in P8.
        var curID = window.Alpine && Alpine.store('card').id;
        if (!modal.open || curID !== cardID) return;
        openModal(html);
      })
      .catch(function(){ toast('Saved, but refreshing the card failed — reopen it to see the change.', 'err'); });
  }

  // dtHasFiles: does a drag event carry files? (used by the artifactZone
  // component's zone-scoped drag handlers in components.js)
  function dtHasFiles(e){
    var t = e.dataTransfer && e.dataTransfer.types;
    return t && Array.prototype.indexOf.call(t, 'Files') !== -1;
  }
  function closeModal() { if (modal.open) modal.close(); swapHTML(modal, ''); }
  modal.addEventListener('click', function(e){ if (e.target === modal) closeModal(); });
  document.addEventListener('keydown', function(e){
    if (e.key === 'Escape') {
      // Escape inside an OPEN edit is handled by the editField component
      // (@keydown.escape.prevent.stop="revert()"); it stops propagation, so
      // if we see Escape here, no field was editing. Close the modal.
      if (modal.open) { e.preventDefault(); closeModal(); }
      return;
    }
    // ←/→ navigates between cards on the board (test affordance). Only when
    // the modal is open AND no field/control is focused (so editing text,
    // arrowing through a <select>, or tabbing the board never hijacks the key).
    if (modal.open && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
      var a = document.activeElement;
      if (a && a.tagName === 'INPUT' || (a && (a.tagName === 'TEXTAREA' || a.tagName === 'SELECT' || a.isContentEditable))) return;
      var cur = (window.Alpine && Alpine.store('card').id) || null;
      if (!cur) return;
      var cards = Array.prototype.slice.call(document.querySelectorAll('.card[data-card-id]'));
      if (!cards.length) return; // detail page has no siblings
      var idx = -1;
      for (var i = 0; i < cards.length; i++) { if (cards[i].getAttribute('data-card-id') === cur) { idx = i; break; } }
      if (idx === -1) return;
      var next = e.key === 'ArrowRight' ? idx + 1 : idx - 1;
      if (next < 0 || next >= cards.length) return;
      e.preventDefault();
      var link = cards[next].getAttribute('data-card-link');
      if (link) loadModal(link);
    }
  });
  // Copy-card-link button (labels header). Copies the full card id; falls back
  // to a prompt if the Clipboard API is unavailable (non-secure context).
  modal.addEventListener('click', function(e){
    var b = e.target.closest('[data-copy-id]');
    if (!b) return;
    e.preventDefault(); e.stopPropagation();
    var id = b.getAttribute('data-copy-id');
    var url = window.location.origin + '/ui/cards/' + id;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(url).then(function(){ toast('Copied link'); }, function(){ window.prompt('Copy link:', url); });
    } else { window.prompt('Copy link:', url); }
  });
  // Keyboard: a board card's title <a> is the keyboard affordance (Enter
  // activates it → the click handler below loads the modal). The card root is
  // intentionally not a role=button/tabindex so it doesn't nest interactive
  // descendants (the title link + the owner unclaim button).
  document.body.addEventListener('click', function(e){
    var a = e.target.closest('[data-card-link],[data-create-link]');
    if (a) {
      e.preventDefault();
      loadModal(a.getAttribute('data-card-link') || a.getAttribute('data-create-link'));
    }
  });
  function loadModal(url) {
    var modal = document.getElementById('card-modal');
    swapHTML(modal, '<div class="modal__loading"><div class="skel-line"></div><div class="skel-line"></div><div class="skel-line"></div></div>');
    if (!modal.open) modal.showModal();
    fetch(url, {headers:{'X-Cards-Partial':'true'}})
      .then(function(r){ if (!r.ok) throw new Error(''+r.status); return r.text(); })
      .then(function(html){ openModal(html); })
      .catch(function(){ swapHTML(modal, '<div class="modal__error">Failed to load card.</div>'); });
  }

  // New-card creation (UI sprint P2) is the createModal Alpine component
  // in components.js as of rebuild Phase 7; board-create is boardCreate.


  // Drag-drop (rebuild P9): reactive, owned by the boardPage Alpine
  // component in components.js. No document-level listeners remain.

  // ---- relative time ----
  function refreshAgo(){ document.querySelectorAll('[data-ago]').forEach(function(el){ el.textContent = ago(el.getAttribute('data-ago')); }); }
  document.addEventListener('DOMContentLoaded', refreshAgo);

  // ---- Board live updates (moved from board.html's inline script) ----
  // Subscribe to the board's event stream; on a mutation event re-fetch the
  // board HTML and swap #board so the card lands in the right column.

  // Deep-link creation (P2): /ui/cards/new 303s to the board with ?new=1
  // (&type&status); open the create modal once, then strip the params so a
  // refresh doesn't re-open it.
  function boardDeepLinkCreate(boardId) {
    var p = new URLSearchParams(window.location.search);
    if (p.get('new') !== '1') return;
    var q = ['board=' + encodeURIComponent(boardId)];
    if (p.get('type')) q.push('type=' + encodeURIComponent(p.get('type')));
    if (p.get('status')) q.push('status=' + encodeURIComponent(p.get('status')));
    loadModal('/ui/cards/new/modal?' + q.join('&'));
    p.delete('new'); p.delete('type'); p.delete('status');
    var rest = p.toString();
    history.replaceState(null, '', window.location.pathname + (rest ? '?' + rest : ''));
  }
