# Cards UI 2.0 — Unified Incremental Frontend Rebuild Plan

_Planned 2026-07-08 on branch `frontend-rebuild` via a 5-aspect investigate→plan→review→synthesize workflow (14 agents; 23 review must-address items incorporated)._

## Summary

A single phased plan that folds four per-aspect plans into one timeline with no duplicated foundations. The core insight from the reviews: the four plans each re-invented Phase 0 (Alpine bootstrap + reinit seam + htmx removal) and each re-decided the multi-value core change with conflicting sequencing. This plan collapses those into ONE shared foundation phase and ONE standalone, dependency-first core PR that all UI work consumes.

The spine is the DIVISION OF LABOR rule (Go renders all server data + first paint; Alpine enhances only ephemeral local state; never x-for over server JSON) enforced mechanically by a docaudit guard test landed in Phase 0, not by discipline.

Sequencing rationale confirmed against code: (1) No CSP header exists today, so default Alpine works, but self-host+embed Alpine (not CDN) to match single-binary/boring-tech and avoid future CSP surprise. (2) HX-Request is load-bearing in exactly render.go:700 + 4 fetch sites — dropping htmx must preserve the header (renamed atomically), and the two htmx toast listeners + refreshAgo-on-afterSettle are the ONLY paths for error toasts and post-swap time refresh, so they must be rehomed BEFORE the tag is deleted. (3) The SSE loop (sse.go) is a clean single-writer for/select with two cases; keepalive is a safe third case. (4) Query filters compile to scalar json_extract; multi-value enum filtering silently returns empty unless a json_each membership branch (which already exists for tags) is added — so the query operator is PART of the core multi-value PR, not a later nicety. (5) getFormIfPresent already gives present/absent discipline (used for tags); the multi save-parse must reuse it, not raw r.Form[k].

Reversibility: split "add Alpine component" and "delete wire*" into separate commits for the two highest-coupling conversions so a regression is a revert of one commit, not a restore of deleted code.

## Sequencing rationale

The reviews' three loudest signals drove the ordering. First, all four aspect plans independently re-invented the SAME Phase 0 (Alpine bootstrap + reinit seam + htmx removal) with CONFLICTING htmx-removal timing — one plan dropped htmx in its Phase 0 while its own error-toast listeners weren't rehomed until Phase 5, which (verified) would silently kill the only error-toast path. So Phase 0 here is ONE shared foundation owned by foundations, and htmx removal is split into its own Phase 2 gated on a docaudit test proving a non-htmx toast path exists first.

Second, the multi-value core change was 'recommended' by three plans and sequenced into three different phases with no single owner — risking it being authored twice or landing UI-first against a nonexistent shape. It is now ONE standalone dependency-first PR (Phase 3) that lands green and tested BEFORE any multiselect UI (Phase 6). I confirmed in code why this must be atomic: query filters compile to scalar json_extract (sqlite/filter.go:104) so multi-value enum filtering silently returns empty — the json_each membership operator is therefore PART of the core PR, not a later nicety; and mcp fieldSchema maps enum/user to scalar, so the agent contract forks without an array-schema branch in the same PR.

