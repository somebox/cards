---
name: cards
description: >-
  Drive the `cards` kanban CLI in any project that has a cards board (a `.cards/` workspace
  somewhere in the repo, a `CARDS_WORKSPACE` setting, or a served board on a local port). Use
  this whenever the user asks about the board, backlog, or todo state — "summarize the todo
  cards", "what's coming up?", "what cards were done recently", "what's blocked" — whenever
  sprint planning happens in a project that has a board (including the sprint-plan skill),
  whenever finished work should be recorded on a card (comments, screenshots, commit/PR
  references, status moves), and at the end of a work session to persist the board snapshot.
  Also use it to SET UP Cards in a project that has no board yet — "set up a board", "track
  this project's work", designing or simplifying card types, migrating a backlog in from
  GitHub Issues, Linear, Jira, or Trello, and establishing review, snapshot-hook, or release
  conventions around a board.
  If a project has a cards board, the board is the source of truth for work state: consult it
  before planning and update it after working, even when the user doesn't mention cards by name.
---

# Working a cards board

`cards` is a local kanban service: typed cards in a SQLite-backed workspace, driven by one
binary (CLI, HTTP, web UI, TUI over the same data). This skill covers finding the board,
answering questions from it, planning sprints against it, recording finished work on it, and
persisting it at session end. Full command surface:
[references/cli-reference.md](references/cli-reference.md). Setting a board up for a project,
designing card types, or migrating a backlog:
[references/project-practices.md](references/project-practices.md).

## 1. Find the board and learn its shape (once per session)

Resolve the workspace in this order:

1. **Project config** — `CARDS_WORKSPACE` in the environment or `.claude/settings.local.json`
   (it must point at the `.cards` directory itself, not its parent).
2. **Discovery** — the nearest `.cards/` directory walking up from cwd (like git finds `.git/`),
   or a conventional subdirectory (`dev-workspace/.cards`, `board/`).
3. **A served board** — `CARDS_URL` set, or a health check on a known port:
   `curl -s http://127.0.0.1:8787/v1/health`. A responding server only wins if it serves *this
   project's* workspace — another project's board may happen to hold the port. Confirm (compare
   `GET /v1/workspace` against the local definitions) before routing writes through it;
   otherwise serverless `--workspace` against the local `.cards` is correct.

Then learn the vocabulary before assuming anything:

```bash
cards workspace show   # columns, card types, boards, users — every project differs
```

Column ids vary per project (`todo` vs `ready`, `in-progress` vs `in_progress`). Map the user's
words onto the actual columns: "todo/upcoming" → the pre-work columns (backlog/ready/todo),
"active" → in-progress/review, "done recently" → done ordered by `updated_at`.

**Two backends, one rule:** if a server is serving this workspace, target it
(`cards --url http://127.0.0.1:PORT ...`) so its live UI, event stream, and hooks see your
writes — a serverless write bypasses that process's event bus. No server running → plain
serverless commands are correct.

**Multiple boards:** a project may have more than one workspace — e.g. a dev-tracking board
for the project's own work and a runtime board its application manages. Project CLAUDE.md
rules about which board you may write to always win; when unsure, treat unfamiliar boards as
read-only and ask.

## 2. The rules that hold on every surface

These are the same rules the MCP server states in its handshake, so an agent driving the board
over the CLI and one driving it over MCP behave identically.

<!-- invariants:begin -->
- **Read the workspace first.** Call `workspace` at session start; its card types,
  required fields, columns, transitions, WIP limits and users are the contract.
  Never carry a status or field name over from another project.
- **Writes carry the current `version`.** A `version_conflict` response includes
  the current card — re-read from it and retry. Never blind-retry. Every write
  returns the updated card, so take the next version from that response instead
  of a separate read.
- **Validation errors are actionable.** They name the failing field and carry
  `valid_options`. Correct the value; do not work around the schema.
- **Record evidence as work lands.** Comments carry the narrative — what was
  done, verified, decided, or surprising. Repeating fields carry structured
  records: commits, sources, measurements. Reference commit SHAs and PR URLs,
  and attach a screenshot when a reviewer should see the change, not run it.
- **Move status honestly**, only when the work is really there. Where a review
  column exists, implementation ends there, and the card must make verification
  cheap: acceptance, a verify command, the commits, the evidence.
- **Discoveries become linked cards**, never silent scope creep. File a follow-up
  for anything blocking progress or release; leave speculative ideas for triage.
- **Never invent a local-time timestamp**; card dates are RFC3339 UTC.
- **Who owns the card:** by default you record on your own card. An orchestrator
  may instead own all card bookkeeping — if so it says that in your instructions,
  and you then do the repo work and touch no card.
- **At session end**, make card status reflect reality, then export
  `--state-only` to the project's established snapshot path. Never `import` over
  a non-empty database.
<!-- invariants:end -->

## 3. CLI-specific ground rules

- **Global flags go BEFORE the verb:** `cards --url ... --as claude list`, never
  `cards list --url ...`.
- **Writes need an actor:** `--as <name>` or `CARDS_USER`. Use the actor the project has
  established for you (check settings/CLAUDE.md); default to `claude`.
- **`patch`/`claim` take `--version N`.** `cards get <id>` first if you don't already hold a
  fresh card — but every write returns the updated card, so prefer the version from the last
  response. Comments and links bump the version too.
- **`create` has no `--body` flag** — long context goes in a field (`--field "notes=..."`) or a
  follow-up `comment add`.
