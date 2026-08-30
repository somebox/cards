# Adopting Cards in a project

Read this when the job is to *introduce* Cards to a project, migrate an existing
backlog into it, or design card types — not when working an existing board
(that's [SKILL.md](../SKILL.md)).

## 1. When a project wants a board

Adopt when work outlives a session and more than one actor touches it: a human
plus agents, several agents, or one person across many sessions. Below that, a
TODO file is honestly better — say so rather than installing ceremony.

Layout, in the repo, committed:

```
.cards/
  definitions/          # workspace.json, card-types/, boards/  — git-backed, reviewed
  backlog.jsonl         # the portable snapshot: COMMIT THIS
  .gitignore            # work-cards.db*  — local working state, never committed
  README.md             # this board's conventions, for humans
.claude/skills/cards/   # installed by `cards init` (Claude Code and compatible)
```

The database is machine-local and disposable; the JSONL snapshot is the truth
that travels. Anything a human authors or reviews belongs in `definitions/`;
anything operational belongs in the DB.

```bash
cards init                       # scaffolds .cards/ + installs the skill
cards --workspace .cards         # serverless TUI
cards serve --workspace .cards   # browser UI at /ui/boards/<id>
```

`cards init` writes a tutorial workspace, not the layout above: columns
`todo`/`doing`/`done`, one `task` type (`notes`, `attachment`), a `welcome`
board, no `link_types`, no `.gitignore`. Fine for a first look. For a real
project board, replace the starter definitions — and delete the welcome cards
— *before* creating work. Changing columns while those cards exist fails
validation; `default_board` must name a board file that already exists or the
workspace will not load. Write the JSON in this document into `definitions/`;
don't layer it onto the tutorial.

## 2. Start minimal and climb only when the board complains

This ladder is what two existing Cards projects settled on, and it is the
recommended starting point — not a law. Treat it as a default that has survived
contact with real work, not as proof that it is optimal.

| Type | Required fields | Says |
|---|---|---|
| `epic` | `goal` (text) | a high-level goal and success picture — **not** an implementation checklist |
| `story` | `outcome`, `acceptance` (text) | an outcome with observable acceptance criteria |
| `task` | `actions` (text); optional `verify` (string), `work_log` (repeating) | an actionable unit with a clear verification path |

One field on an epic. Two on a story. Three on a task. That is the whole ladder,
and it is a good default for a new project.

Climb a rung only when a *missing* field has actually cost you something —
a review you couldn't run, a query you couldn't ask. Adding a field changes the
contract everywhere at once (API, CLI, MCP, UI), so each one is a standing tax
on every card of that type. Do not port a mature tracker's taxonomy: you will
import fields nobody fills in, and required fields nobody can answer become
lies.

Signals you have gone too far: fields that are always empty, always the same
value, or restate the title. Delete them.

**Hierarchy is a link, not a field.** Declare a `part-of` link type alongside
`depends-on` / `blocked-by` / `related` and wire `task -part-of-> story
-part-of-> epic`. Blocking is `depends-on`; a card whose targets aren't `done`
shows up in `cards list --blocked`.

**Workspace settings worth turning on from day one** (both reference boards run
all four):

```json
{ "enforce_transitions": true, "strict_fields": true,
  "tag_policy": "locked", "default_board": "engineering" }
```

`strict_fields` and `tag_policy: locked` reject typos instead of silently
creating a second vocabulary. `default_board` stops every surface guessing when
there's more than one board — set it after that board file exists; load rejects
an unknown id. The same coupling bites in reverse: deleting a card type while a
board still lists it in `card_type_ids` also fails load, so rewrite the board in
the same pass as the types it names. Run `cards --workspace .cards workspace
show` after each definitions edit — it is the cheapest check that the workspace
still loads, and one bad edit is far easier to unpick than three. One board with
`wip_limits` (e.g. `{"in_progress": 2}`) beats several boards that split
attention.

## 3. What each level should actually say

- **Epic — the goal and what success looks like.** If it reads as a list of
  tasks, it is a story. Epics rarely move; they are the thing stories point at.
- **Story — the outcome, plus acceptance someone else could check.** Acceptance
  is the contract: observable, and phrased so a reviewer who didn't do the work
  can verify it. "Works properly" is not acceptance.
- **Task — the concrete actions, plus how it is verified.** `verify` should be
  one command a reviewer can run. `work_log` takes the structured record
  (commit, files, measurement); the *narrative* goes in comments.

Titles must survive a done-column scan months later, with no surrounding
conversation: `Finish DB sync testing (P1)`, not `Close P1`.

## 4. Migrating an existing backlog

**There is no importer.** `cards import` restores a Cards snapshot only. A
migration is a short script that reads the source and calls `cards create` —
which is a feature, because it forces the triage below.

1. **Don't import everything.** Most trackers are a graveyard. Take what is
   actually planned; leave the rest in the old system, which stays readable.
2. **Map their shape onto the ladder**, don't reproduce it. Typical mapping:
   milestone/label-epic → `epic`; issue → `story`; sub-issue or checklist item →
   `task`. Their status vocabulary maps onto your columns — decide the mapping
   once and write it into `.cards/README.md`.
3. **Keep provenance.** Add one `source` string field (e.g.
   `gh#412`, `LIN-88`, `PROJ-1043`, plus the URL) so a card can be traced back
   after the old tracker is archived. This is the one field worth adding up
   front — reconstructing it later is impossible.
4. **Wire the hierarchy after creating cards**, with `cards link add <id> --type
   part-of --target <parent>`: you need both ids to exist first.
5. **Record what you did.** Put a short "migration" section in
   `.cards/README.md` naming the source, the date, and what was deliberately
   left behind. Someone will ask why a card isn't there.

Per-source notes: **GitHub** issues carry labels and milestones — labels usually
become tags (declare them in `tag_set` first, since `tag_policy: locked` will
reject unknown ones) and milestones usually become epics. **Linear** and
**Jira** both have richer state machines than five columns; collapse aggressively
and keep their id in `source`. **Trello** cards are usually stories with
checklists that become tasks.

Do a dry run first — `cards create --dry-run` validates without writing — and
migrate into a scratch workspace before the real one.

## 5. Working with other people and agents

**One process serves one workspace**, so pick a mode:

- **Shared server** — one `cards serve`, everyone points `CARDS_URL` at it.
  Live UI, event stream, and hooks all see every write. This is the right mode
  when people work at the same time. Serverless writes bypass that process's
  event bus, so if a server is up, target it.
- **Snapshot sync** — no server; each machine works serverlessly and the
  committed `backlog.jsonl` is the exchange format. Merge conflicts land in one
  file, which is why `--state-only` exists: it is small and diff-clean.

Give every actor a distinct id (`CARDS_USER`, or `--as`) — human or agent — so
history says who did what. Owning a card requires a registered user
(`cards users register --id <id> --kind agent`); commenting does not.

`wip_limits` are a real coordination tool here: they stop two agents claiming
into the same lane. Use `cards take-next` rather than `list` + `claim` when
several actors pull from one queue — it claims atomically, so two agents cannot
take the same card.

## 6. Review, and closing work honestly

Work normally exits through `review`, not straight to `done`. A card arriving in
review should be a **review packet** — everything a second party needs, without
asking:

- the acceptance it claims to meet,
- a `verify` command they can actually run,
- the commits or PR,
- evidence (a screenshot for anything visual, output for anything measured),
- a short note on risk and where to look first.

Where practical the reviewer is a **different session, agent, or person** than
the implementer — the point of the column is a second pair of eyes, and an
implementer reviewing their own work is just a slower `done`. The reviewer
records the outcome, any suggested decisions, and residual risks on the card.

Discoveries become linked follow-up cards, not silent scope creep. File one
immediately when it would block progress or a release; leave non-urgent ideas
for triage rather than flooding the board.

## 7. Time

Store RFC3339 UTC. Convert only when speaking to a person, and never write a
local-time stamp without an offset. A board read across machines and timezones
is exactly where "3pm" becomes unrecoverable.

## 8. Keep the snapshot honest: pre-commit and pre-push

The live DB is gitignored, so the board's committed state is only as fresh as
the last export. Wrap it in a script the project owns:

```bash
scripts/cards-board.sh export         # refresh .cards/backlog.jsonl
scripts/cards-board.sh check          # fail if the snapshot is stale
scripts/cards-board.sh install-hooks  # opt in to pre-commit + pre-push
```

Both hooks earn their place: **pre-commit** export keeps board changes in the
same commit as the code they describe, and **pre-push** check is the backstop
that stops a stale snapshot reaching everyone else. Never hand-edit
`backlog.jsonl` while a server is running against the same workspace, and never
`import` over a non-empty database — it refuses, and that refusal is protecting
someone's live state.

## 9. Releases

Cards has no release command (`cards release <id>` releases *ownership* of a
card). A release is a convention, and a good one is four things landing
together:

1. a **release card** whose acceptance is the release checklist, moved to `done`
   only once the release is actually out;
2. the **tag**, referenced on that card;
3. the **CHANGELOG** entry, written from the cards that closed since the last
   release — which is the payoff for honest titles and acceptance;
4. a **refreshed snapshot** committed with the tag, so the board state at that
   version is recoverable.

Cards closed since the last release are the changelog's raw material: `cards
list --status done` is ordered `updated_at DESC`. If that list doesn't read like
release notes, the titles were the problem.
