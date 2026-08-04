# Changelog

All notable changes to Cards are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versioning follows
[Semantic Versioning](https://semver.org/); while pre-1.0, a **minor** bump
(0.x) may contain breaking changes and a **patch** bump (0.x.y) is reserved for
backwards-compatible fixes.

## [Unreleased]

## [0.3.0] - 2026-08-04

Minor release under pre-1.0 semver: several definition-load and API contract
tightenings (see **Changed**). Boards that declare `transitions` without
`columns` — accepted under v0.2.0 — now fail to load with a dedicated error.

### Added
- **Declared-but-inert settings warn at load.** A knob that is visible in the
  definitions schema, validated at load, and then read by nothing is the
  inverse of "schemas, not magic" — the author sets it, sees no error, and gets
  no behavior change. Load now emits a warning naming each such knob and where
  the work is tracked. The catalog lives next to the check
  (`internal/config/inert.go`), so adding an inert field is a conscious act and
  removing a row is how you record that the feature landed. Warnings, not load
  errors: an inert knob cannot corrupt anything, and refusing to start over one
  would break workspaces that set it in good faith. Two entries remain
  (`settings.event_retention_days`, `extensions[].expose`) — `tag_policy`,
  `default_board`, and `searchable_fields` all left the list this release.
- **`settings.default_board` — name the workspace's primary board.** In a
  multi-board workspace, every surface that had to pick one board with no other
  signal fell back to whichever board id sorted first, so agents routinely
  oriented against the wrong board. Declaring `default_board` now drives the
  TUI's initial board, the `/ui/cards/new` landing, and `take-next` when given
  neither `board_id` nor `type_id`; it is surfaced by `GET /v1/workspace`,
  `cards workspace show`, and the MCP `workspace` tool. Unset preserves the
  alphabetical fallback; an id naming no board fails load with the available
  ids listed. One resolver (`core.DefaultBoardID`) backs every surface.
- **`/v1/openapi.json` now describes the whole API.** The generated OpenAPI 3.1
  document went from 11 paths / 13 operations to **25 / 29**, adding the
  coordination atomics (`claim`, `release`), links, comments, repeating-field
  entries, the durable `/v1/events` catch-up feed, per-card `events`/`history`,
  `/v1/users`, `/v1/health`, and the two `cards serve` reload-seam routes.
  Mutating operations document the `Idempotency-Key` header and the structured
  `403`/`409`/`422` envelopes; `Event.type` is generated from the new
  `core.EventTypes()` so a new event type cannot ship against a stale contract.
  `TestOpenAPICoversEveryRoute` walks the chi route table against the document
  in both directions, so an undocumented endpoint — or a documented phantom —
  fails the build. Found by the 2026-07-25 architecture audit.
- **`cards release` closes the CLI recovery gap.** The new version-checked
  command clears card ownership and can atomically set `--status`; `--force`
  permits an explicit off-graph recovery move. It works through both the
  serverless and `--url` backends and mirrors `POST /v1/cards/{id}/release`.

### Fixed
- **`searchable_fields` is honored.** A card type's declared
  `searchable_fields` now restricts what `upsertFTS` indexes; previously the
  list was declared, validated, and ignored, so every field value — enums,
  branch names, work-log blobs — landed in the index regardless. A type that
  declares nothing still indexes everything, so no existing workspace narrows
  silently, and `title` stays searchable either way. The declaration reaches
  the store through the optional `core.SearchableFieldsSetter` installed by
  `NewService`, so a definitions reload refreshes it instead of freezing the
  rule at startup; a changed declaration rebuilds the index once, gated on a
  digest in a new `meta` table, so rows written under the old rule cannot keep
  matching fields the workspace has since excluded.
- **`tag_policy` is honored — tags worked nowhere on a fresh `cards init`.**
  The core never read `settings.tag_policy`: `validateTags` rejected anything
  outside `tag_set` unconditionally, so the starter workspace (`tag_set: []`,
  `tag_policy: "open"`) refused *every* tag. The web UI's chip control did read
  the setting, so it invited a free tag the API then rejected — failing the
  whole `PATCH` and discarding any other edits in the same save. The
  `unknown_tag` hint also hardcoded `'propose'` regardless of configuration.
  `tag_policy` is now a two-value dial (`open` | `locked`), read by the core on
  both write paths, with the hint naming the policy actually in force.
- **OpenAPI request/response shapes that misdescribed the handlers.**
  `POST /v1/cards/take-next` is documented as returning `{card: …|null}` rather
  than a bare `Card`; `GET /v1/cards` gained the `blocked`, `has_link`,
  `link_target`, `include`, and `sort` parameters it has always read, its
  `limit` ceiling is stated as 500 (not 200), and the `owner` description no
  longer implies the API resolves `me` — the board UI substitutes the viewing
  actor before calling. Artifact upload documents its optional `?version=`
  guard.
- **`transition_illegal.valid_options` stay on the board.** Board `transitions`
  edges must name that board's columns (not arbitrary workspace column ids);
  load rejects off-board edges, and runtime scrubbing keeps error
  `valid_options` and TakeNext from-status filters board-scoped. TakeNext no
  longer treats an empty allowed-from set as "no status filter" (which could
  claim any card and move it off-board), and invalid target statuses now return
  a structured validation error instead of looking like an empty queue. Card
  `8c04883d`; commit `c05e227` plus the pending review fixes.
- **`cards --quiet init` stays quiet.** The peeled global `--quiet` flag is
  reinjected into `init` so both `cards --quiet init` and `cards init --quiet`
  suppress the Next: blurb. Regression coverage for `.cards/` walk-up,
  `CARDS_HOME` / `~/.cards` fallback, and welcome-board seeding. Card
  `b86c7fe9`; commit `367457a`.
- **Ephemeral SSE signals preserve the durable replay cursor.** Live condition
  events with no persisted event id omit the SSE `id:` field, so browser
  `EventSource` reconnects from the last durable fact instead of resetting to
  zero and skipping catch-up.

### Changed
- **`tag_policy` drops `propose`; unset now means `locked`.** The `propose`
  mode was specified in v0.4 and never implemented on any path — accepting a
  write back into a git-backed definitions file is extension territory, not
  core. Load now **rejects** `propose` with a structured error naming the
  migration rather than silently picking a side. Unset defaults to `locked`
  (previously `propose`), which preserves what every workspace actually did
  while the setting went unread; set `"tag_policy": "open"` for free tags.
- **Events integration docs** align with the shipped contract: `wip_limits` vs
  `monitors`, workspace `settings.persist_conditions` (not per-monitor
  `persist: true`), and `card_deleted` on the mutation taxonomy.
- **Workspace/board concepts** docs restored and expanded (discovery,
  onboarding, multi-workspace, portability).
- **FTS5 vs LIKE disposition** recorded (`docs/design/fts-vs-like-disposition.md`);
  demo seed research card closed with the keep-FTS5 conclusion.
- **Board load validation:** a board with `transitions` but no `columns` fails
  at load time instead of accepting a broken graph.

## [0.2.0] - 2026-07-20

### Added
- **TUI filter/sort parity.** The terminal UI slices a board the way the web
  UI does: `f` opens a server-side filter prompt (saved filters, `owner:me`
  substitution), `F` cycles the sort presets, `T` narrows by card type, and
  `/` stays local find. The presets are shared code with the web UI
  (`internal/uioptions`), so the two surfaces cannot drift; active directives
  survive live refreshes.
- **Portable artifact bundles.** `cards export --with-artifacts` copies the
  referenced attachment blobs (sha256-verified) into an `artifacts/`
  directory beside the JSONL snapshot; `cards import --with-artifacts`
  restores them into a fresh workspace and fails loudly — before any card
  state lands — on a tampered, corrupt, or missing blob. Default export and
  import stay pointer-only. `scripts/board.sh` exports bundles and
  auto-detects committed blobs on import, re-verifying them on every sync.
- **A runnable extension seed.** The demo workspace ships `review-bot.mjs`
  (~150 lines, zero dependencies): a supervised `service` extension that
  listens on the SSE stream, picks up cards reaching review, and comments on
  them — with an end-to-end test covering SSE resumption across a server
  restart. The copy-me template for "extensions over plugins".
- **Headless TUI screenshots.** `tui.Snapshot` renders a frame without a
  terminal (keys can open modals for the capture); `scripts/tui-screenshots.sh`
  converts the ANSI frame to HTML and captures it with the same headless
  Chrome the web-UI screenshots use. `docs/assets/img/tui-*.png` are generated.
- **The web UI and `cards --help` show the binary version.** The nav carries
  a quiet version chip and the help header reads
  `Cards v0.2.0 (<commit>) — typed-card coordination.` — the same string
  `cards version` reports.
- **Attachments are visible in dense themes.** Board cards gained a
  paperclip count in the stats strip for themes that suppress thumbnails —
  the labels theme shows it (and now explicitly hides thumbnail blocks);
  the default theme keeps its image thumbnails and download chips.
- **Create boards from the UI, and reload definitions without a restart.**
  `POST /v1/workspace/reload` (and `cards reload`) re-runs the definitions
  loader and atomically swaps the workspace: a load error returns the
  validation message and the previous definitions keep serving — never a
  half-loaded state. The SQLite store and the live event bus survive the
  swap, so SSE streams and hook supervisors stay attached, and open boards
  refetch on a `definition_reloaded` event. On top of that seam, the nav's
  "+" opens a create-a-board modal (name, columns, card types, optional WIP
  limit) that writes `definitions/boards/<id>.json` — a reviewable file,
  exactly as if hand-written — validates it through the real loader (rolling
  the file back on failure), and reloads.
- **Themes are CSS-only now.** The labels theme's special-cased detail header
  was the one place a theme changed markup; the shared header now carries
  stable hooks (type-icon cell, meta key/value items, an id-copy button) that
  every theme lays out its own way — the labels two-row attribute header is
  reproduced purely in CSS, and the default theme keeps its classic meta line
  (plus a subtle copy-id affordance). Theme web-fonts moved from template
  conditionals to a data manifest. Two new `go test`-run guards: templates may
  not branch on the theme name, and `style.css` braces must balance (a single
  dropped brace once silently swallowed a whole theme).
- **Labels theme: comment/link counts and the blocked badge are back** on
  board cards, in a slim third row that collapses when empty — blocked stays
  a text badge, never colour-only.
- **Create cards from the board.** "+ New Card" (nav) and a per-lane "+" open
  an in-board creation modal: pick the card type (the workspace's types with
  their icons/colors), then fill a form generated from that type's schema —
  required marks, enum defaults pre-selected, disallowed columns disabled with
  a reason, per-field validation errors from the structured API error. A
  lane's "+" pre-sets the status so cards land where you created them; a
  per-form Idempotency-Key makes double-submit safe. The old full-page
  `/ui/cards/new` form is replaced by a redirect into the modal flow.
- **Comments and repeating entries are editable from the card modal.** A
  composer adds comments (⌘/Ctrl-Enter submits, Esc clears) and each comment
  gains in-place edit; repeating fields (`work_log`, `change_log`, …) get an
  add/edit/remove editor whose sub-form is rendered from the SAME
  `item_fields` definition the API validates against — user fields default to
  the acting user, date fields to today. All of it is a thin client of the
  existing `/v1` endpoints with the card's version; a stale write renders
  "card changed — reload" instead of clobbering. Entry feeds got a layout
  pass: author chip, right-aligned timestamp, aligned key/value grid,
  hover-revealed actions.

### Changed
- **The ocean example theme was removed** from the demo workspace and the
  project board; `jeeruh` remains the workspace-theme starting point and the
  themes guide now uses a neutral `my-theme` example.
- **Release workflow actions bumped to their Node 24 majors**
  (`upload-artifact` v7, `download-artifact` v8, `action-gh-release` v3);
  artifact downloads now fail on digest mismatch by default.

### Internal
- **Docs-integrity guards grew teeth.** `docs/reference/implementation-status.md`
  anchors are symbol-pinned (`<!-- guard: symbol=... -->`) and a docaudit
  test fails when a cited symbol moves away from its cited line; strict mode
  runs under `-tags strictdoc`. The JSONL snapshot contract is pinned by
  byte-stability and frozen-fixture tests plus a `board.sh` smoke test.

## [0.1.3] - 2026-07-06

### Changed
- **Stable download links.** Release archives are now named without the version
  (`cards_<os>_<arch>.tar.gz`), so
  `https://github.com/somebox/cards/releases/latest/download/cards_<os>_<arch>.tar.gz`
  always resolves to the newest binary. The version is still stamped into the
  binary (`cards version`) and bundled `CHANGELOG.md`.

## [0.1.2] - 2026-07-06

### Added
- **Release automation.** Pushing a `v*` tag now builds cross-platform binaries
  (linux/darwin/windows · amd64/arm64, CGO-free via the pure-Go SQLite driver)
  and publishes them as a GitHub Release (`.github/workflows/release.yml`).

## [0.1.1] - 2026-07-06

The "recommit to the agent loop" sprint: the CLI/HTTP/MCP surfaces become a
uniform machine contract, the coordination loop works out of the box, and the
first board-user capability (browser attachment upload) ships.

### Added
- **Attach files from the board UI.** A card's artifact field in the modal is
  now an upload control: click-to-browse (a real, keyboard-reachable file
  input) is the primary action, with drag-and-drop as an enhancement scoped to
  the modal (it can't collide with the board's column-move drag). The upload
  has a full state machine — idle, drag-over, uploading, success, and worded
  errors (a client-side size pre-check, the server's 413, and a
  "card changed — reload" message on a version conflict). Uploaded artifacts
  render on the board card as image thumbnails (height-capped) or a download
  chip, live via the existing `artifact_added` SSE.
- **The loop works out of the box.** The starter `task` card type now ships
  an `attachment` artifact field, so `cards init NEW && cards attach …` works
  from a fresh install with no manual schema edit (pre-existing workspaces:
  add the field to `definitions/card-types/task.json`; additive, no version
  bump). `--workspace`/`$CARDS_WORKSPACE` accept the project root as well as
  its `.cards` child — every entry point (client verbs, serve, init) shares
  ONE resolution rule, and a directory where BOTH the root and `.cards` are
  workspaces errors with the concrete choices instead of guessing. `cards
  init X` refuses when X is already a workspace (it would nest one inside
  the other). A Go test keeps the demo workspace's card types field-synced
  with the starter assets.
- **Short ids work on every verb.** All mutating commands and endpoints
  (`patch`, `comment`, `link` — including the link *target* —, `attach`,
  `claim`, `release`, `delete`, `upgrade-schema`, entry edits, `history`) now
  accept the 8-char short id anywhere a full `card_…` id was required;
  previously only reads resolved short ids. References are normalized to the
  full id before any write, so events, link rows, and comments always record
  full ids.

### Changed
- **Attachment upload gained an optional concurrency guard.** `POST
  /v1/cards/{id}/artifacts/{field}?version=N` (and `cards attach --version N`)
  rejects a stale write with `version_conflict` before any bytes are stored,
  mirroring `DELETE`; omitting the version proceeds against the current card,
  so `cards attach <id> <field> <file>` is unchanged. MCP `attach_artifact`
  stays unguarded.
- **JSON-only definitions, pinned.** Workspace/card-type/board definitions
  load from `.json` only (a `.yaml` definition file is silently ignored); only
  `definitions/extensions.{yaml,yml,json}` accepts YAML. This was already the
  code's behavior and is now enforced by a test so the docs can't drift back.
- **One `ambiguous` error shape on every transport.** An ambiguous short id
  now returns the standard structured error (`error: "ambiguous"`, HTTP 409,
  `candidates` with each match's full id + title, the query in `value`) from
  HTTP, the CLI (exit code 4, candidates listed), and MCP (structured tool
  error instead of a bare `-32603`). *Contract note:* `GET /v1/cards/{id}`'s
  bespoke ambiguous body dropped its `query` field — the short id now rides
  in the standard `value` field.

### Fixed
- **An oversize upload returns a clean 413, not a 500.** A body over the 32 MiB
  cap (which `MaxBytesReader` fails mid-stream) is now reported as a typed
  `artifact_too_large` error instead of falling through as a generic 500.
- **A failed or raced attachment upload no longer orphans a blob.** Artifact
  bytes are now staged to a temp file and published to the content-addressed
  store only after the card write commits; a stale version, a lost
  optimistic-concurrency race, or any store error discards the staged bytes.
- **Ambiguous short id on `DELETE /v1/cards/{id}` returned a 500.** It now
  returns the structured 409 like every other verb.

## [0.1.0] - 2026-07-06

First tagged release. Cards is a local-first, single-tenant coordination
tool: typed work cards on an event-sourced SQLite log, surfaced through an HTTP
`/v1` API, an htmx web UI, a CLI, and an MCP server — one workspace, no external
services.

This release takes the existing engine — typed cards with schema validation and
optimistic concurrency, an append-only event log, nine reactive condition types
(WIP/lane/blocked/temporal) with a tickless deadline scheduler and resumable
SSE — to a shippable baseline, and adds the following.

### Added
- **Attachments, end-to-end.** The content-addressed artifact store (SHA-256
  dedup, MIME sniffing, symlink-safe path confinement) is now wired through
  every surface: `cards attach <id> <field> <file>`, `POST
  /v1/cards/{id}/artifacts/{field}` + `GET /v1/artifacts/*`, MCP
  `attach_artifact`/`get_artifact` (base64), and a `/ui` card-detail view that
  renders an inline thumbnail for images or a download link otherwise.
  `artifact_added` streams on the board SSE so an upload appears live.
- **`cards version`** (and `cards --version`) prints the release version,
  commit, build time, and toolchain/platform.
- **`cards list --include=links,comments`** eager-loads relations so an agent
  can read a card's dependency graph in one call instead of an N+1 of per-card
  GETs.
- **`--workspace` on client verbs** — a cwd-independent way to target a
  serverless workspace (works before or after the subcommand, like `--url`;
  previously only the `$CARDS_WORKSPACE` env var could do this).
- **`cards patch --title`** renames a card under the same optimistic-concurrency
  fence (the API already supported it; the flag was missing).

### Changed
- **`--url` now reaches a running server correctly.** A bare host such as
  `--url http://127.0.0.1:9100` is normalized to the `/v1` mount instead of
  404ing.
- **Per-verb `--help`** now lists a command's flags (`cards patch --help`)
  instead of erroring; the existing `--quiet`/`--json`/`--jsonl` output modes
  are documented in help.
- **CLI exit codes are differentiated** so scripts can branch on failure class
  without parsing stderr: `3` not-found, `4` conflict, `5` invalid request,
  `1` everything else.
- **Bare `cards`** prints usage instead of silently starting a server on port
  8787; run the server explicitly with `cards serve`.

### Fixed
- **Durable condition events survive a crash.** A persist:true condition's
  fired-marker and its event append now commit in one transaction; previously a
  crash between the two separate writes lost the event permanently *and*
  suppressed re-fire.
- **Graceful shutdown drains in-flight hooks.** The extension supervisor now
  awaits running hook subprocesses (bounded, then process-group kill) on
  shutdown instead of leaving them orphaned.
- **SSE replay errors** are surfaced to the client (a `: replay failed` comment)
  and no longer risk iterating an empty result on error.

### Internal
- The technical-debt ledger was reconciled against the code (many entries were
  already fixed); completed review/planning artifacts were archived under
  `docs/archive/`.

[Unreleased]: https://github.com/somebox/cards/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/somebox/cards/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/somebox/cards/compare/v0.1.3...v0.2.0
[0.1.3]: https://github.com/somebox/cards/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/somebox/cards/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/somebox/cards/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/somebox/cards/releases/tag/v0.1.0
