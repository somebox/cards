# Integration & Events

How other apps, agents, and scripts integrate with Cards: observe changes,
dispatch and complete work, coordinate multi-step flows, and react to
time/threshold conditions. This is the "API first, the UI is one view" contract.

Status legend: **[built]** exists today · **[proposed]** designed here, not yet
implemented. See [`SPEC.md`](SPEC.md) for the normative contract of built
features and [`ARCHITECTURE.md`](ARCHITECTURE.md) for the runtime.

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

**Temporal** (require the periodic evaluator — nothing mutates at the deadline):

| Event | Scope | Fires when |
|---|---|---|
| `status_timeout` | card | a card has been in a status longer than its declared max |
| `card_idle` | card | no event on a card for longer than the idle threshold |

Condition events flow onto the **same** bus as mutation events, so every
consumer below receives them identically.

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
condition that stays tripped does not re-fire every tick. The inverse event
(`wip_cleared`, `lane_refilled`, `card_unblocked`) fires when it recovers.

The core only emits — it does not promote cards, reassign owners, or move
status in response. That is the integrator's job (see Coordinate).

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
`actor`) for audit and "what did I miss while disconnected".

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
  spawned in `serve` — ticking on a configurable interval (default ~60s),
  scanning for crossings and emitting once. It holds no policy; it only emits.
- **Delivery.** All events (both origins, both scopes) publish to the in-process
  bus and the persisted events table, so SSE, the feed, webhooks, and hooks see
  a single unified stream.

## Build order

1. Actor/owner stream filters + `GET /v1/events` feed (observe: watch/follow).
2. Board-scoped event model (`scope`, nullable `card_id`, `board_id`).
3. Monitors + condition events (WIP, time-in-status, empty lane, idle) + evaluator.
4. `transition_rejected` (watch friction).
5. Artifact upload (attach files).
6. `card_ready`/`card_unblocked` (DAG coordination).
7. Priority/rank + reprioritize-on-`lane_drained`.
8. Webhooks (push delivery).
