# Sprint plan 2026-07-22 — Solid Substrate, Demonstrated Composition

> Produced by the /sprint-plan workflow (survey → align → strategy → per-candidate
> investigation → draft → 3-lens review → revise → refine), 2026-07-22.
> Board tracker: see the `TRACKER: Sprint 07-22` card in `.cards/`.

## Theme

Ship one real user-facing capability early — a decided, tested card-query endpoint — then settle the one kernel-level question the vision can't push to an extension (SQLite concurrent-read behavior, where a well-evidenced park is a first-class success), pin the contract surfaces extensions ride on, and spend that trust on visible composition: runnable extension examples and a schema-authoring UI. Every phase either ships something a user notices or is honestly labeled enabling work with a rollback story; asserted principles (small core, big composition; stable documented contracts) become demonstrated ones.

## Phase 1 — Foundation hygiene & write-seam extraction

**Goal.** Clear the cheap, verdict-independent taxes every later phase pays. Deliberately trimmed: the SQLite pragma/DSN 'centralization' from the draft plan is DROPPED here — today exactly one production site builds pragmas (sqlite.Open, sqlite.go:35), so extracting buildDSN() now would be speculative abstraction; it moves into Phase 3's branch-(a), gated on the measurement verdict actually requiring a second DSN. This phase is honest enabling work, not a user demo.

### Steps

- **Do:** Regenerate .cards/backlog.md from backlog.jsonl (via scripts/board.sh) so the committed overview stops stale-listing the already-closed release card 30026459 (status:"done" in the jsonl) under its `### backlog` section.
  - **Outcome:** In backlog.md, card 30026459 appears under `### done`, not `### backlog`, matching its status:"done" in backlog.jsonl; every card's backlog/todo/review/done section membership in backlog.md matches its status field in backlog.jsonl (no card is listed under a section that contradicts its jsonl status).
  - **Files:** `.cards/backlog.md, .cards/backlog.jsonl, scripts/board.sh`
- **Do:** Extract the write-then-validate-then-rollback-then-reload pattern from reloadableApp.handleCreateBoard into a shared helper (writeDefinitionAndReload) that owns the mu / selfWriteGate / reloadLocked sequence, and re-express board-create on top of it. This helper is also where Phase 5's old-vs-new card-type diff check will live — it is the only layer that holds both the prior generation and the new one (validateCardType cannot diff; see Phase 5).
  - **Outcome:** handleCreateBoard is a thin caller of the helper; existing reload tests (cmd/cards/reload_test.go) stay green; the mutex/self-write semantics live in exactly one place, with a documented seam for a future compat/diff hook.
  - **Files:** `cmd/cards/reload.go (handleCreateBoard:275, reloadLocked:158, selfWrite gate)`
- **Do:** Document the `cards mcp` no-reload limitation: MCP tool schemas are baked at process construction (mcpCmd, serve.go:192) with zero reload wiring and stdio MCP has no server-push channel, so definition edits made while an agent session is live do not reach it — recovery is reconnect. State plainly that additive-optional-default schema edits are what keep a stale agent's in-flight writes valid.
  - **Outcome:** docs/extensions/mcp.md states the staleness, the reconnect recovery path, and the additive-edit validity property explicitly, before Phase 5 makes it user-visible.
  - **Files:** `docs/extensions/mcp.md, cmd/cards/serve.go (mcpCmd:192)`
- **Do:** Record two definition-of-done lines on the sprint tracker: (1) any change flipping a [proposed]/[built] status anchor updates implementation-status.md in the SAME commit (docaudit boundary tests enforce it in CI); (2) any phase that flips anchors carries a one-line rollback note — what reverts if the phase is cut mid-sprint — so Phases 3 and 5 cannot leave the tracker half-flipped.
  - **Outcome:** Both DoD lines are written into the sprint tracker card; no phase below can flip a doc claim without the anchor update, and every anchor-flipping phase has a written revert story.
  - **Files:** `.cards/ (sprint tracker card_b3079e56), docs/reference/implementation-status.md`

