# Cards

Cards is a local coordination service for defining, reviewing and assigning tasks. A project defines
its card types, boards, columns, transitions, and extensions in a JSON schema
(extensions may also be declared in YAML).
The `cards` binary loads those definitions, stores card state and events in
SQLite, and exposes the same model through HTTP, CLI, MCP, and a small web UI.

It was built for projects where plain todos are too local, but a hosted tracker
is more process than the team needs. Humans, scripts, and agents can claim
cards, update typed fields, append evidence, and resume from the card history
later. The web board is useful, but it is only one view over the same API.

![Cards board UI](./media/board.png)

## Terminal UI

The same binary also opens a full terminal UI — run `cards` with no command
on an interactive terminal (it prints usage instead in scripts and pipes, so
automation is unaffected). It runs against the resolved workspace with no
server required, and refreshes live from the event bus.

```
 Demo workspace · Engineering · my 1                                                         ● live 
  Backlog 23 │ To Do 9 │ In Progress 0 │ Review 0 │ Done 146                                        
 ─ Done · 146 cards                                                                          119/146
  Programming… Events seam 1f: Eve… ·          15d ↪2 ▾1  ╭───────────────────────────────────────╮
  Programming… Events seam 1a: ext… ·          15d ↪1 ▾2  │                                       │
  Programming… Events seam 1e: mig… ·          15d ↪2 ▾1  │   ## Events: actor/owner stream       │
  Programming… Events seam 1d: com… ·          15d ↪2 ▾1  │   filters + GET /v1/events catch-up   │
  Programming… Events seam 1c: Emi… ·          15d ↪3 ▾1  │   feed                                │
  Programming… UI: tags as chips w… ·          15d ↪1 ▾3  │   Programming Task · Done ·           │
  Programming… Events: condition e… ·          16d ↪6 ▾4  │   unassigned · v8 · card_cf… · 17d    │
▌ Programming… Events: actor/owner… ·          17d ↪1 ▾1  │   ## description                      │
                                                             ╰───────────────────────────────────────╯
h/← lane ← • l/→ lane → • j/↓ down • enter open • / find • ? keys • q quit                          
```

Board columns are tabs (`h`/`l` switches lanes, `shift+tab` switches boards).
`enter` opens the selected card as a markdown document — schema fields, in/
outbound links, comments, activity — in a split pane; `enter` again makes it
fullscreen; `esc` steps back out. `s` moves a card through its legal
transitions, `o` assigns, `e` edits the title, `c` comments, `m` claims, `/`
searches, and `?` shows the full key reference.

## How It Works

A card has a fixed envelope and schema-defined fields. The envelope gives every
card an `id`, `type_id`, `title`, `status`, `owner`, `version`, links, comments,
and timestamps. The custom data lives under `card.fields` and is validated by
the card type.

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

That same definition drives the API, CLI, MCP tool schema, and UI form. Adding a
field to the card type changes the contract everywhere without adding a separate
UI model.

Boards are views over cards. They choose the types and columns to show, and can
add transition rules such as `todo -> in_progress -> review -> done`. Cards
live in one workspace, statuses come from the workspace columns, and boards add
useful constraints without owning a separate copy of the data.

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

Scaffold your own workspace and serve it — zero configuration:

```bash
cards init          # scaffold ./.cards with a starter "welcome" board
cards serve         # serve it at http://127.0.0.1:8787
open http://127.0.0.1:8787/ui/boards/welcome
```

That's the whole system running: one `.cards/` folder holding your board
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
cards patch <id> --status doing --version 1
cards comment add <id> --body "on it"
```

`cards serve` with no `--workspace` walks up for a `.cards/` workspace like git
finds `.git/`, falling back to a personal workspace at `~/.cards`. (Bare `cards`
prints usage; run the server explicitly.) To run the bundled demo board (the
project's own dogfooding backlog) instead, point at it explicitly:

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
`tag_policy`, and `default_user`. Card types define the shape of `fields`.
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

Extensions are normal processes. The core does not load plugin code; it starts
a declared command for hooks or lets a service subscribe to the API and event
stream. A hook can be as small as:

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

## Project Layout

The binary entry points live in `cmd/cards/`. The service model is in
`internal/core/`, with transports in `internal/httpapi/`, `internal/cli/`, and
`internal/mcp/`. Workspace loading, SQLite storage, and hooks live in
`internal/config/`, `internal/sqlite/`, and `internal/hooks/`. The demo
workspace is under `examples/`, and the longer design references are in `docs/`.

## Documentation And Development

**Full documentation lives at <https://somebox.github.io/cards/>** — start there
for the [product overview](https://somebox.github.io/cards/), a
[2-minute get-started](https://somebox.github.io/cards/get-started/), the
[MCP/agent guide](https://somebox.github.io/cards/agents/mcp/), and the CLI,
schema, events, and extension references.

The site is built from the Markdown in [`docs/`](docs/) with MkDocs Material
(`mkdocs.yml`); the source files remain browsable on GitHub. A code-verified map
of what's actually built vs. proposed is in
[`docs/reference/implementation-status.md`](docs/reference/implementation-status.md).

Cards is in beta. The core service, HTTP API, CLI, MCP server, web UI, and
hook system are implemented, but the API should still be treated as
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
