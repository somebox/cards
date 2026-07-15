# Using Cards

This page covers how a project runs on Cards day to day, then documents every
operation in detail. Everything goes through the same service layer, so
validation, versioning, and events behave identically whether you use the CLI,
the HTTP API, or the MCP tools — the examples below show all three, and the
responses are real output from the bundled demo workspace.

New to Cards? Start with [Get started](get-started.md) to install it and serve
a board; come back here when you want the full operation reference.

## A working session

A realistic loop, with a human and an agent sharing one board:

1. **Capture the work.** You add cards from wherever you are — the web board,
   or `cards create --type task --title "..."` mid-thought in the terminal.
2. **Let the agent survey.** Point your harness at the board
   ([MCP quickstart](agents/mcp.md)) and ask it to review the open cards and
   propose a sprint. It reads real typed fields — not a stale plan file — and
   records its proposals as comments.
3. **Work runs on the board.** The agent takes cards with `take_next`, moves
   them through the columns, appends work-log entries (commit hashes, notes)
   as it goes. You watch the live board, drag priorities around, and leave
   comments the agent picks up on its next pass. Neither of you can trample
   the other: every write is version-checked.
4. **Review and close.** Cards land in `review` with their evidence attached —
   the work log, the branch, the conversation. You review, merge the PR, move
   the card to `done` (or transition rules stop the agent at `review`, so the
   final move is yours).
5. **Persist the board.** `cards export --state-only` snapshots everything to
   `backlog.jsonl`; commit it with the merge. The board state ships with the
   code, in the same PR history.

Next time anyone picks the project up — you on another machine, a new
contributor, an agent in a fresh sandbox — they clone the repo and have the
full board: what was done, what's open, why decisions went the way they did.

## Your work lives in the repo

Two things in git carry everything:

- **`definitions/`** — the schema (card types, columns, boards). Plain JSON,
  always committed.
- **`backlog.jsonl`** — the state snapshot (cards, comments, links, users).
  One JSON object per line, so it diffs cleanly and reads like a changelog in
  review.

