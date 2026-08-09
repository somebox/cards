# Sprint plan 2026-08-08 — Drive it, then build against it

> Produced by the /sprint-plan workflow (ground → candidates → falsification → plan),
> 2026-08-07 and 2026-08-08, revised after human review both times.
> Covers **two sprints in order**. No board tracker card — sprint planning lives here,
> in markdown; the board carries only work we are actually planning to do.

## Theme

Two consecutive sprints, each with one sentence a user could repeat back.

**Sprint A — an agent can drive the board from a shell in one round-trip.** Today an
agent leaving a comment must know that the word `add` is mandatory, and any agent
changing a card must fetch it first purely to learn its version number, so every write
costs two round-trips. Reading the board back returns full JSONL that burns the agent's
context.

**Sprint B — you can build a client against Cards without reading Go.** Today the health
endpoint answers `"poc"` on every build including tagged releases, so a launcher cannot
tell what it is talking to; precise questions ("which cards are on branch X?") cannot be
asked over HTTP at all; and the machine-readable API description exists only inside a
running server, so step one of writing a client is starting the server you have not
written a client for yet.

A third pass — consolidating the two competing "what's actually built" reference docs —
is sequenced **after both**, because Sprint A and Sprint B change the things those docs
describe. Writing it down first means writing it twice.

## Sprint A — agent CLI ergonomics

Committed in `71c65d4`. Ship order, and the cut rule.

| # | Card | Commitment |
|---|---|---|
| 1 | `0f7be7f6` | `cards comment <id> --body` alias + `comment --help` |
| 2 | `069ec1d1` | `--if-match latest` for patch / claim / release, service-side |
| 3 | `c0102825` | `--md` output mode for get / list — **shrink valve** |

**Order.** `0f7be7f6` first: no core changes, and it establishes the CLI-test pattern the
others reuse. `069ec1d1` second: the only item touching the optimistic-concurrency write
path and the HTTP contract. `c0102825` last, and it is the item to cut — the theme
sentence is about *writes*, and `--md` is the read half.

### Decisions locked on the cards

- **`0f7be7f6` is the comment alias alone.** It originally bundled repeating-field entry
  sugar; that is scoped out until `069ec1d1` lands, so there is one version opt-in to
  point at rather than two. If the sugar returns, its flag-to-entry mapping must derive
  from the card type's `FieldDef.item_fields` rather than hardcoding `work_log` knowledge,
  and it must not silently fetch a version.
- **`069ec1d1` re-reads service-side, under the write lock** — not a CLI GET-then-write,
  which would give the CLI a concurrency story HTTP does not share. Spelling is
  `--if-match latest`: bare `--force` collides with `cards release --force`
  (transition bypass, `internal/cli/commands.go:258`), and `--version latest` overloads
  the CAS argument with a sentinel. Concurrent latest-writes still 409 — no
  last-write-wins, ever. `release` is in scope alongside patch and claim because it
  carries the same hard `--version` requirement (`commands.go:265-266`) and is the verb
  agents hit at the end of every work item. `docs/spec/api-surface.md` changes for all
  three endpoints; `api_change` stays additive.
- **`c0102825` ships `--md` as a documented, golden-tested contract or not at all.**
  Best-effort pretty text is how output surfaces rot — consumers pin to it either way.
  No new `cards show` verb; `get --md` is enough. Default JSONL output is untouched. The
  real work is extracting `cardMarkdown` off the TUI model (`internal/tui/tui.go:1749`,
  ~139 lines reading `m.types` / `m.columnName` / `m.legalTargets` / `m.linkLabel`), so
  `go test ./internal/tui` is part of the gate — TUI/CLI render drift is the risk.

## Sprint B — HTTP integration surface

Ship order, and the cut rule.

| # | Card | Commitment |
|---|---|---|
| 1 | `fc92019c` | Health returns version / workspace_id / config_digest / db_path |
| 2 | `3fd62d32` | `POST /v1/cards/query` — **shrink valve** |
| 3 | `8afb9008` | Publish OpenAPI + `cards openapi` |

