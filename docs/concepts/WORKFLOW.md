# The workflow

Cards keeps the tracker in the repo — plain JSON, no vendor, no export step.
This page walks through how a project actually runs on it, and why the
snapshot at the end is the feature everything else hangs off.

## A working session

A realistic loop, with a human and an agent sharing one board:

1. **Capture the work.** You add cards from wherever you are — the web board,
   or `cards create --type task --title "..."` mid-thought in the terminal.
2. **Let the agent survey.** Point your harness at the board
   ([MCP quickstart](../agents/mcp.md)) and ask it to review the open cards
   and propose a sprint. It reads real typed fields — not a stale plan file —
   and records its proposals as comments.
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

## The mechanics

Two things in git carry everything:

- **`definitions/`** — the schema (card types, columns, boards). Plain JSON,
  always committed.
- **`backlog.jsonl`** — the state snapshot (cards, comments, links, users).
  One JSON object per line, so it diffs cleanly and reads like a changelog in
  review.

The live `work-cards.db` (SQLite) is machine-local and gitignored — it's the
working store, not the record. `cards import` restores a snapshot into a
fresh workspace and refuses a non-empty database, so a restore can never
silently overwrite live state.

```console
$ cards export --state-only --out backlog.jsonl   # snapshot the board
$ git add backlog.jsonl && git commit -m "board: sprint 12 wrap"
# …elsewhere…
$ git pull && cards import --in backlog.jsonl     # same board, new machine
```

This repo ships `scripts/board.sh` wrapping the loop, including a pre-commit
hook so the snapshot never goes stale:

```console
$ scripts/board.sh export           # snapshot → commit → push
$ scripts/board.sh import           # restore on another machine
$ scripts/board.sh import --force   # re-sync a machine that already has state
$ scripts/board.sh install-hook     # auto-export before every commit
```

## What the snapshot buys you

- **Handoff is `git clone`.** No tracker migration, no access requests, no
  export requests — the project's memory is in the project.
- **Review the work, not just the code.** The snapshot diffs in the PR:
  reviewers see which cards closed, what was logged, what was discussed.
- **Agents can be ephemeral.** A fresh sandbox imports the snapshot and has
  full context — including the history a resuming agent reads to pick up
  where the last one stopped.
- **No lock-in, in either direction.** The whole format is JSON you can read
  with `jq`. Leaving Cards means parsing one file; adopting it means writing
  one.

The event journal (the fine-grained mutation log) stays SQLite-owned and is
not part of the snapshot — the snapshot rebuilds card state, not the
machine-local log. See [events](../events/EVENTS.md) for what lives where.

## Where to go next

- [Using Cards](../reference/OPERATIONS.md) — export/import alongside every
  other operation, with CLI, HTTP, and MCP examples.
- [Agents & MCP](../agents/mcp.md) — wiring a harness into the loop above.
- [Concepts](CONCEPTS.md) — the vocabulary this page takes for granted.
