# Sprint plan 2026-07-22 — Solid Substrate, Demonstrated Composition

> Produced by the /sprint-plan workflow (survey → align → strategy → per-candidate
> investigation → draft → 3-lens review → revise → refine), 2026-07-22.
> Revised after code/doc verification, 2026-07-22.
> Board tracker: **`card_4c5326d4`** (`TRACKER: Sprint 07-22`) in `.cards/`.

## Theme

Ship one real user-facing capability early — a decided, tested **POST /v1/cards/query** endpoint — then **measure and decide** the one kernel question extensions cannot own (SQLite read-under-write on file+WAL, where **park with evidence** is a first-class success). Spend the rest of the sprint on **one runnable composition proof** (PR-sync via the query API). Everything else is **stretch** or **follow-up**, labeled honestly so a mid-sprint cut still leaves user value and a closed P2b verdict.

## Committed vs stretch

| Commitment | Phase | User-visible? |
|---|---|---|
| POST `/v1/cards/query` + contract tests | 2 | Yes |
| File+WAL measurement, P2b verdict, docs; park default; close 07-12 tracker | 3 | Decision + docs (demo conditional on verdict) |
| PR-sync example + local harness + `implementation-status.md` anchors | 4 | Yes (composition) |
| MCP no-reload docs; sprint DoD on tracker | 1 | Enabling |

**Stretch (cut without failing the sprint):** OpenAPI/release/link structural pins; SSE filter contract tests; changelog-from-cards script; full schema-authoring UI; additive card-type PATCH; MCP drift echo; reader-pool / write-hold implementation (Phase 3 only if predeclared threshold + capacity); `writeDefinitionAndReload` extraction (when a second definition-write caller lands).

## Phase 1 — Foundation hygiene

**Goal.** Cheap enabling work: document MCP staleness, record sprint DoD on the **07-22** tracker. **Dropped:** backlog.md regeneration — `30026459` is already under `### done` in `.cards/backlog.md`; `scripts/board.sh` only exports JSONL (`cmd_export`), it does not generate markdown (overview is produced separately, e.g. pi-cards export / documented markdown sync). **Deferred:** extracting `writeDefinitionAndReload` until Phase 5 stretch adds a second caller (`POST /v1/card-types` on the reload seam); board-create stays as today (`cmd/cards/reload.go:275-347`). **Deferred:** buildDSN/pragma abstraction until Phase 3 stretch branch-(a), if ever.

### Steps