**Order is not arbitrary.** Both `fc92019c` (health schema at
`internal/openapi/openapi.go:217-224`) and `3fd62d32` (new route, trips
`TestOpenAPICoversEveryRoute`) force an `openapi.go` edit. If `8afb9008` landed first,
the committed artifact would be regenerated twice mid-sprint and the sprint would publish
a stale spec, twice. Publishing **last** means one regeneration and a correct spec on the
day it is published. Health goes first because it is smallest, establishes the
`httpapi.New` options plumbing, and forces the cheap `openapi.go` edit early rather than
beside the risky one.

**If capacity forces a cut:** ship `fc92019c` + `8afb9008` and leave `3fd62d32`
unstarted. Do **not** cut OpenAPI publication to save the query endpoint — health fixes a
documented contract that a tagged release currently violates, and publication is the
sprint's headline; the query endpoint is new capability that no existing client depends
on (`docs/spec/query-dsl.md` documents the DSL as not wired over HTTP today).

**Start gate:** do not begin until Sprint A's three cards have left `todo`. `069ec1d1`
rewrites `docs/spec/api-surface.md` for three endpoints that this sprint documents again.

### Decisions locked on the cards

- **`fc92019c` — four keys, named:** `version`, `workspace_id`, `config_digest`,
  `db_path` (absolute). `docs/architecture/index.md:376` promises four *concepts*, not
  names. `workspace_id` stays singular (`docs/design-notes.md:35-38` records that as
  deliberate).
- **`fc92019c` — `version` stays on `SetVersion`; the options carry digest and path
  only.** `SetVersion` is a package var because the version is immutable per process. The
  config digest is not: every reload builds a fresh `Server` (`cmd/cards/reload.go:173`),
  so a package var would serve a stale digest after a reload — defeating the field's whole
  purpose. Per-`Server` state via variadic functional options, matching the existing
  `core.ServiceOption` / `WithClock` / `WithBus` pattern (`internal/core/service.go:64-82`).
  A variadic parameter is source-compatible, so of 17 `httpapi.New` call sites only the 3
  production ones change.
- **`fc92019c` — both values arrive precomputed from the composition root.** `core.Store`
  gains no `Path()`; `sqlite.Store` deliberately drops the path after building the DSN
  (`internal/sqlite/sqlite.go:37`). `resolveWorkspaceDir` already returns an absolute path
  and `dbPath(dir)` joins onto it. The digest is computed **once at Server construction**,
  after a successful `Load` — never per request, or health would report changes the server
  has not adopted. Document it as a *change detector*, not an identity of the loaded config.
- **`fc92019c` — the port promise is a doc bug, not a missing field.**
  `docs/architecture/index.md:378` says launchers read the selected port from health,
  which cannot work: you need the port to call it. A launcher binds a chosen port (or lets
  the OS assign one and reads it off the listener), then calls health to confirm workspace
  identity. Health is identity and liveness, not port discovery.
- **`3fd62d32` — a board is a view, not a fence.** Cards are the source of truth; boards
  are views on top of them, parts of one workspace rather than things handed out
  separately. So a board's column and type lists are query *defaults*, and naming
  `type_id` or `status` explicitly replaces them — correct behavior for a view. Only
  `default_filter` is an unconditional AND. The card's original acceptance promised
  "narrow-never-widen board isolation" across all three legs, which describes a fence;
  it is rewritten to state the real behavior and pin it with a test, because a client
  author needs to know it and nothing tells them today. `applyBoardScope` is **not**
  changed here: doing so would move `GET /v1/cards`, MCP `list` and the TUI, and the
  naive fix widens scope to the whole workspace (an empty `TypeIDIn` is read by the store
  as *no filter* — `internal/core/service.go:1524-1526`).
