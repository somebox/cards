# Cards sprint: restore contract-honesty, fix the WIP census users see, ship discoverable workspace themes

_Sprint plan generated 2026-07-07 via `/sprint-plan` (focus: core data types, APIs, events, integrations, metrics, infra)._

## Framing

Honest framing (per design-ux review): this is primarily a foundation/correctness sprint — doc truth, an accurate column census, and theme-loading precursors — with ONE user-facing deliverable at the end: workspace-loaded, discoverable, shareable themes. The earliest user-visible win is deliberately Phase 2's accurate WIP badges on real boards (>500 cards), demoed on the board UI itself, not just JSON. Foundation precedes value; the core stays a thin client of stable contracts; the widest-blast-radius change (SQLite read pool) stays deferred. Two review-driven scope corrections anchor the revision: the census fix targets only the single shared countColumn (the item-iterating monitor scans are a different bug class, explicitly deferred with its debt kept visible), and the stylesheet cache stamp must move from process state to per-generation instance state before any theme concatenation is safe.

## Phase 1 — Restore the drift audit (doc truth) [internal/foundation]

**Goal.** Make INTEGRATOR-REFERENCE.md, ROADMAP.md, and SPEC-API-SURFACE.md tell the truth about the shipped workspace-reload feature so downstream tasks stop paying a verification tax against a lying source-of-truth doc. Zero code risk. Labeled internal/foundation per review: judge it on correctness, not UX.

**Steps:**

1. Rewrite the INTEGRATOR-REFERENCE.md §6 'Reload definitions caveat [drift]' block (the paragraph asserting 'there is no reload handler and no file watching' / 'reload in practice means restart the server') to describe the shipped POST /v1/workspace/reload: atomic generation swap, store/bus survive across generations, 422-on-failure keeps the OLD generation serving, definition_reloaded fans out on SSE. Verified against cmd/cards/reload.go (reloadableApp.reloadLocked, atomic.Pointer swap) and cmd/cards/reload_test.go::TestReloadFailureKeepsOldDefinitions.
   - _Files:_ docs/reference/INTEGRATOR-REFERENCE.md (§6 reload-caveat block; the 'One remaining [drift] constant is definition_reloaded' sentence higher in the events section; the schema-authoring/reload table row)
   - _Outcome:_ grep -n 'no reload handler\|reload.*means restart\|no file watching' docs/reference/INTEGRATOR-REFERENCE.md returns nothing, and §6 instead describes calling POST /v1/workspace/reload and observing 422 with the old definitions still serving on a bad config.
2. Fix the ROADMAP.md §7 'Workspace reload' entry (currently 'proposed, card 4b507da7'): that card was tombstoned into f6d2f5ea / commit f772951. Move the entry to the built/shipped section (or delete it), drop the dangling 4b507da7 reference, and add a one-line note that the enabler card was absorbed.
   - _Files:_ docs/ROADMAP.md (§7 Workspace reload entry)
   - _Outcome:_ grep -n '4b507da7' docs/ROADMAP.md returns nothing, and 'Workspace reload' no longer appears under a proposed/unbuilt heading.
3. Update the SPEC-API-SURFACE.md 'POST /workspace/reload -> not yet implemented' line: replace it with the implemented route, request/response shape, and 422-on-bad-config semantics. Add POST /v1/boards (create-a-board) in the same file if it is likewise missing or misdescribed.
   - _Files:_ docs/spec/SPEC-API-SURFACE.md (the POST /workspace/reload line)
   - _Outcome:_ grep -n 'not yet implemented' near the reload/boards lines returns nothing; the reload and create-a-board routes documented in SPEC-API-SURFACE.md match the routes mounted in internal/httpapi/server.go.
4. Close (do NOT implement) the 'add a CSS brace-balance test' follow-up: it already exists as internal/docaudit/uicontract_test.go::TestStyleCSSBalanced. In the same doc-truth pass, add [not built] honesty markers to docs/extensions/MCP.md for any tools/kinds it lists that are absent from internal/mcp/mcp.go. Note: the design-ux reviewer's 'THEMES.md does not exist' claim is incorrect — it lives at docs/design/THEMES.md (verified present); the template comments citing it are NOT dangling. No new doc is created; this plan uses the full path docs/design/THEMES.md everywhere to prevent the same confusion.
   - _Files:_ backlog card carrying the brace-test follow-up; docs/extensions/MCP.md
   - _Outcome:_ The brace-test follow-up card is closed with a comment citing internal/docaudit/uicontract_test.go::TestStyleCSSBalanced; each MCP tool/kind in docs/extensions/MCP.md that is absent from internal/mcp/mcp.go carries a [not built] marker.

