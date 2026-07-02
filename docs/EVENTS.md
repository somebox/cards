# Events — Core Design (Revised)

How events are produced, persisted, delivered, observed, and replayed inside the
cards core.

This revision keeps the original strengths (single emission seam, persist-before-
publish, replayable log) while making responsibilities sharper and the runtime
model simpler to reason about.

**Status legend:** **[built]** in code today · **[proposed]** designed here, not
yet implemented · **[refactor]** exists but moves/renames under this design.

Related: [`INTEGRATION.md`](INTEGRATION.md), [`SPEC.md`](SPEC.md),
[`ARCHITECTURE.md`](ARCHITECTURE.md), [`PHILOSOPHY.md`](PHILOSOPHY.md).

---

> **Current status (2026-07):** **Steps 1–2 are implemented and merged; Step 3
> is in progress.** `go build ./...` + `go test ./...` are green.
>
> **Step 1 (seam hardening) — done.** The `EventLog` interface, `Emitter`
> (`Emit`/`Signal`/`stamp`/`dispatchCommitted`), `Bus` + `InProcBus`, typed
> constructors, and `commitCard` are landed; all mutation paths route through
> typed constructors + `commitCard` (or `dispatchCommitted` for the atomic
> `TakeNext` path). The §4 "no raw `Event{...}` literals" rule is enforced by a
> test guard. The §10 test seams are built: in-memory `EventLog` fake +
> conformance suite (`internal/core/eventlogtest`), an observer `Recorder`, an
> injected clock, and golden JSON fixtures (`internal/core/testdata/events`,
> driven from `internal/core/events_test.go`).
>
> **Step 2 (board scope) — done.** `scope`/`board_id` on the envelope, the
> `BoardEvent` constructor, the nullable-`card_id` schema migration + backfill
> (verified against the real demo DB), and feed/SSE filtering by `board_id`/
> `scope`.
>
> **Step 3 (condition events) — in progress.** 3a (WIP signal) merged; 3b
> (`persist:true` escalation) in review (PR #15); 3c–3e planned — see §12,
> Step 3.
>
> **Step 4 (outbox/tailer) — future**, unstarted by design.
>
> *(Some per-section `[proposed]` tags below still predate Steps 1–2 landing and
> are being swept separately to avoid churn against in-flight PRs; the status
> here and §12 are the authoritative state.)*

---

## 1) Design goals and non-goals

### Goals

1. **Correctness first.** Durable facts are never published before they are
   committed.
2. **One obvious write path.** Call sites construct a domain event and hand it to
   one seam; no ad-hoc envelope stamping.
3. **Small abstractions.** Keep interfaces minimal and fakeable in tests.
4. **Safe API boundaries.** The important invariant — commit before dispatch —
   should be enforced by API shape, not caller discipline.
5. **Operational clarity.** Each surface (log, bus, observers) has explicit
   semantics and failure behavior.
6. **Stable contracts.** Event payloads are wire contracts, protected by typed
   constructors and golden tests.
7. **Low dependency footprint.** Pure Go interfaces + stdlib primitives.

### Non-goals

- Not a full event-sourcing framework.
- Not exactly-once delivery over the network.
- Not a cross-process message broker.

---

## 2) Principles

1. **Persist before publish.** Durable events become visible on the live bus only
   after commit.
2. **Append-only log for facts.** Durable event history is replayable and treated
   as an immutable journal.
3. **Separate facts from signals.**
   - **Facts**: durable domain history (mutation events, selected board events).
   - **Signals**: ephemeral runtime hints/conditions (may be dropped; not replayed).
4. **The core records what happened; consumers decide what to do.**
5. **Make the easy path the safe path.** Constructors + seam stamping enforce
   actor/time/scope invariants by default.

---

## 3) Event value (wire compatible)

```go
type Event struct {
	ID      int64     `json:"id"`                 // monotonic, assigned on append
	Version int       `json:"version,omitempty"`  // event contract version; v1 default [proposed]
	Scope   Scope     `json:"scope,omitempty"`    // "card" | "board" [proposed]
	CardID  string    `json:"card_id,omitempty"`  // required when Scope==card
	BoardID string    `json:"board_id,omitempty"` // required when Scope==board [proposed]
	Type    EventType `json:"type"`
	Actor   string    `json:"actor"`              // stamped by seam
	At      time.Time `json:"at"`                 // stamped by seam
	Diff    any       `json:"diff"`
}
```

### Invariants

- Event envelopes are created via constructors only:

```go
func CardEvent(cardID string, t EventType, diff any) *Event
func BoardEvent(boardID string, t EventType, diff any) *Event // [proposed]
```

- Constructors set identity/scope/type/version/diff fields only.
- `Actor` and `At` are stamped by the seam (`ActorFromCtx` + injected clock).
- `Version` defaults to `1`; additive payload fields can stay on the same
  version, while renames/removals/semantic changes require a new version.
- `Diff` remains `any` in the envelope to keep the wire model lightweight, but
  built-in event payloads use named Go structs and typed constructors.
---

## 4) Event contracts and compatibility

Events are integration contracts, not incidental log lines. The envelope remains
small and stable; payload evolution is controlled per event type.

Rules:

- Built-in event diffs are represented by named Go structs, even though the
  envelope field is `any`.
- Prefer event-specific constructors for common mutation events:

```go
func StatusChanged(cardID string, before, after string) *Event
func OwnerChanged(cardID string, before, after string) *Event
func CommentAdded(cardID string, commentID string) *Event
```

- Raw `Event{...}` literals are allowed only in constructors and tests.
- Compatibility is protected with golden JSON fixtures: one fixture per public
  event type/version.
- Consumers must tolerate unknown fields. Producers must not rename, remove, or
  change the meaning of existing fields without introducing a new version.

This keeps `Diff any` pragmatic without letting payloads become undocumented
shapes.


## 5) Two lanes: facts vs signals

The system exposes two explicit write verbs:

```go
// Durable fact: stamp -> persist -> dispatch
Emit(ctx context.Context, evs ...*Event) error

// Ephemeral signal: stamp -> dispatch (no persist)
Signal(ctx context.Context, evs ...*Event)
```

### Rule of thumb

- If an event is needed for audit/recovery/catch-up, it is a **fact** (`Emit`).
- If an event is only a live runtime hint, it is a **signal** (`Signal`).

### Guardrail

When in doubt, choose **fact**. Durability can be ignored by consumers; absence
cannot be recovered.

---

## 6) Core abstractions

| Abstraction | Responsibility | Kind |
|---|---|---|
| `Event` | one occurrence | struct |
| `EventLog` | durable append + query + replay | interface |
| `Bus` | best-effort live fanout | interface |
| `Emitter` | public seam for standalone facts/signals; owns internal stamp/dispatch | struct |
| `EventObserver` | in-process instrumentation hook | func |

### 6.1 EventLog `[built]`

```go
type EventLog interface {
	Append(ctx context.Context, evs ...*Event) error
	List(ctx context.Context, q EventQuery) ([]Event, error)
	Page(ctx context.Context, q EventQuery) (*Page[Event], error)
	Replay(ctx context.Context, fromID int64, fn func(*Event) error) error
}
```

Notes:
- Card mutation events still persist transactionally with card writes for
  atomicity.
- Standalone durable events use `Append` directly.

### 6.2 Bus `[built]`

```go
type Bus interface {
	Subscribe(filter EventFilter, buf int) *Subscriber
	Unsubscribe(id int64)
	Publish(e *Event)
}
```

Required behavior:
- Non-blocking publisher path.
- Slow subscriber policy is explicit (drop subscriber + metric/log marker).
- `EventFilter` must honor `scope`, `card_id`, and `board_id` consistently.

### 6.3 Emitter `[built]`

```go
type Emitter struct {
	log       EventLog
	bus       Bus
	now       func() time.Time
	observers []EventObserver
}

func (e *Emitter) Emit(ctx context.Context, evs ...*Event) error
func (e *Emitter) Signal(ctx context.Context, evs ...*Event)

// Internal/package-private helpers used only by transaction-aware service code:
func (e *Emitter) stamp(ctx context.Context, evs []*Event)
func (e *Emitter) dispatchCommitted(evs []*Event)
```

Contract:
- `stamp` is idempotent (only fills unset `Actor`/`At`).
- `dispatchCommitted` is post-commit only and is not exposed to arbitrary call
  sites.
- Normal service code uses `Emit`, `Signal`, or a transaction-aware service
  helper such as `commitCard`; it does not call stamp/dispatch directly.
- Call sites never assign `ID`, `Actor`, or `At` manually.

### 6.4 EventObserver `[proposed]`

```go
type EventObserver func(e *Event)
```

Observer guidance:
- Observers run synchronously during dispatch.
- They must be fast and non-blocking.
- Any I/O must be offloaded internally (goroutine/channel).

---

## 7) Emission lifecycle

Common lifecycle:

```
Event -> Stamp(actor, at) -> Persist? -> Dispatch(bus + observers)
```

### 7.1 Transactional card mutations (facts)

```go
func (s *Service) commitCard(ctx context.Context, next *Card, evs []*Event) error {
	s.emitter.stamp(ctx, evs)
	if err := s.store.UpdateCard(ctx, next, evs); err != nil { // atomic with event rows
		return err
	}
	s.emitter.dispatchCommitted(evs)
	return nil
}
```

### 7.2 Standalone facts

`emitter.Emit(ctx, BoardEvent(...))`

### 7.3 Signals

`emitter.Signal(ctx, CardEvent(...))` for ephemeral conditions/notifications.

Hard invariant: **no dispatch before durable commit for fact events**. The API
should make the safe path the only normal path: `dispatchCommitted` remains
package-private, and card writes go through `commitCard` rather than open-coded
stamp/store/dispatch sequences.

---

## 8) Failure semantics (explicit)

1. **Persist fails (fact path):** return error; dispatch does not run.
2. **Dispatch fails:** bus/observer failures never roll back committed facts.
3. **Observer panic:** recover per observer, report error metric/log, continue
   remaining observers (recommended implementation).
4. **Slow subscribers:** dropped per bus policy; recovery via feed replay.
5. **Process crash after commit but before dispatch:** durable correctness is
   preserved because the event is in the log, but live subscribers/observers may
   miss that event in the synchronous dispatch model. Consumers that need
   correctness recover through the feed.
6. **Escalated conditions are at-least-once across restarts.** The crossing
   dedup that suppresses repeat condition emissions (e.g. the WIP
   exceeded/cleared state map) is in-memory. After a restart a condition that is
   still true re-fires on the next triggering mutation, appending a *duplicate*
   durable fact. Consumers must therefore treat escalated condition facts as
   **idempotent assertions of a state** — keyed by their identity (board +
   column + type), deduped by the consumer — not as counted occurrences.
   Temporal conditions (§12, Step 3d) avoid the duplicate entirely with a
   fired-marker reconstructible from denormalized state; instant conditions
   either adopt the same discipline or accept at-least-once by contract.
7. **Escalated-condition append failure.** `Condition` routes escalated types
   through `Emit`; if that append fails, the durable audit trail — the whole
   reason `persist:true` was set — silently gains a hole. Best-effort callers
   (e.g. `evaluateWIP`) must not fail the triggering mutation on it, but the
   seam must **surface** the append error (log / observer / metric), never
   swallow it. (A signalled condition dropping is by definition for nobody; an
   escalated one dropping is data loss.)

This keeps data integrity deterministic while making live delivery best-effort.
If post-commit live/observer delivery must itself become reliable, evolve to the
outbox/tailer model in §12, Step 4.

---

## 9) Delivery semantics by surface

- **Event log / feed** (`Page`, `Replay`): durable, ordered by `id ASC`,
  replayable.
- **Live bus / SSE**: at-most-once best-effort for current subscribers.
- **Observers**: in-process hooks only; not durable.

Consumer correctness model:
- Track cursor (`last_seen_id`).
- Treat handlers as idempotent.
- Recover gaps via durable feed, then resume live.

> Note: we avoid "exactly-once" claims at transport boundaries; practical
> correctness is achieved through durable cursors + idempotent consumers.

---

## 10) Testability model (first-class)

> None of the fakes/fixtures below exist yet; they are Step 1 acceptance
> criteria, not already-satisfied requirements (`internal/core/events_test.go`
> does not exist as of this writing).

Required test seams:

1. **In-memory `EventLog` fake** for append/page/replay tests.
2. **Recording `Bus` fake** for publish order/filter/drop behavior.
3. **Recorder observer** for assertion-friendly capture.
4. **Injected clock** for deterministic `At` values.

Minimum acceptance tests:

- stamp determinism (`Actor`, `At`, idempotent stamp)
- persist-before-dispatch invariant
- failed persist emits nothing
- monotonic IDs from store append order
- replay round trip reproduces durable stream
- bus filter correctness (`scope/card/board/type/actor`)
- subscriber drop behavior under full buffer
- observer panic isolation
- golden JSON compatibility for every public event type/version
- service mutation -> expected event table tests

Shift-left checks:

- Forbid raw `Event{...}` literals outside constructors/tests.
- Forbid manual assignment of `ID`, `Actor`, or `At` outside the store/emitter.
- Keep event constructors small and table-tested.
- Treat fixture changes as compatibility-affecting review items.

---

## 11) Event catalog (current and staged)

### 11.1 Durable mutation facts `[built]` (scope: card)

- `card_created`
- `status_changed`
- `field_updated`
- `owner_changed`
- `tags_changed`
- `item_appended`
- `item_updated`
- `item_removed`
- `link_added`
- `link_removed`
- `comment_added`
- `comment_edited`
- `schema_upgraded`
- `artifact_added` **[proposed — constant declared; no upload route or emit site yet]**
- `definition_reloaded` **[proposed — constant declared; no reload trigger implemented yet]**

(Per-type `diff` shapes remain as currently documented and wire-compatible.)

### 11.2 Condition signals `[proposed]`

Examples: `status_timeout`, `card_idle`, `wip_exceeded`, `lane_drained`,
`transition_rejected`.

Default to `Signal`; promote to durable fact only if recovery/audit use-cases
require replay.

### 11.3 Board-scoped facts `[proposed, staged second]`

Add `scope` + `board_id`, keep `card_id` optional by scope. Migration remains
backward compatible for existing card-event consumers.

---

## 12) Staged implementation plan

### Step 1 — seam hardening (no wire/schema change) `[built]`

- Extract `EventLog` interface from store. ✓
- Introduce `Emitter` (`Emit`, `Signal`, internal `stamp`/`dispatchCommitted`). ✓
- Route all mutation paths through `commitCard`. ✓
- Add constructor usage (`CardEvent(...)` initially; event-specific constructors
  for common mutations). ✓
- Add test fakes + seam acceptance tests. *(not yet done — see §10)*
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
- **3b — persist:true escalation `[in review, PR #15]`.** One `Emitter.Condition`
  seam routes each condition by policy: types in workspace
  `settings.persist_conditions` go through `Emit` (durable, replayable), the rest
  through `Signal`. Bus/observer delivery is identical either way. See §11.2.
- **3c — remaining instant conditions.** `lane_drained` / `lane_refilled`,
  `card_blocked` / `card_unblocked`. **Unify with 3a rather than repeat it:** WIP
  *and* lane-drained/refilled both derive from a single **column census** (one
  `ListCards` per affected column per mutation) and one shared crossing-state
  map — do not build a second parallel counting path. `card_blocked` /
  `card_unblocked` reuse the existing `CardQuery.blocked` definition (triggered
  by link mutations, and by a target card's status change), *not* a second notion
  of "blocked". Table-driven test per condition type; verify no condition handler
  mutates card state (§2, principle 4 — the core records, it does not act).
- **3d — deadline scheduler.** Min-heap keyed by earliest deadline; no fixed
  tick; sleeps until the next deadline; empty heap = zero wakeups. Deadlines are
  reconstructible from a denormalized `status_since` column + a fired-marker
  keyed by (card, status, status_since) — nothing is persisted for scheduling
  itself. Lazy / refcounted: a monitor is armed only while it has a live consumer
  (SSE filter, hook/webhook, or `persist:true`) and dropped when the last
  consumer disconnects. The scheduler emits through `Condition` (3b), so it holds
  no opinion on durability — validating that 3b lands before 3d. Note: a
  `persist:true` temporal type is effectively a *permanent* consumer, so its
  deadline never refcounts to zero ("lazy" does not apply to it — say so).
  Injected clock; no real sleeps in tests.
- **3e — wire temporal conditions.** Attach `status_timeout` / `card_idle` to the
  3d scheduler. Integration test with an injected clock and a *real* SSE
  subscriber: the deadline arms on status entry, fires exactly once on clock
  advance, and does not fire if the card leaves the status first;
  reconnect/disconnect refcounting exercised end to end.

**Cross-cutting hardening — fold into 3c's PR, not separate slices:**

- **Board-membership caveat.** Condition census counts by *type* membership
  (`TypeIDIn`); a board defined by a `DefaultFilter` (e.g. `hipri`) is not counted
  correctly, and the census caps at 500 cards. Document the limitation now; fix
  only if/when filter-defined boards gain WIP or lane limits.
- **Config validation.** Validate each `persist_conditions` entry against the
  known condition-type catalog at load; warn on unknown types — today a typo
  (`"wip_exceded"`) silently no-ops.
- **Append-error surfacing.** `Condition`'s escalated path must log/observe a
  failed durable append rather than swallow it (see §8, point 7).
- **Rename (cosmetic).** `dispatchCommitted` now serves both the durable and the
  ephemeral path; rename to `dispatch` so the name stops implying commit.

**Dogfood.** Once 3b (PR #15) merges, set `persist_conditions: ["wip_exceeded"]`
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
