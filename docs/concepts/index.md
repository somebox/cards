# Concepts

## The sixty-second version

Cards is this project's issue tracker, and it lives in the repo. A folder of
JSON definitions describes the kinds of work we track (card types), the states
work moves through (columns), and the views we look at it through (boards).
The `cards` binary turns that folder into a web board, a REST API, a CLI, and
MCP tools for agents — all enforcing the same schema. Board state is
snapshotted to a JSONL file and committed alongside the definitions, so
cloning the repo gets you the whole board: cards, comments, links, history of
decisions. There is no account, no hosted service, and nothing to sign into.

That's the whole pitch. The rest of this page is the vocabulary and the
reasoning.

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

## The model in five terms

- **Workspace** — one directory, one tracker. It holds the definitions, the
  live SQLite database, and uploaded artifacts. One server process serves one
  workspace.
- **Definitions** — the JSON files under `definitions/` that declare card
  types, columns, boards, and extensions. They are the schema, they are meant
  to be committed, and each workspace owns a complete copy (there is no global
  registry to depend on).
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

## How a board scopes cards

When a query names a board, the service folds the board's scope into it:

| Mechanism | Behavior |
|---|---|
| `card_type_ids` | Restricts to those types, unless the caller filters types itself. |
| `columns` | Restricts to those statuses, unless the caller filters status itself. |
| `default_filter` | Hard boundary — AND-ed with any caller filter. A board view can be narrowed but never widened past its own scope. |

## One workspace or several?

Because card types are global within a workspace and boards filter, several
boards can present different slices of the same workspace — one board per
sub-app is the natural way to model a project with several parts. Isolate
either with a card type per sub-app (`card_type_ids`), or with one shared type
plus a discriminator field and a `default_filter`.

Rule of thumb:

- **New project, same vocabulary** → a new **board** in the existing workspace.
- **New project, different vocabulary or hard isolation** → a new
  **workspace** (a separate directory, served by its own process). Workspaces
  never share definitions or data.

## Portability is the point

`cards export --state-only` writes the whole board — cards, comments, links —
to a `backlog.jsonl` that diffs cleanly and is committed next to
`definitions/`. `cards import` restores it into a fresh workspace. The live
SQLite database is machine-local and gitignored; the JSON is the durable,
shared form. This is the backup, sync, *and* collaboration mechanism — see
[the workflow](../using-cards.md) for how it plays out day to day.

## Common setups

- **Personal.** One workspace in your home directory (`~/.cards`), many
  boards — one per project or area. `cards` with no arguments finds it.
- **A project repo.** A `.cards/` workspace committed to the repo
  (definitions + snapshot). Every contributor — human or agent — runs a local
  server against the checkout. This project does exactly that; the bundled
  demo workspace is the real backlog.
- **Team / shared host.** The same repo workspace, served on one host behind
  a reverse proxy. There is no built-in auth by design — see
  [users & auth](../guides/users-and-auth.md).

## Where to go next

- [The workflow](../using-cards.md) — how a project actually runs on Cards day to day.
- [Card definitions](../reference/card-definitions.md) —
  authoring card types.
- [Workspace & boards](../reference/workspace-and-boards.md) — columns, boards,
  transitions, and the workspace file layout.
- [Using Cards](../using-cards.md) — every operation with CLI, HTTP,
  and MCP examples.
- [Philosophy](philosophy.md) — the design principles and what Cards refuses
  to be.