**Demo.** grep -rn 'no reload handler\|reload.*means restart\|no file watching' docs/ returns nothing; INTEGRATOR-REFERENCE §6 now describes calling POST /v1/workspace/reload and observing 422 with the old definitions still serving on a bad config.

**Exit criteria:**
- INTEGRATOR-REFERENCE.md §6, ROADMAP.md §7, and SPEC-API-SURFACE.md all describe workspace-reload as shipped, with matching 422-keeps-old-generation semantics
- The redundant brace-balance test follow-up card is closed (not implemented), citing TestStyleCSSBalanced
- go test ./... still green (docs-only change)

## Phase 2 — Accurate column census (first user-visible win)

**Goal.** Fix the 500-card undercount in the single shared countColumn so WIP/lane events (wip_exceeded/cleared, lane_drained/refilled) and /v1/breaches are trustworthy on real boards — and demo it on the board UI, where users actually see WIP badges. Rescoped per arch-data + reliability reviews: countColumn (service.go, the single column-census query it calls ListCards with Limit:500) is the ONE census function, called by both evaluateColumn and Breaches; the other Limit:500 sites (MaxTimeInStatus, rebuildCardIdle, breaches.go blocked-card scan) iterate card rows to build per-card deadlines/items and CANNOT take a scalar COUNT(*) — they are a different bug class, explicitly deferred.

**Depends on:** Phase 1 — Restore the drift audit (doc truth) [internal/foundation]

**Steps:**

1. Add an unclamped CountCards(ctx, CardQuery) (int, error) issuing COUNT(*), implemented in internal/sqlite. Before committing to a method on the full core.Store interface, check the blast radius on test doubles/fakes (per arch-data suggestion): if fakes are numerous, prefer a narrow optional counter capability the service type-asserts, otherwise add the interface method. Decide this BEFORE writing the sqlite implementation.
   - _Files:_ internal/core/store.go (interface or narrow capability), internal/sqlite/sqlite.go, existing test fakes
   - _Outcome:_ A COUNT(*)-backed counting query distinct from clamped ListCards exists; go build ./... and go test ./internal/sqlite pass with every store fake either updated or provably unaffected.
2. Use CountCards ONLY inside countColumn (replacing its ListCards+len with a COUNT) — this fixes both evaluateColumn and Breaches through the single shared function. Do NOT touch MaxTimeInStatus, rebuildCardIdle, or the breaches.go blocked-card scan, and do NOT touch clampCardLimit's shared 500 ceiling. Edit the evaluateColumn caveat comment (the 'membership is TypeIDIn only ... and the census caps at 500 cards' block above evaluateColumn): remove ONLY the 'and the census caps at 500 cards' clause and KEEP the DefaultFilter/TypeIDIn-membership caveat (a known-live bug this change does not fix — deleting it would convert documented debt into hidden debt, per reliability review). Open one tracking card covering (a) the DefaultFilter census gap and (b) the >500 truncation in the item-iterating monitor/breach scans.
   - _Files:_ internal/core/service.go (countColumn body and the evaluateColumn caveat comment), backlog (new tracking card)
   - _Outcome:_ countColumn returns a COUNT(*) with no 500 cap; the TypeIDIn/DefaultFilter caveat sentence still stands in the comment while the '500 cards' clause is gone; a single backlog card tracks both deferred items (DefaultFilter gap + item-scan truncation).