**Demo.** An internal correctness gate, stated honestly (no user demo here by design): backlog.md regenerated with every card's section matching its jsonl status, go test ./cmd/... green with board-create riding the extracted write seam, and the MCP limitation note visible in docs.

**Exit criteria.**
- backlog.md section membership matches backlog.jsonl status for every card (release card 30026459 listed under done, not backlog)
- Board-create uses the extracted writeDefinitionAndReload helper with reload tests green
- MCP no-reload limitation + reconnect recovery + additive-edit validity documented in docs/extensions/mcp.md
- docaudit-as-DoD and rollback-note rules recorded on the tracker
- No buildDSN/pragma abstraction extracted (explicitly deferred to Phase 3 branch-a)

## Phase 2 — Card query endpoint (the sprint's early user win)

**Goal.** Give external clients a real card-resolution API before any invisible hardening runs — the smallest genuine user-facing capability, pulled forward so a late-sprint slip cannot zero out user value. This is a contract decision plus a small build, NOT 'wiring': CardQuery.Filter is a structured JSON DSL (map[string]any, types.go:404) and the board default_filter is a hard isolation boundary (service.go:577-579), so shape, scoping, and failure modes must be decided, documented, and pinned. Shape decision: POST /v1/cards/query with the filter in the request body — this deliberately declines the draft's GET ?filter= querystring, sidestepping JSON-in-URL encoding pain for the CLI/extension consumers this targets and keeping predicates out of access logs.

### Steps

- **Do:** Decide and document the contract in docs/spec/query-dsl.md and api-surface.md: POST /v1/cards/query taking {board_id?, filter, limit?}; when board_id is present the user filter is AND-ed with the board's default_filter (narrow-only — it can never widen past board isolation); without board_id it is an explicit, documented unscoped workspace query.
  - **Outcome:** query-dsl.md's 'GET /cards?filter= is not currently wired' claim is replaced by the decided POST contract; the board-scoping composition rule is written down before any code.
  - **Files:** `docs/spec/query-dsl.md (:38), docs/spec/api-surface.md`
- **Do:** Implement the endpoint through the existing DSL evaluator (buildCardWhere, the same one board default_filter and take-next already compile to SQL), reusing the established malformed-filter -> 422 error path (pattern: TestTakeNextMalformedFilterIs422 in filter_test.go) and adding a predicate depth/size bound so a pathological filter cannot cheaply monopolize the single connection.
  - **Outcome:** POST /v1/cards/query returns filtered cards; malformed filters return the structured 422; oversized/deep filters are rejected with a structured error naming the bound.
  - **Files:** `internal/httpapi/api.go, internal/httpapi/server.go, internal/core/service.go (evaluator call sites), internal/httpapi/filter_test.go`
- **Do:** Pin the contract in the same phase (do not defer to Phase 4): tests for the 422 shape, the depth bound, and — critically — that board_id + user filter intersects with default_filter and can only narrow, never widen.
  - **Outcome:** The narrow-never-widen isolation property and error shapes are regression-guarded before the Phase-4 examples and Phase-5 UI depend on the endpoint.
  - **Files:** `internal/httpapi/ (new contract-test file), docs/spec/api-surface.md`

**Demo.** From a plain curl or the CLI, resolve cards by a real structured query — e.g. find the open card whose branch field matches a PR head — instead of grepping backlog.jsonl or relying on naming conventions. The first thing this sprint ships is something an external client can use today.

**Exit criteria.**
- POST /v1/cards/query decided, documented in query-dsl.md + api-surface.md, and implemented through the existing evaluator
- Malformed filter returns the structured 422; predicate depth/size bound enforced and tested
- Contract test proves board_id + user filter can only narrow, never widen past default_filter isolation
- GET ?filter= querystring explicitly not shipped (documented as declined in query-dsl.md)

