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
          // Selects (enum/user controls since P5): a historical value that is
          // no longer among the options (or a free-added user) must survive
          // prefill — synthesize its option rather than silently blanking.
          if (inp.tagName === 'SELECT' && v && !Array.prototype.some.call(inp.options, function (o) { return o.value === v; })) {
            var opt = document.createElement('option');
            opt.value = v; opt.textContent = v;
            inp.appendChild(opt);
          }
          inp.value = v;
          inp.dispatchEvent(new Event('change', { bubbles: true })); // combobox label resync
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

  // ---- Combobox (rebuild P5): filter-as-you-type enhancement over a native
  // single <select> for enum/user fields. The native select STAYS in the DOM
  // as the synced source of truth — forms, the create collector, and the
  // entry editor all read it; the component only mirrors picks into it (and
  // dispatches 'change' so dirty-tracking fires). Options are SERVER-RENDERED
  // <li> elements (division of labor: no x-for over server data); filtering
  // toggles their visibility via comboMatch. If Alpine never boots, ready
  // stays false and the native select remains the working control. ----
  Alpine.data('combobox', function () {
    return {
      ready: false, open: false, q: '', active: '', label: '', isEmpty: true,
      current: '', allowFree: false, native: null,
      init: function () {
        this.native = this.$root.querySelector('select');
        if (!this.native) return;
        this.allowFree = this.$root.getAttribute('data-free') === '1';
        var self = this;
        // Stay truthful when anyone sets the native value programmatically
        // (entry-editor prefill, Escape-revert, another component).
        this.native.addEventListener('change', function () { self.syncLabel(); });
        this.syncLabel();
        this.ready = true;
      },
      syncLabel: function () {
        var opt = this.native.selectedOptions && this.native.selectedOptions[0];
        var text = opt ? opt.textContent.trim() : '';
        this.current = this.native.value;
        this.isEmpty = !this.current;
        this.label = this.isEmpty ? '—' : text;
      },
      options: function () {
        return Array.prototype.slice.call(this.$root.querySelectorAll('.combobox__option[data-value]'));
      },
      visible: function () {
        var q = this.q, self = this;
        return this.options().filter(function (el) { return comboMatch(el.textContent, q) || el.getAttribute('data-value') === '' && !q; });
      },
      match: function (el) {
        if (el.getAttribute('data-value') === '') return !this.q; // "—" only when unfiltered
        return comboMatch(el.textContent, this.q);
      },
      // free-add (user fields): offer the typed text as a pickable value when
      // it matches no existing option exactly.
      freeVisible: function () {
        if (!this.allowFree || !this.q.trim()) return false;
        var q = this.q.trim().toLowerCase();
        return !this.options().some(function (el) { return el.textContent.trim().toLowerCase() === q; });
      },
      noneVisible: function () { return this.visible().length === 0 && !this.freeVisible(); },
      openMenu: function () {
        this.q = ''; this.active = this.native.value || ''; this.open = true;
        var self = this;
        this.$nextTick(function () {
          var f = self.$root.querySelector('.combobox__filter');
          if (f) f.focus();
        });
      },
      toggle: function () { this.open ? this.close() : this.openMenu(); },
      close: function () { this.open = false; },
      pickValue: function (v) {
        if (v !== '' && !Array.prototype.some.call(this.native.options, function (o) { return o.value === v; })) {
          var o = document.createElement('option');
          o.value = v; o.textContent = v;
          this.native.appendChild(o); // free-add: the native select must carry it to submit it
        }
        this.native.value = v;
        this.native.dispatchEvent(new Event('change', { bubbles: true }));
        this.syncLabel(); this.close();
      },
      pick: function (ev) { this.pickValue(ev.target.closest('[data-value]').getAttribute('data-value')); },
      pickFree: function () { if (this.freeVisible()) this.pickValue(this.q.trim()); },
      move: function (dir) {
        var vis = this.visible();
        if (!vis.length) return;
        var idx = -1, self = this;
        vis.forEach(function (el, i) { if (el.getAttribute('data-value') === self.active) idx = i; });
        var next = idx + dir;
        if (next < 0) next = vis.length - 1;
        if (next >= vis.length) next = 0;
        this.active = vis[next].getAttribute('data-value');
        vis[next].scrollIntoView({ block: 'nearest' });
      },
      enter: function () {
        var vis = this.visible(), self = this;
        var hit = vis.filter(function (el) { return el.getAttribute('data-value') === self.active; })[0] || vis[0];
        if (hit) { this.pickValue(hit.getAttribute('data-value')); return; }
        this.pickFree();
      }
    };
  });

  // ---- Multiselect (rebuild P6): chip control over a native
  // <select multiple> for multiple:true enum/user fields. Same architecture
  // as the combobox: the native select stays the submitted control and the
  // no-JS fallback; every option's chip AND menu row are SERVER-RENDERED,
  // and this component only toggles selection state + visibility. Explicit
  // clear-all works because the edit form carries a hidden "" sentinel input
  // (field_control), so an empty selection posts and unsets server-side. ----
  Alpine.data('multiSelect', function () {
    return {
      ready: false, open: false, q: '', sel: [],
      native: null,
      init: function () {
        this.native = this.$root.querySelector('select[multiple]');
        if (!this.native) return;
        this.syncFromNative();
        var self = this;
        this.native.addEventListener('change', function () { self.syncFromNative(); });
        this.ready = true;
      },
      syncFromNative: function () {
        this.sel = Array.prototype.map.call(this.native.selectedOptions, function (o) { return o.value; });
      },
      has: function (v) { return this.sel.indexOf(v) !== -1; },
      commit: function () {
        // sel → native option.selected, then a change event for dirty-tracking
        // (guard: change also triggers syncFromNative, which is a no-op here).
        var sel = this.sel;
        Array.prototype.forEach.call(this.native.options, function (o) { o.selected = sel.indexOf(o.value) !== -1; });
        this.native.dispatchEvent(new Event('change', { bubbles: true }));
      },
      add: function (v) {
        if (this.has(v)) return;
        this.sel = this.sel.concat([v]);
        this.commit(); this.q = '';
        this.markInvalid(null);
      },
      removeValue: function (v) {
        this.sel = this.sel.filter(function (x) { return x !== v; });
        this.commit();
      },
      backspace: function () {
        // Backspace in the empty filter removes the last chip.
        if (!this.q && this.sel.length) this.removeValue(this.sel[this.sel.length - 1]);
      },
      match: function (el) {
        var v = el.getAttribute('data-value');
        return !this.has(v) && comboMatch(el.textContent, this.q);
      },
      anyMenu: function () {
        var self = this;
        return Array.prototype.some.call(this.$root.querySelectorAll('.combobox__option[data-value]'), function (el) { return self.match(el); });
      },
      pick: function (ev) { this.add(ev.target.closest('[data-value]').getAttribute('data-value')); },
      enter: function () {
        // Enter picks the first visible menu option.
        var self = this;
        var first = Array.prototype.filter.call(this.$root.querySelectorAll('.combobox__option[data-value]'), function (el) { return self.match(el); })[0];
        if (first) this.add(first.getAttribute('data-value'));
      },
      openMenu: function () { this.open = true; },
      close: function () { this.open = false; },
      // per-chip structured-error mirror: fieldError (create path) calls this
      // with the offending value; any edit clears it.
      invalid: '',
      markInvalid: function (v) { this.invalid = v || ''; }
    };
  });

  // ---- Tag chips (rebuild P6): policy-aware, free-add-first tag editor over
  // the comma-joined input the server already expects (name="tags" stays the
  // submitted control + no-JS fallback). tag_set suggestions are
  // server-rendered menu rows; chips for the CURRENT value are the one
  // sanctioned x-for — they mirror the live edit state of the input the user
  // is typing into (ephemeral), never API data (first paint is the
  // server-rendered view cluster + input). Under 'propose' (default) Enter/
  // comma chips arbitrary text; other policies restrict to tag_set. ----
  Alpine.data('tagChips', function (cfg) {
    return {
      ready: false, open: false, q: '', values: [], invalid: '',
      input: null,
      init: function () {
        this.input = this.$root.querySelector('input[name="tags"]');
        if (!this.input) return;
        this.values = (this.input.value || '').split(',').map(function (s) { return s.trim(); }).filter(Boolean);
        this.ready = true;
      },
      commit: function () {
        this.input.value = this.values.join(', ');
        this.input.dispatchEvent(new Event('change', { bubbles: true }));
      },
      canFree: function () { return cfg.policy === 'propose' || cfg.policy === 'open'; },
      inSet: function (v) {
        return Array.prototype.some.call(this.$root.querySelectorAll('.combobox__option[data-value]'), function (el) {
          return el.getAttribute('data-value') === v;
        });
      },
      addTag: function (v) {
        v = (v || '').trim().replace(/,+$/, '');
        if (!v || this.values.indexOf(v) !== -1) { this.q = ''; return; }
        if (!this.canFree() && !this.inSet(v)) { this.invalid = v; return; } // locked policy: tag_set only
        this.values = this.values.concat([v]);
        this.invalid = '';
        this.commit(); this.q = '';
      },
      submit: function () { this.addTag(this.q); },
      removeTag: function (v) {
        this.values = this.values.filter(function (x) { return x !== v; });
        if (this.invalid === v) this.invalid = '';
        this.commit();
      },
      backspace: function () {
        if (!this.q && this.values.length) this.removeTag(this.values[this.values.length - 1]);
      },
      match: function (el) {
        var v = el.getAttribute('data-value');
        return this.values.indexOf(v) === -1 && comboMatch(el.textContent, this.q);
      },
      pick: function (ev) { this.addTag(ev.target.closest('[data-value]').getAttribute('data-value')); },
      markInvalid: function (v) { this.invalid = v || ''; }
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
