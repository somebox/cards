# TUI bus & surface review — disposition

**Status:** disposition recorded (2026-07-18, sprint 2026-07-18 Phase 4) · closes DEBT-61
**Scope:** `internal/tui/` (model ~1.6k LOC), `cmd/cards/tui.go`, the bare-`cards` guard in `cmd/cards/main.go`, and the in-process bus subscription the TUI makes on `core.Service`.
**References:** DEBT-61 (`debt-ledger.md`), TUI landing `0421efd`, bus `internal/core/bus.go`.

DEBT-61 was filed as a *review gap* (fresh surface, no debt-ledger coverage), with
four sub-questions and one escalation question. Findings and dispositions below.

## Sub-questions

### 1. Dependency health (bubbletea v2 / lipgloss v2 / glamour) — **sound**

No `nolint` exclusions anywhere in `internal/tui/` or `cmd/cards/tui.go`. The
dependency set is the deliberate price of a terminal UI (landing commit
`0421efd`); binary size (~30 MB) is consistent with the rest of the single
binary. No action.

### 2. Live-refresh subscription teardown — **sound (verified)**

`tui.go` unsubscribes on model teardown
(`defer svc.Bus().Unsubscribe(m.sub.ID)`, which also releases any blocked
`waitEvent` goroutine). Subscriptions are registered lazily on first board
view and released on quit paths. No leak found. No action.

### 3. Actor resolution matches the rest of the surfaces — **sound**

`cmd/cards/tui.go` resolves actor exactly like the CLI: `--as` → `CARDS_USER`
→ `USER` → workspace `default_user`, and all service writes go through
`core.WithActor(ctx, actor)` (`m.ctx()`), the same seam the HTTP transport
uses via `X-Work-Cards-Actor`. No action.

### 4. Markdown detail pane — **one real defect found and fixed**

The review exposed a genuine bug: **inbound links silently never rendered.**
`syncExtras` queried inbound cards (`LinkTarget`) *without* eager-loading
their link sets, but the markdown renderer reads the relation type from each
inbound card's own links — so the `← <type>` row could never appear. It went
unnoticed because the seeded board's most-recent card previously had no
inbound links; a sprint's board mutations moved a card *with* inbound links
into the selected slot and `TestCardMarkdownSections` went red.

- **Fix (landed):** `syncExtras` now lists with `Include: ["links"]`
  (`internal/tui/tui.go`).
- **Test hardening (landed):** `openDemo` now snapshots the demo DB like
  `openDemoCopy` — render tests were reading the *live* `work-cards.db`, so
  any dogfooding mutation (comments, status flips) could flip content
  assertions. Hermetic now.

### Non-TTY / script safety — **regression test landed**

The `interactive()` guard (both streams TTYs, no `--json`/`--jsonl`) had no
test. `cmd/cards/interactive_test.go` now pins: piped stdin → never
interactive; bare `run()` with piped stdin prints usage. `cards </dev/null`
stays script-safe.

## Escalation question: is the in-process bus subscription load-bearing?

**Disposition: load-bearing, and correct. No redesign (wontfix).**

The TUI must work **serverless** — bare `cards` resolves a workspace and runs
in-process against the local DB, no server required. Its live refresh
therefore *must* be the in-process `core.InProcBus`. This is a different
mechanism from the multi-process SSE fan-out (`GET /v1/events/stream`) by
design:

| | in-process bus (`core.Bus`) | SSE stream (`/v1/events/stream`) |
|---|---|---|
| Topology | one process, one workspace | many processes/clients |
| Durability | ephemeral; slow consumers dropped, never block writes | durable cursor resume (`Last-Event-ID`) |
| Consumer | TUI (and hook supervisor) | external agents/extensions |
| Cross-process | **no** — a `cards serve` on the same DB does *not* notify a serverless TUI | yes |

The known, accepted consequence: a serverless TUI does **not** see writes
made by a concurrently-running `cards serve` on the same workspace (and vice
versa) until its own refresh. That is a *documented boundary*, not a defect
to engineer away — multi-process live coordination is what the SSE stream
(and a future outbox) is for, and the TUI deliberately is not a consumer of
it (stdio transport, serverless posture). If multi-process TUI refresh is
ever wanted, the honest path is `CARDS_URL` client mode, not a bus redesign.

**Filter/sort directives (sprint 2026-07-19 P4):** the TUI's active
sort/owner/type/filter directives are client-side query composition — changing
them triggers a local re-fetch (`refresh` rebuilds the `CardQuery` from the
model fields), **not** a bus round-trip. Because directives live on the model,
bus-driven re-fetches preserve them; that invariant is machine-checked by
`TestFilterSortDirectivesSurviveRefresh` (`internal/tui/tui_test.go`), not
narrated.

## Cap honored

Per the sprint plan this review was capped at: subscribe-teardown observe,
non-TTY regression, and in-process-vs-SSE documentation — **not** a new
architecture project. DEBT-61 is closed as: findings recorded, one defect
fixed (inbound-link rendering + hermetic tests), remaining questions
wontfix/defer with the boundary documented here and in
`docs/architecture/index.md`.