- **`3fd62d32` — no complexity bounds, no request-body cap.** An earlier draft locked
  filter depth/node/array limits and a `MaxBytesReader` cap. Both removed: this is a
  local, single-tenant, trusted service, and hardening it against a hostile caller is
  permission theater (philosophy principle 7). Queries stay simple — filters, pagination,
  basic string matching. A deployment needing limits puts a proxy in front or writes an
  extension.
- **`3fd62d32` — `GET ?filter=` is declined for encoding and log leakage**, not merely
  "not wired". The rationale existed only in the 07-22 plan; it moves into the spec so the
  decline is not a naked editorial claim.
- **`8afb9008` — raw committed JSON plus a short explainer page.** Swagger UI is decided
  against: `mkdocs.yml` has no `plugins:` key today, and adding one silently drops the
  implicit `search` plugin unless re-declared. Non-markdown files under `docs_dir` are
  copied verbatim, so `docs/reference/openapi.json` publishes with zero config, no nav
  entry, and no Go toolchain in `deploy-pages.yml`.
- **`8afb9008` — one drift-guard test with a `-update` flag, no generator script.**
  `openapi.Build` is already a pure function of definitions (needs only workspace + types;
  `config.Load` opens no SQLite), so a shell script wrapping a CLI wrapping a library plus
  a separate guard test is three artifacts doing one job. Mirror
  `internal/httpapi/render_golden_test.go:20-23`, which carries the regenerate command in
  its failure message. `cards openapi` and the guard must share one exported byte-producer
  or they will disagree byte-for-byte.
- **`8afb9008` — the published spec is the demo workspace's.** Routes, envelope and error
  shapes are universal; **type schemas are not**. A reader who assumes `programming-task`
  is part of the API contract will build the wrong client, so the explainer page must say
  so — an acceptance item, not a footnote.

## Sprint C (sequenced, not yet scoped) — one code-verified reference

`cddf3086` (consolidate the two competing reference docs) + `2f12f16f` (now seven factual
drifts) are the coupled core. `51c0facf` and `460ff3ec` are detachable doc-only cleanups.

Two constraints already recorded on those cards: Sprint B does **not** auto-resolve any of
`2f12f16f`'s drifts — re-audit against HEAD after it lands. And `2f12f16f`'s tag-param item
edits `docs/spec/query-dsl.md`, the same file `3fd62d32` edits, so land `3fd62d32` first.

The seventh drift added 2026-08-08: `docs/spec/data-model.md:198-199` claims
`card_type_ids` "is merged into `default_filter` as `type_id $in [...]`". The code treats
it as a default, not a hard AND. Per the board-is-a-view decision above, the code is right
and the spec moves.

## Deliberately out of scope

- Changing `applyBoardScope` to intersect. A behavior change to shipped endpoints; needs
  its own card and a changelog entry.
- Rate limits, body caps, filter complexity bounds. See principle 7.
- A mkdocs job in the PR gate. `mkdocs build --strict` runs only on push to main
  (`deploy-pages.yml:33`), so a broken docs reference fails the deploy and the last good
  site keeps serving — a docs-only fix-forward. Adding a Python toolchain to every PR
  deserves its own decision.
- Repeating-field entry sugar for comments; MCP and HTTP client ergonomics; any TUI
  behavior change beyond what the `--md` renderer extraction strictly requires.

## Risks & mitigations

- **`c0102825`'s renderer extraction drifts TUI and CLI output.** Mitigation: golden
  fixtures on both sides, `go test ./internal/tui` in the gate.
- **`069ec1d1` widens into a last-write-wins door.** Mitigation: the opt-in never becomes
  the default; a `-race` two-writer test asserts the loser still gets `version_conflict`.
- **`3fd62d32` half-merges.** `TestOpenAPICoversEveryRoute` fails in both directions, so a
  documented phantom route or an undocumented real one turns CI red for everyone.
  Mitigation: route + `paths()` entry + the `implementation-status.md` endpoint table land
  in one commit.
- **Card line citations drift.** Five wrong `file:line` references were found and corrected
  across these cards during planning. Verify a citation before trusting it; find functions
  by name.