Third, dependency chains within the UI: field_control consolidation (Phase 1) must precede every field component because they all render through it. The combobox (Phase 5) precedes the multiselect (Phase 6) because multiselect is built on the combobox foundation, and Phase 6 also depends on the Phase-3 data shape. Leaf components (Phase 4) go before the coupled edit-modal pair (Phase 8) to prove the initTree seam on low-risk surfaces first. The seam is only flipped to pure-initTree (deleting openModal's manual fan-out) in Phase 8, after all seven wire*() are converted — so the app stays green at every step. The board rebuild (Phase 9) is late because it depends on the modal work (Phase 8) and the foundation, and its internal ordering is itself critical: the reviews flagged that a persistent auto-reconnecting EventSource combined with per-filter full navigation would COMPOUND the very socket-exhaustion bug being fixed, so the Alpine-intercepted filters + board_fragment partial land together with (or before) the persistent connection, never staggered. Keepalive is a discrete tested server change gating the reconnect work because the SSE loop is a single-writer select (verified sse.go) that a second goroutine would corrupt.

Reversibility runs throughout: each wire*() conversion deletes its function, but for the two highest-coupling conversions (edit modal, board drag) add-component and delete-wire are split into separate commits behind a one-line switch, so a regression is a revert not a code restore. And because no automated test executes Alpine (httptest is first-paint-only — verified: no browser test in the suite, no test asserts the literal 'hidden' attribute), every component phase's exit criteria state that 'go test green' is necessary but NOT sufficient — a preview/manual behavior check is mandatory.

## Phase 0 — Shared foundation: Alpine in, reinit seam, guard tests (no behavior change)

**Goal.** Alpine is present and self-hosted, every innerHTML-swap site funnels through ONE swapHTML(container,html)=innerHTML+Alpine.initTree(container)+refreshAgo() seam, and the division-of-labor rule is enforced by a test. Zero components converted; app is behavior-identical. This is the ONE Phase 0 for all four aspects — executed once, owned by foundations.

**Steps:**
1. Vendor a pinned Alpine build as a static asset in internal/httpapi/templates/, add it to the //go:embed glob (server.go:24-25), and serve it at /ui/alpine.js?v={{assetStamp}} mirroring the style.css handler. Self-host, NOT CDN (matches single-binary/boring-tech; sidesteps future CSP-without-unsafe-eval being a network surprise).
2. Add <script defer src="/ui/alpine.js?v={{assetStamp}}"></script> to layout.html <head>. Keep the htmx <script> tag for now (removed in Phase 2 after listeners are rehomed).
3. Introduce swapHTML(container,html) in layout.html and route ALL current innerHTML-swap sites through it: openModal (layout.html:61-72), reloadModal (layout.html:266-274), and the board SSE handler (board.html:69). Initially swapHTML also runs the 7 wire*() calls so behavior is byte-identical.
4. Define the reinit invariant ONCE (in the ADR): only ever call Alpine.initTree on the FRESHLY-INSERTED subtree, never a persistent x-data root, never document; rely on Alpine teardown-on-removal for old nodes. swapHTML is the single seam.
5. Add Alpine.store('modal')={cardID,version,open} seeded from data-card-id/data-version attributes added to the modal fragment root (server-rendered). Leave wire*() reading the old modalVersion() for now.
6. Land docaudit guard tests WITH this phase: (a) Alpine script tag present; (b) no x-for bound to a server-injected JSON variable (grep guard — enforces division-of-labor mechanically); (c) initTree/swapHTML referenced at each swap site and no raw innerHTML swap bypasses swapHTML; (d) a non-htmx error-toast path exists (pre-req gate for Phase 2 htmx removal).
7. Write the ADR (docs/architecture/ or docs/NOTES.md): Alpine adopted + self-hosted; division-of-labor rule; Alpine.data()-only / no-inline-eval convention; note default build depends on 'unsafe-eval' and the @alpinejs/csp build is the escape hatch if a CSP is ever added; note the pre-existing remote Google Fonts link (layout.html:9) as known-out-of-scope; record the Pinemix license check result (HARD GATE — must confirm license permits harvesting x-data before any Pinemix-derived component is authored).

**Demo.** Open a card modal, edit a scalar field, trigger a board SSE refresh — all still work exactly as before, but the browser console shows Alpine loaded and every DOM swap now flows through swapHTML. Show the docaudit test failing if a bogus x-for over a JSON var is added.

**Exit criteria:**
- go build ./cmd/cards && go test ./... && go vet ./... green.
- window.Alpine defined; Store('modal').cardID/version populated when a modal is open; opening a modal and firing an SSE event both route through swapHTML with identical behavior to before.
- New docaudit guards pass (Alpine present, no x-for-over-JSON, swapHTML is the sole swap seam, non-htmx toast path exists).
- ADR merged with Pinemix license confirmed compatible.

## Phase 1 — Consolidate field rendering into one field_control partial (no Alpine behavior change)

**Goal.** Collapse the THREE duplicated {{if eq .Def.Type}} switches (card_create.html, card_modal.html scalar edit, card_modal.html repeating item sub-form) into ONE Go partial. Behavior-identical per surface. This is the prerequisite seam for every later field component.

**Depends on:** Phase 0

**Steps:**
1. Create internal/httpapi/templates/field_control.html with {{define "field_control"}} covering all 10 types, parameterized by CONTEXT that selects the SUBMISSION CONTRACT (not just style): create emits data-create-input for the JSON POST; edit emits name="field:<id>" for the /ui/cards/{id}/save form POST; entry emits data-entry-input read by the append/patch JS. All three emit data-kind.
2. Add a fieldCtx/fieldControlData helper in render.go packaging Def+value+Users+Display+naming-prefix so all call-sites pass a uniform dot.
3. Replace the three switches with {{template "field_control"}} includes. Do NOT normalize the user-field inconsistency yet (that changes rendered bytes and the entry collector — deferred to Phase 3b).
4. Capture THREE separate golden snapshots (create, edit, entry) and diff each against its own pre-refactor baseline. Exit criterion is byte-equivalence WITHIN each context, not across contexts (the contexts are legitimately divergent contracts).
5. Add a docaudit assertion that the schema→control switch exists in exactly one place (grep finds no second/third switch).

**Demo.** Show create form, modal inline-edit, and repeating-entry all rendering from the single field_control.html. Show git diff proving the three old switch blocks are gone and replaced by one partial + three includes.

**Exit criteria:**
- One switch in the codebase; grep for the second/third switch returns nothing; docaudit single-switch assertion passes.
- Each of create / modal-edit / entry renders byte-equivalent controls vs its OWN baseline; go test ./internal/httpapi green (template parse + modal_editing + card_create tests unchanged).
- No visual/behavior change in default+journal+labels+dark.

## Phase 2 — Clean htmx removal + header decouple

**Goal.** Excise the unused htmx library and decouple partial rendering from its header name, leaving no dead dependency and no silent regression. Gated on the Phase-0 docaudit guard proving a non-htmx toast path exists.

**Depends on:** Phase 0

**Steps:**
1. Rehome the two htmx event listeners (htmx:responseError / htmx:afterRequest, layout.html:46-57 — the ONLY error-toast path for those fetches) into a plain 'app:error' custom-event listener + direct toast() calls.
2. Move refreshAgo off htmx:afterSettle (layout.html:717 — the ONLY post-swap time refresh) into swapHTML (already done structurally in Phase 0) + DOMContentLoaded.
3. Rename the HX-Request header to a neutral X-Cards-Partial in ONE atomic commit touching render.go:700 (wantsPartial) + all 4 fetch sites (layout.html:267, 412, 564; board.html:63). Keep accepting HX-Request for one release only if any non-UI consumer is found (grep confirmed none today: only render.go:700 + those 4 sites reference it).
4. Delete the htmx <script> tag (layout.html:10); update the server.go:3 'htmx UI' comment and the board.html:52 comment.
5. Add a docaudit test asserting no 'htmx' or 'hx-' string remains in templates or Go (grep clean).

**Demo.** Force a 4xx (e.g. stale-version save) and show the error toast still appears with htmx fully removed. Show network tab: modal fetch carries X-Cards-Partial and returns a fragment.

**Exit criteria:**
- grep for 'htmx' / 'HX-Request' across templates and Go returns nothing; docaudit htmx-absent guard passes.
- Partial-vs-full-page rendering still correct (modal loads a fragment via X-Cards-Partial; direct GET loads full page).
- Request-failure toasts still fire on a 4xx (verified in preview — go test green is necessary but NOT sufficient here).
- go test ./... && go vet ./... green; page weight drops by the htmx bundle.

## Phase 3 — Standalone multi-value core PR (dependency-first, ONE owner, tested before any multi UI)

**Goal.** Land FieldDef.Multiple as ONE self-contained, cross-transport, green PR so multiselect has a real data shape everywhere BEFORE any UI component is built. No aspect re-decides this; all four consume it. Resolves the reviews' top blocker (three plans independently owning the same core change).

**Depends on:** Phase 1

**Steps:**
1. Add `Multiple bool json:"multiple,omitempty"` to FieldDef (internal/core/types.go). Scope to enum + user only for v1; card_link multiple is an explicit fast-follow, documented, not shipped now.
2. RATIFY THE UNSET CONTRACT as a normative line in SPEC-DATA-MODEL and enforce it in ONE place in core (validate.go normalization): an unset optional multiple field is ABSENT (key not present), never null and never []. A present field is always a JSON array. Every transport inherits this.
3. Branch validate.go: enum/user accept []string when Multiple (each element in Options / existence-checked), emitting the standard structured {field,value,allowed} error per bad element.
4. Save-parse (ui.go:563): reuse the SAME present-vs-absent discipline as tags via getFormIfPresent — absent field → don't set req.Fields[fid]; present + Multiple → serialize r.Form[k] slice to a JSON array; single-value path unchanged bit-for-bit (gate on def.Multiple). Add the create JSON path array variant.
5. render.go fieldViews/renderValue: serialize/parse []any values for view + prefill.
6. QUERY DSL membership is PART of this PR (confirmed: filters compile to scalar json_extract at sqlite/filter.go:104, which silently never matches an array). Add a json_each membership branch for multiple=true fields, mirroring the existing tags json_each pattern (sqlite/filter.go:156-168), plus a filter_test covering it. Until it lands, multi-value fields are documented as non-filterable — never ship a filter that silently returns empty.
7. MCP schema (mcp.go fieldSchema): when f.Multiple, emit {type:'array',items:{type:'string',enum:Options}} for enum and {type:'array',items:{type:'string'}} for user; add an mcp test asserting the array schema.
8. CLI patch array handling; backlog.jsonl round-trip; update SPEC-DATA-MODEL + INTEGRATOR-REFERENCE; add ONE demo-workspace field with multiple:true so export/import + MCP introspection are exercised by the dogfooding backlog.
9. Tests: go test ./... -race; import/export round-trip for a multiple field; a regression test proving single-value enum/user is unchanged.

**Demo.** CLI: create a card with a multi-value enum, patch it to add/remove values, filter the board by membership (returns the right cards), export to backlog.jsonl and re-import into a fresh workspace — value survives. MCP introspection shows the array schema.

**Exit criteria:**
- A definition with "type":"enum","multiple":true validates arrays, rejects out-of-set values with structured valid_options, round-trips through export/import, is filterable via json_each membership, and is readable via CLI + MCP (array schema).
- Single-value enum/user behavior unchanged (regression tests green).
- The unset field is ABSENT on the wire across create JSON, modal save, export — one contract, tested.
- go test ./... -race green. NO UI multi-value component exists yet.

## Phase 4 — Migrate self-contained leaf components (artifact, comments, entries)

**Goal.** Prove the Alpine pattern end-to-end on the lowest-coupling components, retiring their wire*() functions. Each conversion routes fetches through a shared window.cardsAPI.send() helper (version-aware fetch + structured-error parse + 409/413 + actor header + STALE_MSG in one place).

**Depends on:** Phase 0, Phase 1

**Steps:**
1. Add window.cardsAPI.send({method,url,body,version,headers}) in the Phase-0 bootstrap: injects actor header + Content-Type, parses !ok bodies to {status,message,field,valid_options}, special-cases 409→{stale:true} and 413. STALE_MSG lives here once.
2. artifactZone Alpine.data on [data-artifact-upload]: {state,message}; [data-state] becomes :data-state (CSS unchanged); scoped @dragover/@drop (never document); size pre-check + octet-stream POST ?version via cardsAPI; 409/413 messages preserved. Delete wireArtifactUpload.
3. commentComposer on [data-comments] + per-row commentEdit on [data-comment-id]: x-model text, :disabled=!text.trim()||saving, Ctrl/Meta+Enter submit, Escape clear; inline edit toggles server body vs x-model textarea (no createElement). Success → reloadModal(store.cardID). Delete wireComments.
4. entryEditor on [data-entry-field]: values{} x-model on dynamic keys, add-mode defaults (user→CARDS_ACTOR, date→today), edit-prefill from [data-raw-key]/[data-raw-val], number coercion preserved, POST vs PATCH by editingID, {entry,version:store.version}, 409→stale, DELETE ?version with confirm(). Reuses field_control for sub-fields. Delete wireEntryEditors.
5. Verify x-model round-trips match the old collectors exactly incl. number coercion (kind==='number'→Number) and tags comma-split; a silent type regression sends wrong types to the API.

**Demo.** In a freshly SSE-reloaded modal: upload an artifact via drag-drop, add and edit a comment with Ctrl+Enter, add/edit/remove a repeating entry — all working with correct versions, all wire*() for these gone.

**Exit criteria:**
- Drag-drop + click upload work; [data-state] transitions + 409/413/oversize messages identical.
- Add/edit comment and add/edit/remove entry work with correct types and versions; inline errors show; defaults preserved.
- wireArtifactUpload, wireComments, wireEntryEditors removed; components reinit correctly after a modal swap (proves initTree seam on real components).
- go test ./internal/httpapi green AND a preview behavior check passes (httptest is first-paint-only and cannot catch Alpine regressions — the manual/preview check is mandatory, not optional).

## Phase 5 — Unified combobox (single-select enum + user) + normalize user control

**Goal.** Replace the 3 divergent user controls and naive selects with ONE filter-as-you-type combobox that progressively enhances the native <select> fallback. Locked decision: combobox ALWAYS for enum + user (one control shape everywhere, one WYSIWYG surface to verify), native <select> retained purely as the no-JS base the combobox overlays on boot.

**Depends on:** Phase 1, Phase 4

**Steps:**
1. Author token-only .combobox/.combobox__menu/.combobox__option in style.css reading ONLY neutral + role tokens (--c-surface, --c-border-2, --sh-md, --r-md, --role-body-*), NEVER per-type hue tokens (--type-* is overridden by board inline styles and would render un-dark-adapted inside a board). Add the new hooks to DESIGN.md stable-hook list.
2. Implement the combobox Alpine component (harvest Pinemix x-data ONLY — open/close, roving-tabindex arrow nav, type-ahead filter, click-outside; discard all Tailwind; re-skin with our classes). Enhance the native <select> field_control renders; hide native ONLY after Alpine confirms init (fallback stays functional if Alpine fails to boot).
3. field_control (3b): normalize the user field to the combobox in ALL contexts (create/edit/entry) AND update entryEditor's value collection to match the new control type — this changes rendered bytes deliberately, so it is a separate step from the Phase-1 byte-equivalent extraction. Preserve free-add for user IF the field is existence-checked-but-open (confirm with owner — see open decisions); otherwise seed from $.Users.
4. WYSIWYG gate: field__view and field__edit render at identical box geometry (width/height/padding/line-height/font), no layout shift on toggle. The combobox popover must not shift the field box. Verify in default+journal+labels+dark.
5. Add a themecss round-trip test styling .combobox__menu per theme; token-only lint (no raw hex / literal font-size in new blocks); accessibility exit (keyboard nav, ARIA listbox).

**Demo.** Same enum/user field renders one combobox across create + modal + entry, with keyboard typeahead; toggle JS off and show the native select still saves. Switch through default/journal/labels/dark showing no geometry shift on click-to-edit.

**Exit criteria:**
- User field is the SAME combobox in create, modal, and entry; enum uses it too. Disabling JS yields a working native <select> that saves.
- Keyboard nav + typeahead work; WYSIWYG parity holds (no layout shift on edit) in all 4 themes; combobox reads only neutral/role tokens.
- New .combobox* hooks in DESIGN.md; token-only lint + themecss round-trip green; go test ./internal/httpapi green + preview a11y check passes.

## Phase 6 — Token multiselect UI (multi enum/user) + policy-aware tag chips

**Goal.** Real chip multiselect built on the Phase-3 data shape and the combobox foundation, plus upgrade tags from comma-text to policy-aware chips that preserve today's free-add UX.

**Depends on:** Phase 3, Phase 5

**Steps:**
1. Add token-only .multiselect/.multiselect__chips (each chip is .chip); view renders a wrapping .chip cluster; empty state honors the existing is-empty faint 'Not set / Add…' voice (style.css:613) not a bare box. Add hooks to DESIGN.md.
2. tokenSelect/wcMultiSelect Alpine component (harvest Pinemix multi behavior: add/remove chip, filter, keyboard incl. Backspace-to-remove, roving between chips, listbox role). field_control branches on .Def.Multiple → multiselect (repeated hidden name= inputs) vs single combobox.
3. Tags chip control is POLICY-AWARE and FREE-ADD-FIRST (do NOT regress today's free-text+datalist at card_modal.html:66): datalist/dropdown offers tag_set SUGGESTIONS, but Enter/comma always creates a chip from arbitrary typed text when TagPolicy allows (default is 'propose' per config.go:115). For non-propose policies offer only tag_set values. Commits the comma-joined value the server already expects.
4. Mirror structured errors: per-value {field,value,allowed} → per-chip .is-invalid (not whole-field); a rejected proposed tag shows on that specific chip.
5. WYSIWYG gate for the chip cluster: the read-only VIEW cluster and the editable chip INPUT share chip sizing tokens + container padding — no height/padding/line-height shift on toggle. Verify in all 4 themes.
6. No-JS: native <select multiple> for enum/user multiple (verify it round-trips through the r.Form[k] slice save path end-to-end in a no-JS test); comma-text for tags. Accept native multi-select ONLY as inline-edit fallback (create is JS-built — see open decisions).

**Demo.** Add several values to a multi-enum field as chips, remove one via Backspace, save — board card shows a wrapping chip cluster. Type a brand-new tag and show it becomes a chip (propose). Toggle JS off and show native multi-select still saves.

**Exit criteria:**
- tags is a chip control that still free-adds under 'propose'; a multiple:true enum/user field selects as chips, saves an array, and no-JS falls back to native multi-select/comma-text that actually round-trips.
- Per-chip structured-error mirroring works; chip cluster empty state matches the is-empty voice; WYSIWYG parity holds in all 4 themes.
- go test ./internal/httpapi green (incl. no-JS multiple round-trip test) + preview behavior/a11y check; board stays shippable/green.

## Phase 7 — Convert create + board-create forms (structured-error mirror + idempotency)

**Goal.** Port the two forms into Alpine without changing the structured-error contract or idempotency behavior. Highest-value form path; keeps the same field id → input name mapping.

**Depends on:** Phase 4, Phase 5

**Steps:**
1. createForm Alpine.data on [data-create-modal]: {fields{},errors{},alert,saving,idemKey}; idemKey minted once in x-init (replaces form.dataset.idemKey); type-picker keeps loadModal(?type=); submit builds the request with the SAME title/status/tags/field:<id> mapping + number/tags coercion, POST with Idempotency-Key header via cardsAPI; structured error → errors[name] + :class={'is-invalid':errors[name]} + [data-error-for] x-text + valid_options hint; required check preserved.
2. boardForm Alpine.data on [data-board-create]: checkbox-array x-model for columns/types, wipColumn/wipLimit, validate, POST /v1/boards, redirect to /ui/boards/{id}.
3. Verify the title/status/tags/field: mapping and enum valid_options against a real invalid submit; double-click doesn't double-create (idempotency).
4. Delete wireCreateModal + wireBoardCreate.

**Demo.** Submit a create form with a bad enum value → field-scoped error with the allowed list; double-click submit → exactly one card created. Create a board with checkbox column/type selection → redirect to the new board.

**Exit criteria:**
- Bad enum → field-scoped error + valid list; double-click doesn't double-create; board create redirects.
- Per-field error attributes ([data-error-for], .is-invalid) present in rendered DOM; go test ./internal/httpapi green + preview check.
- wireCreateModal + wireBoardCreate removed.

## Phase 8 — Convert edit modal (dirty-save + click-to-edit) and flip the seam to pure initTree

**Goal.** Port the most coupled pair, then make Alpine.initTree the SOLE reinit path and delete openModal's manual 7-function fan-out. Reversibility: keep the old wire path behind a one-line switch for this commit; split add-component from delete-wire.

**Depends on:** Phase 4, Phase 5, Phase 7

**Steps:**
1. editField Alpine.data per [data-field]: {editing,dirty}; view x-show=!editing, edit x-show=editing; @click/@keydown.enter.space activates; control @input sets dirty + $dispatch('field-dirty'); clean @blur (setTimeout 0) reverts only if !dirty. Preserve WYSIWYG (x-show toggles the visible control, geometry is CSS-identical). Keep the server-rendered initial visibility state so the (grep-confirmed absent) hidden-attr assumptions and any presence-based tests hold.
2. editForm Alpine.data on form.save-form: {dirty,saving} via the $dispatch bubble (replaces the shared class scan); Save :disabled=!dirty||saving; Save still uses FormData(form) (NOT an Alpine state object) so ALL fields submit even when only one was edited; append version read from store.version AT SUBMIT TIME (not an init closure); swap modal via swapHTML.
3. Rewrite loadModal/reloadModal to use swapHTML + store.cardID superseded-guard (compare store.cardID to the id being refreshed — replaces currentModalCardID() regex on form.action). Switch arrow-nav to read store.cardID. Remove currentModalCardID() + modalVersion().
4. Rewrite swapHTML to innerHTML + Alpine.initTree(node) + refreshAgo and REMOVE the 7 wire calls from openModal (they are all gone by now). Board SSE handler: swap then Alpine.initTree + refreshAgo.
5. Split into two commits: (1) add editField/editForm + flip a data-attr switch to use them; (2) delete the old wireDirtySave/wireClickToEdit once confirmed in preview — so a regression is a one-line revert.

**Demo.** Click a field to edit inline (no geometry shift), edit two fields, save — all fields persist. Trigger an SSE modal reload mid-session and show click-to-edit still binds. Show layout.html has zero wire*() functions.

**Exit criteria:**
- Click-to-edit, clean-blur revert, dirty-gated FormData save work; no layout shift on edit; modal reloads reinit correctly; 409 uses store.version at submit time.
- NO wire*() remain in layout.html; openModal is just swapHTML; currentModalCardID()/modalVersion() removed; arrow-nav reads store.cardID.
- go test ./... + go vet green + preview behavior check (edit → save → reload → edit again survives an SSE swap).

## Phase 9 — Board UI 2.0: resilient $store.live, board_fragment partial, filter-stall fix, reactive drag

**Goal.** Fix the confirmed filter-stall connection-exhaustion bug and rebuild board interactivity on the Alpine foundation, server-first. Ordering is critical: the persistent EventSource and the Alpine-intercepted filters land TOGETHER (or filters first) so the single connection is never combined with per-filter navigation.

**Depends on:** Phase 0, Phase 8

**Steps:**
1. SERVER keepalive first (discrete, tested): add a ticker case to the existing single-writer for/select in sse.go (~20s ': keepalive'), interleaved in the SAME select — NOT a second goroutine (would corrupt the byte stream for all clients). Add an httptest reading the stream past one interval asserting a ': keepalive' frame with no interleaved corruption. Gate the reconnect work on this test.
2. SERVER board_fragment: add {{define "board_fragment"}} (lanes loop) in board.html and a partial branch in uiBoard (ui.go:138-151) rendering ONLY the fragment for X-Cards-Partial — removes the client-side .board scraping hack. boardData() unchanged.
3. $store.live global: ONE EventSource for the page lifetime, decoupled from filter changes; records lastEventId on EVERY message; status open|reconnecting|down; exponential-backoff reconnect (500ms→8s cap) reopening with ?since=<lastId> (sse.go already replays, Limit:500 — fall back to a full swapBoard on reconnect regardless, as the from-truth safety net if replay is exceeded); consumes ': dropped, reconnect' as a reconnect trigger.
4. Land the Alpine-intercepted filters + pushState + board_fragment swap TOGETHER WITH (or before) the persistent EventSource — never a persistent auto-reconnecting socket combined with per-filter full navigation (that compounds churn against the 6-per-origin cap). Filter <select>/chips use @change=applyFilters (keep <form method=get> for no-JS); applyFilters pushState + swapBoard (fetch fragment, lane-level .lane__body innerHTML replace + count update, Alpine.initTree on each swapped body only — never the persistent .board-view root); add a popstate handler for Back/Forward.
5. board() x-data on .board-view: filter state mirrored from URL; move()/release() = existing moveCard/releaseCard logic ported verbatim (version GET, Idempotency-Key, 422 force-move confirm, structured-error toast); draggingId/dragOverColumn reactive props driving :class (replaces document-level classList toggles); optimistic local move reconciled by the authoritative swapBoard; clear drag state on any failure (no stranded card); debounced (~150ms) onLiveEvent → swapBoard.
6. Keep native HTML5 DnD (no Sortable/library — native satisfies the flow). Reactive filtering stays server-authoritative via swapBoard (no client-side card filtering — that forks the query contract).

**Demo.** Rapidly click filters 20 times with the network tab open — exactly one SSE connection throughout (the old bug would exhaust sockets). Drag a card between lanes (instant optimistic move, reconciled by swap). Kill the server, make a change elsewhere, restart — board reconnects and shows the change.

**Exit criteria:**
- Rapidly toggling filters/sort 20x opens NO new SSE connections (network tab shows exactly one /v1/events/stream persists) — this is the specific regression check for the fixed bug, at the END of the same phase that touches SSE.
- Idle >60s no longer drops the stream (keepalive observed); killing+restarting the server auto-reconnects and replays missed events; keepalive httptest green with single-writer discipline.
- Filter change updates only affected lane bodies without full reload; Back/Forward restores prior filter view; drag-move + unclaim + force-move behave as before; a failed/denied move visually reverts.
- No document-level drag listeners remain; go test ./... green + preview connection-count + drag checks.

## Phase 10 — Migrate residual components, generalize live layer, final cleanup

**Goal.** Retire the last vanilla scaffolding, prove $store.live generalizes beyond the board, and land the full theme/a11y contract sweep.

**Depends on:** Phase 6, Phase 9

**Steps:**
1. Convert any remaining document-delegated handlers left as thin listeners (card-link open, copy-id, arrow-nav) — keep them plain/delegated (Alpine is a poor fit for document-level delegation); switch arrow-nav to store.cardID (done in Phase 8, verify).
2. Optionally subscribe breaches.html to $store.live (condition events) using the same swap pattern to prove the store generalizes; home.html stays static unless owner wants Recent-activity live (open decision).
3. Remove any helpers fully subsumed by cardsAPI (sayStatus/apiErrText) if now unused; confirm no net new remote fetches were introduced.
4. Full contract sweep: TestStyleCSSBalanced / TestTemplatesAreThemeBlind / TestThemeRulesAreScoped + themecss validator against every built-in + workspace theme; token-only lint on all new component blocks; docaudit assertions (Alpine present, htmx absent, single field switch, no x-for-over-JSON, swapHTML sole seam) all green.
5. Manual accessibility + prefers-reduced-motion + prefers-color-scheme pass per DESIGN.md checklist; consider one scripted preview screenshot check per new hook across default/journal/labels/dark to replace repeated manual 4-way toil.

**Demo.** Full walkthrough: no-JS board renders and inline-edits via native forms; JS-on board is fully live and reactive; switch all four themes with no structural break; show grep proving zero wire*() and zero htmx remain.

**Exit criteria:**
- layout.html contains zero wire*() functions; all interactivity is Alpine on existing classes or thin delegated listeners; no imperative DOM state juggling remains.
- breaches (if wired) live-updates via the shared store; no net new remote fetches.
- go test ./... && go vet ./... green; all docaudit CSS/theme/division-of-labor guards green; every theme renders correctly (tokens only, structure unchanged); no-JS first paint of a board is complete and usable.

## Risks

- Alpine.initTree double-binding: mitigated by the ONE invariant (initTree only the freshly-inserted subtree, never a persistent x-data root or document) enforced via the single swapHTML seam + a docaudit guard that no raw innerHTML swap bypasses it. Removes the need for per-component init guards.
- No automated Alpine coverage: httptest is first-paint-only and cannot catch initTree/SSE/behavior regressions; no test asserts the 'hidden' attribute (grep-confirmed) so x-show is safe, but behavior is untested. Mitigation: every component phase requires a preview/manual behavior check as a mandatory gate; 'go test green' is necessary but not sufficient. An optional Playwright/preview smoke job is the escape hatch if regressions recur.
- Multi-value contract fork if landed non-atomically: unset shape (absent vs null vs []) must be ratified in SPEC-DATA-MODEL and enforced in one place in core BEFORE Phase 3 ships; query json_each membership and MCP array schema must be in the SAME PR or filtering/agents silently break. All confirmed against code (sqlite/filter.go:104 scalar extract; mcp fieldSchema scalar map).
- htmx removal silent regression: the two htmx:* listeners are the ONLY error-toast path and htmx:afterSettle is the ONLY post-swap time refresh (verified). Mitigation: rehome both before deleting the tag; a docaudit guard blocks tag removal until a non-htmx toast path exists.
- HX-Request rename miss: coupled in exactly render.go:700 + 4 fetch sites (verified). A missed site returns a full page into an innerHTML swap (nested <html> in modal). Mitigation: rename in one atomic commit; grep-clean docaudit test.
- SSE keepalive concurrency: the loop is a single-writer select (verified sse.go); implementing keepalive as a second goroutine corrupts the byte stream for all clients. Mitigation: add a ticker case to the existing select; httptest asserts a keepalive frame with no interleaved corruption; gate reconnect work on it.
- Board filter-stall made worse in the interim: a persistent reconnecting EventSource + per-filter navigation compounds socket churn. Mitigation: land Alpine-intercepted filters + board_fragment together with (or before) the persistent connection; regression check = 'toggle filters 20x → exactly one SSE connection' at the end of the same phase.
- WYSIWYG parity (DESIGN.md P3) for NEW controls: combobox popover and chip-cluster view↔edit can shift box geometry. Mitigation: explicit geometry-parity exit criterion (identical width/height/padding/line-height/font, no layout shift) on Phases 5 and 6, verified in all 4 themes — a gating check, not a watch-out.
- tags free-add regression: today's control is free-text+datalist (card_modal.html:66) under default TagPolicy 'propose' (config.go:115). A constrained allow-list chip picker would regress it. Mitigation: chip control is free-add-first and policy-aware; datalist is suggestions, Enter/comma creates arbitrary chips when policy allows; rejected proposed tags mirror per-chip .is-invalid.
- New CSS hooks reading per-type hue tokens render un-dark-adapted inside boards (board inline --type-* wins over the dark remap). Mitigation: hard rule — combobox/multiselect popovers read only neutral + role tokens, never --type-*.
- 'UI 2.0 board' perceived as no-visible-change: much of the board work is reliability plumbing. Mitigation: reframe honestly as 'reliability + interactivity substrate' and surface the optimistic-drag instant-move + filter-stall fix as the user-visible wins; the demo makes the connection-count fix tangible.
- Pinemix license: harvesting x-data (even reference-only) needs a compatible license. Mitigation: hard gate in Phase 0 exit criteria — license confirmed before any Pinemix-derived component is authored.

## Open decisions (owner ratification needed)

- Multi-value UNSET contract (product-owner ratification, gates Phase 3): recommend an unset optional multiple field is ABSENT on the wire (never null, never []), a present field always a JSON array. Must be a normative line in SPEC-DATA-MODEL enforced in one place in core.
- FieldDef.Multiple scope: recommend enum + user for v1; card_link multiple deferred as an explicit documented fast-follow (multi-link is high user value but keeps the v1 core delta minimal). Confirm card_link is out of v1.
- Multi-user display: the current .chip--owner is singular. Recommend shipping multi-ENUM display+control in Phase 6 first (chips exist), and enabling multiple:true on USER only once the N-owner board/card display (wrapping .chip--owner, overflow/avatar behavior) is designed and themed. Confirm whether multi-user UI is in this sprint or a fast-follow.
- User field free-add: the entry sub-form today accepts arbitrary typed user ids (free-text datalist). Does the unified combobox preserve free-add for external/unknown assignees, or constrain to injected $.Users? (Same free-add tension as tags.)
- Combobox threshold: LOCKED recommendation is combobox ALWAYS for enum + user (one control shape, one WYSIWYG/theme surface). Confirm the owner wants always-combobox vs a documented count threshold (a threshold means both shapes must pass the 4-theme + WYSIWYG check).
- No-JS scope honesty: the CREATE modal is JS-built today (wireCreateModal builds the POST body), so 'no-JS create' is NOT a current guarantee. Recommend scoping the progressive-enhancement promise to view + inline-edit (which post real forms) and native multi-select as an inline-edit-only fallback — unless the owner wants a real <form> POST create fallback added.
- Alpine delivery: recommend self-host + embed behind assetStamp (matches single-binary/boring-tech, survives a future CSP, cache-busted like CSS) over CDN. Confirm; also confirm the Alpine version pin.
- CSP posture: none exists today (verified), so the default eval-based Alpine build works. If any CSP is planned soon, decide now whether to adopt the @alpinejs/csp build from the start (different authoring syntax, changes every component) rather than retrofitting.
- tags 'propose' policy behavior: when a user free-adds a tag under 'propose', does propose auto-extend workspace.tag_set (needs a core/API path) or is the card save expected to surface the existing structured propose hint per-chip? (Do not ship free-add that ignores TagPolicy.)
- Alpine.data() factory naming: pick ONE convention before Phase 4 so plans don't produce colliding factories (the 'wc' prefix is the only namespaced proposal; the non-prefixed names editField/entryEditor/createForm are the alternative). Renames of .combobox/.multiselect/.tag-input class names are breaking once they become stable theme hooks — lock them in Phase 5/6.
- Query DSL operator surface: confirm json_each membership (contains/has) is the accepted v1 semantics for multi-value fields (vs equality-on-array), since it lands in the Phase-3 core PR.
- home.html Recent-activity and breaches.html live-update: recommend breaches subscribes to $store.live to prove generalization; home stays static. Confirm whether home should also go live.
- Google Fonts remote link (layout.html:9) predates this work and violates the no-remote spirit themecss enforces for CSS. Out of scope but flagged — decide separately whether to vendor theme fonts as data: URIs.
- Preview/Playwright smoke test: blocking CI job or manual-only? Affects how much the plan can rely on it to catch initTree/SSE regressions given the no-build ethos.

---

## Appendix: per-aspect plans (source material)

### Schema → form controls: one reusable Alpine "field-control" seam replacing the triplicated schema→control switch, richer

**Design.** The Alpine-based redesign for the schema→control seam (see full detail above).

**Components:** Go: `{{define "field_control"}}` partial (internal/httpapi/templates/field_control.html) — the singl; Go: a `fieldCtx`/`fieldControlData` template helper (render.go) that packages Def+value+Users+Displa; Alpine: `combobox` factory — filter-as-you-type over DOM <option>s, single + multiple, keyboard + AR; Alpine: `tokenSelect` factory — chip multiselect (tags + multi enum/user), emits comma-string or rep; Alpine: `fieldEdit` factory — click-to-edit toggle replacing wireClickToEdit.; Alpine: `entryForm` factory — repeating sub-form replacing wireEntryEditors, reusing field_control.; JS: `reinit(node)=Alpine.initTree(node)` global + call-sites in openModal() and the SSE board refetc; CSS: `.combobox`, `.combobox__list`, `.combobox__opt`, `.tokens`, token/chip-in-input states — all b

### Interactivity layer — replace the 690-line vanilla wire*() layer with Alpine.js components; drop the unused htmx depende

**Design.** DIVISION OF LABOR (non-negotiable): Go templates render the full initial DOM and all server data; Alpine adds only local/ephemeral state (edit-open, dirty, uploading, composer text, validation-error text). No x-data component renders a server list via x-for over JSON — the entries grid, comment feed, field views, board cards stay 100% Go-rendered. Alpine data is seeded from server values via plain HTML attributes / x-model on server-rendered inputs, never from an injected JSON blob that forks the API contract.

ALPINE LOAD + REINIT SEAM (foundational, build first):
1. Load Alpine with `defer` 

**Components:** Bootstrap: inline pre-Alpine script defining window.cardsAPI.send() (version-aware fetch + structure; Alpine.store('modal'): {cardID, version, open} seeded from modal fragment data-card-id/data-version;; Alpine.data('commentComposer') on [data-comments].; Alpine.data('commentEdit') per [data-comment-id] (replaces createElement edit).; Alpine.data('entryEditor') on [data-entry-field].; Alpine.data('artifactZone') on [data-artifact-upload].; Alpine.data('createForm') on [data-create-modal].; Alpine.data('boardForm') on [data-board-create].

### Board & layout (UI 2.0): board.html lanes/cards/filters, the SSE live-update model, home.html, breaches.html, and the ap

**Design.** DIVISION OF LABOR (non-negotiable): Go templates render every lane, card, filter option, home/breaches row — all server data, correct with JS off. Alpine owns only ephemeral/local interactivity: the live connection, optimistic drag state, filter-panel UI state, and re-init after swaps. No Alpine x-for over server JSON anywhere.

FOUR ALPINE UNITS:

1) $store.live (global, app-wide, defined once in layout.html) — the resilient live layer. Owns exactly ONE EventSource for the page lifetime, decoupled from filter changes (this alone kills the filter-stall bug). API: connect(boardID, types), lastI

**Components:** $store.live (Alpine global store): single EventSource, Last-Event-ID resume, backoff reconnect, keep; board() Alpine component (x-data on .board-view): filter state, reactive drag state, move()/release(; swapBoard() targeted-swap helper: fetch board_fragment partial, lane-level replace + count update, A; Server: {{define "board_fragment"}} in board.html wrapping the lanes loop; uiBoard partial branch (u; Alpine.initTree() reinit seam wired into both swapBoard (board) and openModal (modal aspect) — found; toast bridge: replace htmx:responseError/afterRequest listeners with a plain 'app:error' custom-even; filter-panel markup: Alpine-bound selects/chips using .select/.chip tokens, @change="applyFilters()"; FieldDef.Multiple bool + enum/user multi validation (core) — the cross-cutting data shape; board onl

### Design system & components — the token/CSS layer and how Alpine components fit it while keeping themes working

**Design.** DIVISION OF LABOR is the spine: Go templates render the initial DOM and ALL server data (first paint correct, no-JS still functions); Alpine adds ONLY local/ephemeral interactivity (open/closed, dirty, filter text, keyboard nav, staged-not-yet-saved values). No Alpine x-for over server JSON — server data is always in the server-rendered DOM.

COMPONENT NAMING: two namespaces that never collide. (1) CSS presentation stays BEM-ish on existing hooks (.field, .input, .chip, .field__view) — themeable, structural, unchanged. (2) Alpine behavior is a small registry of Alpine.data() factories with a `

**Components:** field_control Go template partial ({{define "field_control"}}) — single source of truth for schema→c; wcEditable — Alpine.data replacing wireClickToEdit: {editing:false, dirty:false}; x-show toggles .fi; wcCombobox — single-select enum/user picker re-skinned from Pinemix; markup .combobox/.combobox__men; wcMultiSelect — multi-value enum (the 'multiple:true' shape); selected values render as .chip cluste; wcTagInput — tags field as chip composer over the workspace tag_set (replaces raw comma-text + datal; wcUserPicker — user field via wcCombobox seeded with .Users; resolves the create-vs-entry select/dat; wcEntryEditor — Alpine.data replacing wireEntryEditors; add/edit/remove repeating entries; renders s; wcCreateForm, wcComments, wcArtifactUpload — thin Alpine wrappers migrating the remaining wire*() fu

### Foundations & migration — adopting Alpine.js safely and migrating the /ui frontend incrementally without a big-bang rewr

**Design.** Alpine-based redesign is described in the design field above; see phases for sequencing.

**Components:** swapHTML(container, html) primitive in layout.html — the single innerHTML+Alpine.initTree+refreshAgo; Alpine.data('cardModal') — owns dirty-flag + click-to-edit view↔edit toggle (replaces wireDirtySave ; Alpine.data('artifactUpload') — idle/dragover/uploading/success/error state machine (replaces wireAr; Alpine.data('comments') — composer + inline edit local state (replaces wireComments); Alpine.data('entryEditor') — repeating-field add/edit/remove sub-form local state (replaces wireEntr; Alpine.data('createForm') and Alpine.data('boardCreate') — form dirty/submit + structured-error mirr; A pinned, self-hosted alpine.js static asset added to templates/ and embed.FS, served at /ui/alpine.; An ADR document (docs/architecture/ or docs/NOTES.md) recording: Alpine adopted, division-of-labor r