## Phase 3 — Close the SQLite concurrent-read spine

**Goal.** Measure file+WAL read-under-write through the real HTTP path, then record the P2b a/b/c verdict and un-stall the Sprint 07-12 tracker. Framing change from the draft: park-with-evidence (c) is the DEFAULT, reliability-preserving outcome — the current single-conn + _txlock=immediate design (sqlite.go:31-35) is a deliberate, documented correctness choice that closed the SQLITE_BUSY_SNAPSHOT/stale-read class, and a reader pool would re-open exactly that hazard behind an unchanged signature. Branch (a) must therefore clear a behavioral bar, not just a signature bar.

### Steps

- **Do:** Write a production-shaped (file+WAL, NOT :memory:) benchmark driving reads through the real HTTP ListCards path under concurrent writes, with the file+WAL shape asserted in the test itself, fixtures landed as a reusable package (adoptable later by docs/design/benchmark-suite.md), and a scaled-down `go test` threshold assertion (read latency under write load stays under a stated bound) so the benchmark remains a live regression guard in CI rather than rotting into documentation.
  - **Outcome:** A repeatable benchmark produces read-latency-under-write numbers; a cheap thresholded variant runs in ordinary `go test`; reusing the in-memory sqlitetest harness is structurally impossible without failing the shape assertion.
  - **Files:** `internal/sqlite/, internal/sqlite/sqlitetest, docs/design/benchmark-suite.md`
- **Do:** Record the P2b verdict (a reader-pool / b write-hold-cut / c park) as a written a/b/c decision on card_495d2e09 with the measured numbers behind it, treating (c) as the default absent evidence of a real user-felt problem.
  - **Outcome:** card_495d2e09 carries an explicit verdict with data; P3 (card_57e1bde9) is scopeable.
  - **Files:** `.cards/ (card_495d2e09)`
- **Do:** Execute the chosen branch. If (a) reader-pool: FIRST extract the buildDSN/pragma helper (deferred from Phase 1 — this is the moment a second DSN actually exists), then split writer/reader DSNs on it, and run an explicit behavioral audit of every Service method that reads after writing (start from ClaimAtomic, take-next, create-then-reload, and any handler that writes then re-reads), routing read-your-own-writes reads to the writer connection, with a race test asserting read-your-own-writes holds through the pool. If the audit turns out non-trivial, that is itself evidence for (c) — downgrade to park. If (b) write-hold-cut: move work only WITHIN UpdateCard's tx boundary; event-insert atomicity relative to the card row (sqlite.go:604-638, evs committed in one tx) is structural and must not break. If (c): close with evidence, no machinery.
  - **Outcome:** The verdict is implemented race-clean (go test -race ./internal/sqlite) or explicitly parked. If (a) lands, the read-your-own-writes behavioral contract is tested, not just doc-commented — 'signature unchanged' is NOT accepted as proof callers are unaffected.
  - **Files:** `internal/sqlite/sqlite.go (Open:28, OpenDSN:45, UpdateCard:604), internal/core/store.go, internal/core/service.go (ClaimAtomic, take-next), .cards/ (card_57e1bde9, card_c7a70b64)`
- **Do:** Update docs to reflect the outcome and close the tracker: add the single-conn known-limitation (or its resolution) to implementation-status.md and architecture/index.md, re-home the split parent card_c7a70b64, close Sprint 07-12 tracker card_b3079e56, and record the rollback note per the Phase-1 DoD rule.
  - **Outcome:** The live planning spine's open items are resolved in the same change as the docs, so 'park with evidence' cannot decay into 'park and forget'.
  - **Files:** `docs/reference/implementation-status.md, docs/architecture/index.md, .cards/ (card_b3079e56, card_c7a70b64)`

