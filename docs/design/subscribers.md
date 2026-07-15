# RFC: Durable subscribers (outbox/tailer + webhook delivery)

**Status:** Design-ready, **gated**. Phase 6's go/no-go recommended **no-go**
(defer Sprint B) — see [outbox-gonogo.md](outbox-gonogo.md). This RFC lands
regardless of that verdict so the design is resolved *cold*, reviewed without
coding pressure, and ready to build the moment a real go signal arrives (a
consumer the team relies on losing events that matter in real use).

**Scope:** the three things that are design failures if drifted into rather than
decided — a name collision, a silent-no-op deliverability gap, and an ambiguous
dispatch topology — plus the entity model, liveness ownership, migrations,
testability, auth/SSRF, and the hooks-migration fork. No code is proposed here;
this is the contract the eventual implementation must honor.

---

## 1. Context (verified against HEAD)

- The event log is crash-safe ground truth: a `persist:true` condition's marker
  and its event append commit atomically (Phase 2,
  `MarkConditionFiredAndAppend`). A cursor consumer can trust `events.id` order.
- `Emitter.Emit` (durable path) runs **stamp → `log.Append` → `dispatch`**
  synchronously at commit; `dispatch` publishes to the in-process `Bus` and
  notifies `EventObserver`s (`internal/core/events.go`). The hook `Supervisor`
  is a live `Bus` subscriber with an empty filter (`internal/hooks`).
- `core.Subscriber` (`internal/core/bus.go`) is `{ID int64, Filter EventFilter,
  Ch chan *Event}` — an **ephemeral in-process bus listener**. No cursor, no
  durability; dropped on unsubscribe or slow-consumer eviction.
- `MonitorScheduler` arms a temporal type iff `bus.HasSubscriberFor(t) ||
  emitter.IsPersisted(t)` (`monitor.go:170`). `PersistConditions` is documented
  **startup-only** ("configure once, before emission", `events.go`).
- Condition events default to the **Signal path** (dispatch-only, never
  appended); only types escalated via `PersistConditions` reach `Emit`.
- The `events` table is `(id INTEGER PK AUTOINCREMENT, card_id, board_id, scope,
  type, actor, at, diff)`. `migrateEventsScope` rebuilds this table with the
  idempotent-DDL pattern; the events indexes are (re)created after it.
- A deterministic fake clock exists: `internal/core/clocktest.Fake` (`Now`,
  `After`, `Advance`).
- Extension kinds today: `hook`, `service`, `run` (`internal/config/extensions.go`).

---

## 2. Decision 1 — Naming: durable **Consumer**, ephemeral **Subscriber**

The durable entity is a **different species** from `core.Subscriber`: it has a
persistent cursor, survives restarts, and accrues delivery/dead-letter state.
Shipping both under "Subscriber" guarantees ongoing confusion across code, the
`subscriptions` table, and the REST surface.

**Decision:** name the durable entity **`Consumer`**. `core.Subscriber` keeps its
current meaning (ephemeral bus listener) unchanged — renaming the hot in-process
type churns more code for no gain, and "subscriber = live listener" is the
common mental model.

Mapping (make this explicit in code and docs):

| | `core.Subscriber` (exists) | `Consumer` (new) |
|---|---|---|
| Lifetime | connection-scoped, in-process | durable, survives restart |
| Position | none (live tail only) | stored cursor (`last_delivered_id`) |
| Recovery | reconnect + SSE `Last-Event-ID` replay | tailer resumes from cursor |
| Delivery state | none | attempts, last error, dead-letter |
| Backpressure | buffered channel, evict-on-slow | retry/backoff, then dead-letter |

The REST surface is `/v1/subscribers` (the user-facing noun "subscriber" is
fine); the internal durable type is `Consumer`. SSE `Last-Event-ID` is reframed
in docs as a *client-held cursor* — the same idea a `Consumer` persists
server-side — but they are not the same struct.

---

## 3. Decision 2 — Deliverability: **reject-at-registration** (default)

The trap: condition events default to Signal (never appended), so a `Consumer`
whose filter names an ephemeral condition type would match **nothing** in the
log — a silently no-op subscription for half the catalog.

Options were (a) **auto-escalate** the type to `persist:true` at registration, or
(b) **reject** the registration. Auto-escalation must mutate the startup-only
`PersistConditions` global at runtime *and* re-arm `MonitorScheduler` (which keys
off `IsPersisted`), a known-gnarly path that breaks a documented invariant.

