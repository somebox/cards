# Cards

Cards is a local coordination service for defining, reviewing, and assigning
work. A project defines its card types, boards, columns, transitions, and
extensions in JSON (extensions may also be YAML). The `cards` binary loads
those definitions, stores card state and events in SQLite, and exposes the
same model through HTTP, CLI, MCP, a web UI, and a terminal UI.

It was built for projects where plain todos are too little structure and a
hosted tracker is more process than the team needs. Humans, scripts, and
agents can claim cards, update typed fields, append evidence, and resume from
the card history later. The web board and TUI are useful, but each is only one
view over the same API.

![Cards board UI](./media/board.png)

## Terminal UI

The same binary opens a terminal UI — run `cards` with no command on an
interactive terminal (in scripts and pipes it prints usage instead, so
automation is unaffected). It runs against the resolved workspace with no
server required, refreshes live from the event bus, and filters and sorts
with the same query surface as the web UI.

![Cards terminal UI](./docs/assets/img/tui-board.png)

Press `?` in the TUI for the key reference, or see the
[terminal UI section of the CLI reference](https://somebox.github.io/cards/reference/cli/#terminal-ui).

## How It Works

A card is a JSON document with a fixed core structure and typed,
schema-defined fields. Every card has an `id`, `type_id`, `title`, `status`,
`owner`, `version`, links, comments, and timestamps. The custom data is
defined in `card.fields` and validated against the card type's schema.

Here is a shortened task definition:

```json
{
  "id": "programming-task",
  "name": "Programming Task",
  "schema_version": 1,
  "fields": [
    { "id": "description", "type": "text", "required": true },
    { "id": "branch", "type": "string", "required": true },
    {
      "id": "work_log",
      "type": "repeating",
      "item_fields": [
        { "id": "notes", "type": "text" },
        { "id": "author", "type": "user", "required": true }
      ]
    }
  ],
  "allowed_columns": ["backlog", "todo", "in_progress", "review", "done"]
}
```

That definition is shared by the API, the CLI, the MCP tool schema, and the UI
form. Adding a field to the card type changes the contract everywhere without
a separate UI model — schemas are the contract, not one input among several.

Boards are views over cards. They choose the types and columns to show, and can
add transition rules such as `todo -> in_progress -> review -> done`. Cards
belong to one workspace, statuses come from the workspace columns, and boards
add constraints without owning a separate copy of the data.

The design follows a small set of principles: a minimal core (cards, fields,
events, links, comments, columns, storage), definitions as git-backed files,
events and hooks instead of a workflow engine, and extensions as separate
processes. The full list, with the reasoning behind each, is in the
[philosophy](https://somebox.github.io/cards/concepts/philosophy/).

## Install

There is no separate database server — `cards` is a single self-contained
binary with embedded SQLite (via the pure-Go `modernc.org/sqlite` driver).

**Download a prebuilt binary** (no toolchain required) from the
[latest release](https://github.com/somebox/cards/releases/latest) — pick the
archive for your platform (`linux`/`darwin`/`windows` × `amd64`/`arm64`;
Windows ships a `.zip`). For example, on macOS (Apple Silicon):

```bash
curl -L -o cards.tar.gz \
  https://github.com/somebox/cards/releases/latest/download/cards_darwin_arm64.tar.gz
tar -xzf cards.tar.gz && cd cards_darwin_arm64
./cards version
```

On macOS an unsigned download is quarantined by Gatekeeper the first time;
clear it with `xattr -d com.apple.quarantine ./cards` (or right-click → Open).
Move `cards` onto your `PATH` (e.g. `sudo mv cards /usr/local/bin/`) to drop
the `./`.

**Or, with Go `1.26.4`+ installed**, build from source:

```bash
go install github.com/somebox/cards/cmd/cards@latest   # or @v0.1.2
# from a checkout: go build -o cards ./cmd/cards
```

## Quick Start

Setup only requires scaffolding a workspace and serving it — there is no
configuration beyond the definition files themselves:

```bash
cards init          # scaffold ./.cards with a starter "welcome" board
cards serve         # serve it at http://127.0.0.1:8787
open http://127.0.0.1:8787/ui/boards/welcome
```

This is the complete system: one `.cards/` folder holding the board
definitions and a `work-cards.db` SQLite file, with a web UI, a `/v1` REST
API, and an MCP (agent) interface over it. Click a card to edit fields inline,
drag it between columns, or attach a file.

Drive the same board from the command line — point the CLI at the running
server so its live UI and event stream stay in sync:

```bash
export CARDS_URL=http://127.0.0.1:8787   # target the server (omit to run serverless)
export CARDS_USER=me                     # actor for writes

cards create --type task --title "My first task" --status todo
cards list                               # the board as JSON lines
cards patch <id> --status in_progress --version 1
cards comment add <id> --body "on it"
```

`cards serve` with no `--workspace` walks up for a `.cards/` workspace like git
finds `.git/`, falling back to a personal workspace at `~/.cards`. A bare
`cards` on a terminal opens the TUI against that same workspace; in scripts and
pipes it prints usage instead. To run the bundled demo board (the project's own
dogfooding backlog), point at it explicitly:

```bash
cards serve --workspace ./examples/demo-workspace --port 8787 --seed
open http://127.0.0.1:8787/ui/boards/engineering
```

The server exposes the API under `/v1` and writes `work-cards.db` in the
workspace directory. Create a card through HTTP:

```bash
curl -X POST http://127.0.0.1:8787/v1/cards \
  -H "Content-Type: application/json" \
  -H "X-Work-Cards-Actor: alice" \
  -H "Idempotency-Key: demo-create-oauth" \
  -d '{
    "type_id": "programming-task",
    "title": "Implement OAuth flow",
    "status": "todo",
    "fields": {
      "description": "Add GitHub OAuth to the local sign-in flow.",
      "branch": "feat/oauth"
    }
  }'
```

Use the CLI against the same server:

```bash
export CARDS_URL=http://127.0.0.1:8787/v1
export CARDS_USER=alice

./cards list --board engineering --status todo
./cards take-next --board engineering --type programming-task --status in_progress
./cards history card_123
```

For MCP clients, run the stdio server:

```bash
./cards mcp --workspace ./examples/demo-workspace
```

## Configuration

A workspace is a directory with `definitions/` and, after the server starts, a
SQLite database:

```text
workspace/
  work-cards.db
  definitions/
    workspace.json
    card-types/
      programming-task.json
    boards/
      engineering.json
    extensions.json
  artifacts/
  .cards/ext/
```

`definitions/workspace.json` declares the shared vocabulary: columns, tags,
link types, users, and settings such as `enforce_transitions`, `strict_fields`,
`tag_policy`, and `default_user`. Card types define the schema of `fields`.
Boards define filtered views and transition rules.

The field catalog is intentionally small: `string`, `text`, `number`, `date`,
`enum`, `tags`, `user`, `card_link`, `repeating`, and `artifact`. More specific
behavior, such as validating a file path or starting CI, belongs in an extension
that reads cards and writes results back through the API.

### Syncing a board across machines

`definitions/` is committed; `work-cards.db` (live SQLite state) is **gitignored
and machine-local**. To move a board between machines, commit a portable
snapshot of the card state. `cards export --state-only` writes exactly that —
definitions + current cards, links, and comments — while the mutation log stays
SQLite-owned (only `card_deleted` tombstones ride along), so the snapshot is
small and diffs cleanly. `cards import` restores it into a fresh workspace (it
refuses a non-empty DB — never a silent overwrite). `scripts/board.sh` wraps
both:

```bash
scripts/board.sh export            # snapshot the live board -> backlog.jsonl, then: git commit && git push
# on the other machine:
git pull
scripts/board.sh import            # restore backlog.jsonl into a fresh workspace DB
scripts/board.sh import --force    # re-sync a machine that already has board state (wipes its DB first)
scripts/board.sh install-hook      # optional: auto-export before every commit so the snapshot never goes stale
```

Defaults to `examples/demo-workspace`; set `CARDS_WS=<dir>` for another
workspace. The event journal, condition marks, and any delivery state are *not*
in the snapshot by design (they are SQLite-owned durable state) — a restore
rebuilds card state, not history. See `docs/events/core.md` for the
event contract and `docs/events/rollout.md` for staged rollout status.

## API And Runtime Behavior

All transports use the same service layer. That layer handles schema validation,
transition checks, optimistic concurrency, idempotency, event creation, links,
comments, and full-text indexing.

Mutating an existing card requires the current `version`; a stale write returns
a structured `version_conflict` error with the current card attached. Retried
writes can use `Idempotency-Key`, scoped per actor, so a network retry does not
create duplicate cards or claim a different next task.

The event stream is available at `GET /v1/events/stream` over SSE, with
`Last-Event-ID` replay for clients that reconnect.

## Extensions

Extensions are separate processes. The core does not load plugin code; it
starts a declared command for hooks or lets a service subscribe to the API and
event stream. A hook is a declared command run on matching events:

```json
{
  "extensions": [
    {
      "id": "review-notify",
      "kind": "hook",
      "on": "status_changed",
      "filter": { "board_id": "engineering", "to_status": "review" },
      "run": ["bash", ".cards/ext/notify.sh"]
    }
  ]
}
```

Run declared hooks with the server, or run the supervisor separately:

```bash
./cards serve --workspace ./examples/demo-workspace --run-extensions
./cards run-extensions --workspace ./examples/demo-workspace
```

The demo workspace also declares a supervised service, `review-bot`: a small
SSE worker (Node stdlib only, no npm dependencies) that claims cards reaching
`review` and comments — see `examples/demo-workspace/README.md`. **It
requires Node on `PATH`**; on a Node-less machine its launcher logs
`node: not found — service review-bot skipped` and stays down instead of
restart-looping. `scripts/review-bot_test.sh` proves the loop end to end.

## Project Layout

The binary entry points are in `cmd/cards/`. The service model is in
`internal/core/`, with transports in `internal/httpapi/`, `internal/cli/`,
`internal/mcp/`, and `internal/tui/`. Workspace loading, SQLite storage, and
hooks are in `internal/config/`, `internal/sqlite/`, and `internal/hooks/`.
The demo workspace is under `examples/`, and the longer design references are
in `docs/`.

## Documentation And Development

**Full documentation is at <https://somebox.github.io/cards/>** — start there
for the [product overview](https://somebox.github.io/cards/), a
[2-minute get-started](https://somebox.github.io/cards/get-started/), the
[MCP/agent guide](https://somebox.github.io/cards/agents/mcp/), and the CLI,
schema, events, and extension references.

The site is built from the Markdown in [`docs/`](docs/) with MkDocs Material
(`mkdocs.yml`); the source files remain browsable on GitHub. Local entry
points worth bookmarking:

- [`docs/concepts/index.md`](docs/concepts/index.md) — vocabulary (workspace,
  definitions, card types, cards, boards) and use-case setups
- [`docs/reference/implementation-status.md`](docs/reference/implementation-status.md)
  — code-verified map of what's built vs. proposed
- [`docs/README.md`](docs/README.md) — full local doc index

Cards is in beta. The core service, HTTP API, CLI, MCP server, web UI, TUI,
and hook system are implemented, but the API should still be treated as
project-local unless a release notes otherwise.

PRs and issue reports are welcome. Start with
[`CONTRIBUTING.md`](CONTRIBUTING.md) for code quality and style standards. For
local development, build with `go build ./cmd/cards`, run the demo workspace,
and use the docs above as the current contract for changes. UI templates and
CSS are embedded in the Go binary, so edit/review loops need a rebuild;
`scripts/dev-server.sh` automates that by rebuilding and restarting the demo
server on source/template/config changes:

```bash
scripts/dev-server.sh
open 'http://127.0.0.1:8787/ui/boards/engineering?theme=labels'
```

If [`air`](https://github.com/air-verse/air) is installed, the script delegates
to `.air.toml`; otherwise it uses a small dependency-free watcher.

Coding agents working in this repo should start from [`CLAUDE.md`](CLAUDE.md) —
it maps the design rules, doc locations, run/test commands, and web UI/CSS
conventions.

## Releases

Versions follow [Semantic Versioning](https://semver.org/). While pre-1.0, a
minor bump (`0.x`) may include breaking changes and a patch bump (`0.x.y`) is
reserved for backwards-compatible fixes. Every change is recorded in
[`CHANGELOG.md`](CHANGELOG.md), and each binary reports its provenance:

```bash
cards version         # e.g. "cards v0.1.2 (a1b2c3d4e5f6) built 2026-07-06T…"
```

A working-tree `go build` reports `dev` plus the commit; a tagged release
build stamps the version via `-ldflags "-X main.version=vX.Y.Z"`.

**Cutting a release** (maintainers): move the `Unreleased` section of
`CHANGELOG.md` under a new `## [X.Y.Z]` heading, commit, then:

```bash
git tag vX.Y.Z && git push origin vX.Y.Z
```

The [`release` workflow](.github/workflows/release.yml) builds
cross-platform binaries (linux/darwin/windows × amd64/arm64, CGO-free via the
pure-Go SQLite driver) with the version stamped in, packages each as a
`.tar.gz` (`.zip` for Windows) alongside the README and CHANGELOG, and
publishes a GitHub Release with auto-generated notes and the archives
attached.

## License

MIT
