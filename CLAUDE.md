# CLAUDE.md — agent instructions for the `cards` repo

This file orients coding agents (Claude, etc.) working in this repository.
It is a map, not a substitute for the docs it points at. When a task touches
a specific area, read the linked doc before editing — it is the contract.

> Repo: <https://github.com/somebox/cards.git> · Go module `github.com/somebox/cards` · requires Go `1.26.4`+.

## What this project is

**Cards** is a local-first, single-binary coordination service for typed
"cards" (tasks, bugs, notes, anything you define). A workspace is a directory
of JSON definitions (`definitions/`) plus a `work-cards.db` SQLite file. The
same `cards` binary exposes one typed-card model through five transports that
all share one service layer:

- **HTTP/REST API** under `/v1` (with SSE event stream at `/v1/events/stream`)
- **CLI** (`cards create|list|patch|comment|...`)
- **MCP** server for agent clients (`cards mcp`)
- **Web UI** under `/ui` (server-rendered Go templates + Alpine.js, no JS build step)
- **TUI** (bare `cards` on a terminal; quit with `q`). Serverless, in-process —
  its live refresh is the in-process bus, **not** the multi-process SSE stream
  (see `docs/design/tui-bus-disposition.md`)

There is **no separate database server** — SQLite is embedded via the pure-Go
`modernc.org/sqlite` driver (no CGO). One process serves exactly one workspace.

### Design principles (normative — read philosophy.md before non-trivial work)

1. **Small core, big composition.** The core does cards, fields, events,
   links, comments, columns, storage. Everything else (dispatchers, agents,
   UIs, sync, reports, validations) is an *external process* talking to the
   API or event stream. The core grows reluctantly. The field catalog stays
   small (10 types); rich payload validation is extension territory.
2. **Files where they help.** Core definitions are git-backed JSON; extension
   declarations may be YAML where implemented. Anything authored/reviewed/
   versioned by humans belongs in a file; anything operational/queried belongs
   in SQLite. **The committed, portable form of card state is a JSONL
   snapshot** (`backlog.jsonl`) — `cards export --state-only` / `cards import`
   move a board between machines; the live DB is gitignored and machine-local.
3. **Schemas, not magic.** Behavior comes from explicit, introspectable typed
   schemas. No hidden context injection, no behind-the-scenes mutation, no
   implicit defaults that aren't visible in the API response. Adding a field
   to a card type changes the contract everywhere (API, CLI, MCP, UI) without
   a separate UI model.
4. **Progressive disclosure.** Introspection is scoped; MCP tool surfaces can
   be narrow. The core does not push every type/board/tool into every session.
5. **Hooks, not engines.** No automation engine, workflow DSL, or rules
   language. There are events, hooks (subprocess scripts), and external SSE
   subscribers. Need automation? Write an extension.
6. **Extensions over plugins.** Extensions are independent processes in any
   language, communicating via HTTP API + event stream. They never load into
   the core process (crash-isolated, language-agnostic, keeps the core small).
7. **YOLO defaults.** Default deployment is local-only, single-tenant, trusted.
   No permission theater. Auth/isolation is the host's responsibility.
8. **Stable, documented contracts.** HTTP and event contracts are versioned
   and meant to outlive any specific implementation. The CLI and any client
   libraries are layered on top of the same contract.
9. **Fail loudly, guide recovery.** Every rejection is a structured error:
   which field, which value, what was allowed. Agents retry; agents self-correct.
10. **Boring tech.** SQLite, JSON/YAML, HTTP/SSE, subprocess. No new languages,
    protocols, or databases.

Read the full version: [`docs/concepts/philosophy.md`](docs/concepts/philosophy.md).

## Where things live (read before editing)

| Area | Path | What it is |
|---|---|---|
| Module entry | `cmd/cards/` | CLI binary + subcommand wiring (`serve`, `mcp`, `extensions`, `do`) |
| Core model | `internal/core/` | cards, schemas, transitions, validation, events, `Store` interface |
| Definitions | `internal/config/` | load/merge/validate JSON core definitions + extension config |
| Storage | `internal/sqlite/` | SQLite impl (FTS5, migrations) |
| HTTP + UI | `internal/httpapi/` | REST, SSE, server-rendered web UI (Go templates + Alpine.js) |
| MCP | `internal/mcp/` | MCP adapter over core services |
| Hooks | `internal/hooks/` | hook supervisor (spawns subprocesses on events) |
| Artifacts | `internal/artifacts/` | content-addressed artifact blob storage with path confinement |
| CLI client | `internal/cli/` | CLI commands over the HTTP API (wired into `cmd/cards/`) |
| Doc audit | `internal/docaudit/` | docs-integrity guards as ordinary Go tests (no non-test code) |
| OpenAPI | `internal/openapi/` | OpenAPI 3.1 document generated from live workspace definitions |
| Seed | `internal/seed/` | demo users/cards inserted into an empty workspace DB (`--seed`) |
| Starter | `internal/starter/` | embedded starter material (welcome cards) scaffolded by `cards init` |
| Theme CSS | `internal/themecss/` | workspace theme CSS validation against the `docs/design/themes.md` contract |
| Project board | `.cards/` | the live dogfooding backlog — definitions + `backlog.jsonl` (+ `backlog.md` overview) |
| Demo workspace | `examples/demo-workspace/` | frozen example material (docs, screenshots, what `cards init` scaffolds) |
| TUI | `internal/tui/`, `cmd/cards/tui.go` | terminal UI behind bare `cards` (`interactive()` guard: both streams TTY, no `--json`) |
| UI templates/CSS | `internal/httpapi/templates/` | `layout.html`, `board.html`, `card_modal.html`, `style.css`, etc. |