3. Add a regression test: a board with >500 cards in one column under a WIP limit asserts wip_exceeded fires at the true count and /v1/breaches reports that same count. Run on the standard sqlite test backend; if seeding 600 cards needs a file DB rather than :memory: or is slow, state that in a test comment so the future read-pool PR doesn't inherit an unexplained flake. Declined (reliability): a second test pinning the item-scans' 500 truncation as 'known behavior' — pinning known-wrong behavior adds churn when the real fix lands; the tracking card + retained comment carry that debt instead.
   - _Files:_ internal/core/service_test.go (or breaches_test.go)
   - _Outcome:_ A named test fails against the pre-fix ListCards+Limit:500 countColumn and passes against the COUNT(*) fix, reporting the true (>500) count in both the wip_exceeded event and /v1/breaches, with backend/runtime expectations noted in a comment.

**Demo.** Seed a column with 600 cards under a WIP limit and open the board in the browser: the WIP badge / wip_exceeded state shows the real count on the board UI (previously silently capped at 500), and /v1/breaches agrees.

**Exit criteria:**
- CountCards (or a narrow type-asserted counter) lands with a single sqlite implementation; no wire/API/SPEC change
- Only countColumn changed in the census path; clampCardLimit and the three item-iterating scans (MaxTimeInStatus, rebuildCardIdle, breaches blocked scan) are untouched
- The TypeIDIn/DefaultFilter caveat comment is retained; one tracking card is opened covering both the deferred scan truncation and the DefaultFilter gap
- The >500-card regression test is green on the standard backend; go test ./internal/core ./internal/sqlite passes

## Phase 3 — Theme foundations (stamp seam, real validator, contract decision) [internal/foundation]

**Goal.** Land the precursors that make workspace themes safe: move the stylesheet cache stamp onto per-generation instance state (fixing the arch-data blocker — assetStamp is a package-level var at internal/httpapi/server.go:30 initialized once per PROCESS, so 'verify it rotates on reload' is impossible without moving the seam), build a parse-based CSS validator with a stated threat model, settle the theme composition contract (precedence + fonts + back-compat), and lift internal/config coverage before loadThemes lands there.

**Depends on:** Phase 1 — Restore the drift audit (doc truth) [internal/foundation]

**Steps:**

1. Move the stamp OFF the package var (server.go:30): add it as a field on httpapi.Server, set to a fresh time.Now() in New() (server.go:53), and change the assetStamp FuncMap closure (server.go:63) to read that field instead of the package var. Then reloadableApp.reloadLocked's call to httpapi.New (cmd/cards/reload.go:99) naturally yields a new stamp per generation. Verify with an integration test in cmd/cards (reload_test.go) — NOT an httpapi-only unit test, which cannot observe the generation swap (per reliability review) — that performs a real reload and asserts the served stylesheet URL stamp changed.
   - _Files:_ internal/httpapi/server.go (Server field, New(), FuncMap closure at line 63; remove package var at line 30), cmd/cards/reload_test.go (integration test)
   - _Outcome:_ The package-level assetStamp var is gone; the stamp is a Server field; a cmd/cards reload_test.go test performs a real POST /v1/workspace/reload and asserts the served /ui/style.css stamp value changed between generations despite the 24h Cache-Control.
2. Generalize the docaudit CSS checks into a reusable validator with an EXPLICIT threat model (per reliability review — brace-balance + prefix-grep is not a scoping guarantee): a small CSS tokenizer (not regex) that verifies every top-level rule's selector is prefixed by html[data-theme="<name>"], rejects @import and non-data:/remote url(), and checks brace balance. Errors are structured and recovery-guiding — theme name, file, line, specific violation, fix hint (e.g. 'rule at line 12 is not scoped under html[data-theme="neon"]') — this payload is what a Phase 4 422 surfaces to a theme author. Stated trust posture: themes are workspace files (YOLO-trusted operator content), so the guard is a contract check, not a sandbox; it enforces scoping mechanically so a shared theme can't accidentally break the base UI or add remote origins.
   - _Files:_ internal/docaudit (or a small internal/cssguard package the docaudit tests consume), tests with intentionally-escaping CSS (e.g. 'html[data-theme=x]{} } body{...}')
   - _Outcome:_ The validator runs against an arbitrary CSS string and, on the adversarial 'html[data-theme=x]{} } body{...}' input plus an @import and a remote url() input, returns a structured error naming theme/file/line/violation that brace-counting alone would miss; a well-scoped theme passes.
