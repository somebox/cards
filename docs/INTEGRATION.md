# Integration & Events

How other apps, agents, and scripts integrate with Cards: observe changes,
dispatch and complete work, coordinate multi-step flows, and react to
time/threshold conditions. This is the "API first, the UI is one view" contract.

Status legend: **[built]** exists today · **[proposed]** designed here, not yet
implemented. See [`SPEC.md`](SPEC.md) for the normative contract of built
features and [`ARCHITECTURE.md`](ARCHITECTURE.md) for the runtime.

## Quickstart (Node) **[built]**

Cards emits an **event** on every state change, on a single stream, so your app
reacts to work instead of polling for it. There are three ways in.

**1. Catch up — the feed.** A cursor-paged log of what happened; use it on
startup to replay what you missed.

```js
const res = await fetch(`http://127.0.0.1:8787/v1/events?board_id=engineering&since=${lastSeen}`);
const { events } = await res.json();        // each: { id, type, actor, at, card_id, diff }
```

**2. React live — the SSE stream.** A long-lived connection, filtered by card,
board, or type. `?card_id=` is "watch this card"; `?types=status_changed` is
"watch transitions".

```js
import { EventSource } from "eventsource"; // or browser-native

const es = new EventSource(
  "http://127.0.0.1:8787/v1/events/stream?board_id=engineering&types=status_changed,comment_added"
);
es.onmessage = (e) => {
  const evt = JSON.parse(e.data);
  if (evt.type === "status_changed" && evt.diff.after === "review") runReviewBot(evt.card_id);
};
```

The stream is **resumable**: it sets `Last-Event-ID` and `EventSource` sends it
back on reconnect, so a dropped connection replays the gap rather than losing it.

**3. The worker loop.** Events pair with the claim API to pull work, do it, and
write results back:

```js
es.addEventListener("message", async (e) => {
  if (JSON.parse(e.data).type !== "card_created") return;
  const card = await claimNext("programming-task", "in_progress"); // POST /v1/cards/take-next
  if (!card) return;
  await doWork(card);
  await comment(card.id, "done ✓");                                 // POST /v1/cards/{id}/comments
  await patch(card.id, { status: "review", version: card.version }); // PATCH /v1/cards/{id}
});
```

Mutation events (above) exist today; **condition events** (`status_timeout`,
`wip_exceeded`, …) arrive on the *same* stream once implemented, so this consumer
code does not change. The rest of this document is the full contract.

## Three planes

- **Observe** — learn what changed: the event stream (live), the events feed
  (catch-up/audit), and webhooks (push).
- **Act** — change state: claim work, attach results (comments / work-log /
  files), advance status, upgrade schema.
- **Coordinate** — compose: transition graphs, dependency links, and a class of
  **condition events** (timeouts, WIP, empty lanes) that turn implicit state
  into signals.

A guiding line from [`PHILOSOPHY.md`](PHILOSOPHY.md): **Cards emits signals; the
integrator owns the response.** Cards is not a workflow engine — it never acts
on a condition. It tells you a card sat in `review` too long; *you* decide to
escalate. This keeps policy in your domain and mechanism in the core.

## The event model

Every event has `id`, `type`, `actor`, `at`, `diff`, and a **scope**. Today
events are card-scoped (`card_id` always set); this proposal adds board-scoped
events for board-level conditions.

There are two **origins** of events:

### Mutation events — emitted on a write **[built]**

A direct, synchronous consequence of an API call. Always card-scoped.

| Event | Fires when |
|---|---|
| `card_created` | a card is created |
| `status_changed` | status moves |
| `field_updated` | a scalar field changes |
| `owner_changed` | owner set/cleared |
| `tags_changed` | tags added/removed |
| `item_appended` / `item_updated` / `item_removed` | a repeating-field entry changes |
| `link_added` / `link_removed` | a link changes |
| `comment_added` / `comment_edited` | a comment changes |
| `artifact_added` | a file/artifact is attached |
| `schema_upgraded` | a card is re-pinned to a new schema version |
| `definition_reloaded` | workspace definitions reload (workspace-scoped) |

### Condition events — emitted when a declared threshold crosses **[proposed]**

Not tied to a single write. Declared as **monitors** (below) and emitted by the
core's evaluator. Two trigger kinds:

**Instant** (evaluated synchronously right after the mutation that could trip them):

| Event | Scope | Fires when |
|---|---|---|
| `wip_exceeded` / `wip_cleared` | board | a column's card count rises above / falls back under its WIP limit |
| `lane_drained` / `lane_refilled` | board | a watched column's matching count hits 0 / recovers |
| `card_blocked` / `card_unblocked` | card | a card's open-blocker set becomes non-empty / empties |
| `transition_rejected` | card | an enforced transition is attempted and refused (opt-in) |

**Temporal** (nothing mutates at the deadline — see the deadline scheduler below):

| Event | Scope | Fires when |
|---|---|---|
| `status_timeout` | card | a card has been in a status longer than its declared max |
| `card_idle` | card | no event on a card for longer than the idle threshold |

Condition events flow onto the **same** bus as mutation events, so every
consumer below receives them identically. Unlike mutation events they are
**ephemeral and derived** — see [Ephemeral signals](#condition-events-are-ephemeral).

## Monitors — declaring conditions **[proposed]**

Monitors are data, not code (schema-is-the-process). Declared per board (or
workspace defaults), they tell the core which condition events to emit:

```jsonc
// definitions/boards/engineering.json
"monitors": {
  "wip_limit":          { "in_progress": 5, "review": 3 },
  "max_time_in_status": { "in_progress": "8h", "review": "2d" },
  "idle_after":         "3d",
  "alert_when_empty":   ["todo"],
  "emit_rejections":    true
}
```

Durations use Go-style strings (`"8h"`, `"2d"` → normalized). The core emits the
matching event **once per crossing** (idempotent): it tracks the last-emitted
state per `(board, column, condition)` and per `(card, status-entry)` so a
condition that stays tripped does not re-fire. The inverse event
(`wip_cleared`, `lane_refilled`, `card_unblocked`) fires when it recovers.

The core only emits — it does not promote cards, reassign owners, or move
status in response. That is the integrator's job (see Coordinate).

### A temporal event, step by step

`max_time_in_status: { review: "8h" }`. A card enters `review` at `T` (a
`status_changed` event with `at=T`); its deadline is `T + 8h`, **computed, not
discovered**. At the deadline the core re-checks the card is still in `review`
and, if so, emits `status_timeout` once. Two pieces of state make this exact:
`status_since` (when the card entered its current status — a denormalized
column, since `updated_at` moves on any edit) and a fired-marker keyed by
`(card, status, status_since)` so re-entering `review` arms a fresh deadline.
If the card moves out of `review` first, the deadline is simply discarded.

### The deadline scheduler (no fixed tick)

There is no polling interval. Pending deadlines live in a **min-heap ordered by
fire time**, and a single timer is set to the *earliest* one:

- on `status_changed` (consumed from the bus): cancel the card's old deadline,
  push the new one, reset the timer if the head changed;
- on wake: pop everything now-due, re-verify + emit, reset the timer to the new head;
- **empty heap ⇒ no timer at all** — zero wakeups when nothing is pending.

Resolution is automatic: the scheduler always sleeps until the nearest real
deadline. The heap is **reconstructible from state** (query cards in monitored
statuses, compute `status_since + max`, skip fired), so a restart or
`definition_reloaded` just rebuilds it — nothing to persist. One safety net: a
max-sleep cap (~1h) so a dropped bus event can't strand a deadline. Instant
conditions never touch the heap; they evaluate synchronously on the mutation
that could trip them.

### Lazy: monitors run only while someone is listening

Because condition events are ephemeral and derived, a monitor's deadlines are
scheduled **only while it has a live consumer** — an SSE subscriber whose
`types` filter includes the event, a declared hook/webhook on that type, or an
explicit `persist: true`. When the last consumer for `status_timeout`
disconnects, those deadlines are dropped from the heap and the core stops
computing them; when a consumer re-subscribes, the relevant deadlines are
rebuilt from current state. A signal that would have fired while nobody was
listening was, by definition, for nobody — and catch-up does not rely on replay
(see below). So cancellation has two clean triggers: the condition no longer
holds, or no one is left to tell.

### Condition events are ephemeral

Mutation events are **facts**: persisted, replayable via `Last-Event-ID`, always
emitted. Condition events are a **derived view** over state: by default not
persisted, computed only for whoever is watching. This is what makes the lazy
scheduler above safe — there is no stored stream to fall behind on.

Catch-up therefore splits in two: replay missed *facts* from the feed, and ask
for *current* conditions via the [breaches query](#current-breaches-catch-up-for-conditions).
A breach is itself derivable from the facts — the feed shows a card entered
`review` at `T` and is still there, so "it's 9h overdue" is computable; the
condition event is just a convenience signal on top. A monitor may set
`persist: true` to also record its events in the feed (audit/history), at which
point they replay like mutation events.

## Observe

### Live stream (SSE) **[built]**, filters extended **[proposed]**
```
GET /v1/events/stream?card_id=&board_id=&types=&actor=&owner=
```
Resumable via `Last-Event-ID` / `since=`. `card_id`/`board_id`/`types` are
built; `actor=` (events a user caused) and `owner=` (events on a user's cards)
are proposed — they make "watch this card", "follow @alice", and "follow my
cards" the same primitive.

### Catch-up feed **[proposed]**
```
GET /v1/events?actor=&owner=&type=&board_id=&since=&cursor=
```
A cursor-paged query over the persisted events table (which already stores
`actor`) for audit and "what did I miss while disconnected". The feed is a log
of **facts** — mutation events only (plus any monitor marked `persist: true`).

### Current breaches (catch-up for conditions) **[proposed]**
```
GET /v1/breaches?board_id=&type=         (or GET /v1/cards?breaching=status_timeout)
```
Condition events are [ephemeral](#condition-events-are-ephemeral), so you don't replay missed ones —
you ask the current truth. This computes, on demand, which cards *currently*
violate which monitors by evaluating thresholds against live state. It's the
catch-up path for conditions and doubles as a dashboard's "needs attention"
panel. A reconnecting integrator does two things: replay missed mutations via
the feed (`Last-Event-ID`), then query current breaches.

### Webhooks **[proposed]**
A `webhook` extension kind: the core POSTs each matching event to an external
URL with retry + HMAC signature + cursor replay. For integrators that cannot
hold an SSE connection. (Today a `hook` extension can shell out to `curl`; a
first-class webhook adds delivery guarantees.)

### Per-card watch **[built]**
`GET /v1/cards/{id}/events` and `/history` for replay/poll of one card.

## Act

- **Claim work** — `POST /v1/cards/take-next` (oldest unowned matching),
  `claim`, `release`. **[built]**
- **Attach results** — `POST /v1/cards/{id}/comments` (text), append to a
  `work_log` repeating field (structured progress) **[built]**; and
  `POST /v1/cards/{id}/artifacts` to attach a file/log/report (multipart or
  `{uri,mime,size,sha256}`), stored under workspace `artifacts/`, emitting
  `artifact_added` **[proposed]**.
- **Advance** — `PATCH /v1/cards/{id}` (status/fields, optimistic concurrency),
  `upgrade-schema`. **[built]**

A worker's loop: `take-next` → do work → attach a comment + artifact →
`patch` status to `review`/`done`.

## Coordinate

### Multi-step flows
Two patterns, both built on existing primitives:
1. **One card, staged statuses** — a board transition graph
   (`todo → in_progress → review → done`) with a hook per stage that dispatches
   the next step.
2. **Linked-card DAG** — `depends-on` / `blocked-by` links between cards; the
   `blocked` query hides a card until its blockers are `done`. The proposed
   `card_unblocked` event turns "a step became ready" into a push signal so a
   coordinator reacts instead of polling.

### Reprioritization when the ready lane empties
`take-next` returning empty is the pull signal today. Add **[proposed]**:
- a `priority`/`rank` field with `take-next`/`list` ordering by it, so
  reprioritizing is "set priority" and is honored deterministically; and
- the `lane_drained` event on the ready column as a push signal.

An extension subscribes to `lane_drained`, then promotes the next backlog card
by priority — the *policy* (which card, when) lives in the extension, the
*signal* and *ordering* in the core.

## Runtime notes (architecture)

- **Event scope.** To carry board-level condition events, the event model gains
  a `scope` (`card` | `board`); `card_id` becomes nullable and a `board_id` is
  recorded for board-scoped events. Card-scoped events are unchanged.
- **The evaluator.** Instant conditions are evaluated in the service
  immediately after the triggering mutation commits. Temporal conditions run in
  a **monitor evaluator** goroutine — a sibling to the hook supervisor already
  spawned in `serve` — driven by a deadline min-heap, not a fixed tick: it sleeps
  until the earliest pending deadline, wakes, re-verifies and emits, and sleeps
  again (no wakeups when the heap is empty). Deadlines are scheduled only while a
  monitor has a live consumer and are reconstructible from state, so nothing is
  persisted. It holds no policy; it only emits. See the deadline scheduler and
  lazy-monitor sections above.
- **Delivery.** All events (both origins, both scopes) publish to the in-process
  bus and the persisted events table, so SSE, the feed, webhooks, and hooks see
  a single unified stream.

## Build order

1. Actor/owner stream filters + `GET /v1/events` feed (observe: watch/follow).
2. Board-scoped event model (`scope`, nullable `card_id`, `board_id`).
3. `status_since` denormalized column (arming temporal deadlines).
4. Monitors + instant condition events (WIP, empty lane, blocked) — synchronous.
5. Deadline-heap evaluator + temporal events (time-in-status, idle), lazy/refcounted.
6. `GET /v1/breaches` (current-conditions catch-up for the ephemeral signals).
7. `transition_rejected` (watch friction).
8. Artifact upload (attach files).
9. `card_ready`/`card_unblocked` (DAG coordination).
10. Priority/rank + reprioritize-on-`lane_drained`.
11. Webhooks (push delivery).
