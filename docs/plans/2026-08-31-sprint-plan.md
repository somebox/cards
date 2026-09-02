# Sprint plan 2026-08-31 — Start Sprint A as written

> Produced by the /sprint-plan workflow (ground → candidates → falsification → plan),
> 2026-08-30, with the ground-state numbers re-checked by hand against
> `.cards/backlog.jsonl` on 2026-08-31 and corrected below.
>
> **This is a re-confirmation, not a new plan.** The sprint itself is already scoped in
> [`2026-08-08-sprint-plan.md`](2026-08-08-sprint-plan.md) — ship order, cut rule, locked
> decisions and risks all live there and are not restated here. This page records what
> changed in the three weeks since, what was re-verified at HEAD, and the questions that
> must be answered before an agent claims the first card.
>
> Re-checked 2026-09-02 against `origin/main` (`ec28a0b`). Sprint A is **not done**.
> This file stays a start-the-sprint note, not an archive.

## Status as of 2026-09-02

Sprint A's three cards are still `todo`. The three claimed gaps are still open
at HEAD (re-checked, table below). Sprint B's start gate still holds. **Start
Sprint A as written.**

What changed since 2026-08-31:

- Agent-guidance **shipped** on main as [#29](https://github.com/somebox/cards/pull/29)
  (`2a7f854`); gitignore follow-up `ec28a0b`. Cards `524c06f7` and `086bb35f`
  are `done`. That work is orthogonal to Sprint A — it is how an agent finds,
  adopts, and operates a board, not the CLI write-ergonomics theme.
- Decision 1 is therefore answered: **Sprint A is still the direction.** Do not
  reorder it behind Sprint B; `069ec1d1` still rewrites `docs/spec/api-surface.md`
  for endpoints Sprint B would document again.
- Decision 2 is answered: the review lane those two cards occupied is empty.
  `in_progress` is empty. Nothing is blocking a claim of `0f7be7f6`.
- User-facing docs for the shipped work already live in
  [`docs/agents/instructions.md`](../agents/instructions.md),
  [`docs/get-started.md`](../get-started.md), and
  [`docs/reference/cli.md`](../reference/cli.md). They were not rolled into
  [`implementation-status.md`](../reference/implementation-status.md) — that
  audit is Sprint C, sequenced after A and B.

Still open before (or alongside) the first claim: decisions 3–5, and the
bookkeeping items below.

## Finding: Sprint A was never started, and it gates everything else

The 2026-08-08 plan committed two sprints in order. Sprint A's three cards are still
`todo` at HEAD. That matters beyond the cards themselves, because Sprint B carries an
explicit start gate ([`2026-08-08-sprint-plan.md:92`](2026-08-08-sprint-plan.md)):

> **Start gate:** do not begin until Sprint A's three cards have left `todo`.

The reason is concrete rather than procedural: `069ec1d1` rewrites
`docs/spec/api-surface.md` for three endpoints that Sprint B would then document again.
So the HTTP integration sprint is not startable, per the repo's own committed sequencing,
until this one moves.

The planning value added by this pass is therefore near zero, and saying so is the honest
deliverable. These cards were scoped and decision-locked on 2026-07-27, re-verified
2026-08-07, and re-verified again here. They are ready to hand to implementation without
a planning pass. **Start Sprint A as written.**

## Re-verified at HEAD (2026-08-31, again 2026-09-02)

Every gap the sprint claims is still open. Checked, not asserted. Line
citations still resolve at `ec28a0b`:

| Card | Claim | Check |
|---|---|---|
| `0f7be7f6` | `comment` still has no `--body` alias | `internal/cli/commands.go:496` still falls through to `unknown comment subcommand %q` |
| `069ec1d1` | No `--if-match` anywhere | zero hits for `if-match\|ifmatch` across `internal/cli`, `internal/httpapi`, `internal/core` |
| `c0102825` | Renderer is TUI-private | `func (m *model) cardMarkdown` exists only at `internal/tui/tui.go:1749`; sole callers are `tui.go:1744` and `tui_test.go:263` |

All three cards confirmed `status: todo` in `.cards/backlog.jsonl`. HTTP
`GET /v1/health` still returns `"version": "poc"` (`internal/httpapi/api.go:27`)
— that is Sprint B's `fc92019c`, not this sprint. MCP `initialize` no longer
returns `"poc"`; that was the agent-guidance work and does not close the health
card.

## Correction: the review lane is smaller than the workflow reported

The ground-state pass read the board through the serverless CLI, which reads
`.cards/work-cards.db`. That file is **older than the committed snapshot**
(`work-cards.db` 2026-08-30 01:58 vs `backlog.jsonl` 2026-08-30 06:50), so the pass
reported a five-card review lane that no longer exists.

Against the committed JSONL on 2026-08-31 the picture was **2 in review**, not 5.
Those two (`524c06f7`, `086bb35f`) shipped in #29 and are `done` as of 2026-09-02.
`in_progress` is empty. A serverless CLI read of a stale `work-cards.db` will
still show the old five-card review lane — that is the DB, not the board.

**Action item independent of the sprint:** if the local DB disagrees with
`backlog.jsonl`, run `scripts/board.sh import --force` before trusting a
serverless CLI read of this workspace.

## Decisions needed before the first card is claimed

1. **Is Sprint A still the direction?** **Yes (2026-09-02).** Agent-guidance
   shipped and does not replace these three cards. Sequencing in the 08-08 plan
   still holds.
2. **Does the review lane close first?** **Closed (2026-09-02).** Those two
   cards are `done`; nothing is in `review` or `in_progress`.
3. **Confirm the cut rule.** Under capacity pressure `c0102825` is *dropped, not shrunk* —
   its own card says documented-and-golden-tested or not at all. Worth a yes/no:
   pre-split the `tui.go` `cardMarkdown` extraction into its own card so the refactor risk
   is visible and separately droppable.
4. **How is `--if-match latest` described** in the docs and in the agent skill? Explicit
   `--version` must stay the canonical path, or principle 9 (fail loudly, guide recovery)
   erodes as agents copy the convenient flag. Decide the wording before `069ec1d1` is
   written, since it edits `docs/spec/api-surface.md`.
5. **Dispose of `card_9f626548`** (global request-body cap, currently `backlog`). Commit
   `42eeea6` already rejected body caps as permission theater under principle 7, and
   `3fd62d32`'s locked-decisions block repeats it. Mark it wontfix citing that commit, or
   reopen the decision deliberately — leaving it open while a sibling card ships the
   opposite rationale is a contradiction an implementing agent will trip over.

## Bookkeeping surfaced by this pass

- **`card_ea7ea2a3`** (CLI `run-extensions` / `do` / `extensions list`) is `todo` but
  appears already shipped — `cmd/cards/main.go:90-94` wires all three verbs. Retire it
  rather than plan it.
- **Sprint 07-22 tail.** Several cards tagged to that sprint sit in `backlog`/`blocked`
  weeks after it otherwise closed (`0fd1b9b7`, `57e1bde9`, `ad67971f`). Confirm the sprint
  is done and drop them, or re-file them as current work.

## Candidates rejected

- **HTTP integration surface** (`fc92019c`, `3fd62d32`, `8afb9008`) — blocked by its own
  committed start gate, above. This is Sprint B and it stays Sprint B.
- **Board legibility** (`81086204`, `b3e07b9d`, `8fea3fc0`) — the lead card bundles four
  named deliverables of which only one is about legibility, and its own parent tracker
  (`86515fd2`) marks it parked. The a11y card needs an undecided A/B/C design call before
  it is implementable at all.
- **Doc consolidation** (`2f12f16f`, `cddf3086`) — the committed plan sequences it after
  both sprints, because both sprints change what those docs say. Writing it first means
  writing it twice.