### Doc map (the contract for each area)

- **Concepts / mental model:** [`docs/concepts/index.md`](docs/concepts/index.md)
- **Principles (normative):** [`docs/concepts/philosophy.md`](docs/concepts/philosophy.md)
- **Architecture (Go core, packaging):** [`docs/architecture/index.md`](docs/architecture/index.md)
- **Web UI design system + theming contract:** [`docs/architecture/design-system.md`](docs/architecture/design-system.md) ← read before any CSS/template work
- **API + data model (normative):** [`docs/spec/index.md`](docs/spec/index.md), split into `api-surface.md`, `data-model.md`, `events-history.md`, `query-dsl.md`, `card-types.md`
- **Card schemas & workspace:** [`docs/reference/card-definitions.md`](docs/reference/card-definitions.md) (card types & fields), [`docs/reference/workspace-and-boards.md`](docs/reference/workspace-and-boards.md) (columns, boards, transitions), `card-type-examples.md`, `cli.md`
- **Using Cards (operations, CLI/API/MCP):** [`docs/using-cards.md`](docs/using-cards.md)
- **Code-verified drift audit (built vs proposed):** [`docs/reference/implementation-status.md`](docs/reference/implementation-status.md) — the single-page source of truth for what's actually implemented
- **Events:** [`docs/events/index.md`](docs/events/index.md) → `core.md` (contract), `rollout.md` (history), `integration.md` (consumption)
- **Extensions / MCP:** [`docs/extensions/index.md`](docs/extensions/index.md), [`docs/extensions/mcp.md`](docs/extensions/mcp.md)
- **Design rationale notes:** [`docs/design-notes.md`](docs/design-notes.md)
- **Design explorations (not built):** `docs/design/` (e.g. `style-field.md`)

## How to run the server (and reload on changes)

The UI templates and CSS are **embedded in the Go binary** (`internal/httpapi/templates/`
via `embed.FS`), so editing them requires a rebuild + restart to see in the
browser. Two ways:

### One-shot build + serve

```bash
go build -o cards ./cmd/cards
./cards serve --workspace ./.cards --port 8787 --seed
open http://127.0.0.1:8787/ui/boards/engineering
```

### Auto-reload dev server (preferred for UI work)

```bash
scripts/dev-server.sh
open 'http://127.0.0.1:8787/ui/boards/engineering?theme=labels'
```