The live `work-cards.db` (SQLite) is machine-local and gitignored — it's the
working store, not the record. `cards import` restores a snapshot into a fresh
workspace and refuses a non-empty database, so a restore can never silently
overwrite live state. (The reference form of these commands is in
[Snapshot: export / import](#snapshot-export-import) below.)

`scripts/board.sh` wraps the loop, including a pre-commit hook so the snapshot
never goes stale:

```console
$ scripts/board.sh export           # snapshot → commit → push
$ scripts/board.sh import           # restore on another machine
$ scripts/board.sh import --force   # re-sync a machine that already has state
$ scripts/board.sh install-hook     # auto-export before every commit
```

What the committed snapshot buys you: handoff is `git clone` (no migration, no
access requests); reviewers see which cards closed and what was discussed in
the PR diff; a fresh agent sandbox imports it and has full context; and the
format is JSON you can read with `jq`, so there's no lock-in in either
direction. The fine-grained event journal stays SQLite-owned and is not part
of the snapshot — the snapshot rebuilds card state, not the machine-local log.
See [events](events/index.md) for what lives where.

## The operations

Conventions that apply everywhere:

- **Actor** — writes record who did them. CLI: `CARDS_USER` or `--as`. HTTP:
  the `X-Work-Cards-Actor` header. MCP: `CARDS_USER` in the server's
  environment.
- **Versions** — mutating a card requires its current `version`. A stale write
  fails with `version_conflict` and the current card attached; re-read and
  retry. See [Update a card](#update-a-card) for the exact payload.
- **Card ids** — anywhere a command takes a card id, the 8-character short id
  shown on the board works too (`4430ab22` for
  `card_4430ab22bf58417c8f64af6bbe8738a2`). An ambiguous short id fails with
  the candidates listed rather than guessing.
- **CLI backends** — CLI commands run serverless against the workspace folder
  by default; set `CARDS_URL` to target a running server (preferred when one
  is up, so its event stream and hooks observe your writes). Setup, output
  modes, and environment variables: [CLI usage](reference/cli.md).

!!! tip "Your workspace serves its own OpenAPI spec"
    A running server exposes an OpenAPI 3.1 document at `/v1/openapi.json`,
    generated from the live workspace — the per-type field schemas in it are
    your card types, not a generic placeholder. Point codegen or an API client
    at it for exact request shapes.

### Introspect the workspace

Learn the columns, card types, boards, and users before doing anything else —
agents especially should drive decisions from this rather than guessing.

=== "CLI"

    ```console
    $ cards workspace show
    {
      "workspace": { "id": "demo", "columns": [...], "tag_set": [...] },
      "card_types": { "programming-task": { "fields": [...] }, ... },
      "boards": { "engineering": { "columns": [...], "transitions": {...} } },
      "current_schema_versions": { "programming-task": 1, ... }
    }
    ```

=== "HTTP"

    ```console
    $ curl http://127.0.0.1:8787/v1/workspace
    ```

    Returns the same document: `workspace`, `card_types`, `boards`,
    `current_schema_versions`.

=== "MCP"

    Call the `workspace` tool (no arguments). Same document as the API.

### Create a card

Fields are validated against the card type's schema; unknown fields and bad
enum values are rejected with the allowed options.

=== "CLI"

    ```console
    $ cards create --type programming-task --title "Add rate limiting to /v1" \
        --status todo --field branch=feat/rate-limit --field kind=feature \
        --field description="Token bucket per actor on mutating routes."
    {
      "id": "card_4430ab22bf58417c8f64af6bbe8738a2",
      "type_id": "programming-task",
      "title": "Add rate limiting to /v1",
      "status": "todo",
      "fields": {
        "branch": "feat/rate-limit",
        "description": "Token bucket per actor on mutating routes.",
        "kind": "feature"
      },
      "version": 1,
      "created_by": "dev",
      "created_at": "2026-07-13T15:03:36Z",
      ...
    }
    ```

=== "HTTP"

    ```console
    $ curl -X POST http://127.0.0.1:8787/v1/cards \
        -H "Content-Type: application/json" \
        -H "X-Work-Cards-Actor: dev" \
        -H "Idempotency-Key: create-rate-limit-1" \
        -d '{
          "type_id": "programming-task",
          "title": "Add rate limiting to /v1",
          "status": "todo",
          "fields": { "branch": "feat/rate-limit", "kind": "feature",
                      "description": "Token bucket per actor on mutating routes." }
        }'
    ```

    Returns the created card (`version: 1`). `Idempotency-Key` is scoped per
    actor — a retried request returns the original card instead of creating a
    duplicate.

=== "MCP"

    Each card type gets its own generated tool; schema fields are top-level
    arguments:

    ```json
    {
      "tool": "create_programming-task",
      "arguments": {
        "title": "Add rate limiting to /v1",
        "status": "todo",
        "branch": "feat/rate-limit",
        "kind": "feature",
        "description": "Token bucket per actor on mutating routes."
      }
    }
    ```

    Returns the created card. Note: the MCP surface has no idempotency keys —
    use HTTP if you need retry-safe creation.

### Read a card

=== "CLI"

    ```console
    $ cards get 4430ab22          # short id from the board works
    { "id": "card_4430ab22bf58417c8f64af6bbe8738a2", "title": "Add rate limiting to /v1", ... }
    ```

=== "HTTP"

    ```console
    $ curl http://127.0.0.1:8787/v1/cards/card_4430ab22bf58417c8f64af6bbe8738a2
    ```

=== "MCP"

    ```json
    { "tool": "get_card", "arguments": { "card_id": "card_4430ab22bf58417c8f64af6bbe8738a2" } }
    ```

### List and search

Filters compose; results are paged (`items` + `next_cursor`). Full-text search
uses the same entry point.

=== "CLI"

    ```console
    $ cards list --board engineering --status in_progress
    {"id":"card_4430ab22...","title":"Add rate limiting to /v1","status":"in_progress",...}
    {"id":"card_7e090c38...","title":"Fix cursor pagination off-by-one","status":"in_progress",...}

    $ cards list --q "rate limiting" -q     # --q = search filter; -q (quiet) = ids only
    card_4430ab22bf58417c8f64af6bbe8738a2
    ```

    Other filters: `--owner`, `--type`, `--blocked`, `--has-link`,
    `--link-target`, `--limit`, `--cursor`; `--include links,comments`
    eager-loads relations.

=== "HTTP"

    ```console
    $ curl "http://127.0.0.1:8787/v1/cards?board=engineering&status=in_progress"
    { "items": [ ... ], "next_cursor": "" }

    $ curl "http://127.0.0.1:8787/v1/cards?q=rate+limiting"
    ```

=== "MCP"

    ```json
    { "tool": "list_cards",   "arguments": { "board_id": "engineering", "status": "in_progress" } }
    { "tool": "search_cards", "arguments": { "query": "rate limiting" } }
    ```

### Update a card

Requires the current `version`. The two failure modes below are the ones
agents hit most — both are structured and recoverable.

=== "CLI"

    ```console
    $ cards patch 4430ab22 --status in_progress --version 1
    { ..., "status": "in_progress", "version": 2 }

    $ cards patch 4430ab22 --status review --version 1      # stale
    cards: version_conflict: Stale version; another mutation has occurred.

    $ cards patch 4430ab22 --version 2 --field kind=chore   # bad enum
    cards: unknown_enum (kind): Unknown enum value. [valid: feature, bug, design, infra]
    ```

=== "HTTP"

    ```console
    $ curl -X PATCH http://127.0.0.1:8787/v1/cards/card_4430ab22... \
        -H "Content-Type: application/json" -H "X-Work-Cards-Actor: dev" \
        -d '{ "version": 2, "status": "review" }'
    ```

    A stale version returns `409` with the current card attached, so one
    round-trip recovers:

    ```json
    {
      "error": "version_conflict",
      "message": "Stale version; another mutation has occurred.",
      "card": { "id": "card_4430ab22...", "status": "in_progress", "version": 4, ... }
    }
    ```

=== "MCP"

    ```json
    {
      "tool": "update_programming-task",
      "arguments": { "card_id": "card_4430ab22...", "version": 2, "status": "review" }
    }
    ```

    Validation errors carry `valid_options` so the agent can correct itself
    and retry.

### Claim, release, take-next

Ownership and queue semantics. `take-next` atomically claims the next eligible
card — two workers calling it concurrently get different cards.

=== "CLI"

    ```console
    $ cards claim 4430ab22 --version 2 --status in_progress

    $ cards take-next --board engineering --type programming-task --status in_progress
    {
      "card": {
        "id": "card_10bb3d54...",
        "title": "Add OpenAPI spec for /v1/cards",
        "owner": "dev",
        "status": "in_progress",
        "version": 2,
        ...
      }
    }
    ```

=== "HTTP"

    ```console
    $ curl -X POST "http://127.0.0.1:8787/v1/cards/take-next" \
        -H "Content-Type: application/json" -H "X-Work-Cards-Actor: dev" \
        -d '{ "board_id": "engineering", "type": "programming-task", "status": "in_progress" }'
    ```

    Release ownership with `POST /v1/cards/{id}/release` (optional new
    status; version-checked).

=== "MCP"

    ```json
    { "tool": "take_next", "arguments": { "board_id": "engineering", "type": "programming-task" } }
    { "tool": "claim",     "arguments": { "card_id": "card_4430ab22...", "version": 2 } }
    { "tool": "release",   "arguments": { "card_id": "card_4430ab22...", "version": 3 } }
    ```

    Note: `release` is HTTP/MCP-only — the CLI has no release verb today.

### Comment

Comments append to the card and bump its version (they are part of the
audited record).

=== "CLI"

    ```console
    $ cards comment add 4430ab22 --body "Bucket size 100, refill 10/s."
    { ..., "comments": [ { "id": "cm_ba03d13a0946436b", "author": "dev",
      "body": "Bucket size 100, refill 10/s.", ... } ], "version": 3 }
    ```

=== "HTTP"

    `POST /v1/cards/{id}/comments` with `{ "body": "..." }`.

=== "MCP"

    ```json
    { "tool": "add_comment", "arguments": { "card_id": "card_4430ab22...", "body": "Bucket size 100, refill 10/s." } }
    ```

### Work-log entries (repeating fields)

Typed append-only feeds — the natural place for agents to record commits,
results, and measurements. Entries are version-checked like any mutation.

=== "CLI"

    ```console
    $ cards append 4430ab22 work_log --version 3 --entry-json \
        '{"commit_hash":"9d4e1f2","notes":"middleware + tests","author":"dev","timestamp":"2026-07-13T09:30:00Z"}'
    {
      "fields": {
        "work_log": [
          { "entry_id": "ent_831a651d86294d1e", "commit_hash": "9d4e1f2",
            "notes": "middleware + tests", "author": "dev",
            "timestamp": "2026-07-13T09:30:00Z" }
        ]
      },
      "version": 4
    }
    ```

    `patch-entry` and `remove-entry` edit or delete an entry by `entry_id`.

=== "HTTP"

    `POST /v1/cards/{id}/entries/{field}` with the entry JSON and `version`.

=== "MCP"

    ```json
    {
      "tool": "append_entry",
      "arguments": {
        "card_id": "card_4430ab22...", "field": "work_log", "version": 3,
        "entry": { "commit_hash": "9d4e1f2", "notes": "middleware + tests",
                   "author": "dev", "timestamp": "2026-07-13T09:30:00Z" }
      }
    }
    ```

    `update_entry` and `remove_entry` take `entry_id`.

### Links

Typed relations between cards (`depends-on`, `blocks`, … — the vocabulary is
the workspace's `link_types`).

=== "CLI"

    ```console
    $ cards link add 4430ab22 --type depends-on --target 10bb3d54
    $ cards link remove 4430ab22 depends-on card_10bb3d54...
    ```

=== "HTTP"

    `POST /v1/cards/{id}/links` with `{ "type": "depends-on", "target": "card_..." }`.

=== "MCP"

    ```json
    { "tool": "add_link", "arguments": { "card_id": "card_4430ab22...", "type": "depends-on", "target": "card_10bb3d54..." } }
    ```

### Attachments (artifact fields)

Store files against an `artifact` field; local artifacts live under the
workspace's `artifacts/` root with path confinement.

=== "CLI"

    ```console
    $ cards attach 4430ab22 attachment ./bench-results.png
    ```

=== "HTTP"

    `POST /v1/cards/{id}/artifacts/{field}` (multipart upload);
    `GET /v1/artifacts/{uri}` fetches bytes.

=== "MCP"

    `attach_artifact` takes base64 `content`; `get_artifact` returns base64
    with size. For large files prefer the HTTP surface.

### History, events, breaches

Read-side tools for resuming work and watching the board.

=== "CLI"

    ```console
    $ cards history 4430ab22
    {"at":"...","actor":"dev","type":"card_created","summary":"card created: \"Add rate limiting to /v1\" (type=programming-task, status=todo)"}
    {"at":"...","actor":"dev","type":"status_changed","summary":"status: todo → in_progress"}
    {"at":"...","actor":"dev","type":"comment_added","summary":"comment added (cm_ba03d13a0946436b)"}

    $ cards events 4430ab22 --limit 2      # raw events with diffs
    {"id":7,"type":"status_changed","diff":{"before":"todo","after":"in_progress"},...}

    $ cards breaches
    {"type":"lane_drained","scope":"board","board_id":"engineering","column":"todo"}
    {"type":"wip_exceeded","scope":"board","board_id":"engineering","column":"in_progress","count":4,"limit":3}
    ```

=== "HTTP"

    `GET /v1/cards/{id}/history` · `GET /v1/cards/{id}/events` ·
    `GET /v1/breaches` · live stream: `GET /v1/events/stream` (SSE, with
    `Last-Event-ID` replay) — see [consuming events](events/integration.md).

=== "MCP"

    ```json
    { "tool": "history",  "arguments": { "card_id": "card_4430ab22..." } }
    { "tool": "events",   "arguments": { "since": 0, "types": ["status_changed"] } }
    { "tool": "breaches", "arguments": { "board_id": "engineering" } }
    ```

    There is no SSE subscription over MCP — poll `events` with `since`, or use
    the HTTP stream.

### Snapshot: export / import

CLI-only, local (reads SQLite directly). This is the git-portability
mechanism described above: commit the snapshot, restore it elsewhere.

```console
$ cards export --state-only --out backlog.jsonl     # definitions + cards, links, comments
$ cards import --in backlog.jsonl                   # refuses a non-empty DB — never overwrites
```

### Server & workspace commands (CLI-only)

| Command | What it does |
|---|---|
| `cards init [dir] [--global]` | Scaffold a workspace (`./.cards`, or `~/.cards`) |
| `cards serve [--port 8787] [--seed] [--run-extensions] [--watch]` | HTTP + web UI server |
| `cards mcp [--workspace <dir>]` | stdio MCP server |
| `cards reload` | Reload definitions on a running server |
| `cards run-extensions` | Run the hook supervisor standalone |
| `cards do <id> [--param k=v]` | Invoke a `run` extension |
| `cards extensions [show <id>]` | List/show declared extensions |
| `cards users register --id ID [--kind human\|agent]` | Register an actor |
| `cards boards show [id]` | Board definition + introspection |
| `cards version` | Version, commit, build info |

Global flags on every command: `--url`, `--as`, `--workspace`, `--json`,
`--jsonl`, `--quiet/-q`. Full setup detail: [CLI usage](reference/cli.md).
