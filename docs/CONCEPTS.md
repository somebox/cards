# Concepts & Setups

This document defines the vocabulary of Cards and shows how the pieces fit
together in different use cases. For the API and field reference, see
[`SPEC.md`](SPEC.md); for workspace authoring, see
[`DEVELOPER-REFERENCE.md`](DEVELOPER-REFERENCE.md); for design principles, see
[`PHILOSOPHY.md`](PHILOSOPHY.md); for a code-verified drift audit of which
features are built vs. proposed, see
[`INTEGRATOR-REFERENCE.md`](INTEGRATOR-REFERENCE.md).

Sections marked **Planned** describe the intended model that is not yet
implemented. Everything else describes current behavior.

## The mental model in one paragraph

A **workspace** is a database with a schema. The schema is a set of
**definitions** — **card types** (field shapes), **columns** (statuses), and
**boards**. **Cards** are rows that belong to the workspace, not to any board.
A **board** is a saved view: it chooses which card types and columns to show,
adds transition rules, and can scope itself to a subset of cards. Card types are
shared across every board in the workspace; boards select and filter, they do
not define. One server process serves exactly one workspace.

## Workspace

A workspace is a directory containing definitions and a SQLite database:

```text
my-workspace/
  definitions/
    workspace.json          # columns, tags, link types, users, settings
    card-types/*.json       # field schemas
    boards/*.json           # views + transitions + presentation
    extensions.json         # declared hooks/services
  work-cards.db             # authoritative state (created on first serve)
  .cards/                   # extension workspace: ext/, logs/, sessions/
  artifacts/                # uploaded files
```

The database is authoritative. The `definitions/` directory is the schema, and
is meant to be committed to version control. One process serves one workspace
(`cards serve --workspace <dir>`); the `/v1/health` endpoint reports a single
workspace id. Running several workspaces means running several processes on
different ports.

## Definitions are workspace-local