**Demo.** User-felt, not just a latency table: the live board UI stays responsive while an agent hammers writes through the API — shown side-by-side with the benchmark numbers and the a/b/c verdict landing on the tracker with evidence attached.

**Exit criteria.**
- File+WAL, HTTP-driven benchmark exists with its shape asserted in-test and a thresholded `go test` variant guarding regression
- P2b verdict written on card_495d2e09 with measured evidence; park (c) treated as a first-class success
- If (a): read-after-write call-site audit complete, read-your-own-writes race test green, buildDSN helper extracted here (not before); if (b): tx-boundary-only with event/card atomicity intact; if (c): parked with evidence
- Sprint 07-12 tracker (card_b3079e56) closed; single-conn limitation (or resolution) documented with a rollback note

## Phase 4 — Pin contracts & ship runnable extension examples

**Goal.** Pin the widest contract-bearing surfaces (OpenAPI output, REST error paths, SSE filtering) and immediately spend them on runnable composition proof — two copy-and-adapt extension examples riding the pinned contracts and the Phase-2 query endpoint. Pin-then-demonstrate in one phase keeps the tests contract-weighted (they exist because the examples ride them), not coverage-vanity.

### Steps

- **Do:** Pin apiOpenAPI structurally, not as a byte-golden (a golden of openapi.Build() output pins the generator to itself and detects change, not correctness): property-based assertions that every card type appears, every documented field has a schema entry, and error responses match the documented error envelope. Pin apiRelease and apiRemoveLink error paths (version_conflict, not-found, invalid link type) by asserting against the structured-error shape documented in docs/spec, not current bytes.
  - **Outcome:** A regression in OpenAPI generation or release/link error shapes fails a test that encodes the spec, so a latent handler bug cannot be ossified as a golden.
  - **Files:** `internal/httpapi/api.go (apiOpenAPI:182, apiRelease:250, apiRemoveLink:357), internal/openapi, docs/spec/api-surface.md`
- **Do:** Cover SSE filtering behind documented query params — filterBoardEvents (28.6%), cardInBoard (0%), cardOwnedBy (0%) — reusing the existing sse_test_hooks.go pattern and dialed-down keepalive; add no new timing assumptions. Keep contract goldens in files decoupled from /ui template internals (mirror render_golden_test.go separately); skip boilerplate (uiAsset, uiStylesheet, SetVersion).
  - **Outcome:** board_id and owner SSE filtering verified against events-history.md deterministically; the option to later demote /ui from the kernel is preserved.
  - **Files:** `internal/httpapi/sse.go (filterBoardEvents:146, cardInBoard:160, cardOwnedBy:179), internal/httpapi/sse_test_hooks.go, internal/httpapi/ (contract-test file)`
- **Do:** Build the changelog-from-cards example as a read-only diff over the sorted backlog.jsonl snapshot between two tags, grouped by type_id — a dimension every card has — NOT by the `kind` enum, which is defined only on programming-task and would dump every other type into a misc bucket that reads as broken. Optionally sub-group programming-task entries by kind. While here, fix the latent incoherence this exposes: the engineering board's presentation.style_field:"kind" points at a field 5 of its 6 card types lack (only programming-task defines `kind`) — correct or document it in the same change.
  - **Outcome:** A runnable script emits a changelog that reads sensibly on a realistic mixed board (demo proves this, not just that a fallback bucket is 'exercised'); the presentation.style_field mismatch is resolved or documented.
  - **Files:** `examples/, scripts/board.sh, .cards/backlog.jsonl, .cards/definitions/boards/engineering.json`
