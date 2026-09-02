# cards CLI reference (condensed, field-verified)

Everything runs through the one `cards` binary. `cards <command> --help` for any command's
flags. This file is the deeper surface behind SKILL.md; the upstream docs are at
<https://somebox.github.io/cards/> (source: `docs/` in the cards repo).

## Environment & backends

| Variable | Purpose |
|---|---|
| `CARDS_URL` | API base of a running server. **Unset = serverless** (in-process against the workspace). |
| `CARDS_WORKSPACE` | Workspace dir for serverless mode — must be the `.cards` dir itself, not its parent. |
| `CARDS_USER` | Default actor for writes (`--as` overrides per command). |

- Client verbs (`list`/`get`/`create`/`patch`/`comment`/...) accept `--url` and `--workspace`
  as global flags, but combining `--workspace` with `--url` is an error.
- Prefer the server when one is running: serverless writes bypass its event bus (live UI and
  hooks won't see them).
- Global flags on every command, placed **before the verb**: `--url`, `--as`, `--workspace`,
  `--json`, `--jsonl`, `--quiet`/`-q`.

## Working with cards

| Command | Notes |
|---|---|
| `list` | Filters: `--board --owner --status --type --q --blocked --has-link --link-target --limit --cursor`; `--include links,comments`. Order: `updated_at DESC, id DESC`. Default output JSONL. |
| `get <id>` | One card as JSON. 8-char short ids fine (**bare hex only** — `card_` + short hex is `not_found`); ambiguous short ids fail listing candidates. |
| `create` | `--type T --title T [--status S] [--field k=v]... [--tag t]... [--dry-run]`. **No `--body`.** No owner at create. |
| `patch <id>` | `--version N [--title] [--status] [--owner] [--field k=v]... [--dry-run]`. Stale version → `version_conflict` with current card on stderr. |
| `claim <id>` | `--version N [--status S]` — sets owner to the actor. Owner must be a registered user (`users register`). |
| `take-next` | Atomically claim oldest unowned match: `[--type] [--board] [--assign-to] [--status] [--filter-file]`. `{card:null}` = nothing eligible. |
| `comment add <id> --body B` | Appends evidence; **bumps card version**. `cards comment <id> --body B` is an alias for add. `comment edit <id> <comment_id>` to fix. |
| `link add/remove <id>` | `--type T --target ID [--note N]`. Types come from the workspace (`cards workspace show`), not a fixed enum. Typical boards declare `depends-on` / `blocked-by` / `related`; hierarchy is a `part-of` type (child → parent) — see [project-practices.md](project-practices.md). Stored on the source. Idempotent. |
| `append <id> <field>` | Repeating-field entry: `--version N --entry-json '{...}'`. Repeating fields are NOT patchable via `patch`. |
| `attach <id> <field> <file>` | Upload to an `artifact` field (screenshots, evidence files). |
| `delete <id>` | Leaves a tombstone event. |

## Reading history and state

| Command | Notes |
|---|---|
| `history <id>` | Resumption-ready timeline: creates, moves, comments, entries. |
| `events <id>` | Raw events with `{before, after}` diffs; `[--types t1,t2] [--limit N]`; `events stream` follows live. |
| `feed` | Workspace-wide event feed, **oldest-first, id-ascending** (`--cursor`/`--since` are event-id floors, not timestamps). Built for catch-up from a known point, not for "latest activity" — use `list` ordering for recency. |
| `breaches` | Current WIP / drained-lane / blocked condition violations. |
| `workspace show` | Columns, card types, boards, users — read this before assuming column names. |
| `boards show [id]` | Board definition (filters, transitions). |

## Workspace lifecycle

| Command | Notes |
|---|---|
| `init [dir] [--global]` | Scaffold `.cards/` (or `~/.cards` personal) and install this skill into `.claude/skills/cards/`. `--no-skill` opts out; an existing skill directory is never overwritten. To update, review local edits, delete it, and re-run init. |
| `serve` | `[--workspace] [--port 8787] [--seed] [--run-extensions] [--watch]`. Web UI at `/ui/boards/<id>`. |
| `export` | `[--out F] [--state-only] [--with-artifacts]`. `--state-only` = definitions + current cards/links/comments (+ delete tombstones), small and diff-clean; the event log stays SQLite-owned. |
| `import` | `--in F [--with-artifacts]`. **Refuses a non-empty DB** — never a silent overwrite. |
| `users register` | `--id ID [--kind human\|agent] [--display-name N]` — required before an actor can *own* cards (comments/creates need no registration). |
| `reload` | Reload definitions on a running server. |
| `version` | Version + commit + build info. |

## Output modes

- `--json` one object (default for get/create/patch); `--jsonl` newline-delimited (default for
  list/events); `-q` ids only, built for `xargs`:
  ```bash
  cards list --status done -q | xargs -n1 cards get -q
  cards list --board engineering --status in_progress | jq -r .title
  ```
- Errors are structured on stderr: `code (field): message [valid: ...]`.

## The data model in one paragraph

A card = fixed envelope (`id`, `type_id`, `title`, `status`, `owner`, `tags`, `links`,
`comments`, `version`, timestamps) + schema-validated custom `fields` (types: string, text,
number, date, enum, tags, user, card_link, repeating, artifact). Cards live in ONE workspace;
boards are filtered views, not containers (membership derives from `type_id`). Status values
are workspace column ids. `?blocked=true` means: has a `depends-on`/`blocked-by` link whose
target isn't `done` yet — when all targets reach done, the card drops out of the blocked set.

## Gotchas learned live

- Global flags before the verb — `cards list --url ...` does not work.
- `patch` after `comment add` needs a fresh `get` (the comment bumped the version).
- `CARDS_WORKSPACE` pointing at the *parent* of `.cards` silently opens an empty workspace.
- The Mongo-style filter DSL (`$and/$or/...`) exists only in board `default_filter` and
  `take-next --filter-file` — not on `list`.
- Timestamps are UTC; convert before making local-time claims.
- A bare `cards` on a TTY opens the TUI; in scripts/pipes it prints usage (harmless).
