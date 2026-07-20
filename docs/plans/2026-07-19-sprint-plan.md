# Sprint plan — 2026-07-19

> Final, review-revised sprint plan for the `cards` repo. Supersedes
> `.pi/run/sprint-plan/draft_plan.md`; every blocker and major from
> `review.md` is dispositioned in **Review disposition** at the end.

**Sprint goal.** Make Cards' verifiable contracts self-checking and its
extension model runnable, so contributors and agents land on real files,
real lines, and a real running composition seam — not 404s, rotten anchors,
or spec-only claims. Quoting the north star: *"Harden Cards' verifiable
contracts (SoT, snapshot, anchors) and make the extension model runnable —
keep the kernel small, composition real, and agents oriented."*

- **Focus:** `none`
- **Candidate count:** 4
- **North star (one line):** Harden verifiable contracts (SoT, snapshot,
  anchors) and make the extension model runnable — small kernel, real
  composition, oriented agents.

Concretely, by end of sprint a reviewer can: run one `go test` that fails
loudly if any `implementation-status.md` anchor rots (proven by a negative
demo where the symbol moves but the cited line is bumped to a different
function); import a frozen, hash-pinned `backlog.jsonl` fixture into a fresh
store and re-export it byte-for-byte identical; run an automated
`review-bot_test.sh` that starts `cards serve --run-extensions`, drives a
card to `review`, asserts the seeded `service` extension posts a comment
back, kills/restarts the server mid-stream and asserts SSE resumption, and
asserts the bot reaches a stable subscribed state; and drive the TUI with
sort/filter parity against the same `boardSortOptions` presets the web UI
uses — with `/` (find) and the filter modal on distinct keys. No kernel
schema, no new dependencies, no new languages.

---

## Thread — how the phases compose

The sprint runs **orient → pin → prove → parity**:

1. **Phase 1 (orient)** makes the docs that orient everyone trustworthy and
   self-healing, with a guard whose extraction contract is explicit and
   whose negative proof covers the moved-and-renumbered rot class.
2. **Phase 2 (pin)** hardens the portable-state contract against a frozen,
   hash-pinned fixture (not the live board) so the extension seed's writes
   are provably portable, not assumed.
3. **Phase 3 (prove)** is the composition climax with an automated test —
   not a manual demo — proving the SSE → take-next → comment seam, including
   reconnect resumption and supervisor stability.
4. **Phase 4 (parity)** is the user-value capstone: sort/filter parity
   against `boardSortOptions`, with `/` (find) and the filter modal on
   non-colliding keys and a `me`-substitution regression test.

Phases 3 and 4 are independent after Phase 1; ordering the composition climax
before the polish capstone keeps the north-star's "runnable" prong
front-loaded. Preserve this sequencing — the review did not disprove it.

```
Phase 1 (drift-sot)  ──┬──> Phase 2 (jsonl-snapshot, S)
                       ├──> Phase 3 (runnable-extension-seed, M)
                       └──> Phase 4 (tui-parity, M)
```

---

## Phase 1 — SoT refresh + anchor guard (foundation)

**Purpose:** Make the doc that enforces docs-vs-code honesty itself honest
and machine-checked, with an extraction contract explicit enough that the
guard cannot green-light the moved-and-renumbered rot class that recurred.

### Do

