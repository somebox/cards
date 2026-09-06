# Sprint plan 2026-09-06 — Finish the agent write loop; start the docs refresh

> Produced by the /sprint-plan workflow (ground → candidates → falsification → plan) on
> 2026-09-06, then extended by hand for the second thread the workflow did not cover
> (the README and site refresh). Board state re-read against the live server on
> `:8787` the same day. Two threads, run in parallel; the second one has one
> sequencing dependency on the first.

## Status as of 2026-09-06

- `in_progress` and `review` are both **empty**. WIP limit on `in_progress` is 3.
- Sprint A from [`2026-08-08`](2026-08-08-sprint-plan.md) is **one-third done**:
  `0f7be7f6` (comment alias) shipped in `90b838d` and closed today. `069ec1d1`
  and `c0102825` are still `todo`.
- The init → adopt happy path was simulated end to end and its three gaps fixed on
  `fix/happy-path-simulation` (`8920121`): a fresh workspace's `default_user` can now
  own cards, `list -q` prints nothing on an empty result, and the adoption playbook
  carries the ladder JSON it used to only describe. Card `8150dda9`, closed.
- Bookkeeping from the 08-31 note is done: `ea7ea2a3` (run-extensions / do /
  extensions list) was verified shipped and closed; the mkdocs `not_in_nav` and MCP
  README path drive-bys landed in `5910c51`. `mkdocs build --strict` is clean.

## Thread 1 — agent write loop (engineering)

**Theme sentence.** An agent can claim, update and read back a card in one round-trip
each, without a preceding GET and without dumping full work logs into its context
window.

This is Sprint A's remaining two cards plus the one identity bug the happy-path
simulation exposed, and one new card that proves the theme as a whole. The workflow
verified all eight code claims behind it; none were refuted. It was the only one of
three candidates whose verdict came back *survives* (the other two are recorded under
*Rejected* below).

| # | Card | Commitment | Status |
|---|---|---|---|
| 1 | `1c877e6c` | TakeNext runs the same owner check as PATCH/claim (`checkUserExists`, which since `8920121` accepts definition-declared users), **and** the demo's `review-bot` gets declared/registered in the same PR so the demo does not regress | `todo` (promoted from backlog today) |
| 2 | `069ec1d1` | `--if-match latest` on patch / claim / release, re-read service-side under the write lock; explicit `--version N` keeps exit code 4 on stale | `todo` |
| 3 | `c0102825` | `--md` output for get / list; extract `cardMarkdown` off the TUI model into a shared renderer; short ids; no work_log entries or comment bodies in list output — **shrink valve** | `todo` |
| 4 | `13935666` | One integration test: take-next → patch `--if-match latest` → get `--md`, three calls, zero GETs, plus the empty-pool exit | `backlog`, gated on decision 4 |

**Order.** 1 before 2 (both touch the claim path; two PRs editing `claimWithRetry`
at once is the risk). 3 is independent and can run beside them. 4 last, by
construction. The cut rule from 08-08 still holds: under pressure `c0102825` is dropped,
not shrunk.

