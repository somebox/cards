# Concepts

## The sixty-second version

Cards is this project's coordination board, and it can live *in* the repo. A
folder of JSON definitions describes the kinds of cards you track (card types),
the states they move through (columns), and the views you look at them through
(boards). The `cards` binary turns that folder into a web board, a REST API, a
CLI, MCP tools for agents, and a terminal UI — all enforcing the same schema.
Board state is snapshotted to a JSONL file and committed alongside the
definitions, so cloning the repo gets you the whole board: cards, comments,
links, and field values. (The event journal stays in the local SQLite file;
a full export can include it when you need an audit dump.) There is no
account, no hosted service, and nothing to sign into.

That's the whole pitch. The rest of this page is the vocabulary and how the
pieces fit together in different setups.

## Why not something that already exists?

There's plenty of tracking software, and a `TODO.md` is genuinely fine for
many projects. Cards earns its place in the gap between them:

**Against a markdown file** — markdown has no structure to defend. Anyone (and
especially any agent) can rewrite the whole file; two writers conflict; there's
no history of *why* a line changed, no typed fields to query, no way to say
"give me the next open bug." Cards gives every item typed, validated fields, a
versioned history of every change, and queries — while staying just as local
and just as much *yours* as the markdown file was.

**Against a hosted tracker** — GitHub Issues, Jira, and Linear assume the
tracker is a service somebody else runs. Your project's memory ends up behind
an account, shaped by a vendor's model, and reachable only by their API. For a
small team that's mostly overhead; for an open-source project it splits the
project's state from the project's code; for agent workflows it ties your
agents to whatever integration the vendor ships.

Cards is built around the opposite assumption: **the tracker is part of the
project.** That has two consequences worth naming:

- **Open source.** Fork the repo, get the board. Maintainer handoff is a
  `git pull`. Anyone auditing the project can read not just what the code does
  but how the work unfolded — the backlog is right there, diffable, in plain
  JSON.
- **Agent work.** Card types become typed MCP tools automatically, and
  validation errors tell the agent what was allowed so it can correct itself.
  Versioned writes mean two agents can't silently overwrite each other, and
  everything an agent does — claims, field updates, comments, work logs — is
  on the record for a human to review. The board is persistent shared memory,
  which a scratch `plan.md` isn't.

## The mental model in one paragraph

A **workspace** is a database with a schema. The schema is a set of
**definitions** — **card types** (field shapes), **columns** (statuses), and
**boards**. **Cards** are rows that belong to the workspace, not to any board.
A **board** is a saved view: it chooses which card types and columns to show,
adds transition rules, and can scope itself to a subset of cards. Card types are
shared across every board in the workspace; boards select and filter, they do
not define. One server process serves exactly one workspace.

## The model in five terms

- **Workspace** — one directory, one tracker. It holds the definitions, the
  live SQLite database, and uploaded artifacts. One server process serves one
  workspace.
- **Definitions** — the JSON files under `definitions/` that declare card
  types, columns, boards, and extensions. They are the schema, they are meant
  to be committed, and each workspace owns a complete copy (there is no global
  registry to depend on). Extensions may also be declared as YAML; see
  [Extensions](../extensions/index.md).
- **Card types** — field schemas. An ordered list of typed fields (ten field
  types: `string`, `text`, `number`, `date`, `enum`, `tags`, `user`,
  `card_link`, `repeating`, `artifact`) plus the columns cards of that type
  may occupy. One definition drives the web form, the API contract, the CLI,
  and the generated MCP tools.
- **Cards** — the work items. Every card has the same envelope (`id`, `title`,
  `status`, `owner`, `version`, links, comments, timestamps); the custom data
  lives under `fields` and is validated by the card type. A card's `status`
  is always one of the workspace's column ids. Cards belong to the workspace,
  not to a board.
- **Boards** — saved views. A board picks which card types and columns to
  show, can enforce a transition graph (`todo → in_progress → review → done`),
  set WIP limits, and carry presentation hints. Boards select and filter;
  they never own cards. Several boards can slice the same workspace
  differently.

## Workspace layout

A workspace is a directory containing definitions and a SQLite database:

```text
my-workspace/                 # or ./.cards, or ~/.cards
  definitions/
    workspace.json            # columns, tags, link types, users, settings
    card-types/*.json         # field schemas
    boards/*.json             # views + transitions + presentation
    extensions.json           # declared hooks/services (YAML also accepted)
  work-cards.db               # live state (created on first serve; gitignored)
  backlog.jsonl               # portable state snapshot (committed)
  artifacts/                  # uploaded files
```

