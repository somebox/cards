<!-- EVENTS-ROLLOUT.md — deployment and rollout for the event system.
     See EVENT.md for the index. -->

## 12) Staged implementation plan

### Step 1 — seam hardening (no wire/schema change) `[built]`

- Extract `EventLog` interface from store. ✓
- Introduce `Emitter` (`Emit`, `Signal`, internal `stamp`/`dispatchCommitted`). ✓
- Route all mutation paths through `commitCard`. ✓
- Add constructor usage (`CardEvent(...)` initially; event-specific constructors
  for common mutations). ✓
- Add test fakes + seam acceptance tests. ✓ (see §10)
- `TakeNext` uses `ClaimAtomic` (already persists) — dispatch via
  `emitter.dispatchCommitted`, not `Emit`, to avoid double-persisting. ✓

### Step 2 — board scope support

- Add `scope`/`board_id` fields and filtering semantics.
- Add schema migration and backfill.
- Extend bus/feed query filters.
- Add board-scope tests.

### Step 3 — condition event rollout

Rolled out seam by seam, each its own reviewable slice. **Instant** conditions
(synchronously evaluable, inline after the triggering mutation) land first;
**temporal** conditions (scheduler-backed) land last, after the instant
machinery has validated the Signal / Emit / `persist:true` paths.

- **3a — WIP signal `[built]`.** `wip_exceeded` / `wip_cleared` fire when a board
  column crosses its configured limit; ephemeral `Signal`; crossing-deduped so
  they fire only on a state change, not every mutation.
- **3b — persist:true escalation `[built]`.** One `Emitter.Condition`
  seam routes each condition by policy: types in workspace
  `settings.persist_conditions` go through `Emit` (durable, replayable), the rest
  through `Signal`. Bus/observer delivery is identical either way. See §11.2.
- **3c — remaining instant conditions `[built]`.** `lane_drained` /
  `lane_refilled` and `card_blocked` / `card_unblocked`. **Unified with 3a as
  specified:** `Service.evaluateColumn` runs a single **column census** (one
  `ListCards` per affected column per mutation) feeding both the WIP-limit
  crossing and, for columns in `board.monitors.alert_when_empty`, the
  drained-lane crossing, through one shared crossing-state map
  (`Service.condState`, keyed `board\x00column\x00{wip,lane}`) via
  `evaluateCrossing` — no second parallel counting path. `evaluateColumn` now
  fires from every column-changing mutation path: `PatchCard` (status move,
  as 3a already had), `CreateCard` (a card landing directly in a capped/
  watched column — was a gap), and `TakeNext` (claim + optional status move,
  evaluated from the returned `status_changed` diff — was a gap). `card_blocked`
  / `card_unblocked` reuse `CardQuery.Blocked`'s exact SQL predicate — both
  compile from the shared `internal/sqlite.blockedLinkTypesIN` fragment via
  the new `Store.Blockers`/`Store.BlockingDependents`, so "blocked" has one
  definition, not two. `evaluateBlocked` (keyed per-card in the same
  `condState` map) fires from `AddLink`/`RemoveLink` (the source card's own
  blocked state) and from any committed status change, via
  `reevaluateDependents`, on every card depending on the card that moved
  (covers a target reaching "done" — unblocks — and leaving it again —
  re-blocks). Table-driven tests cover all instant-condition paths, a
  card-state-immutability assertion (§2 principle 4 — the core records, it
  does not act), a `Blockers` ≡ `CardQuery.Blocked` agreement test, and the
  escalated-append-failure-is-logged-not-fatal case (§8 point 7).
- **3d — deadline scheduler `[built]` (machinery only; no condition type
  registered yet — see 3e).** Enablers: a `Clock` seam (`core.WithClock`,
  production `wallClock` default, `clocktest.Fake` for tests — waitable, not
  just readable) and the `status_since` column (additive migration,
  maintained by `CreateCard`/`PatchCard`/`ClaimAtomic`). `MonitorScheduler`
  (`internal/core/monitor.go`): a `container/heap` min-heap keyed by earliest
  deadline; no fixed tick; sleeps until the next deadline (capped at 1h, the
  INTEGRATION.md safety net); an empty heap parks on its wake channel alone —
  zero wakeups, proven by a call-counting fake clock. Deadlines are
  reconstructible from denormalized card state (e.g. `status_since`) via a
  per-type `rebuild` callback; nothing is persisted for the heap itself — only
  the fired-marker (`condition_marks` table, `INSERT OR IGNORE` = atomic
  check-and-set, pruned to the latest key per (card, type) on a fresh fire) is
  durable, giving exactly-once even across a restart (tested: two scheduler
  instances over the same store, the second's rebuild skips the already-fired
  key). Lazy / refcounted: `InProcBus` gained `SetOnSubscriptionChange` (fired
  on subscribe, unsubscribe, *and* the slow-consumer drop inside `Publish` —
  a lost consumer never calls `Unsubscribe` itself) and `HasSubscriberFor`; a
  type arms iff `bus.HasSubscriberFor(t) || emitter.IsPersisted(t)` — a
  `persist:true` type is a permanent consumer (armed with zero subscribers),
  exactly as specified. A serverless CLI process (no subscribers, nothing
  persisted) never arms and the scheduler goroutine never does real work.
  Live-verified against the real demo workspace with a synthetic condition
  type and the real wall clock (not just the fake).
