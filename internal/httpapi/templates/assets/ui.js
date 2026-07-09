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

  // ---- Modal ----
  var modal = document.getElementById('card-modal');
  function openModal(html) {
    swapHTML(modal, html);
    if (!modal.open) modal.showModal();
    wireDirtySave(modal);
    wireClickToEdit(modal);
    refreshAgo(); // convert injected <time data-ago> to relative text
  }
  // Comments, repeating entries, and artifact upload are Alpine components
  // (components.js) as of rebuild Phase 4; their fetches go through cardsAPI
  // (api.js). apiErrText + ago live in helpers.js (pure, unit-tested).
  // currentModalCardID reads the card the open modal is showing, from the
  // save-form's action (/ui/cards/{id}/save). Used to guard async refreshes
  // against the user having closed the modal or switched to another card.
  function currentModalCardID() {
    var form = modal.querySelector('form.save-form');
    if (!form) return null;
    var m = /\/ui\/cards\/([^/]+)\/save/.exec(form.getAttribute('action') || '');
    return m ? m[1] : null;
  }
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
        if (!modal.open || currentModalCardID() !== cardID) return; // superseded — leave it
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
      // Escape while editing a field reverts just that field; otherwise closes the modal.
      var active = document.activeElement;
      var openEdit = active && active.closest && active.closest('[data-edit]:not([hidden])');
      if (openEdit) {
        e.preventDefault();
        revertField(openEdit);
        return;
      }
      if (modal.open) { e.preventDefault(); closeModal(); }
      return;
    }
    // ←/→ navigates between cards on the board (test affordance). Only when
    // the modal is open AND no field/control is focused (so editing text,
    // arrowing through a <select>, or tabbing the board never hijacks the key).
    if (modal.open && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
      var a = document.activeElement;
      if (a && a.tagName === 'INPUT' || (a && (a.tagName === 'TEXTAREA' || a.tagName === 'SELECT' || a.isContentEditable))) return;
      var cur = currentModalCardID();
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


  // ---- Dirty-save: activate the Save button only when a field changes ----
  function wireDirtySave(root) {
    // Scoped to the edit modal's save-form: the CREATE modal is an Alpine
    // component (createModal in components.js) with its own primary button
    // and saving guard, and must not be dirty-gated here.
    var bar = root.querySelector('form.save-form .modal__footer button.btn--primary');
    if (!bar) return;
    bar.disabled = true;
    root.querySelectorAll('[data-field] .input, [data-field] .select, [data-field] .textarea').forEach(function(el){
      el.addEventListener('input', function(){ el.classList.add('is-dirty'); bar.disabled = false; });
      el.addEventListener('change', function(){ el.classList.add('is-dirty'); bar.disabled = false; });
    });
    bar.onclick = function(){
      var form = root.querySelector('form.save-form');
      if (!form) return;
      // Every field is a real <input>/<select>/<textarea> in the form
      // regardless of view/edit mode, so FormData(form) already carries the
      // current value of every field — dirty or not — with no extra work.
      var fd = new FormData(form);
      fd.append('version', form.getAttribute('data-version'));
      fetch(form.action, {method:'POST', body:fd, headers:{'X-Cards-Partial':'true'}})
        .then(function(r){ if (!r.ok) throw r; return r.text(); })
        .then(function(html){ openModal(html); toast('Saved'); })
        .catch(function(r){ r.text().then(function(t){
          var msg = 'Save failed'; try { msg = JSON.parse(t).message || msg; } catch(_){}
          toast(msg, 'err');
        }); });
    };
  }

  // ---- Click-to-edit: each [data-field] has a read-mode [data-view] and an
  // edit-mode [data-edit] (hidden by default). Clicking/focusing the view
  // swaps to the edit control; blurring without a change swaps back; a real
  // change marks the field dirty (wireDirtySave picks it up) and stays in
  // edit mode until Save/Cancel so the user can see what they changed. ----
  function wireClickToEdit(root) {
    root.querySelectorAll('[data-field]').forEach(function(field){
      var view = field.querySelector('[data-view]');
      var edit = field.querySelector('[data-edit]');
      if (!view || !edit) return; // read-only field (links/comments/metadata/repeating)
      var ctl = edit.querySelector('.input, .select, .textarea');
      view.addEventListener('click', function(){ activateEdit(field); });
      view.addEventListener('keydown', function(e){
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); activateEdit(field); }
      });
      if (!ctl) return;
      ctl.addEventListener('blur', function(){
        // Only revert if the field was never marked dirty — a dirty field
        // stays open so Save/Cancel remain meaningful and visible.
        setTimeout(function(){
          if (!ctl.classList.contains('is-dirty')) revertField(field);
        }, 0);
      });
    });
  }
  function activateEdit(field) {
    var view = field.querySelector('[data-view]');
    var edit = field.querySelector('[data-edit]');
    if (!view || !edit) return;
    view.hidden = true;
    edit.hidden = false;
    var ctl = edit.querySelector('.input, .select, .textarea');
    if (ctl) { ctl.focus(); if (ctl.select) ctl.select(); }
  }
  function revertField(field) {
    var view = field.querySelector('[data-view]');
    var edit = field.querySelector('[data-edit]');
    if (!view || !edit) return;
    edit.hidden = true;
    view.hidden = false;
  }

  // ---- Drag-and-drop status changes ----
  document.addEventListener('dragstart', function(e){
    var card = e.target.closest('.card');
    if (!card) return;
    e.dataTransfer.setData('text/plain', card.getAttribute('data-card-id'));
    e.dataTransfer.effectAllowed = 'move';
    card.classList.add('is-dragging');
  });
  document.addEventListener('dragend', function(e){
    var card = e.target.closest('.card');
    if (card) card.classList.remove('is-dragging');
  });
  document.addEventListener('dragover', function(e){
    var col = e.target.closest('.lane__body');
    if (!col) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    col.classList.add('is-drag-over');
  });
  document.addEventListener('dragleave', function(e){
    var col = e.target.closest('.lane__body');
    if (col && !col.contains(e.relatedTarget)) col.classList.remove('is-drag-over');
  });
  document.addEventListener('drop', function(e){
    var col = e.target.closest('.lane__body');
    if (!col) return;
    e.preventDefault();
    col.classList.remove('is-drag-over');
    var cardID = e.dataTransfer.getData('text/plain');
    var status = col.getAttribute('data-status');
    moveCard(cardID, status);
  });
  function moveCard(cardID, status) {
    fetch('/v1/cards/' + cardID, {headers:{'Accept':'application/json'}})
      .then(function(r){ return r.json(); })
      .then(function(c){
        // Try a normal move first. If the transition is illegal (enforced
        // board), retry with force=true after a confirm (force-move).
        return fetch('/v1/cards/' + cardID, {
          method:'PATCH',
          headers:{'Content-Type':'application/json','X-Work-Cards-Actor':CARDS_ACTOR,'Idempotency-Key':'move-'+cardID+'-to-'+status},
          body: JSON.stringify({version: c.version, status: status})
        }).then(function(r){
          if (r.status === 422) {
            // transition_illegal — offer force-move.
            return r.json().then(function(e){
              if (e.error === 'transition_illegal' && confirm('Move to "'+status+'" is not an allowed transition. Force-move anyway?')) {
                return fetch('/v1/cards/' + cardID, {headers:{'Accept':'application/json'}})
                  .then(function(r2){ return r2.json(); })
                  .then(function(c2){
                    return fetch('/v1/cards/' + cardID, {
                      method:'PATCH',
                      headers:{'Content-Type':'application/json','X-Work-Cards-Actor':CARDS_ACTOR,'Idempotency-Key':'force-'+cardID+'-to-'+status},
                      body: JSON.stringify({version: c2.version, status: status, force: true})
                    });
                  });
              }
              throw {message: e.message || 'Move failed'};
            });
          }
          if (!r.ok) throw r;
          return r;
        });
      })
      .then(function(r){ if(!r.ok) throw r; return r.json(); })
      .then(function(){ /* SSE will re-render the board */ })
      .catch(function(r){
        var msg = (r && r.message) || 'Move failed';
        if (r && r.text) { r.text().then(function(t){ var m='Move failed'; try{m=JSON.parse(t).message||m;}catch(_){} toast(m,'err'); }); return; }
        toast(msg,'err');
      });
  }
  // releaseCard clears a card's owner (unclaim). Called when clicking the
  // owner chip on a board card.
  function releaseCard(cardID, ev) {
    ev.stopPropagation(); ev.preventDefault();
    if (!confirm('Unclaim this card?')) return;
    fetch('/v1/cards/' + cardID, {headers:{'Accept':'application/json'}})
      .then(function(r){ return r.json(); })
      .then(function(c){
        return fetch('/v1/cards/' + cardID + '/release', {
          method:'POST',
          headers:{'Content-Type':'application/json','X-Work-Cards-Actor':CARDS_ACTOR,'Idempotency-Key':'release-'+cardID},
          body: JSON.stringify({version: c.version})
        });
      })
      .then(function(r){ if(!r.ok) throw r; return r.json(); })
      .then(function(){ /* SSE will re-render */ })
      .catch(function(r){ r.text().then(function(t){ var m='Release failed'; try{m=JSON.parse(t).message||m;}catch(_){} toast(m,'err'); }); });
  }

  // ---- relative time ----
  function refreshAgo(){ document.querySelectorAll('[data-ago]').forEach(function(el){ el.textContent = ago(el.getAttribute('data-ago')); }); }
  document.addEventListener('DOMContentLoaded', refreshAgo);

  // ---- Board live updates (moved from board.html's inline script) ----
  // Subscribe to the board's event stream; on a mutation event re-fetch the
  // board HTML and swap #board so the card lands in the right column.
  function wireBoardLive(boardId, types) {
    var board = document.getElementById('board');
    if (!board || typeof EventSource === 'undefined') return;
    var es = new EventSource('/v1/events/stream?board_id=' + encodeURIComponent(boardId) + '&types=' + types);
    types.split(',').forEach(function(t){ es.addEventListener(t, handle); });
    function handle() {
      fetch(window.location.href, {headers: {'X-Cards-Partial':'true'}})
        .then(function(r){ return r.text(); })
        .then(function(html){
          var doc = new DOMParser().parseFromString(html, 'text/html');
          var fresh = doc.querySelector('.board');
          if (fresh) swapHTML(board, fresh.innerHTML);
        })
        .catch(function(){});
    }
    es.onerror = function() { es.close(); };
  }

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