- **Do:** Build the PR-sync example as a GitHub-Action / CI-step variant (kind: run) — not a service-kind webhook receiver (`expose` is parsed-but-unconsumed; no reverse proxy exists). Resolve the card via the Phase-2 POST /v1/cards/query endpoint, use raw Node stdlib fetch like review-bot.mjs, keep secrets to one documented env var, and include a local harness that feeds a canned webhook payload so the example is testable in `go test`/CI without a live GitHub Action or token.
  - **Outcome:** A GitHub Action step patches the linked card on PR events via a real query API (no branch-name-carries-card-id convention ossified); the canned-payload harness proves it locally.
  - **Files:** `examples/, examples/demo-workspace/.cards/ext/review-bot.mjs (pattern), .cards/ (card_fa6d5c2f, card_469c93e2)`
- **Do:** Flip the associated doc-status anchors in the SAME change: docs/using-cards.md 'Pull requests'/'Changelogs' to built, docs/extensions/index.md worked examples, implementation-status.md anchors; record the rollback note per the Phase-1 DoD rule.
  - **Outcome:** docaudit tests (TestImplStatusAnchorsResolve, TestImplStatusBoundaryCommit) pass; no stale [proposed] claim remains.
  - **Files:** `docs/using-cards.md, docs/extensions/index.md, docs/reference/implementation-status.md`

**Demo.** Run the PR-sync harness on a canned payload and watch the card move on the live board via the query endpoint; generate a changelog between two tags that reads cleanly on the mixed dogfood board; and show that the OpenAPI document a client downloads is now structurally guaranteed to match the spec.

**Exit criteria.**
- apiOpenAPI pinned structurally (no self-referential byte-golden); apiRelease/apiRemoveLink error paths pinned against spec docs
- SSE board/owner filtering covered without new timing assumptions; contract goldens decoupled from /ui internals
- changelog-from-cards groups by type_id and reads sensibly on a mixed board; the engineering board's presentation.style_field:"kind" incoherence resolved or documented
- PR-sync ships as a GitHub-Action variant with one secret env var, card resolution via /v1/cards/query, and a local canned-payload harness
- All doc-status anchors flipped in-commit with rollback notes; docaudit green

## Phase 5 — Schema-authoring UI (create-first, additive-safe)

**Goal.** Make 'one schema drives every transport' tangible: author card-type schemas from the web UI. Internally ordered so a mid-phase cut still ships the core slice — create-a-new-type lands before any edit capability. The mutability invariant is framed on the EXISTING versioning model (cards carry schema_version; Service.UpgradeSchema at service.go:937 re-pins one card at a time, forward-only) rather than a parallel invented migration concept, and the diff check lives in the write path, not the loader.

### Steps