`scripts/dev-server.sh` rebuilds and restarts the demo server when files under
`cmd/`, `internal/`, or `.cards/definitions/` change. If
[`air`](https://github.com/air-verse/air) is installed it delegates to
`.air.toml`; otherwise it uses a small dependency-free polling watcher. Override
with `PORT=` / `CARDS_WS=` / `CARDS_DEV_NO_AIR=1`. Logs land in `.pi/run/`.

Other entry points (all share the same service layer):

```bash
./cards mcp --workspace ./.cards                    # stdio MCP server for agents
./cards list --board engineering                     # CLI (set CARDS_URL + CARDS_USER to hit a server)
./cards export --state-only --out backlog.jsonl      # portable snapshot
./cards import --in backlog.jsonl                    # restore into a fresh workspace
scripts/board.sh export|import|import --force|install-hook   # wraps the above for git sync
```

The default server URL is `http://127.0.0.1:8787`. The web UI's live board
updates via SSE; the open card modal does not, so a mutation that isn't a
simple field patch may need a manual modal refresh.

## How to build / test / vet

```bash
go build ./cmd/cards
go test ./...                 # all packages
go vet ./...
go test ./internal/httpapi ./internal/config ./internal/core   # the three touched most
```

CI also runs `golangci-lint` and `go test -race`; keep new code race-clean.

## Web UI / CSS conventions (read `docs/architecture/design-system.md` first)

The UI is a single token-driven CSS system in
`internal/httpapi/templates/style.css` — **no build step, no Tailwind, no JS
asset pipeline.** Four normative principles gate changes (violations are bugs):

1. **Containers own spacing** — rhythm comes from container `gap`, never
   child margins. One scale: `--s-1..6`. Page containers may use
   `clamp()`/`min()`; components inherit space, never hardcode it.
2. **Type is compact but never small** — one scale `--t-xs..xl`, nothing below
   `--t-xs`. **Roles, not literals**: every text-bearing rule reads a
   `--role-*` typography token (see below), not an ad-hoc `font-size`.
3. **Editing is WYSIWYG** — click-to-edit changes chrome, not geometry. The
   edit control matches the view's font/size/line-height/padding exactly.
4. **Themes remap tokens, never structure** — a theme is a token remap plus
   optional scoped rules on stable hooks; it must not require markup changes.

### Typography roles (`--role-*`) — the seam for sizing/weight

`--role-heading-weight`, `--role-strong-weight`, and per-role sets for card
title (`--role-title-card-*`), detail title (`--role-title-detail-*`), field
label (`--role-label-*`), header metadata (`--role-meta-*`), and body/value
(`--role-body-*`), all declared in `:root` and overridden per named theme
(`html[data-theme="labels"] { ... }`). **When you change a font size or
weight, change the role token, not the selector** — every element sharing the
role updates together and can't drift out of sync.

**Units:** `rem` for `font-size` and spacing; unitless or `rem` for
`line-height`; **never `vw`/`vh` for text** (same element renders a different
px size per window — unreadable in devtools, breaks zoom). `clamp()` is fine
for *layout* (modal width, main padding), not for type roles. `px` only for
hairlines/icon dimensions (`--edge`/`--stroke`/`--rule`).

**Tweaking in devtools:** inspect the element → Styles → find the `--role-*`
custom property the matched rule reads → edit it where it's declared
(`:root` or the theme's `html[data-theme="…"] { }` block).

### Stable theme hooks (renames are breaking)

`:root` token remap · `html[data-theme="<name>"]` (named theme; via
`?theme=<name>` sticky or `settings.theme`) · `[data-board="<id>"]` wrapper
(board `Theme` → whitelisted inline tokens) · `.card[data-type="<id>"]` +
`[data-icon="<name>"]` (type/option icon + `--card-stock`/`--card-stock-bg`).
Icons are monochrome `currentColor` mask-images keyed by `[data-icon]`
(aliases: `card`, `star`, `bug`, `check`, `flask`, `target`, `code`, `pen`,
`wrench`); add a new icon by adding one CSS mask alias, not by editing markup.
See `docs/design/style-field.md` for the proposed enum-value → icon/color
mapping (`option_themes` + board `presentation.style_field`).

### Before you commit UI changes

- Run `go test ./internal/httpapi` — template parse errors surface there.
- Hard-refresh the browser (templates/CSS are embedded; the dev server
  rebuilds automatically, but the browser caches `/ui/style.css`).
- Respect `prefers-color-scheme: dark` and `prefers-reduced-motion`.
- Emit RFC3339 timestamps as `<time data-ago="...">`, never a raw `time.Time`.

## Conventions that catch agents out

- **Idempotency + optimistic concurrency:** mutating a card requires the
  current `version`; a stale write returns a structured `version_conflict`
  with the current card attached. Retries use `Idempotency-Key` (per-actor).
  Don't paper over these — surface and retry.
- **One workspace per process.** Multi-workspace = run multiple processes on
  different ports. Don't add a multi-tenant router to the kernel.
- **The event log is coordination memory, not an archive.** The materialized
  card is the durable work product; the log may be trimmed.
- **`backlog.jsonl` is the committed, portable state** (in `.cards/`,
  alongside a generated `backlog.md` overview). The live
  `work-cards.db` is gitignored and machine-local. Use
  `scripts/board.sh export|import` to sync boards across machines; never edit
  the JSONL by hand when a server is running against the same workspace.
- **Docs vs. code drift:** `SPEC*.md` is normative intent;
  `docs/reference/implementation-status.md` is the code-verified audit of what's
  actually built. When a spec claim and code disagree, fix the code or update
  the spec deliberately — don't leave them split.
- **No new dependencies without justification.** Keep it boring and buildable
  without a framework.

## Quick "where do I…" index

- Add a card-type field / change a schema → `internal/core/types.go` +
  `card-definitions.md`
- Add/fix an HTTP route → `internal/httpapi/api.go`, register in `server.go`,
  document in `api-surface.md`
- Change the board UI look → `internal/httpapi/templates/style.css` (role
  tokens), `design-system.md` is the contract
- Add a CLI subcommand → `internal/cli/`, wire in `cmd/cards/`
- Add an MCP tool → `internal/mcp/`, document in `docs/extensions/mcp.md`
- Add an event / condition → `internal/core/events.go`, `core.md`
- Add an extension hook → `internal/hooks/`, `docs/extensions/index.md`
- Tweak a theme → override `--role-*` (and palette / `--modal-*` geometry) in
  the theme's `html[data-theme="<name>"] { }` block; do not restructure
  templates (the `labels` detail header is the documented exception)
