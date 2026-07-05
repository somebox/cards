# Events — Core Design (Revised)

How events are produced, persisted, delivered, observed, and replayed inside the
cards core.

This revision keeps the original strengths (single emission seam, persist-before-
publish, replayable log) while making responsibilities sharper and the runtime
model simpler to reason about.

**Status legend:** **[built]** in code today · **[proposed]** designed here, not
yet implemented · **[refactor]** exists but moves/renames under this design.

Related: [`INTEGRATION.md`](../events/INTEGRATION.md), [`SPEC.md`](../spec/SPEC.md),
[`ARCHITECTURE.md`](../architecture/ARCHITECTURE.md), [`PHILOSOPHY.md`](../concepts/PHILOSOPHY.md).

---

> **Current status (2026-07):** Steps 1–3 implemented and merged
> (seam hardening, board scope, condition events). Step 4 (outbox/tailer)
> future. See `EVENTS-ROLLOUT.md` §12 for the authoritative per-step
> status and `EVENTS-CORE.md` for the conceptual reference.

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

---

## See also

- [EVENTS-CORE.md](EVENTS-CORE.md) — conceptual reference (§1–§11)
- [EVENTS-ROLLOUT.md](EVENTS-ROLLOUT.md) — deployment and rollout (§12–§13)