3. Decide the theme COMPOSITION contract, not just a name (per arch-data: Board.Theme map[string]string at types.go:207 and the workspace-default theme name at types.go:141 are different axes — one overrides hue tokens, the other selects a named theme). Document in docs/architecture/DESIGN.md + docs/design/THEMES.md: (1) full resolveTheme precedence: ?theme query > sticky cookie > board's named theme (new tier) > workspace settings.theme > default; (2) the board's inline Board.Theme token map layers ON TOP of whatever named theme is active (orthogonal axes, both kept); (3) back-compat: existing workspaces setting Board.Theme keep working unchanged; (4) font policy for WORKSPACE themes: fonts must be self-hosted via @font-face inside the theme's own CSS (data: or theme-local files) — a downloaded theme may NOT introduce new remote origins (local-first; per both design-ux and arch-data). The embedded themes' existing Google Fonts links (themeFonts, server.go:35 + layout.html) stay as-is but get documented as a built-in-only mechanism.
   - _Files:_ docs/architecture/DESIGN.md (theme contract section), docs/design/THEMES.md, internal/core/types.go (BoardPresentation naming only if needed)
   - _Outcome:_ DESIGN.md + docs/design/THEMES.md state the full precedence chain, the Board.Theme-map-layers-on-top rule, the Board.Theme back-compat guarantee, and the workspace-theme self-hosted-fonts policy — all decided before any Phase 4 resolveTheme code, so adding the named-theme tier later is not a breaking change to a stable hook.
