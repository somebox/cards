# Changelog

All notable changes to Work Cards are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versioning follows
[Semantic Versioning](https://semver.org/); while pre-1.0, a **minor** bump
(0.x) may contain breaking changes and a **patch** bump (0.x.y) is reserved for
backwards-compatible fixes.

## [Unreleased]

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

First tagged release. Work Cards is a local-first, single-tenant coordination
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

[Unreleased]: https://github.com/somebox/cards/compare/v0.1.3...HEAD
[0.1.3]: https://github.com/somebox/cards/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/somebox/cards/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/somebox/cards/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/somebox/cards/releases/tag/v0.1.0