**Decision:** **reject at registration** is the default. Registering a `Consumer`
(or webhook subscriber) whose filter includes a type that is not durable is a
`422` with a clear error naming the type and that it is ephemeral. Auto-escalation
is a **specified-only future extension**, not built now; if it is ever added it
must first make `PersistConditions` runtime-mutable with a re-arm hook, as its
own change.

**Required supporting change:** `index.md` gains a **versioned per-type
durable/ephemeral column** in the event catalog (§11), so "is this type
deliverable to a durable consumer?" has one authoritative, wire-documented
answer. A type's durability is part of its contract.

---

## 4. Decision 3 — Dispatch topology: **tailer feeds only new durable consumers**

`Emit` already dispatches synchronously to the bus + observers at commit. A
tailer that *also* republished appended events to the bus would double-deliver
every persisted event to existing bus subscribers (SSE, the hook supervisor).

**Decision:** the tailer is an **additive, parallel** path. It reads the `events`
table by `id` ascending and feeds **only `Consumer`s** (the durable, cursored
entities). The existing synchronous dispatch to `Bus`/`EventObserver` is
**unchanged** — SSE and hooks keep receiving events exactly as often as today.
The tailer never republishes to the bus.

Consequences, pinned as contract:
- The `EventObserver`/hook synchrony contract ("observers notified synchronously
  for every dispatched event") is **preserved** — the tailer does not touch it.
- Two delivery paths coexist by design: synchronous best-effort (bus/observers)
  and durable at-least-once (tailer→consumers). A regression test must assert
  existing bus/SSE subscribers receive each event exactly as often as before.
- Rejected alternative: "tailer replaces synchronous dispatch." It would unify
  the paths but change hook-firing timing and SSE latency that tests *and the
  live /ui board* assert on. The /ui board's `EventSource`-driven re-render is
  the canary — not worth risking for a unification with no user benefit.

---

## 5. Decision 4 — Liveness ownership

`MonitorScheduler` must keep a temporal type armed while a durable `Consumer` is
interested, even when that consumer is disconnected-but-behind (its cursor has
not caught up). Today interest = `HasSubscriberFor || IsPersisted`.

**Decision:** durable interest is computed in **one place** — a
`ConsumerRegistry` that owns the set of live `Consumer` filters — and folded into
the scheduler's interest predicate as a third term:
`HasSubscriberFor(t) || IsPersisted(t) || registry.HasConsumerFor(t)`. The
registry is the single owner of "is any durable consumer interested in `t`," so
liveness has exactly one source of truth (mirroring how the bus owns ephemeral
interest). A `Consumer` for a temporal type is, by construction, a durable
interest — so registering one implies the type is persisted (Decision 3's
reject-at-registration guarantees the type is durable before a consumer exists).

---

## 6. Entity model

```
Consumer {
  id            string          // stable, client-facing
  filter        EventFilter     // types / board / actor / owner (durable types only)
  cursor        int64           // last_delivered_id (events.id high-water mark)
  kind          "pull" | "webhook"
  endpoint      string?         // webhook URL (kind=webhook)
  secret        string?         // HMAC key (kind=webhook), never returned after create
  created_at    timestamp
  state         "active" | "paused" | "dead"
}

DeliveryAttempt {           // per (consumer_id, event_id) for kind=webhook
  consumer_id   string
  event_id      int64
  attempts      int
  last_status   int?          // HTTP status of last try
  last_error    string?
  next_at       timestamp?    // backoff schedule (fake-clock testable)
  dead_lettered bool
}
```

- **Cursor advances only past acknowledged delivery.** For `pull`, the client
  acks by fetching with a cursor floor (like the existing feed). For `webhook`,
  the cursor advances past an event only after a `2xx` or after it is
  dead-lettered — so a crash mid-dispatch re-delivers (at-least-once), never
  drops.
- **At-least-once, dedupe by `event.id`.** Documented explicitly: consumers must
  be idempotent keyed on `event.id`. This is the same guarantee SSE replay + the
  feed already give; the tailer extends it to push.

---

## 7. Migrations

New tables: `consumers`, `delivery_attempts` (webhook). Both are additive
`CREATE TABLE IF NOT EXISTS` — no rebuild of `events`. **Ordering:** they must be
created **after** `migrateEventsScope` runs (which rebuilds `events`), following
the existing idempotent-DDL pattern, so a future events-table migration never
drops consumer state. Cursor is a column on `consumers`, not a separate table, so
advancing it is a single-row update in the same tx as the delivery record.

---

## 8. Webhook delivery kind + `/v1/subscribers`

- **Extension kind.** Add `webhook` alongside `hook`/`service`/`run`, delivered
  by a tailer `Consumer` of `kind=webhook`: at-least-once with retry/backoff
  (fake-clock tested), HMAC-signed body, per-subscriber dead-letter. Reuses the
  Phase-3 graceful-drain convention for its worker and the Phase-2-hardened log.
- **Surface.** `/v1/subscribers` (create/list/status/delete) reusing
  `withActor`/`idempotent` middleware, exposing cursor + delivery health
  (attempts, last error, dead-letter). JSON-only in the first cut; a `/ui`
  health view mirroring the breaches page is an accepted follow-up, not a blocker.
- **SSRF is an enforced control, not an assumption.** Runtime registration of an
  arbitrary outbound URL is the first runtime-mutable, extension-like entity in
  an otherwise file-declared model. It ships **only** with an enforced
  default-deny **egress allowlist** (config-declared host/CIDR), validated at
  **both** registration and **dispatch** time (a dispatch-time re-check blocks
  DNS-rebinding). If the allowlist can't land, runtime registration is withheld
  and webhook subscribers stay file-declared like every other extension. A
  documented "trusted network" assumption is **not** an acceptable substitute.

---

## 9. Testability

Retry/backoff and cursor timing are tested with `internal/core/clocktest.Fake`
(`Advance` drives backoff deterministically) — no real sleeps, no flakiness.
Required regression coverage before any of this is called done:

- At-least-once across restart: a `Consumer` with a stored cursor, server
  restart, reconnect → every missed event in `id` order, at-least-once.
- Dedupe honesty: a `kill -9` mid-dispatch produces a **duplicate** delivery
  segment that a consumer dedupes by `event.id` — asserted by test, not a doc
  footnote.
- No double-delivery to the existing bus: pre-existing `Bus`/SSE subscribers
  receive each event exactly as often as before the tailer existed.
- Liveness: a temporal type stays armed for a disconnected-but-behind `Consumer`;
  temporal and SSE timing tests pass; the /ui board `EventSource` re-render stays
  correctly ordered under the async tailer.

---

## 10. Hooks migration (deferred fork)

The hook `Supervisor` is currently an ephemeral bus subscriber (at-most-once, no
replay). Migrating it onto durable `Consumer` cursors would give hooks replay,
but changes their delivery contract. **Decision:** do **not** migrate hooks in
this design. `hook` stays at-most-once bus-driven; `webhook` is the new
at-least-once durable kind. Revisit only if a concrete need for replayable hooks
appears — it is a separate RFC, not a rider on this one.

---

## 11. Delivery taxonomy (to complete in index.md §9)

The contract table an integrator builds against:

| Surface | Semantics | Replay | Dedupe key |
|---|---|---|---|
| Shell `hook` | at-most-once | none | n/a |
| SSE / feed (pull) | at-least-once (client cursor) | `Last-Event-ID` / `since` | `event.id` |
| `webhook` (push, new) | at-least-once | tailer cursor | `event.id` |

Also reconcile index.md §8.6: escalated-condition restart duplication compounds
with webhook retries — integrators dedupe by `event.id`; point at the dedupe
regression test (§9).

---

## 12. Non-goals

- Exactly-once delivery at any transport boundary (achieved via durable cursor +
  idempotent consumer, never claimed as exactly-once).
- Auto-escalating ephemeral condition types at registration (specified-only
  future extension; reject-at-registration ships).
- Migrating hooks to durable cursors (§10).
- In-core reaction to conditions — conditions only ever emit; the tailer delivers
  *outward*, reaction stays in the external consumer. Reviewers reject any PR
  where a condition performs an in-core write.
- A cross-process broker / projection system — this is delivery, not a bus.

---

## 13. Build order (when un-gated)

1. `index.md` per-type durability column + §9 taxonomy (paper, no code).
2. `Consumer` entity + `consumers` table + `ConsumerRegistry` + liveness fold-in.
3. Tailer (cursor read, additive path, no republish) + at-least-once/restart and
   no-double-delivery tests.
4. `webhook` kind + retry/backoff/HMAC/dead-letter (fake-clock tests).
5. `/v1/subscribers` + enforced egress allowlist (registration + dispatch).
6. `/ui` subscriber health view (follow-up).

Steps 2–6 are **gated** on a real go signal; step 1 can land anytime as
documentation.