- **3e — temporal conditions `[built]`.** `status_timeout`/`card_idle` wired
  onto the 3d scheduler via `Service.monitorObserver`, an `EventObserver` —
  zero new call sites in the mutation paths. `status_changed`/`card_created`
  arm `status_timeout` at `status_since + max`; every durable card-mutation
  event re-arms `card_idle` at `updated_at + idle_after` — a condition event
  itself never resets it (`isConditionType` guards this; test-pinned: firing
  `wip_exceeded` alongside real mutations must not push back a card's idle
  deadline). Fire-time re-verify checks identity (`status`+`status_since` for
  timeout, `updated_at` for idle) so a stale deadline discards silently
  instead of firing on outdated state. The mandated integration test (real
  SSE client + injected clock, `internal/httpapi/temporal_test.go`) confirms
  the full contract: arms on a live subscriber, fires exactly once at the
  deadline, no duplicate on a further advance, disarms on disconnect (a
  second card's breach is never observed with nobody listening), and a fresh
  subscriber's rebuild fires that still-true breach exactly once on
  reconnect. Board config: `monitors.max_time_in_status`/`idle_after`
  (duration strings via the new `ParseMonitorDuration`, which adds a `"d"`
  days suffix `time.ParseDuration` lacks). Live-verified against a scratch
  demo copy with a 2-second override: the rebuild path correctly enumerated
  and fired every real backlog card already sitting in `review`, and a newly
  created card fired precisely at its own deadline. `/v1/breaches` does not
  yet include temporal items — deferred (noted in NOTES.md), not required by
  this milestone.

**Cross-cutting hardening — folded into 3c's first PR, all `[built]`:**

- **Board-membership caveat.** Condition census counts by *type* membership
  (`TypeIDIn`); a board defined by a `DefaultFilter` (e.g. `hipri`) is not counted
  correctly, and the census caps at 500 cards. Documented on `evaluateColumn`;
  fix only if/when filter-defined boards gain WIP or lane limits.
- **Config validation.** `internal/config.validatePersistConditions` checks
  each `persist_conditions` entry against `core.ConditionTypes()` at load and
  appends a warning (`config.Result.Warnings`, printed by `cmd/cards` via
  `openWorkspace`) for unknown types — a typo (`"wip_exceded"`) now surfaces
  instead of silently no-op'ing. `monitors.alert_when_empty` unknown columns
  hard-fail at load, matching the existing board-validation convention.
- **Append-error surfacing.** `evaluateCrossing` logs a failed escalated
  append (`log.Printf("ERROR: escalated condition append failed ...")`)
  instead of discarding `Condition`'s return value (see §8, point 7).
- **Rename (cosmetic).** `dispatchCommitted` → `dispatch` — it always served
  both the durable and the ephemeral path; the old name implied commit.

**Dogfood.** 3b is merged — set `persist_conditions: ["wip_exceeded"]`
on the demo workspace so we exercise the escalation path the same way integrators
(picraft) will — otherwise signals are invisible after the fact and can't be
dogfooded ("did WIP fire yesterday?" is unanswerable for an un-escalated signal).

### Step 4 — optional outbox/tailer evolution `[future]`

If synchronous post-commit dispatch becomes insufficient, make the durable log
itself the delivery source:

```text
request transaction -> card rows + event rows
background tailer   -> reads log in id order -> bus/SSE/observers/projections
consumers           -> track durable cursors
```

Benefits:
- closes the commit-then-crash-before-dispatch live-delivery gap
- isolates subscriber/observer backpressure from request latency
- gives projections and integrations a durable cursor model

Costs:
- adds a worker/tailer and cursor bookkeeping
- live delivery becomes slightly asynchronous
- more operational surface

This is deliberately staged as an evolution, not Step 1. The current design is
acceptable while live bus/observer delivery is best-effort and feed recovery is
the correctness path.

---

## 13) Why this revision is simpler

- Keeps one seam for consistency, but draws a hard line between durable facts
  and ephemeral signals.
- Keeps `dispatchCommitted` internal so commit-before-dispatch is enforced by
  API shape rather than convention.
- Makes failure behavior explicit, including the synchronous dispatch crash gap.
- Uses conservative delivery language (idempotent consumers + cursor recovery).
- Treats event payloads as versioned contracts while keeping the envelope simple.
- Prioritizes test seams, golden fixtures, and shift-left checks over additional
  framework surface.

In short: **small interfaces, explicit semantics, durable correctness, and
pragmatic evolution path.**

## See also

- [EVENTS.md](EVENTS.md) — index
- [EVENTS-CORE.md](EVENTS-CORE.md) — conceptual reference (§1–§11)