Definitions live in the workspace's `definitions/` directory and nowhere else.
There is no global, shared, or inherited definition library: each workspace owns
a complete, self-contained copy of its schema. This locality is deliberate — it
is what makes a workspace portable as `definitions/` + a JSONL export (see
[Import / export](#import--export-and-portability)).

A new workspace gets its schema by copying starter definitions in, then editing
that copy. Customizing a workspace means editing the JSON under its own
`definitions/` and reloading the server. Reloading definitions does not migrate
existing cards (see schema versioning in [`SPEC.md`](SPEC.md)).

## Card types

A card type is a field schema: an ordered list of typed fields
(`string`, `text`, `number`, `date`, `enum`, `tags`, `user`, `card_link`,
`repeating`, `artifact`) plus the columns a card of that type may occupy. The
same definition drives the HTTP API, the CLI, the MCP tool schema, and the UI
form — adding a field changes the contract everywhere at once.

**Card types are global within a workspace.** Every board draws from the same
type catalog. A type is defined once in `definitions/card-types/<id>.json` and
is available to all boards.

## Cards

A card has a fixed envelope and schema-defined fields. The envelope gives every
card an `id`, `type_id`, `title`, `status`, `owner`, `version`, links, comments,
and timestamps; the custom data lives under `card.fields`. A card's `status` is
always one of the workspace's column ids. Cards belong to the workspace — a card
is never "in" a board; it simply matches (or does not match) a board's view.

`owner` is the canonical assignment field, used by built-in filters
(`owner=me`) and by `claim` / `take-next`.

## Boards

A board is a Kanban lens over the workspace's cards. It does not own cards; it
scopes and presents them. A board defines:

- **`columns`** — which statuses (workspace column ids) appear, and their order.
- **`card_type_ids`** — which card types appear on the board.
- **`default_filter`** — an optional [filter DSL](SPEC.md) expression that
  scopes which cards the board shows, beyond type and column.
- **`transitions`** + **`settings.enforce_transitions`** — an optional status
  graph the board enforces (e.g. `todo -> in_progress -> review -> done`).
- **`presentation`** — UI hints: preview fields per type, lane grouping, accent
  field, detail sections, and named saved filters.

### How a board scopes cards

When a query names a board (`?board_id=eng`), the service folds the board's
scope into the query:

| Mechanism | Behavior |
|---|---|
| `card_type_ids` | Restricts to those types, unless the caller already set a type filter. |
| `columns` | Restricts to those statuses, unless the caller already set a status filter. |
| `default_filter` | **Hard boundary.** AND-ed with any caller filter, so a board view can be narrowed but never widened past its own scope. |

`card_type_ids` and `columns` are convenience scopes the caller may override;
`default_filter` is an isolation boundary the caller cannot escape.

## Multiple boards in one workspace

Because card types are global and boards filter, several boards can present
different slices of the same workspace. This is the natural way to model a
project with several sub-apps: one workspace, one board per sub-app.

There are two isolation strategies:

**1. A card type per sub-app.** Give each sub-app its own type and scope each
board with `card_type_ids`:

```jsonc
// boards/web.json
{ "id": "web", "name": "Web app",
  "columns": ["todo", "in_progress", "review", "done"],
  "card_type_ids": ["web-task"] }
```

**2. A shared type plus a discriminator.** Use one `task` type with a
discriminating field (an `enum` like `app`, or a tag) and scope each board with
`default_filter`:

```jsonc
// boards/web.json
{ "id": "web", "name": "Web app",
  "columns": ["todo", "in_progress", "review", "done"],
  "card_type_ids": ["task"],
  "default_filter": { "fields.app": { "$eq": "web" } } }
```

Both give clean isolation: a card created for the API sub-app will not appear on
the web board. Choose strategy 1 when sub-apps need genuinely different fields,
and strategy 2 when they share a shape and differ only by which sub-app they
belong to.

> Note: board-scoped event streams (SSE) currently determine board membership by
> `card_type_ids` only; `default_filter` is applied to card listings, not yet to
> event-stream membership.

## Multiple workspaces

When two efforts need fully separate vocabularies, columns, or histories, use
separate workspaces — separate directories, each served by its own process on
its own port. Workspaces never share definitions or data; the boundary is total.

Rule of thumb:

- **New project, same vocabulary →** a new **board** in the existing workspace.
- **New project, different vocabulary or hard isolation →** a new **workspace**.

## Import / export and portability

`cards export --workspace <dir>` dumps the full workspace state (cards, events,
users) as JSONL. `cards import` restores such a dump into a fresh, empty
workspace. Committing a JSONL export alongside `definitions/` makes the whole
workspace reproducible from version control without a database server — this is
the intended sync and backup mechanism for shared workspaces.

## Setups by use case

**Personal / projects.** One workspace in the user's home, many boards inside it
— one per project or area. Data persists outside any repo. *(See the Planned
section for the zero-config launch path.)*

**Developing Cards (dogfooding).** The workspace is `examples/demo-workspace`,
committed in the repo, with an `engineering` board holding the real build
backlog. Run it explicitly with `cards serve --workspace ./examples/demo-workspace`.

**Team / shared.** A workspace committed to a project repo (definitions plus a
JSONL export under version control). Each contributor runs a local server
against the checkout, or one host serves it behind a reverse proxy. There is no
baked-in auth; isolation is the host's responsibility (see
[`ARCHITECTURE.md`](ARCHITECTURE.md)).

## Setup and customization

Cards resolves a workspace the way git resolves a repository:

- **`cards init`** scaffolds a new workspace from baked-in starter definitions
  (the columns `todo`/`doing`/`done`, a simple `task` type, and a `welcome`
  board) and seeds an onboarding board. By default it creates `./.cards/` in the
  current directory; `cards init --global` creates the personal workspace
  instead. It never clobbers an existing workspace.
- **`.cards/` is the workspace marker.** Running `cards` with no arguments walks
  up from the current directory to find the nearest `.cards/` (which holds
  `definitions/`, `work-cards.db`, and the `ext/`, `logs/`, `sessions/` subdirs)
  and serves it — the way git resolves `.git/`.
- **Global fallback.** With no `.cards/` found anywhere up the tree, `cards`
  serves a personal workspace at `~/.cards` (override with `CARDS_HOME`),
  creating and seeding it on first run.
- **The `welcome` board is the tutorial.** Its cards explain editing
  definitions, adding boards, the CLI/MCP surface, and export/import, so a fresh
  workspace is self-documenting. Delete them once you're oriented.

`cards serve --workspace <dir>` remains the explicit form (used, for example, by
the repo's `examples/demo-workspace`); an explicit path is never auto-created.
Customizing a workspace still means editing the JSON under its own
`definitions/` and restarting.