**Then Sprint B**, unchanged from 08-08: `fc92019c` health → `3fd62d32` query endpoint
→ `8afb9008` OpenAPI publication. Its start gate (Sprint A's cards have left `todo`)
is the reason it is not in this batch.

### Verification

- Per card, from the cards' own acceptance: TakeNext rejects an unregistered assignee
  with the structured `unknown_user` PATCH already returns; a `go test -race`
  concurrent-writer test pins that concurrent `latest` writes still 409; a golden test
  for `--md` with the migrated TUI test proving no render drift.
- Batch-level: card 4's test exists and passes. If it does not, the sprint did not ship
  its theme.
- Gates on every PR: `go build ./...`, `go vet ./...`, `go test -race ./...`, plus
  `go test ./internal/tui ./internal/cli ./internal/core`.

## Thread 2 — README and site refresh (docs)

**Goal.** Fewer words, one storyline, one diagram. The README and the site should tell
the same story in the same order and stop duplicating each other.

**What the survey found** (re-read today, numbers from `wc`):

- `README.md` is 428 lines / 2,218 words. It opens with the same two definitional
  paragraphs as the site home, then re-documents install, quick start, project
  adoption, configuration, sync, API behaviour, extensions, dev workflow and
  releases — most of it a second copy of site pages, with three different
  "quick start" blocks and `CARDS_URL` shown both with and without `/v1`.
- `docs/index.md` is 315 lines: a value grid, a doc grid, a themes gallery, three CTA
  rows. Its best asset — the side-by-side *definition JSON → rendered screen* — is the
  second section, and the actual pitch for the agent audience ("a board beats a
  markdown plan") is fourth.
- The nav has 7 tabs and 33 pages. *Guide* mixes concepts, reference and walkthroughs;
  *Reference* lists rollout history, design notes and the roadmap next to the spec.
- `get-started.md` (117 lines) is the right shape and stays the spine.
- `using-cards.md` is 635 lines: a 30-line narrative followed by a 440-line catalog of
  every verb in CLI, HTTP and MCP — reference material sitting in the guide.
- Visuals: 12 images exist, but nothing shows the whole system on one picture and
  nothing shows an agent loop. The README's hero (`media/board.png`) is a dev build
  with a stale nav item.

**The storyline** (shared by README and home, in this order):

1. **Hook.** A kanban board that lives in your repo and that your agents can drive.
   One screenshot.
2. **Why.** Todos are too little; a hosted tracker is too much. People, scripts and
   agents on one board, and a bad write is rejected with what was allowed.
3. **Sixty seconds.** `init` → open the board → one CLI write → point an agent at it.
4. **How it works.** One diagram: definitions in git + SQLite → one service layer →
   HTTP, CLI, MCP, web UI, TUI.
5. **Choose your path.** I use the board · I'm wiring an agent · I'm building on it.

| # | Card | Commitment | Sequencing |
|---|---|---|---|
| P | `65f8ae38` | Parent: the storyline, tone rules and the acceptance numbers (README ≤120 lines, nav ≤5 tabs / ≤20 pages, home ≤150 lines, strict build clean) | closes when the four below are done |
| D4 | `a3adf5b5` | Visuals: the one diagram (SVG, both palettes), refreshed screenshots at one size and theme, an agent-loop demo | **start now** |
| D3 | `3651b81b` | Home page rebuilt on the storyline; nav to five tabs (Start · Guide · Agents · Build on it · Project); catalog pages out of the nav; retire the `v0.1.x` badge | **start now**, takes the diagram from D4 |
| D2 | `45a21e3f` | README cut to ≤120 lines: hook, why, three-command start, one short schema example, path links, install, license | after `069ec1d1` and `c0102825`, so the CLI snippet shows the final flags |
| D5 | `f59debb8` | `using-cards.md` split: the working session stays in Guide (≤200 lines); the operations catalog moves under *Build on it* and is deduplicated against `reference/cli.md` | after `069ec1d1`, same reason |

All five are in `backlog` with `parent` links to `65f8ae38`; promoting them to `todo`
is the decision below. D3 is linked `related` to `cddf3086` (consolidating the two
reference docs), which changes what *Build on it* lists but is Sprint C work and stays
out of this batch.

### Verification

- `mkdocs build --strict` on every docs PR (a local venv builds it in about a second;
  CI runs the same command in `deploy-pages.yml`).
- Line counts from the acceptance table, checked with `wc -l` in the PR description.
- Every README link resolves against the built site and the repo.
- The three quick-start commands in the README run as written on a fresh checkout.
- A before/after screenshot of the home page above the fold, attached to D3.

## Decisions needed before the first card is claimed

1. **`--md` as a stable contract.** `c0102825` declares it stable, which makes the shape
   hard to change after it ships. *Recommendation:* approve the shape now — 8-char
   short ids, title / type / status / owner / version, typed fields, link edges; no
   work_log entries or comment bodies in `list`, both included in `get`. If that is
   not settled, ship it marked experimental for one release.
2. **Where the shared markdown renderer lives.** *Recommendation:* a new
   `internal/render` package; `internal/core` should not know about markdown, and the
   TUI keeps calling it.
3. **Item 1 is a behaviour break** for any extension worker that claims without
   registering. *Recommendation:* accept it with a CHANGELOG entry and declare
   `review-bot` in the demo workspace's `workspace.users` — since `8920121` a
   declared user is a registered user, so no runtime registration step is needed.
   Grep the examples for other unregistered claimers before tightening.
4. **Is card 4 in scope?** *Recommendation:* yes, as its own card. Folding it into
   `069ec1d1`'s PR leaves the theme with no independent proof.
5. **CLI-only or an HTTP `If-Match` header too?** *Recommendation:* CLI only this
   sprint; the service-side mechanism is written so an `If-Match` header can reuse it
   in Sprint B without touching the write path twice.
6. **Promote the docs cards.** D4 and D3 can start today and are independent of the
   engineering thread. *Recommendation:* promote both to `todo` now; leave D2 and D5 in
   `backlog` until `069ec1d1` merges.
7. **Dispose of `9f626548`** (global request-body cap). Still open from 08-31; commit
   `42eeea6` and `3fd62d32`'s locked decisions both reject it under principle 7.
   *Recommendation:* mark it wontfix citing that commit.
8. **The Sprint 07-22 tail** (`0fd1b9b7`, `57e1bde9`, `ad67971f`, all blocked) and
   `c7a70b64` "SPLIT PARENT". *Recommendation:* close them as superseded unless
   someone still wants them; they are the only blocked cards on the board.

## Candidates rejected

- **http-integration-surface** (Sprint B) — verdict *weakened*, and its start gate has
  not opened. Two of its five items are broken as filed: `9f626548` contradicts a
  decision recorded on its sibling `3fd62d32`, and `d6b50bee`'s actual deliverable lives
  on `2f12f16f`. The three solid cards (`fc92019c`, `3fd62d32`, `8afb9008`) are the next
  sprint, in that order.
- **definitions-visible-and-editable** (`8903f9aa`, `0707a008`, `3d8ed6d9`) — verdict
  *weakened*: `cmd/cards/reload.go` deliberately 409s an existing board file rather than
  doubling as an editor, so the board settings editor needs a new PATCH handler plus
  schema and concurrency work; three cards carry about four cards of work.

## Unverified

- The companion doc edits the cards call for (`docs/spec/api-surface.md`,
  `docs/reference/cli.md`) were flagged as real scope but not sized.
- Line numbers cited on the cards for `cardMarkdown` and `TakeNext` were correct at
  planning time and drift; find code by name.
- That `review-bot` is the only unregistered claimer. Verified it is one; not verified
  it is the only one.
- The docs acceptance numbers (120 / 150 / 200 lines, 5 tabs, 20 pages) are targets
  chosen from today's counts, not measured against a reader.
