# Changelog

All notable changes to Work Cards are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versioning follows
[Semantic Versioning](https://semver.org/); while pre-1.0, a **minor** bump
(0.x) may contain breaking changes and a **patch** bump (0.x.y) is reserved for
backwards-compatible fixes.

## [Unreleased]

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

[Unreleased]: https://github.com/somebox/cards/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/somebox/cards/releases/tag/v0.1.0