- **Do:** Document the `cards mcp` no-reload limitation: MCP tool schemas are fixed at process start (`cmd/cards/serve.go` → `mcp.New`, ~217); stdio MCP has no server-push channel, so definition edits while a session is live do not update tools — recovery is reconnect. Note that agents omitting fields introduced after connect may still succeed on patch when those fields are optional with defaults (today's validation uses the loaded type, not a live drift signal).
  - **Outcome:** `docs/extensions/mcp.md` states staleness, reconnect recovery, and the practical write behavior.
  - **Files:** `docs/extensions/mcp.md`, `cmd/cards/serve.go`
- **Do:** Record two definition-of-done lines on sprint tracker **`card_4c5326d4`**: (1) any flip of a `[proposed]`/`[built]` anchor in `implementation-status.md` lands in the **same commit** (`TestImplStatusAnchorsResolve` in every `go test`; boundary lag strict under `-tags=strictdoc` / docaudit job); (2) any committed phase that flips anchors includes a one-line rollback note.
  - **Outcome:** DoD visible on the 07-22 tracker; **`card_b3079e56`** (07-12 tracker) is referenced only when closing the SQLite spine in Phase 3, not for 07-22 DoD.
  - **Files:** `.cards/` (`card_4c5326d4`), `docs/reference/implementation-status.md`

**Demo.** No user demo (by design): MCP limitation visible in docs; DoD on the correct tracker.

**Exit criteria.**
- MCP no-reload + reconnect documented in `docs/extensions/mcp.md`
- DoD lines on **`card_4c5326d4`**
- No speculative `writeDefinitionAndReload` or buildDSN extraction in Phase 1

## Phase 2 — Card query endpoint (early user win)

**Goal.** Smallest genuine external capability: **POST /v1/cards/query** with JSON `filter` (declines GET `?filter=` — encoding and log leakage). Not mere wiring: `CardQuery.Filter` is `map[string]any` (`internal/core/types.go:406`); compilation lives in **`internal/sqlite/filter.go`** / `buildCardWhere`; `Service.ListCards` applies board scope via **`applyBoardScope`** (`internal/core/service.go:568-586`) — when `board_id` is set, that includes **`card_type_ids`**, board **`columns`** (status scope when caller did not set status), **and** AND-composition with the board's **`default_filter`** when present (narrow-only for the DSL part). Dogfood `engineering` has no `default_filter`; contract tests should use a fixture board that has one (e.g. patterns in `TestBoardDefaultFilterScope` in `internal/core/service_test.go`).

### Steps

- **Do:** Decide and document in `docs/spec/query-dsl.md` and `api-surface.md`: POST `/v1/cards/query` with `{board_id?, filter, limit?, include?, sort?, cursor?}` — explicitly defer or include pagination/sort/include in the contract; minimal committed body is `{board_id?, filter, limit?}` plus board scoping rules above.
  - **Outcome:** POST contract replaces the narrative that only GET list + take-next consume the DSL; GET `?filter=` documented as declined.
  - **Files:** `docs/spec/query-dsl.md`, `docs/spec/api-surface.md`
- **Do:** Implement a thin HTTP handler → `Service.ListCards` with `Filter` in the JSON body; add predicate **depth/size bounds** in the compiler path (none exist today in `filter.go`). Reuse malformed-filter → **422** (`validation_failed`), same as `TestTakeNextMalformedFilterIs422` in `internal/httpapi/filter_test.go`.
  - **Outcome:** Endpoint works; pathological filters rejected with a structured error naming the bound.
  - **Files:** `internal/httpapi/api.go`, `internal/httpapi/server.go`, `internal/sqlite/filter.go`, `internal/httpapi/filter_test.go`
- **Do:** Contract tests in the same phase: 422 shape, depth bound, and HTTP pin on board scoping (transport layer on top of existing service tests for `default_filter` AND semantics).
  - **Outcome:** Phase 4 PR-sync can depend on this endpoint without a follow-up isolation fix.
  - **Files:** `internal/httpapi/` (new contract tests)

**Demo.** **curl** (or an explicit new CLI subcommand if added — `cards list` does not accept filter JSON today): resolve cards by structured query (e.g. branch field match for PR-sync).

**Exit criteria.**
- POST `/v1/cards/query` documented and implemented via `ListCards` + sqlite filter compile
- Malformed filter → structured 422; depth/size bound tested
- Contract test for `board_id` scoping (types/columns/default_filter composition)
- GET `?filter=` declined in query-dsl.md

## Phase 3 — SQLite concurrent-read decision (measurement default)

**Goal.** **P3a committed:** file+WAL measurement, written a/b/c verdict on **`card_495d2e09`**, document single-conn + `_txlock=immediate` (`internal/sqlite/sqlite.go:28-36`, `MaxOpenConns(1)` — WAL enables multi-conn *when* pool > 1; production intentionally stays at 1). **Default outcome: (c) park with evidence.** **P3b stretch:** reader-pool or write-hold work on **`card_57e1bde9`** only if a **predeclared product threshold** is met *and* sprint capacity remains; otherwise close P3b as parked and link to P2b numbers. **Not in this phase:** `card_c7a70b64` (unrelated split parent). **Close** Sprint 07-12 tracker **`card_b3079e56`** when P2b/P3 are resolved, with pointer to 07-22 adoption.

**Branch notes (stretch only).** **(a)** reader-pool: requires buildDSN split, full read-your-own-writes audit (ClaimAtomic, take-next, write-then-read handlers), race tests — non-trivial audit ⇒ park. **(b)** write-hold-cut: 07-12 cards describe shortening hold by restructuring side effects; card+events today share one tx (`UpdateCard`, `sqlite.go:604-638`) — meaningful cut is high-risk; treat as non-default or drop from a/b/c and use **a vs c** only.

### Steps

- **Do:** Production-shaped benchmark: tempdir **`sqlite.Open`** (assert DSN includes `journal_mode(WAL)` and single-conn policy), optionally wrapped with HTTP `ListCards`; record p50/p95 on **`card_495d2e09`**. **PR CI:** shape/invariant tests only — **no hard-fail on absolute ms** (see `docs/design/benchmark-suite.md` §4). Nightly/manual may record metrics to logs or `bench-baseline.json`.
  - **Outcome:** Repeatable numbers; sqlitetest memory harness cannot satisfy file+WAL assertion.
  - **Files:** `internal/sqlite/`, `docs/design/benchmark-suite.md`; fix misleading sqlitetest comments if they claim P2b topology.
- **Do:** Write verdict **(a)/(b)/(c)** with data on **`card_495d2e09`**; execution status on **`card_57e1bde9`** links to P2b. Pre-register: sustained write load with one conn queues reads — **(a)** only if measured latency crosses an explicit threshold stated before the run.
  - **Outcome:** P3b scopeable or explicitly parked.
  - **Files:** `.cards/` (`card_495d2e09`, `card_57e1bde9`)
- **Do:** Update docs: single-conn limitation (or resolution if stretch (a) landed) in `implementation-status.md` and a Storage subsection in `docs/architecture/index.md`; rollback note on **`card_4c5326d4`**; close **`card_b3079e56`**.

**Demo.** If **(c)** (expected): benchmark numbers + documented limitation — UI/list traffic still shares the writer connection; do not promise responsiveness under hammer writes unless **(a)** shipped. If **(a)** stretch lands: before/after side-by-side.

**Exit criteria.**
- File+WAL measurement with in-test shape assertion; PR CI correctness-only (no ms threshold gate)
- Verdict on **`card_495d2e09`**; park **(c)** valid; **`card_b3079e56`** closed
- Docs updated with rollback note
- Reader-pool implementation **not** required for sprint success

## Phase 4 — PR-sync composition (committed) + contract stretch

**Goal.** **Committed:** one runnable **PR-sync** example (GitHub Action / `kind: run` step — not `expose` webhook receiver): resolve card via Phase 2 **POST /v1/cards/query**, Node stdlib fetch like `review-bot.mjs`, one documented secret env var, **local canned-payload harness** (pattern: `scripts/review-bot_test.sh` / `cmd/cards/reviewbot_test.go`). Flip **`implementation-status.md`** guard anchors in the same commit (docaudit — not prose-only “built” in `using-cards.md`); update tracker cards **`card_fa6d5c2f`**, sync **`card_469c93e2`** if changelog stretch ships.

**Stretch:** structural OpenAPI pins (`apiOpenAPI`, release/remove-link errors); SSE `filterBoardEvents` / `cardInBoard` / `cardOwnedBy` contract tests (behavior largely built — test debt); **changelog-from-cards** script grouped by **`type_id`** (not `kind` — only programming-task defines `kind`); **`presentation.style_field: "kind"`** on engineering is **valid** (load requires field on ≥1 type; cards without `kind` fall through per `docs/design/style-field.md` — document, do not “fix” board JSON unless product wants a change).

### Steps (committed)

- **Do:** Build PR-sync under `examples/` with local harness; document in `docs/extensions/index.md` and `docs/using-cards.md` Pull requests section as narrative (anchors live in `implementation-status.md`).
  - **Files:** `examples/`, `card_fa6d5c2f`, `docs/reference/implementation-status.md`, `docs/extensions/index.md`

**Stretch steps (optional)**

- OpenAPI structural assertions; SSE contract tests; `scripts/changelog.sh` + **`card_469c93e2`** body updated to type_id grouping.

**Demo.** Run PR-sync harness on canned payload; card updates via query API.

**Exit criteria (committed).**
- PR-sync example + harness; query-based card resolution; implementation-status anchors + rollback note; docaudit green

**Exit criteria (stretch).**
- OpenAPI/SSE/changelog items as above

## Phase 5 — Schema-authoring UI (stretch)

**Goal.** **Stretch only:** make “one schema drives every transport” tangible for **create-new-type** from the web UI. **Not committed this sprint:** additive PATCH, in-form affected-card counts, MCP per-result drift echo, full 10-field form polish.

**Why edit is deferred:** Today `PatchCard` validates against the **current** loaded `CardType` and `validateFields` can **inject defaults** for missing fields (`internal/core/validate.go:41-59`) without bumping `Card.schema_version`. Normative docs (`card-definitions.md`, spec) describe **pinned** versions and **`UpgradeSchema`** with **`migrations[N].field_defaults`**. A honest additive-edit UI requires **core** version-aware validation plus auto-authored migration steps — not loader-only diff rules.

**If stretch proceeds:** (1) Write create-only contract in `card-definitions.md` (schema_version 1 on create; breaking edits out of scope). (2) **`POST /v1/card-types`** on the **reload seam** (`reloadableApp.ServeHTTP`, same pattern as `POST /v1/boards` — **not** inner `httpapi` router); extract **`writeDefinitionAndReload`** when this second caller exists. (3) Create-only form (board_create.html pattern, role tokens). (4) MCP: **docs-only** drift recovery (reconnect + re-call `workspace` tool); no workspace-level `schema_version` echo — versions are per-type (`current_schema_versions` on GET `/v1/workspace`); static MCP cannot see on-disk reload without new fingerprint wiring.

**Demo (stretch).** Create a new type in UI; create a card of that type.

**Exit criteria (stretch).**
- Create-only path on reload seam; docs contract; templates parse under `go test ./internal/httpapi`

## Deliberately out of scope

- GET `/v1/cards?filter=` — declined in favor of POST `/v1/cards/query` (documented in query-dsl.md).
- AUTH implementation (`card_350b1bac`, `card_2680d5f7`); kernel auth stays host/extension territory.
- Outbox/tailer / webhooks extension (`docs/design/outbox-gonogo.md`).
- Service webhook receiver / making `expose` real — GitHub Action pattern is blessed.
- Bulk/lazy schema migration engine; auto-re-pin beyond existing **`UpgradeSchema`**.
- MCP hot-reload of tool schemas mid-session — reconnect + docs.
- buildDSN extraction except Phase 3 stretch **(a)**.
- `@work-cards` client library — examples stay raw HTTP/CLI.
- Full `cards bench` suite — only P2b-shaped fixtures adoptable from Phase 3.
- Per-phase debt ledger (declined as ceremony).
- Drag-drop lane reorder (`card_d44d3e0d`) and nav polish beyond stretch UI.
- Regenerating `.cards/backlog.md` as a Phase 1 task (already consistent; wrong tool cited).
- **`card_c7a70b64`** in SQLite closeout.

## Risks & mitigations

- **Benchmark validity (Phase 3):** assert file+WAL in test; do not use `:memory:` or sqlitetest as P2b ground truth.
- **Reader-pool (stretch):** WAL snapshot hazard on read-after-write; park default; audit + race tests mandatory for **(a)**.
- **CI flakiness:** no absolute latency gates in PR CI (`benchmark-suite.md`).
- **Query isolation (Phase 2):** document full `applyBoardScope`; HTTP contract tests before PR-sync.
- **Schema UI scope creep (Phase 5 stretch):** do not ship edit UI before pinned-version validation + migrations authoring in core.
- **MCP staleness:** reconnect is the recovery path; tool schema drift is the real agent pain, not every write.
- **Phase 4 slip:** PR-sync stays; changelog/OpenAPI/SSE are stretch tails.
- **Doc drift:** anchors in `implementation-status.md` same commit; rollback notes on **`card_4c5326d4`**.
- **Momentum:** Phase 2 fronts user value; Phase 3 decision without mandatory pool ship preserves sprint success.