The live database is machine-local and authoritative while a server (or the
TUI) is running against it. The `definitions/` directory is the schema and is
meant to be committed. One process serves one workspace
(`cards serve --workspace <dir>`); running several workspaces means running
several processes on different ports.

In a git-tracked project the workspace is usually `./.cards` (or, in this
repo, also the frozen demo copy under `examples/demo-workspace/`). Authoring
detail: [Workspace & boards](../reference/workspace-and-boards.md).

## Definitions are workspace-local

Definitions live in the workspace's `definitions/` directory and nowhere else.
There is no global, shared, or inherited definition library: each workspace owns
a complete, self-contained copy of its schema. That locality is deliberate — it
is what makes a workspace portable as `definitions/` + a JSONL export (see
[Import / export](#import--export-and-portability)).

A new workspace gets its schema from `cards init` (baked-in starter definitions)
or by copying another workspace's `definitions/` and editing the copy.
Customizing a workspace means editing the JSON under its own `definitions/` and
reloading the server (`cards reload`, or `cards serve --watch`). Reloading
definitions does not migrate existing cards — see schema versioning in
[Card definitions](../reference/card-definitions.md).

## Card types

A card type is a field schema: an ordered list of typed fields plus the columns
a card of that type may occupy. The same definition drives the HTTP API, the
CLI, the MCP tool schema, and the UI form — adding a field changes the contract
everywhere at once.

**Card types are global within a workspace.** Every board draws from the same
type catalog. A type is defined once in `definitions/card-types/<id>.json` and
is available to all boards. Authoring guide:
[Card definitions](../reference/card-definitions.md).

## Cards

A card has a fixed envelope and schema-defined fields. The envelope gives every
card an `id`, `type_id`, `title`, `status`, `owner`, `version`, links, comments,
and timestamps; the custom data lives under `card.fields`. A card's `status` is
always one of the workspace's column ids. Cards belong to the workspace — a card
is never "in" a board; it simply matches (or does not match) a board's view.

`owner` is the canonical assignment field, used by built-in filters
(`owner=me`) and by `claim` / `take-next` / `release`.

## Boards

A board is a Kanban lens over the workspace's cards. It does not own cards; it
scopes and presents them. A board defines:

- **`columns`** — which statuses (workspace column ids) appear, and their order.
- **`card_type_ids`** — which card types appear on the board.
- **`default_filter`** — an optional [query DSL](../spec/query-dsl.md)
  expression that scopes which cards the board shows, beyond type and column.
- **`transitions`** + **`settings.enforce_transitions`** — an optional status
  graph the board enforces (e.g. `todo → in_progress → review → done`).
- **`wip_limits`** / **`monitors`** — optional column caps and condition
  signals (lane drained, idle, timeouts).
- **`presentation`** — UI hints: preview fields per type, style field, detail
  sections, and named saved filters. Presentation never affects validation or
  writes.

### How a board scopes cards

When a query names a board (`?board_id=eng` / `--board eng`), the service folds
the board's scope into the query:

| Mechanism | Behavior |
|---|---|
| `card_type_ids` | Restricts to those types, unless the caller already set a type filter. |
| `columns` | Restricts to those statuses, unless the caller already set a status filter. |
| `default_filter` | **Hard boundary.** AND-ed with any caller filter, so a board view can be narrowed but never widened past its own scope. |

`card_type_ids` and `columns` are convenience scopes the caller may override;
`default_filter` is an isolation boundary the caller cannot escape.

!!! note "SSE / event-stream membership"
    Board-scoped event streams (SSE) and board membership used by condition
    census currently key off `card_type_ids` only. `default_filter` is applied
    to card listings and queries, not yet to live event-stream membership. If
    you need stream-level isolation between boards that share a type, prefer
    a card type per board (strategy 1 below) until that gap closes.

## Multiple boards in one workspace

Because card types are global and boards filter, several boards can present
different slices of the same workspace. This is the natural way to model a
project with several sub-apps: one workspace, one board per sub-app.

There are two isolation strategies:

**1. A card type per sub-app.** Give each sub-app its own type and scope each
board with `card_type_ids`:

```jsonc
// boards/web.json
{
  "id": "web",
  "name": "Web app",
  "columns": ["todo", "in_progress", "review", "done"],
  "card_type_ids": ["web-task"]
}
```

**2. A shared type plus a discriminator.** Use one `task` type with a
discriminating field (an `enum` like `app`, or a tag) and scope each board with
`default_filter`:

```jsonc
// boards/web.json
{
  "id": "web",
  "name": "Web app",
  "columns": ["todo", "in_progress", "review", "done"],
  "card_type_ids": ["task"],
  "default_filter": { "fields.app": { "$eq": "web" } }
}
```

Both keep listings clean: a card created for the API sub-app will not appear on
the web board. Choose strategy 1 when sub-apps need genuinely different fields
(or need event-stream isolation today); choose strategy 2 when they share a
shape and differ only by which sub-app they belong to.

## Multiple workspaces

When two efforts need fully separate vocabularies, columns, or histories, use
separate workspaces — separate directories, each served by its own process on
its own port. Workspaces never share definitions or data; the boundary is total.

Rule of thumb:

- **New project, same vocabulary** → a new **board** in the existing workspace.
- **New project, different vocabulary or hard isolation** → a new
  **workspace**.

## Import / export and portability

`cards export --state-only --out backlog.jsonl` writes the portable form of
board state — cards, comments, links, users — as sorted JSONL that diffs
cleanly. `cards import --in backlog.jsonl` restores it into a fresh, empty
workspace (it refuses a non-empty DB). The event journal is SQLite-owned and
omitted from state-only snapshots; a full export (without `--state-only`)
includes it when you need an audit dump.

Commit `backlog.jsonl` next to `definitions/`. The live `work-cards.db` is
machine-local and gitignored; the JSON is the durable, shared form. This is the
backup, sync, *and* collaboration mechanism — see
[the workflow](../using-cards.md) for day-to-day export/import and
`scripts/board.sh`.

## Setups by use case

**Personal.** One workspace in your home directory (`~/.cards`, or `$CARDS_HOME`),
many boards inside it — one per project or area. Data persists outside any repo.
`cards` with no arguments finds it when there is no nearer `.cards/`.

**A project repo.** A `.cards/` workspace committed to the repo (definitions +
`backlog.jsonl`). Every contributor — human or agent — runs a local server (or
the TUI) against the checkout. This project does exactly that: its live
dogfooding backlog lives in [`.cards/`](../../.cards/); the frozen demo material
also ships under [`examples/demo-workspace/`](../../examples/demo-workspace/).

**Developing Cards (dogfooding).** Point explicitly at the checkout workspace:

```bash
cards serve --workspace ./.cards --port 8787
# or the frozen demo:
cards serve --workspace ./examples/demo-workspace --port 8787 --seed
```

**Team / shared host.** The same repo workspace, served on one host behind a
reverse proxy. There is no baked-in auth by design — isolation is the host's
responsibility. See [users & auth](../guides/users-and-auth.md).

## Setup, discovery, and onboarding

Cards resolves a workspace the way git resolves a repository:

- **`cards init [dir] [--global]`** scaffolds a new workspace from baked-in
  starter definitions (columns `todo` / `doing` / `done`, a simple `task` type,
  and a `welcome` board) and seeds an onboarding board. By default it creates
  `./.cards/` in the current directory; `cards init --global` creates the
  personal workspace instead. It never clobbers an existing workspace.
- **`.cards/` is the workspace marker.** Running `cards serve` (or a bare
  `cards` for the TUI) with no `--workspace` walks up from the current directory
  to find the nearest `.cards/` that holds `definitions/workspace.json` — the
  way git resolves `.git/`. An explicit `--workspace` path may name the
  workspace dir itself or a project root whose `.cards` child is the workspace.
- **Global fallback.** With no `.cards/` found anywhere up the tree, `cards`
  uses a personal workspace at `~/.cards` (override with `CARDS_HOME`),
  creating and seeding it on first run when that path is empty.
- **The `welcome` board is the tutorial.** Its cards explain editing
  definitions, adding boards, the CLI/MCP surface, and export/import, so a fresh
  workspace is self-documenting. Delete them once you're oriented.

`cards serve --workspace <dir>` remains the explicit form (used, for example, by
CI and the frozen demo workspace); an explicit path that isn't already a
workspace is not auto-created. Customizing a workspace still means editing the
JSON under its own `definitions/` and reloading.

Quick path from a clean machine: [Get started](../get-started.md).

## Where to go next

- [The workflow](../using-cards.md) — how a project actually runs on Cards day to day.
- [Card definitions](../reference/card-definitions.md) — authoring card types.
- [Workspace & boards](../reference/workspace-and-boards.md) — columns, boards,
  transitions, and the workspace file layout.
- [Using Cards](../using-cards.md) — every operation with CLI, HTTP, and MCP
  examples.
- [Philosophy](philosophy.md) — the design principles and what Cards refuses
  to be.
