// components.js — Alpine components for the card surfaces (frontend-rebuild
// Phase 4: the leaf components — comments, repeating entries, artifact
// upload). Registered on alpine:init (Alpine loads deferred, so this classic
// script always runs first). Division of labor (DESIGN.md): the Go template
// renders all server data; these components own ephemeral local state only
// (open/closed, drafts, upload state). All mutations go through cardsAPI.send
// (api.js); server data re-enters via refreshCardSurface → swapHTML.

// refreshCardSurface re-renders the surface a component lives on after a
// successful mutation: inside the card modal → reload the modal fragment;
// on the full detail page → reload the page. (Pre-Alpine, these controls
// were only wired in the modal — the detail page rendered them dead; the
// context-aware refresh is what makes them work there too.)
function refreshCardSurface(cardID, el) {
  if (el && el.closest && el.closest('dialog')) return reloadModal(cardID);
  window.location.reload();
}

document.addEventListener('alpine:init', function () {

  // ---- Comments: composer (add) ----
  Alpine.data('commentComposer', function (cfg) {
    return {
      text: '', saving: false, status: '', isErr: false,
      get canSubmit() { return !!this.text.trim() && !this.saving; },
      say: function (msg, err) { this.status = msg || ''; this.isErr = !!err; },
      clear: function () { this.text = ''; this.say(''); },
      submit: function () {
        var body = this.text.trim();
        if (!body) { this.say('Write something first.', true); return; }
        this.saving = true; this.say('Saving…');
        var self = this;
        cardsAPI.send({ method: 'POST', url: '/v1/cards/' + cfg.cardId + '/comments', body: { body: body } })
          .then(function (res) {
            self.saving = false;
            if (res.ok) { toast('Comment added'); refreshCardSurface(cfg.cardId, self.$root); return; }
            self.say(res.message || 'Comment failed', true);
          });
      }
    };
  });

  // ---- Comments: per-row in-place edit ----
  Alpine.data('commentRow', function (cfg) {
    return {
      editing: false, draft: '', saving: false,
      start: function () {
        this.draft = this.$refs.body.textContent;
        this.editing = true;
        var self = this;
        this.$nextTick(function () { if (self.$refs.ta) self.$refs.ta.focus(); });
      },
      cancel: function () { this.editing = false; },
      save: function () {
        this.saving = true;
        var self = this;
        cardsAPI.send({ method: 'PATCH', url: '/v1/cards/' + cfg.cardId + '/comments/' + cfg.commentId, body: { body: this.draft } })
          .then(function (res) {
            self.saving = false;
            if (res.ok) { toast('Comment updated'); refreshCardSurface(cfg.cardId, self.$root); return; }
            toast(res.message || 'Edit failed', 'err');
            self.editing = false;
          });
      }
    };
  });

  // ---- Repeating entries: schema-driven add/edit/remove. The sub-form is
  // rendered server-side by field_control (entry ctx) from the SAME
  // item_fields definition the API validates; the component only manages
  // visibility, prefill, collection, and routing. ----
  Alpine.data('entryEditor', function (cfg) {
    return {
      open: false, editingID: null, status: '', isErr: false,
      say: function (msg, err) { this.status = msg || ''; this.isErr = !!err; },
      inputs: function () {
        return this.$root.querySelectorAll('[data-entry-input]');
      },
      openForm: function (prefill) {
        this.open = true; this.say('');
        var first = null;
        this.inputs().forEach(function (inp) {
          var k = inp.getAttribute('data-entry-input');
          var kind = inp.getAttribute('data-kind');
          var v = prefill ? (prefill[k] || '') : '';
          if (!prefill) { // sensible add-mode defaults from context
            if (kind === 'user') v = CARDS_ACTOR;
            if (kind === 'date') v = new Date().toISOString().slice(0, 10);
          } else if (kind === 'date' && v) {
            v = v.slice(0, 10); // RFC3339 → the date input's YYYY-MM-DD
          }
          inp.value = v;
          if (!first) first = inp;
        });
        if (first) { this.$nextTick(function () { first.focus(); }); }
      },
      openAdd: function () { this.editingID = null; this.openForm(null); },
      openEdit: function (ev) {
        var row = ev.target.closest('[data-entry-id]');
        this.editingID = row.getAttribute('data-entry-id');
        var prefill = {};
        row.querySelectorAll('[data-raw-key]').forEach(function (el) {
          prefill[el.getAttribute('data-raw-key')] = el.getAttribute('data-raw-val');
        });
        this.openForm(prefill);
      },
      close: function () { this.open = false; this.editingID = null; },
      save: function () {
        var entry = {}, missing = [];
        this.inputs().forEach(function (inp) {
          var k = inp.getAttribute('data-entry-input');
          var kind = inp.getAttribute('data-kind');
          var v = (inp.value || '').trim();
          if (!v) { if (inp.hasAttribute('data-required')) missing.push(k); return; }
          entry[k] = (kind === 'number') ? Number(v) : v;
        });
        if (missing.length) { this.say('Required: ' + missing.join(', '), true); return; }
        this.say('Saving…');
        var self = this;
        var url = '/v1/cards/' + cfg.cardId + '/fields/' + cfg.field + (this.editingID ? '/' + this.editingID : '/append');
        cardsAPI.send({ method: this.editingID ? 'PATCH' : 'POST', url: url, body: { entry: entry, version: cfg.version } })
          .then(function (res) {
            if (res.ok) { toast(self.editingID ? 'Entry updated' : 'Entry added'); refreshCardSurface(cfg.cardId, self.$root); return; }
            self.say(res.message || 'Save failed', true);
          });
      },
      remove: function (ev) {
        var id = ev.target.closest('[data-entry-id]').getAttribute('data-entry-id');
        if (!confirm('Remove this entry?')) return;
        var self = this;
        cardsAPI.send({ method: 'DELETE', url: '/v1/cards/' + cfg.cardId + '/fields/' + cfg.field + '/' + id + '?version=' + encodeURIComponent(cfg.version) })
          .then(function (res) {
            if (res.ok) { toast('Entry removed'); refreshCardSurface(cfg.cardId, self.$root); return; }
            self.say(res.message || 'Remove failed', true);
          });
      }
    };
  });

  // ---- Artifact upload: click-to-browse (primary, keyboard-reachable) +
  // zone-scoped drag-drop (never document/board — cannot collide with the
  // column-move gesture). States: idle / dragover / uploading / success /
  // error, driven by :data-state so the CSS is unchanged. ----
  Alpine.data('artifactZone', function (cfg) {
    return {
      state: 'idle', message: '', kind: '',
      say: function (msg, kind) { this.message = msg || ''; this.kind = kind || ''; },
      upload: function (file) {
        if (!file) return;
        // Client-side oversize pre-check: reject before spending a round-trip.
        if (cfg.maxBytes && file.size > cfg.maxBytes) {
          this.state = 'error';
          this.say('That file is ' + Math.ceil(file.size / 1048576) + ' MiB; the limit is '
            + Math.floor(cfg.maxBytes / 1048576) + ' MiB.', 'error');
          return;
        }
        this.state = 'uploading'; this.say('Uploading ' + file.name + '…');
        var q = (cfg.version && cfg.version !== 0) ? ('?version=' + encodeURIComponent(cfg.version)) : '';
        var self = this;
        cardsAPI.send({ method: 'POST', url: '/v1/cards/' + cfg.cardId + '/artifacts/' + cfg.field + q, body: file })
          .then(function (res) {
            if (res.ok) { self.state = 'success'; toast('Uploaded'); refreshCardSurface(cfg.cardId, self.$root); return; }
            self.state = 'error';
            if (res.stale) { self.say('This card changed since you opened it — reload it and upload again.', 'error'); return; }
            self.say(res.message || 'Upload failed', 'error');
          });
      },
      pick: function (ev) {
        if (ev.target.files && ev.target.files[0]) this.upload(ev.target.files[0]);
      },
      dragover: function (ev) {
        if (!dtHasFiles(ev)) return;
        ev.preventDefault(); ev.stopPropagation();
        ev.dataTransfer.dropEffect = 'copy';
        if (this.state !== 'uploading') this.state = 'dragover';
      },
      dragleave: function (ev) {
        if (ev.target === ev.currentTarget && this.state === 'dragover') this.state = 'idle';
      },
      drop: function (ev) {
        if (!dtHasFiles(ev)) return;
        ev.preventDefault(); ev.stopPropagation();
        var files = ev.dataTransfer.files;
        if (files.length > 1) { this.state = 'error'; this.say('Drop one file at a time.', 'error'); return; }
        this.upload(files[0]);
      }
    };
  });
});