- **Do:** Decide and write down the versioning/mutability contract BEFORE any form code, in terms of the existing model: (1) creating a type starts at schema_version 1; (2) an additive edit (new optional field WITH a default — a hard rule; no removal, no narrowing, no required-without-default) bumps schema_version; (3) existing cards stay pinned at their old version and the new field is documented as absent until re-pinned via UpgradeSchema on next explicit touch — mixed-version reads are a documented, valid state, and because additions are optional-with-default, mixed-version cards validate everywhere (this same property is what keeps a stale MCP agent's in-flight writes valid); (4) breaking edits are out of scope and rejected. No bulk/background migration is built or implied — this resolves, rather than contradicts, the out-of-scope line.
  - **Outcome:** A one-page contract in card-definitions.md defines edit -> schema_version -> UpgradeSchema semantics and the mixed-version read story; the migration engine is honestly deferred with the seam named.
  - **Files:** `docs/reference/card-definitions.md, internal/core/service.go (UpgradeSchema:937), docs/reference/implementation-status.md`
- **Do:** Implement the card-type write path on the Phase-1 writeDefinitionAndReload helper, with the old-vs-new additive-only diff check in that helper — NOT in validateCardType, which validates a single CardType in isolation and also runs at cold startup, so a diff rule there would wrongly reject hand-edited definitions/ files on boot. validateCardType stays a pure single-def validator (it does gain the unconditional 'new-in-def optional fields must carry defaults' style rules that hold for any single def). Structured errors reuse the same error-envelope convention Phase 4 pinned (note: Phase 4 pins release/link/OpenAPI shapes, not this endpoint — the dependency is the convention, not an existing golden), and this endpoint gets its own contract test.
  - **Outcome:** POST (create) and PATCH (additive edit) card-type validate -> diff-check -> write -> reload atomically on the shared mutex semantics; an unsafe edit is rejected with a structured error naming the offending field and why; contract test pins the shapes.
  - **Files:** `cmd/cards/reload.go (writeDefinitionAndReload), internal/httpapi/api.go, internal/config/config.go (validateCardType:228 — unchanged role), internal/core/types.go`
- **Do:** Build the authoring form (server-rendered templates + Alpine.js, board_create.html pattern, role-token contract) covering the 10 field types and their extra keys (options, item_fields, artifact_policy, option_themes with the 4.5:1 contrast floor) — WITH the UX that makes it usable, not just the happy path: (1) additive-only rejections surface as human-readable in-form messages tied to the offending field, including the reason and the affected-card count (queried via the Phase-2 endpoint), never a raw 409; (2) a designed empty/first-run state for the type list; (3) inline field-level validation feedback while authoring; (4) the form obeys the design-system's WYSIWYG edit-geometry principle (edit chrome, not geometry), not only the role tokens.
  - **Outcome:** A user creates a card type in the UI and understands every rejection without reading an API error; go test ./internal/httpapi passes (template parse); no new build step.
  - **Files:** `internal/httpapi/templates/ (new authoring template + style.css role tokens), docs/architecture/design-system.md`
- **Do:** Ship the MCP staleness signal as the concrete, minimal contract change it actually is — stdio MCP has no server-push channel, so a 'handshake to a live session' is impossible; instead echo the workspace schema_version in every tool result so a client can detect drift on its next call, and document the agent-facing behavior: on mismatch, re-introspect or reconnect; in-flight writes remain valid because edits are additive-optional-default. If the echo proves invasive mid-phase, fall back to docs-only (the Phase-1 note) and say so — do not build a pretend push channel.
  - **Outcome:** A live MCP session gets a defined, detectable drift signal with a documented recovery action, and the property that keeps a stale agent's writes valid is an enforced invariant (the diff check), not an accident.
  - **Files:** `internal/mcp/mcp.go, docs/extensions/mcp.md`

**Demo.** Create a brand-new card type in the web UI and immediately create a card of it — one authored schema drives the web form, CLI, API response, and regenerated OpenAPI. Then make an additive edit and show BOTH sides of the invariant: an existing card reads back valid at its old schema_version, and an unsafe edit (remove a field in use) is rejected in-form with the reason and affected-card count.

**Exit criteria.**
- Versioning contract (edit -> schema_version bump -> pinned old cards -> UpgradeSchema re-pin path) written in card-definitions.md before form code; mixed-version reads documented as valid
- Additive-only diff check enforced in writeDefinitionAndReload (loader untouched); new-field-optional-with-default is a hard rule; unsafe edits rejected with structured errors and a contract test
- Form covers all 10 field types with contrast floor, in-form rejection copy with affected-card counts, empty state, inline validation, and WYSIWYG edit-geometry conformance
- schema_version echoed in MCP tool results with documented re-introspect/reconnect behavior (or an explicit documented fallback to docs-only)
- Create-only slice demonstrably shippable on its own if the edit slice is cut

## Deliberately out of scope

- GET /v1/cards?filter= as a querystring surface — deliberately declined in favor of POST /v1/cards/query (JSON-in-URL encoding pain, predicate leakage into access logs); the decline is documented in query-dsl.md so it reads as a decision, not an omission.
- AUTH implementation (--auth token bearer-token impl card_350b1bac and the first-CLI-user-is-admin stopgap card_2680d5f7). At most, demote auth.md's unbuilt token/proxy modes to design-exploration status. Kernel auth stays host/extension territory (P7).
- The outbox/tailer and a webhooks extension — gated by outbox-gonogo.md as 'infrastructure ahead of need'; PR-sync rides the existing crash-safe event log without it.
- A service-kind webhook receiver / making the `expose` field real — the GitHub-Action shape is the blessed integration pattern; `expose` stays documented as reserved.
- Any bulk, background, or lazy-on-read schema migration — the Phase-5 contract deliberately pins existing cards at their old schema_version with documented absent-until-re-pinned semantics (the cheaper of the two honest options); a general migration engine and auto-re-pin are named future work on the UpgradeSchema seam.
- MCP hot-reload of tool schemas mid-session — the shipped scope is drift DETECTION (schema_version echo) plus documented reconnect; live re-registration is a future retrofit on the same seam.
- A speculative buildDSN/pragma abstraction ahead of need — extracted only inside Phase 3 branch-(a), where a second DSN first exists.
- The work_cards / @work-cards client convenience library — extension examples stay raw-HTTP/CLI like review-bot.
- The full cards-bench suite (docs/design/benchmark-suite.md) — this sprint lands only the P2b benchmark's fixtures in an adoptable shape.
- A formal per-phase debt-paid/debt-added ledger (reviewer suggestion, declined as ceremony) — its substance is captured concretely instead: the speculative pragma extraction was cut, park-with-evidence is Phase 3's default, and the reader-pool branch carries an explicit behavioral-audit bar.
- Drag-drop lane reordering (card_d44d3e0d) and broader home/nav polish beyond what the schema-authoring templates require.

## Risks & mitigations

- Benchmark validity (Phase 3): the a/b/c verdict is only sound if the benchmark is file+WAL and HTTP-driven; the file+WAL shape is asserted in the test itself so the faster in-memory sqlitetest harness cannot silently substitute.
- Reader-pool behavioral hazard (Phase 3): a reader connection sees only the last committed WAL snapshot, so read-after-write flows (ClaimAtomic, take-next, create-then-reload) can miss their own writes behind an unchanged signature — the current single-conn + _txlock=immediate design exists precisely to close this class. Mitigated by making park the default, requiring the call-site audit + race test for branch (a), and treating a non-trivial audit as evidence for parking.
- Write-hold-cut correctness (Phase 3 branch b): UpdateCard commits card row + events in one tx (sqlite.go:604-638); the branch is scoped tx-boundary-only so event-vs-card atomicity is never broken.
- Query-endpoint isolation (Phase 2): CardQuery.Filter doubles as the board isolation boundary; shipping the endpoint without the narrow-never-widen test would let a user filter escape board scoping. The test is an exit criterion, not a follow-up.
- Schema-edit data hazard (Phase 5): mixed schema_version cards after an additive edit are now a DOCUMENTED, contract-tested state (new-field-optional-with-default hard rule) instead of an accident — but the contract must land before the form; building the form first re-opens the sprint's biggest scope-collapse risk.
- MCP staleness recovery (Phase 5): the schema_version echo makes drift visible but recovery is still reconnect; an agent that ignores the signal degrades to structured validation errors, which remain retriable but noisy. Additive-optional-default is what keeps its writes valid — that invariant is enforced, not assumed.
- Phase-4 breadth: pin-then-demonstrate makes it the fattest phase; if it slips, the contract tests land first (they gate Phase 5) and the changelog example is the cuttable tail — PR-sync stays because it is the composition proof the sprint is named for.
- Doc/status drift: any [proposed]/[built] flip must update implementation-status.md anchors in the same commit (docaudit-enforced) and carry a rollback note per the Phase-1 DoD rule, so a mid-sprint cut cannot strand half-flipped claims.
- Momentum: user value now fronts the sprint (Phase 2) instead of trailing it, so a late slip in the SQLite or schema-UI tails no longer zeroes out what users see; Phase 3 stays decision-shaped to force the P2b call early.