4. Raise internal/config coverage (currently 51.3%) with targeted loader tests around merge/validate, since loadThemes lands in config.Loader next phase.
   - _Files:_ internal/config/*_test.go
   - _Outcome:_ go test ./internal/config -cover reports coverage above the 51.3% baseline, with new tests exercising the merge/validate paths loadThemes will extend.

**Demo.** Feed the validator a brace-balanced but scope-escaping CSS string and watch it reject with a file/line/violation message; reload the dev server and confirm /ui/style.css serves under a fresh stamp (cmd/cards integration test output).

**Exit criteria:**
- The stamp lives on httpapi.Server instance state (package var removed); the cmd/cards integration test proves rotation across a real reload
- The tokenizer-based validator rejects scope-escapes, @import, and remote url(); its error payload names theme/file/line/violation
- Precedence chain, Board.Theme layering, back-compat, and workspace-font policy are documented in DESIGN.md + docs/design/THEMES.md
- internal/config coverage is above 51.3%; go test ./... green

## Phase 4 — Workspace-loaded, discoverable, shareable themes (user value)

**Goal.** Make the theme sharing story real AND usable: load named themes from definitions/themes/<name>.{css,json}, install by reload, and give users a way to discover and select themes in the product (per design-ux: without discoverability, 'shareable themes' is a plumbing story — a hand-typed ?theme= param is not a sharing story).

**Depends on:** Phase 3 — Theme foundations (stamp seam, real validator, contract decision) [internal/foundation]

**Steps:**

1. Add loadThemes to config.Loader reading definitions/themes/<name>.css + <name>.json (manifest: contract:1, display name; fonts only per the Phase 3 self-hosted policy — no remote origins). Every CSS file passes the Phase 3 tokenizer validator AT LOAD TIME, so an unscoped/malformed theme is rejected in the loader with the structured file/line error and never reaches uiStylesheet (per arch-data: load-time rejection, not serve-time hope).
   - _Files:_ internal/config/ (loader + fixtures)
   - _Outcome:_ Given a definitions/themes/ fixture with one valid and one scope-escaping theme, the loader surfaces the valid theme and returns the Phase 3 structured file/line error for the invalid one; no unvalidated CSS reaches the server.
2. Thread themes through the composition root EXPLICITLY (per arch-data): change httpapi.New()'s signature (server.go:53) to accept the loaded themes/font manifest, update the reloadLocked call site (cmd/cards/reload.go:99), and hold themes as Server instance state replacing the package-level themeFonts map (server.go:35). uiStylesheet concatenates base CSS FIRST then appends workspace theme CSS (stated ordering invariant); resolveTheme (render.go:674) gains the board-named-theme tier per the Phase 3 precedence doc AND validates the requested name against the loaded theme set, falling back to default on miss (fail-safe, not a silent data-theme no-op). No template restructuring — pinned by the existing zero-theme-conditionals test.
   - _Files:_ internal/httpapi/server.go (New signature at 53, instance state replacing themeFonts at 35), internal/httpapi/ui.go (uiStylesheet), internal/httpapi/render.go (resolveTheme at 674), cmd/cards/reload.go:99
   - _Outcome:_ httpapi.New takes the theme manifest and stores it on the Server (package-level themeFonts var removed); uiStylesheet emits base CSS then theme CSS in that order; resolveTheme resolves the board-named-theme tier and returns default for an unknown ?theme name; the theme-blind-templates test still passes.
3. Wire install-by-reload: reloadableApp picks up new/changed theme files on POST /v1/workspace/reload (atomic swap; a malformed theme returns 422 whose body carries the validator's file/line/violation payload; old generation keeps serving), and the Phase 3 per-generation stamp busts the stylesheet cache. Test: install a theme via reload and assert it renders; break its scoping and assert 422 + old theme still serving + error payload names the offending rule.
   - _Files:_ cmd/cards/reload.go, cmd/cards/reload_test.go, internal/httpapi
   - _Outcome:_ A reload test installs a theme file and asserts it renders under a fresh stamp, then breaks the theme's scoping and asserts POST /v1/workspace/reload returns 422 with a body naming the file/line/unscoped rule while the previously-loaded theme keeps serving.
4. Minimal theme discoverability (per design-ux major): a small theme switcher in the board UI nav — a select/menu listing embedded + loaded workspace themes by display name, applying via the existing sticky ?theme mechanism. No settings page, no gallery, no preview — just make loaded themes visible and selectable in the product. Declined (design-ux): pulling a minimal theme-load slice earlier than Phase 4 — the load path genuinely depends on Phase 3's stamp seam and validator, and splitting it would ship the stale-CSS or unvalidated-CSS bug the foundations exist to prevent; the honest reframe + Phase 2's board demo cover the early-value concern instead.
   - _Files:_ internal/httpapi/templates/layout.html (nav), internal/httpapi/render.go (theme list in template data), internal/httpapi/templates/style.css (switcher styling via role tokens)
   - _Outcome:_ The board nav renders a switcher listing every embedded and loaded workspace theme by display name; selecting one applies it via the sticky ?theme mechanism, with no hand-typed URL and no template restructuring flagged by the theme-blind test.
5. Update docs/design/THEMES.md + docs/architecture/DESIGN.md: the two-file GitHub sharing story references the now-real load path, the self-hosted font policy, and the note that CSS-only theme diffs become a permanent external contract once themes circulate. Keep the stated scope boundary: no further presentation sprints without re-checking proportionality against coordination-core work.
   - _Files:_ docs/design/THEMES.md, docs/architecture/DESIGN.md
   - _Outcome:_ docs/design/THEMES.md and DESIGN.md describe the shipped definitions/themes load path, the self-hosted-fonts policy, the CSS-diff-as-external-contract caveat, and the proportionality boundary.

**Demo.** Drop a downloaded theme folder into definitions/themes/, POST /v1/workspace/reload, then — in the browser — pick the theme from the nav switcher and watch the board reskin live with no rebuild. Break the theme's CSS scoping, reload again: 422 whose body names the file, line, and unscoped rule, while the previous theme keeps serving.

**Exit criteria:**
- Workspace themes load, validate at load time through the shared tokenizer guard, and render through existing hooks with zero template restructuring
- The httpapi.New() signature change and the reload.go:99 call site land together; the package-level themeFonts var is replaced by Server instance state
- Install-by-reload works with guided 422-on-malformed and per-generation cache-busting; the reload regression test is green
- Loaded themes are discoverable and selectable in the UI nav; unknown ?theme= names fall back to default
- The theme-blind-templates and CSS-scoping tests pass; go test ./... green; docs/design/THEMES.md + DESIGN.md match the shipped path

## Risks

- Phase 3 stamp seam is the sprint's one true blocker dependency: if the stamp stays a package var (server.go:30), every theme install ships a stale-CSS bug behind a 24h Cache-Control. The plan moves it to per-generation Server state and proves rotation with a cmd/cards integration test — an httpapi-only unit test cannot observe the generation swap and would be false assurance.
- Validator strength vs. claimed guarantee: brace-balance + prefix checks do not prevent scope-escape (a balanced 'html[data-theme=x]{} } body{...}' breaks out). The Phase 3 tokenizer must be tested against exactly these adversarial strings, and the plan states the trust posture (workspace files are operator-trusted; the guard is a contract check, not a sandbox) so 'validated' doesn't imply a security property it lacks.
- Theme composition contract (Phase 3 step 3) is consequential: precedence order, Board.Theme-map layering, and back-compat must be settled in DESIGN.md before Phase 4 wires resolveTheme's new tier — a rename or precedence change after themes circulate is a breaking change to a stable hook.
- Workspace-theme fonts: without the self-hosted-only policy, a community theme silently adds third-party origins to a local-first product. The policy is decided in Phase 3 and enforced by the validator's remote-url rejection; the embedded themes' existing Google Fonts links remain a documented built-in-only exception.
- CountCards interface blast radius: adding a method to core.Store forces every fake to implement it. Phase 2 checks the test-double count first and may use a narrow type-asserted capability instead — decide before writing the sqlite implementation, not after.
- Phase 2 must not overclaim: the demo says 'trustworthy WIP/lane counts', and that is now true for countColumn consumers only — MaxTimeInStatus/idle/blocked scans still truncate at 500 (tracked, documented). Deleting the evaluateColumn caveat comment wholesale would hide the unfixed DefaultFilter bug; only the '500 cards' clause goes.
- Sprint value shape: 3 of 4 phases are internal/foundation, now labeled as such (per design-ux). The mitigations are honest framing, Phase 2's board-UI demo as the early visible win, and the Phase 4 switcher making the payoff genuinely usable — restructuring to ship themes earlier was declined because the load path depends on the stamp and validator foundations.
- Process guardrail (kept, and re-validated this round): require file:line evidence for every 'X does not exist' claim — this batch both confirmed one (the brace test already existed as TestStyleCSSBalanced) and refuted one (THEMES.md 'missing' was a wrong-path lookup; it lives at docs/design/THEMES.md).

## Out of scope (deliberately deferred)

- SQLite read pool (SetMaxOpenConns(1) -> bounded WAL reader pool): widest-blast-radius change in the backlog (~35 Store methods, :memory:-vs-file test semantics, read-your-own-write routing, the _txlock=immediate invariant living only in a comment). Defer to its own PR; pin the _txlock invariant with a test first. Phase 2 takes only the scalar-counting half of that bundled card — a clean seam split.
- Fixing the >500 truncation in the item-iterating monitor/breach scans (MaxTimeInStatus, rebuildCardIdle, breaches.go blocked-card scan): these need unclamped/cursor paging of real card rows, not a COUNT(*) — a different fix class than Phase 2's census. Deferred with a tracking card and the retained code comment so the debt stays visible (per reliability review).
- The DefaultFilter-scoped board census gap (countColumn matches on TypeIDIn only): known-live bug Phase 2 does NOT fix; the warning comment is kept and the gap goes on the same tracking card as the item-scan truncation.
- Theme management UI beyond the minimal nav switcher: no gallery, upload, preview, or per-board theme editor. The switcher makes sharing usable; anything more is a future proportionality-checked decision.
- p2-cleanup-spec-parity (card_c7a70b646): carries two unresolved design decisions (owner=me promotion is SPEC-breaking; ResolveActor wire-vs-delete). Resolve those first; can then run parallel to a future sprint on Phase 1's corrected doc baseline.
- style_field enum-driven card visuals (STYLE-FIELD.md / card_8b3e83d90): gated behind four open design questions; not chained into this sprint's presentation work.
- Authentication (ROADMAP §1): needs-design pending a threat-model RFC that doesn't exist — correctly not built here.
- Durable outbox/webhooks (SUBSCRIBERS.md): formal recorded NO-GO — stays deferred.
- Dormant ROADMAP §7 API cluster and events §5 remainder beyond the census fix — a periodic 'still deferred' stamp is warranted but is not this sprint's work.