- **Short 8-char ids work everywhere** (`cards get 4430ab22`) — but **bare hex only**: the
  natural truncation `card_4430ab22` (prefix + short hex) is `not_found`. Full id or bare
  8 chars, nothing in between.

## 4. Answering board questions

Map intent to query, then *summarize* — the user asked a question, not for raw JSONL.

| User intent | Query |
|---|---|
| "summarize the todo cards" / "what's on the board?" | `cards list --status <each pre-work column>` (add `--include links` to spot dependencies) |
| "what's coming up? / backlog?" | `cards list --status backlog` (+ ready), minus blocked ones |
| "what's in flight?" | `cards list --status in-progress` (and review-type columns), with owners |
| "what was done recently?" | `cards list --status done --limit 20` — list order is `updated_at DESC`, so the top IS most recent. (Don't reach for `cards feed` here: the feed is oldest-first from event 1, paged by `--cursor`/`--since <event-id>` — a catch-up log, not a recency view.) |
| "what's blocked?" | `cards list --blocked` (cards whose `depends-on`/`blocked-by` targets aren't done) |
| "what happened to X?" | `cards history <id>` — the resumption timeline |
| "find the card about X" | `cards list --q "X"` (full-text) |

Summarize with judgment: group by type or epic, respect the order the board gives you
(`updated_at DESC` — don't re-sort arbitrarily), give counts, name each card with its short id
so the user can act on it, and call out anything blocked or stale. For hierarchy
(epic→story→task via `part-of` links; blocking is `depends-on`), `--include links`
shows the edges.

## 5. Sprint planning on a cards board

When planning a sprint in a project with a board — including when the **sprint-plan skill** is
invoked — the board is the survey source and the plan's destination. Fold it in:

**Survey (before proposing anything):**
- Board topology as constraints: `cards boards show <id>` — transition rules, WIP limits, and
  monitors are planning constraints, not decoration (don't plan 5 concurrent cards into a
  WIP-3 lane; an empty promoted lane with a full backlog is itself a finding).
- Active work: in-progress/review columns, with owners — a sprint plan that ignores in-flight
  work is wrong on arrival.
- Pending work in board order: backlog/ready columns. The existing order and any priority
  fields/tags encode decisions already made — respect them; don't silently reshuffle.
- The blocked set (`cards list --blocked`) and dependency links: **defer blocked work** — a
  card whose dependencies aren't done doesn't go in the sprint; its blocker might.
- Recently done cards: context for velocity and for what just unblocked.
- If no live DB is reachable (fresh machine, CI), the committed snapshot (`backlog.jsonl` /
  `board-export.jsonl`) is a legitimate read source for the survey — it's the portable truth.

**Plan like a planner:**
- Where a card is ambiguous, stale, or contradicts the code/docs, **raise it as a
  clarification** in the plan (and optionally as a comment on the card) rather than guessing.
- Propose the sprint as a selection from pending cards, in order, plus any genuinely new work
  as proposed new cards.

**Write the plan back (with the user's approval of the plan):**
- New work → `cards create --type ... --title ... --field ...`; wire dependencies with
  `cards link add <id> --type depends-on --target <other>`.
- Selected cards → move to the ready/committed column via `patch`; tag with the sprint name if
  the project uses tags.
- The board after planning should *be* the sprint plan — someone reading only the board sees
  what was decided.

## 6. Recording work as it lands — the commands

§2 says *what* to record and when. The verbs:

- `cards comment add <id> --body "..."` — the narrative. `cards comment <id> --body "..."` is the same call.
- `cards patch <id> --version N --field k=v` — a dedicated field, when the card type has one
  for commits, branches, or PR links.
- `cards append <id> <field> --version N --entry-json '{...}'` — repeating fields (`work_log`,
  `sources`, `change_log`). Repeating fields are **not** patchable via `patch`.
- `cards attach <id> <field> <file>` — an `artifact` field, for screenshots and evidence files.
  If the type has no artifact field, save under the project's evidence convention and
  reference the path in the comment.
- `cards patch <id> --version N --status <col>` — the status move.
- `cards link add <id> --type related --target <new>` — wire a follow-up card to its origin.

## 7. Session end: persist the board — the commands

§2 says to export before the session ends. Which path:

1. **Use the project's convention if one exists:** a wrapper script (`scripts/board.sh export`),
   or an existing snapshot file (`board-export.jsonl`, `backlog.jsonl`) — re-export **to the
   same path**:
   ```bash
   cards --workspace <ws> export --state-only --out <existing-snapshot-path>
   ```
2. **No convention yet:** `cards --workspace <ws> export --state-only --out <ws>/board-export.jsonl`
   and tell the user where it went.
3. If snapshots are git-tracked in this project and you're committing work anyway, include the
   refreshed snapshot in the commit (respecting the project's commit rules).

`--state-only` is the right default: definitions + current cards/links/comments, small and
diff-clean; the event log stays machine-local by design.

## 8. Adopting Cards in a project

Introducing Cards to a project, migrating a backlog into it, or designing card types is a
different job from working an existing board. Read
[references/project-practices.md](references/project-practices.md) when the task is any of:

- setting up a board for a project that doesn't have one, or deciding whether it needs one;
- designing card types — the minimal epic/story/task ladder, and when to add a field;
- migrating from GitHub Issues, Linear, Jira, or Trello, and keeping provenance;
- coordinating several people or agents (shared server vs snapshot sync);
- setting up review, pre-commit/pre-push snapshot hooks, or release conventions.