1. **Roll the boundary.** Edit `docs/reference/implementation-status.md`:
   roll the audit-changelog boundary line (**L619**, "Current boundary:
   `b3bfed5` → HEAD (`0421efd`, …)") to `bb6ffc5`. **Split** the temporal-breaches
   `1247e3b` evidence row into its own entry under the new boundary — do
   *not* re-audit §4–§8 across the 17 new commits (scope discipline).
2. **Re-pin the 3 rotted anchors** (verified against HEAD `bb6ffc5`),
   each annotated with the new explicit guard marker (see Step 5 syntax):
   - `internal/core/service.go:1491` — `claimWithRetry` call site ("called at…")
   - `internal/core/service.go:1526` — `func claimWithRetry`
   - `internal/sqlite/sqlite.go:730` — `return … ErrClaimRaced`
3. **Re-point 9 dead code-comment doc refs** to the split tree:
   `docs/SPEC.md`→`docs/spec/index.md`, `docs/ARCHITECTURE.md`→
   `docs/architecture/index.md`, drop `docs/DEVELOPER-REFERENCE.md`,
   `docs/MCP.md`→`docs/extensions/mcp.md`. Files (comments only):
   `internal/artifacts/artifacts.go`, `internal/config/config.go`,
   `internal/httpapi/server.go`, `internal/core/{store,types,service}.go`,
   `internal/cli/client.go`, `internal/mcp/mcp.go`, `internal/sqlite/sqlite.go`,
   and **`cmd/cards/main.go:5`** (references `docs/DEVELOPER-REFERENCE.md` —
   the feedback sweep found the original re-point list covered `internal/`
   only and missed this `cmd/` file).
4. **Complete the CLAUDE.md "Where things live" table** — add the 7 missing
   `internal/` packages: `artifacts`, `cli`, `docaudit`, `openapi`, `seed`,
   `starter`, `themecss`.
5. **Add the guard** — new test `TestImplStatusAnchorsResolve` in
   `internal/docaudit/docaudit_test.go` (test-only package; cite the
   package-doc guarantee "no non-test code" in the test comment so a future
   contributor does not promote it into a build-time check). **Extraction
   contract (BLOCKER fix):** each citation must carry an explicit, parseable
   HTML-comment marker the guard parses — *not* prose regex. Syntax:
   ```
   <!-- guard: internal/core/service.go:1491 symbol=claimWithRetry -->
   ```
   placed on its own line immediately above (or inline beside) the cited
   `file:line` reference in `implementation-status.md`. The guard parses the
   marker, opens the file, and asserts the expected symbol appears at the
   cited line/range. A weak "file exists + line in range" check is explicitly
   rejected as negative value. Add a companion `TestCodeCommentDocPathsResolve`
   asserting every `docs/*.md` path in a code comment resolves across
   `internal/` **and** `cmd/` (promoted to a named exit criterion — the
   re-point churn can't recur; `cmd/cards/main.go:5` is in scope). Add the
   cheap boundary-commit assertion the draft deferred: the boundary commit
   cited in `implementation-status.md` must exist in `git rev-list HEAD` and
   be within N=20 commits of HEAD. **Failure mode (feedback fix):** on breach
   the test fails with a message naming the one-line fix ("roll the boundary
   at implementation-status.md L619 to HEAD and re-pin anchors; see Phase 1
   of docs/plans/2026-07-19-sprint-plan.md"). To avoid training contributors
   to ignore docaudit red on unrelated PRs (the commit-count tripwire goes
   red purely because 20 commits accumulated, regardless of anchor health),
   the within-N check is a **warning** in normal `go test ./...` runs and
   **strict** only in a dedicated CI job (e.g. `go test ./internal/docaudit
   -tags=strictdoc`); the anchor and doc-path tests remain strict everywhere.

### Outcome (observable)

- `grep` for the boundary line cites `bb6ffc5`; the `1247e3b` row no longer
  contradicts the stated range.
- Each cited line/range contains the symbol named in its guard marker.
- `grep -rn "docs/SPEC.md\|docs/ARCHITECTURE.md\|docs/DEVELOPER-REFERENCE.md\|docs/MCP.md" internal/ cmd/` returns empty.

### Files

`docs/reference/implementation-status.md`, `CLAUDE.md`,
`internal/docaudit/docaudit_test.go`, plus the 9 comment-only edits above.

### Demo (third-party-runnable)

```bash
go test ./internal/docaudit/ -run TestImplStatusAnchorsResolve -v   # PASS
# Negative proof #1 (deleted symbol): revert one anchor to stale :1472 →
#   FAIL naming "claimWithRetry"; restore.
# Negative proof #2 (BLOCKER — moved-and-renumbered): move claimWithRetry to
#   a new line, then set the guard marker's line number to that new line but
#   point it at a DIFFERENT function (delete a blank line above so the number
#   lands on an unrelated symbol) → FAIL naming the expected symbol,
#   proving the guard checks the symbol AT the line, not just that a symbol
#   exists. Restore.
grep -rn "docs/SPEC.md\|docs/ARCHITECTURE.md\|docs/DEVELOPER-REFERENCE.md\|docs/MCP.md" internal/ cmd/   # empty
git log --oneline 0421efd..HEAD | wc -l   # matches doc's stated boundary count
```

### Exit criteria (binary)

- [ ] Boundary line cites `bb6ffc5`; `1247e3b` row split, no contradiction.
- [ ] All 3 `service.go`/`sqlite.go` anchors carry `<!-- guard: ... symbol=... -->`
      markers and resolve to the named symbol.
- [ ] **Marker syntax stated in this exit criterion:** `<!-- guard: <path>:<line>[-<end>] symbol=<ident> -->`.
- [ ] `TestImplStatusAnchorsResolve` passes; **negative proof #2**
      (symbol moved, line bumped to a different function) FAILs naming the
      expected symbol.
- [ ] `TestCodeCommentDocPathsResolve` passes; zero dead `docs/SPEC.md`/
      `ARCHITECTURE.md`/`DEVELOPER-REFERENCE.md`/`MCP.md` refs in `internal/`
      **or** `cmd/` (covers `cmd/cards/main.go:5`).
- [ ] Boundary-commit assertion: commit exists in `git rev-list HEAD`;
      within-N=20 check is **warning** in normal `go test`, **strict** only in
      the dedicated docaudit CI job; on breach it names the one-line fix.
- [ ] CLAUDE.md table lists all 14 `internal/` packages.
- [ ] `go test ./internal/docaudit ./internal/httpapi ./internal/config` green; `go vet ./...` clean.

---

## Phase 2 — JSONL snapshot residual gaps (contract pinning)

**Purpose:** Pin the four genuine residual gaps in the portable-state
contract against a frozen, hash-pinned fixture — not the live board. The
headline round-trip is *already* pinned by `cmd/cards/portable_test.go`
(`3236f3d`, 7 tests); this phase adds only what that file does not cover.

### Do

1. **CLI wrapper coverage.** Extend `cmd/cards/portable_test.go` with cases
   exercising `exportCmd`/`importCmd` flag parsing (`--out`/`--in` file IO)
   and the fresh-DB pre-flight refusal (`import.go:54`). Reuse
   `openWorkspace`/`dbPath` (`cmd/cards/open.go:19,29`).
2. **Re-export byte-stability (with named ordering guarantee).** Add
   `TestExportStateOnlyByteStable`: export → import → re-export →
   byte-compare. **Before writing the test, state the guarantee under test
   explicitly** (MAJOR fix): the canonical id-sort is already applied by
   `portable.go` (inline `slices.SortFunc` calls at `portable.go:58`
   for users / `:93` for cards / `:118` for events — there are **no** named
   `sortCards`/`sortUsers`/`sortEvents` functions; the sort is inline). If
   byte-stability is already guaranteed by those
   sort calls, the test is a *pin* (regression guard against a future
   non-determinism) and the exit criterion names "id-sorted canonical
   ordering of cards/users/events." If a non-determinism source is found
   (map iteration in event metadata, field ordering), the fix belongs in
   `portable.go` — name it in the test's comment. Diff sorted line sets, not
   raw bytes, so a future ordering failure fails diagnostically.
3. **Frozen, hash-pinned fixture (MAJOR fix — not the live board).** Copy
   `.cards/backlog.jsonl` at sprint start to
   `cmd/cards/testdata/backlog.frozen.jsonl`. The test loads the frozen
   copy, asserts a content hash (`sha256`) computed at freeze time and
   pinned in the test. The live `.cards/backlog.jsonl` is **not** a test
   input. A silent fixture drift trips a loud single-line failure printing
   the regeneration command, not a 190-card diff.
4. **`board.sh` smoke under `go test`.** Add `TestBoardScriptSmoke` (a Go
   test shelling out, *not* a standalone bash script) covering
   `board.sh import --force` and `install-hook` in a temp repo so
   `install-hook` does not clobber the real `.git/hooks/pre-commit`. Runs
   under `go test ./...`, not a forgotten CI step.

### Outcome (observable)

- A test that runs `importCmd` against a non-empty DB fails loud with the
  "workspace already contains cards" refusal.
- Two exports of the same store are byte-identical (sorted-line diff), with
  the id-sorted canonical ordering named as the guarantee under test.
- The frozen fixture imports cleanly in CI; its hash is pinned.

### Files

`cmd/cards/portable_test.go` (new fixture `cmd/cards/testdata/backlog.frozen.jsonl`).

### Demo (third-party-runnable)

```bash
go test ./cmd/cards -run 'TestExportStateOnlyByteStable|TestImportFrozenBacklogSnapshot|TestBoardScriptSmoke' -v
sha256sum cmd/cards/testdata/backlog.frozen.jsonl   # matches the hash pinned in the test
```

### Exit criteria (binary)

- [ ] CLI wrapper flag IO + fresh-DB refusal under test.
- [ ] Re-export byte-stable (sorted-line diff); **the specific ordering
      guarantee under test is named** (id-sorted canonical ordering of
      cards/users/events via `portable.go`'s inline `slices.SortFunc` calls
      (users `:58` / cards `:93` / events `:118`), or a named
      non-determinism fix in `portable.go`).
- [ ] **Test fixture is version-pinned:** `cmd/cards/testdata/backlog.frozen.jsonl`
      with a sha256 asserted in the test; the live `.cards/backlog.jsonl`
      is not a test input.
- [ ] `board.sh import --force` + `install-hook` smoked via a `go test`
      (shelling out), in a temp repo.
- [ ] No production code changed unless a named non-determinism fix was required; `go test ./cmd/cards` green.

---

## Phase 3 — Runnable cards-extension seed (composition proof, automated)

**Purpose:** Demonstrate "extensions over plugins" (philosophy §6) with one
real running process and an **automated test** proving the
SSE → take-next → comment → transition loop — the seam the sprint exists to
prove, not a manual demo.

> **Framing correction (decisive):** the candidate wording ("promote
> pi-extension to a runnable in-repo example") is mis-framed —
> `docs/design/pi-extension.md` §4 ships the pi extension in its own repo
> for four stated reasons. Proceeding literally is a philosophy-§6
> regression. This phase seeds a runnable **cards-extension** `service`
> instead.

### Do

1. **Seed the extension.** New file
   `examples/demo-workspace/.cards/ext/review-bot.mjs` — ~150 lines
   (reconnect-with-backoff, `Last-Event-ID` tracking, and the structured
   log line push this well past the naive ~80), Node
   stdlib `fetch` + a hand-rolled SSE line reader (no npm deps). Loop:
   subscribe to `/v1/events/stream` (sending `Last-Event-ID` on reconnect —
   the contract `api.go:555` documents) → on `status_changed → review` →
   `POST /v1/cards/take-next` → `POST /v1/cards/:id/comments`. Lift the
   snippet already written at `docs/events/integration.md:37-95`.
   **Structured subscribe log (suggestion accepted):** on connect, log a
   stable `{"event":"subscribed","lastEventId":...}` JSON line so the
   supervisor-health test has a stable string to grep.
2. **Wire the supervisor declaration.** Add a `service` entry to
   `examples/demo-workspace/definitions/extensions.json` (`autostart: true`,
   `restart_policy: on-failure`, `run: ["node",".cards/ext/review-bot.mjs"]`).
   **No `internal/hooks/` changes.** Behind `--run-extensions` (not
   default-on for plain `cards serve`).
3. **Doc-status fixes.** In `docs/extensions/index.md`: add "Example 7 —
   runnable SSE worker (Node service) `[built]`"; fix the stale
   `[built — external]` vs `[proposed]` mismatch on Example 6. In
   `docs/design/pi-extension.md`: clarify the one-line status. Cite
   `docs/events/integration.md:37` from the new example.
4. **Node runtime gate (MINOR fix).** The example README block states the
   Node requirement. The supervisor logs a clear
   `node: not found — service review-bot skipped` line on a Node-less
   machine, not a silent restart loop.
5. **Automated test (BLOCKER fix — `scripts/review-bot_test.sh`).** A bash
   harness (runnable standalone and via a `go test` wrapper so it lives in
   `go test ./...`) that:
   - builds `cards`, starts `cards serve --workspace <temp> --run-extensions`
     on a temp workspace **provisioned from `examples/demo-workspace`** so it
     carries a board with a `review` status (engineering) plus the bot's
     `extensions.json` declaration — the temp workspace is a copy of the
     demo workspace, not an empty one,
   - creates a card, transitions it to `review`,
   - asserts the bot's comment appears via `cards show --json`, scoped to
     the **bot's** author identity (BLOCKER — the demo workspace already
     ships a `review-notify` **hook** (`extensions.json` id `review-notify`,
     `on: status_changed`, filter `board_id: engineering, to_status: review`)
     firing on the identical event; two extensions reacting to one
     transition is an intentional hook-vs-service illustration, so the test
     must assert on the bot's comment specifically, not "something
     happened"),
   - **SSE resumption (MAJOR):** kills and restarts the server mid-stream,
     drives a second card to `review`, asserts the bot resumes from the
     last event id and does not miss the `status_changed`,
   - **Supervisor stability (MAJOR):** asserts the bot process reaches the
     `subscribed` state within N seconds (default 10, **env-tunable** via
     `REVIEW_BOT_SUBSCRIBE_TIMEOUT` to avoid flakiness on loaded CI) and
     does not restart more than once in a 5s window.
6. **No-npm-deps assertion (suggestion accepted).** Exit criterion greps
   the seed for `require(` / `import` from non-stdlib and asserts empty.

### Outcome (observable)

- `cards serve --run-extensions` starts the bot; a `status_changed → review`
  event triggers a comment back onto the card, verified by
  `cards show --json`, in an automated test.
- Killing/restarting the server mid-stream does not lose events.

### Files

`examples/demo-workspace/.cards/ext/review-bot.mjs` (new),
`examples/demo-workspace/definitions/extensions.json`,
`docs/extensions/index.md`, `docs/design/pi-extension.md`,
`scripts/review-bot_test.sh` (new) + a `go test` wrapper.

### Demo (third-party-runnable)

```bash
go build -o cards ./cmd/cards
scripts/review-bot_test.sh        # automated: starts serve --run-extensions,
                                  # drives card → review, asserts comment,
                                  # kill/restart mid-stream, asserts resumption,
                                  # asserts supervisor stability
grep -nE 'require\(|import ' examples/demo-workspace/.cards/ext/review-bot.mjs | grep -v 'node:'   # empty
```

### Exit criteria (binary)

- [ ] `review-bot.mjs` runs under the supervisor with `--run-extensions`,
      zero npm deps (grep assertion passes), sends `Last-Event-ID` on reconnect.
- [ ] **`scripts/review-bot_test.sh`** (BLOCKER) passes: a
      `status_changed → review` event triggers a comment back onto the card,
      asserted via `cards show --json`.
- [ ] **SSE resumption test (MAJOR):** kill/restart server mid-stream; bot
      resumes from last event id, no missed `status_changed`.
- [ ] **Supervisor stability test (MAJOR):** bot reaches `subscribed` within
      N seconds (default 10, env-tunable `REVIEW_BOT_SUBSCRIBE_TIMEOUT`);
      no more than one restart in a 5s window. Asserts on the **bot's**
      comment author, not the pre-existing `review-notify` hook.
- [ ] `extensions.json` declares the `service` with autostart + restart policy.
- [ ] Node requirement stated in README; supervisor logs clear "node: not
      found — skipped" on Node-less machines.
- [ ] Doc-status tags fixed (Example 6 mismatch, Example 7 added, `pi-extension.md` one-liner).
- [ ] No `internal/` kernel code changed; `go build ./...` clean.

---

## Phase 4 — TUI filter/sort parity (transport parity, user-value capstone)

**Purpose:** Close the TUI/web parity gap so the TUI is a credible daily
driver of the same query surface, with sort presets sourced from the web
UI's `boardSortOptions` and `/` (find) kept distinct from the filter modal.

> **Rescoping:** outbound links already render (since `0421efd`); `0d33bdd`
> fixed inbound links. This phase ships sort/filter parity only; link-follow
> is out of scope.

### Do

1. **Wire the query surface into `refresh`.** `internal/tui/tui.go:440`
   (`refresh`) builds `core.CardQuery{BoardID, Limit:500,
   Include:["links","comments"]}` with no `Sort`/`Owner`/`TypeID`/`Filter`.
   Add model fields for the active sort/owner/type/filter and compose them
   into the query. Do `me`→actor substitution for saved filters (actor is
   `m.actor`). **Transport-conflation fix (MINOR):** the TUI is
   serverless/in-process (`tui.go:443` calls `m.svc.ListCards` directly) —
   there is no HTTP 422; bad sort/filter DSL returns a Go `*core.Error` from
   `ListCards` (`service.go` validates sort via `ParseSort` up front),
   surfaced via `notifyErr`.
2. **Non-colliding keybindings (BLOCKER + MAJOR fix).** `/` stays Search
   (find) at `tui.go:217`. New bindings:
   - `f` — opens the **filter modal** (server-side jq-DSL; owner/type
     narrows live here too). **Not `/`.**
   - `F` — sort-cycle.
   - `T` — type-filter.
   - Owner-filter is folded into the `f` filter modal (no separate `O`
     binding) to avoid the `o`/`O` "two owner actions one key apart"
     muscle-memory trap. `s` (status) and `o` (edit-owner) untouched.
   - Update `FullHelp` group 2 so the collision is reviewable: the row
     reads `f filter / F sort / T type / / find`, with `s status` and
     `o owner` separate.
   Store active directives on the model so they survive a bus-driven
   `refresh` (which rebuilds the query from scratch).
3. **Source sort presets from `boardSortOptions` (MAJOR fix).** "Parity"
   means the TUI offers the same labeled presets the web UI ships
   (`internal/httpapi/ui.go:402` `boardSortOptions`: `-updated_at`
   "Recently updated", `-created_at` "Newest", `created_at` "Oldest",
   `title` "Title (A–Z)", plus a board-`LaneSort` "Board default" entry) and
   respects `board.Presentation.LaneSort`. **Suggestion accepted (placement
   corrected per external review):** extract a shared `sortOptions(active,
   board)` helper into a **small shared render package** (e.g.
   `internal/uioptions`) — **not `internal/core`**, because "Recently
   updated" is a presentation label and the project's own philosophy says the
   core grows reluctantly. The plan previously hedged "core or shared render
   helper"; resolve the hedge away from core. This turns parity into a
   compile-time guarantee rather than a review-time judgment.
4. **Tests.** `internal/tui/tui_test.go`:
   - sort-order: set `m.sort = "-created_at"`, `m.refresh`, assert
     `m.cards[0].CreatedAt >= m.cards[len-1].CreatedAt`.
   - **`me`-substitution regression (MAJOR fix):** set
     `m.filter = "owner == me"`, `m.refresh`, assert only `m.actor`'s cards
     return.
   - filter-modal behavior assertion.
5. **Note the bus boundary (MINOR).** Add a line to
   `docs/design/tui-bus-disposition.md` that filter/sort changes trigger a
   re-fetch (not a bus round-trip); **cross-reference** the
   surviving-directive test so the invariant is machine-checked, not narrated.

### Outcome (observable)

- Setting `m.sort = "-created_at"` and calling `m.refresh` reorders the lane
  server-side.
- `f` opens the filter modal; `/` still opens find; both work independently.
- TUI sort options equal `boardSortOptions` output for the active board.

### Files

`internal/tui/tui.go`, `internal/tui/tui_test.go`,
`internal/uioptions` (new shared render package holding `sortOptions` —
**not** `internal/core`) + `internal/httpapi/ui.go`
(call site), `docs/design/tui-bus-disposition.md`.

### Demo (third-party-runnable)

```bash
go build -o cards ./cmd/cards
./cards --workspace ./examples/demo-workspace   # bare = TUI
# / + text → substring find (unchanged)
# f + "tag:bug" → server-side filter modal (distinct from /)
# F → cycle sort to "Newest" (-created_at); top card changes; presets match web UI
# enter on a card with links → detail shows "→ blocked-by → <shortid> <title>"
go test ./internal/tui -run 'TestSort|TestFilter' -v
```

### Exit criteria (binary)

- [ ] `Sort`/`Owner`/`TypeID`/`Filter` wired into `refresh`; `me`
      substitution + DSL-error surfacing (`*core.Error` from `ListCards` via
      `notifyErr` — no HTTP 422 implied).
- [ ] **`/` (find) and `f` (filter modal) are separate bindings** (BLOCKER);
      `F` sort, `T` type; owner-filter folded into `f`; `s`/`o` untouched.
      `FullHelp` group 2 updated and reviewable.
- [ ] **TUI sort options equal `boardSortOptions` output for the active
      board** (MAJOR), via a shared `sortOptions` helper (compile-time parity).
- [ ] Sort-cycle + filter-modal keys bound; directives survive `refresh`.
- [ ] **`me`-substitution regression test** (MAJOR): `m.filter = "owner == me"`
      returns only `m.actor`'s cards.
- [ ] Tests assert sort order + filter behavior; `go test ./internal/tui` green.
- [ ] `tui-bus-disposition.md` cross-references the surviving-directive test.
- [ ] No new deps; no schema/API churn; `go build ./...` clean.

---

## Risks — only those the plan confronts

1. **The anchor guard lands too weak to prevent the rot class (Phase 1).**
   A "file exists + line in range" check green-lights `:1472` even though
   `claimWithRetry` is at `:1491`. **Mitigation (BLOCKER fix):** the guard
   parses an explicit `<!-- guard: ... symbol=... -->` marker, not prose
   regex; the negative-proof #2 demo proves it catches the
   moved-and-renumbered case.
2. **Boundary-rolling discipline doesn't actually improve (Phase 1).**
   **Mitigation:** the boundary-commit-within-N=20 assertion closes the
   accepted gap (review minor, accepted). **Feedback refinement:** the
   tripwire is a warning in normal `go test` and strict only in a dedicated
   docaudit CI job, with a failure message naming the one-line fix — so it
   doesn't train contributors to ignore docaudit red on unrelated PRs.
3. **The snapshot phase reads as busywork (Phase 2).** **Mitigation:** scope
   to the 4 residual gaps with "already pinned by `portable_test.go`" context
   up front; byte-stability names its guarantee, the frozen fixture is the
   visibly new value.
4. **The real-snapshot test is a change detector (Phase 2).** **Mitigation
   (MAJOR fix):** frozen, hash-pinned fixture; the live board is not a test
   input.
5. **Scope creep into the `work_cards`/`@work-cards/client` lib (Phase 3).**
   **Mitigation:** seed stays raw-HTTP; lib explicitly out of scope; no-npm
   grep assertion.
6. **Node runtime assumption in `examples/` (Phase 3).** **Mitigation
   (MINOR fix):** behind `--run-extensions`; Node requirement stated;
   supervisor logs clear skip line.
7. **SSE robustness / supervisor restart loop (Phase 3).** **Mitigation
   (MAJOR fix):** `review-bot_test.sh` asserts resumption on kill/restart
   and supervisor stability (subscribed within N=10s env-tunable, ≤1
   restart / 5s).
8. **Sort vs. live refresh (Phase 4).** **Mitigation:** store active
   sort/filter on the model; surviving-directive test cross-referenced from
   the bus-disposition doc.
9. **`me` substitution silently filters wrong (Phase 4).** **Mitigation
   (MAJOR fix):** dedicated `me`-substitution regression test.

## Out of scope (explicit)

- `work_cards` / `@work-cards/client` convenience library (L+, new dep).
- TUI link-follow navigation, especially cross-board jump (borderline L).
- Rolling the SoT boundary §4–§8 audit across all 17 new commits (scope creep).
- `HTTP filter=` list-param gap (acknowledged debt, no candidate depends on it).
- Auth / ACL (frozen design, constraint-deferred).
- Outbox / webhooks (gated no-go, constraint-deferred).
- Closing zombie/stale board inventory — separate housekeeping pass.
- Release tag `v0.1.4` — separate release ceremony.

## Traceability — candidate `path`s per phase

| Phase | Draws from `path` | Effort (rescoped) | Deferred from this `path` |
|---|---|---|---|
| 1. SoT refresh + anchor guard | `drift-sot-refresh-and-anchor-guard` | M | HTTP `filter=` gap; full §4–§8 re-audit |
| 2. JSONL snapshot residual gaps | `jsonl-snapshot-roundtrip-test` | **S** (was M) | Nothing — 4 residual gaps are the entire scope; headline round-trip already pinned |
| 3. Runnable cards-extension seed | `runnable-extension-seed` (reframed) | **M** (was L) | `work_cards`/`@work-cards/client` lib; the pi-extension in-repo (philosophy §6 violation) |
| 4. TUI filter/sort parity | `tui-parity-followups` (reframed) | M | Link-follow navigation / cross-board jump; outbound link *rendering* (already shipped) |

**Deferred entirely (no phase):** auth/ACL, outbox/webhooks, board-inventory
hygiene, release tag, read-pool trilogy, `View` type drop, benchmark suite.

---

## Review disposition

Every blocker and major from `review.md`. Suggestions accepted are noted;
declined suggestions carry a one-line reason.

### Blockers

- **B1 — Phase 1 anchor guard extraction contract unspecified.** **FIXED.**
  Each citation now carries an explicit parseable marker
  `<!-- guard: <path>:<line>[-<end>] symbol=<ident> -->`; the guard parses
  the marker, not prose regex. Marker syntax is named in the exit criterion.
  Negative-proof #2 (symbol moved, cited line bumped to a different function)
  is a named exit criterion proving the guard checks the symbol *at* the
  line.
- **B2 — Phase 4 filter modal bound to `/` (Search collision at
  `tui.go:217`).** **FIXED.** Filter modal moves to `f`; `/` stays find.
  `FullHelp` group 2 updated. Exit criterion states `/` and `f` are separate.
- **B3 — Phase 3 ships the composition seam with zero automated tests.**
  **FIXED.** Added `scripts/review-bot_test.sh` (runnable standalone + via
  `go test` wrapper): starts `serve --run-extensions`, drives a card to
  `review`, asserts the bot's comment via `cards show --json`.

### Majors

- **Phase 4 sort/owner/type key collisions (`s`/`S`, `o`/`O`).** **FIXED.**
  Bindings: `f` filter modal (owner-filter folded in), `F` sort, `T` type;
  `s`/`o` untouched. Final bindings named in the exit criterion.
- **Phase 4 sort parity invented, not sourced.** **FIXED.** TUI sort menu
  sourced from `boardSortOptions` via a shared `sortOptions` helper
  (suggestion accepted — compile-time parity). Exit criterion: "TUI sort
  options equal `boardSortOptions` output for the active board."
- **Phase 2 real-`backlog.jsonl` fixture couples CI to board churn.**
  **FIXED.** Frozen copy at `cmd/cards/testdata/backlog.frozen.jsonl` with a
  sha256 pinned in the test; the live board is not a test input. Exit
  criterion: "test fixture is version-pinned, not the live board."
- **Phase 2 byte-stability may be a tautology.** **FIXED.** The test names
  the specific ordering guarantee under test (id-sorted canonical ordering
  via `portable.go`'s inline `slices.SortFunc` calls at `:58`/`:93`/`:118`);
  if non-determinism is found, the fix
  belongs in `portable.go` and is named in the test comment.
- **Phase 3 seed has no automated test.** **FIXED** — see B3.
- **Phase 3 SSE reconnect contract asserted only by inspection.** **FIXED.**
  `review-bot_test.sh` kills/restarts the server mid-stream and asserts the
  bot resumes from the last event id (no missed `status_changed`).
- **Phase 3 seed process lifecycle untested.** **FIXED.** `review-bot_test.sh`
  asserts the bot reaches `subscribed` within N=10s and restarts ≤1× in a 5s
  window. Structured `{"event":"subscribed",...}` log line (suggestion
  accepted) gives the test a stable grep target.
- **Phase 4 no test for filter-modal `me` substitution.** **FIXED.** Added a
  regression test: `m.filter = "owner == me"`, `m.refresh`, assert only
  `m.actor`'s cards return.

### Minors (all accepted)

- Phase 3 Node runtime gate — README states Node req; supervisor logs clear
  "node: not found — skipped".
- Phase 1 negative-proof #2 (move symbol, bump line to different function) —
  accepted, named exit criterion (folded into B1).
- Phase 4 transport-conflation ("store 422s") — reworded to `*core.Error`
  from `ListCards` via `notifyErr`.
- Phase 1 guard placement package-doc guarantee — cited in the test comment.
- Phase 1 boundary-rolling discipline — accepted the cheap follow-up
  (boundary commit within N=20 of HEAD).
- Phase 4 bus-boundary doc — cross-references the surviving-directive test.

### Suggestions

- **Shared `sortOptions` helper** — **ACCEPTED** (folded into Phase 4 sort
  parity; compile-time guarantee). **Placement corrected per external
  review:** lives in a small shared render package (`internal/uioptions`),
  **not `internal/core`** (presentation label; core grows reluctantly).
- **Structured `subscribed` log line** — **ACCEPTED** (Phase 3, feeds the
  stability test).
- **Promote `TestCodeCommentDocPathsResolve` to a named exit criterion** —
  **ACCEPTED** (Phase 1).
- **`board.sh` smoke under `go test`** — **ACCEPTED** (Phase 2, shelling out).
- **No-`npm`-import assertion in Phase 3 exit** — **ACCEPTED** (grep for
  `require(`/`import` non-stdlib).

---

## External review disposition (post-publish feedback)

A post-publish review verified the plan's `file:line` claims against the
codebase (nearly all exact — the reviewer confirmed the Phase 1 rotted
anchors and their corrections, Phase 3's entire substrate
`--run-extensions`/supervisor/`CARDS_*` env/`take-next`/SSE resumption, and
Phase 4's keymap survey and `boardSortOptions` presets). Five items raised,
all incorporated:

- **E1 — Dead-doc-ref sweep missed `cmd/` (concrete gap).**
  `cmd/cards/main.go:5` references the deleted `docs/DEVELOPER-REFERENCE.md`,
  but Phase 1's re-point list and grep covered `internal/` only. **FIXED:**
  `cmd/cards/main.go` added to the re-point list; `TestCodeCommentDocPathsResolve`
  and every grep widened to `internal/ cmd/`.
- **E2 — `sortCards`/`sortUsers`/`sortEvents` don't exist.** Phase 2 named
  them as the canonical sort; they are actually inline `slices.SortFunc`
  calls at `portable.go:58`/`:93`/`:118` (users/cards/events). **FIXED:**
  symbol references corrected in both the Do step and the exit criterion /
  review disposition so the implementer doesn't hunt for non-existent
  functions; the guarantee (id-sorted canonical ordering) is unchanged.
- **E3 — Phase 3 temp-workspace provisioning unspecified; demo workspace
  already reacts to the same trigger.** **FIXED:** the temp workspace is
  provisioned from `examples/demo-workspace` (carries the engineering board
  with a `review` status + the bot's `extensions.json` declaration); the
  test asserts on the **bot's** comment author specifically, because the
  demo workspace already ships a `review-notify` **hook** (`extensions.json`,
  `on: status_changed`, filter `board_id: engineering, to_status: review`)
  firing on the identical event — two extensions on one transition is an
  intentional hook-vs-service illustration, named as such.
- **E4 — Boundary-within-20-commits assertion fails CI on commit count
  alone.** An unrelated PR goes red purely because 20 commits accumulated,
  training contributors to ignore docaudit failures (defeating Phase 1's
  point). **FIXED:** the within-N check is a **warning** in normal `go test`
  runs and **strict** only in a dedicated docaudit CI job (`-tags=strictdoc`);
  on breach it fails with a message naming the one-line fix. The anchor and
  doc-path tests remain strict everywhere.
- **E5 — Smaller notes.** Boundary line is at `implementation-status.md:619`
  (not ~L780) — **FIXED**. `review-bot.mjs` is ~150 LOC (not ~80) once
  reconnect-with-backoff, `Last-Event-ID` tracking, and the structured log
  are added — **FIXED**, design unchanged. The 10s "reaches subscribed"
  window is now env-tunable (`REVIEW_BOT_SUBSCRIBE_TIMEOUT`) — **FIXED**.
  The shared `sortOptions` helper is placed in a small shared render package
  (`internal/uioptions`), **not `internal/core`** (presentation label; core
  grows reluctantly) — **FIXED**, resolving the plan's earlier hedge.
