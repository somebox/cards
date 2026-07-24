# Cards — Integrator Reference

A single-page, **code-verified** reference for building on the cards service:
the data model, the HTTP API, the MCP surface, events, the actor model, the
git-defined workspace, extensions, and the boundary of what cards deliberately
does *not* do.

This document is written from the source (`internal/...`, `cmd/cards/...`) and
the real example workspace (`examples/demo-workspace/`), not from prose — where
an older narrative doc drifts from the code, the drift is flagged inline. For
the normative contract see [`spec/index.md`](../spec/index.md); for the
events/integration design see
[`events/integration.md`](../events/integration.md); for runtime shape see
[`architecture/index.md`](../architecture/index.md); for the standing
code-verified audit see
[`reference/implementation-status.md`](./implementation-status.md).

**Status legend:** **[built]** exists in code today · **[proposed]** designed,
not yet implemented · **[drift]** documented elsewhere but *not* in the code.

---

## 1. Data model

The unit is a **card**: a fixed envelope managed by the runtime plus
schema-validated custom `fields`. Cards live in **one workspace**; **boards are
filtered views**, not containers (a card has no `board_id`).

### The card object — `internal/core/types.go` (`type Card`)

```go
type Card struct {
	ID            string    `json:"id"`             // "card_<hex>"
	WorkspaceID   string    `json:"workspace_id"`
	TypeID        string    `json:"type_id"`        // the discriminator (see note)
	SchemaVersion int       `json:"schema_version"` // pinned at create
	Title         string    `json:"title"`
	Status        string    `json:"status"`         // a workspace column id
	Fields        any       `json:"fields"`         // map[string]any at runtime
	Owner         string    `json:"owner,omitempty"`
	Tags          []string  `json:"tags,omitempty"`
	Links         []Link    `json:"links,omitempty"`
	Comments      []Comment `json:"comments,omitempty"`
	Version       int       `json:"version"`        // optimistic-concurrency token
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CreatedBy     string    `json:"created_by"`
	StatusSince   time.Time `json:"status_since,omitempty"` // server-maintained; arms temporal monitors
}
```

- **The type discriminator is `type_id`, NOT `card_kind`/`kind`.** Each "kind" of
  card is a full **card type** (a versioned schema with its own fields), declared
  as `definitions/card-types/<id>.json`. `Kind` exists only on `User`
  (`human`|`agent`). → *picraft note: a `card_kind` enum maps to cards'
  `type_id`; but unlike a bare enum, each value is a distinct schema. One board +
  several `type_id`s is the supported shape.*
- **No `board_id` on a card.** Board membership is **derived**: a card shows on a
  board when its `type_id` ∈ the board's `card_type_ids` and it matches the
  board scope. There is no "move card to board" operation.
- **`owner` is a validated user reference** (a `string`, but must be a registered
  user — see §5), not a free string or UUID. Empty = unowned.
- **`version`** is an int, starts at `1`, and increments by 1 on every persisted
  mutation. A no-op patch does not bump it.
- **`status_since`** is server-maintained (never client-writable): set at
  creation and on every status change, and it arms the temporal monitors
  (`status_timeout`/`card_idle`, §4).

### `owner` semantics

- Set via `PATCH` (`owner` field), `claim`, or `take-next`. `claim`/`take-next`
  set **only** `owner` (+ optional `status`) — they never touch custom `fields`.
  → *picraft note: "claiming worker in `owner`, body in a custom field" holds
  — claim leaves your custom fields untouched.*
- Setting `owner` via PATCH or `claim` requires a registered user (`unknown_user`
  422 otherwise). `take-next` currently skips that lookup for `assign_to`/actor
  ownership — see §5 and backlog card `card_1c877e6ca3e04a24bdd3d2ff90286a84`.
- `claim` is compare-and-set on `version`; claiming a card already owned by a
  *different* actor → `409 version_conflict`. `release` sets owner back to `""`.
- **You CANNOT set `owner` at creation** — `CreateCardRequest` has no owner field;
  owner is only set later via patch/claim/take-next.

### Boards, columns, statuses — `internal/core/types.go` (`type Board`)

```go
type Board struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Description   string              `json:"description,omitempty"`
	Columns       []string            `json:"columns"`        // subset of workspace columns
	CardTypeIDs   []string            `json:"card_type_ids,omitempty"`
	DefaultFilter map[string]any      `json:"default_filter,omitempty"` // hard scope, AND-ed in
	Transitions   map[string][]string `json:"transitions,omitempty"`    // from -> [allowed to]
	WIPLimits     map[string]int      `json:"wip_limits,omitempty"`     // column id -> max
	Monitors      *BoardMonitors      `json:"monitors,omitempty"`       // condition watchers
	Presentation  *BoardPresentation  `json:"presentation,omitempty"`   // UI hints + named filters
	// ... Theme, Settings{EnforceTransitions}
}
```

- **Statuses are workspace-global columns.** `Workspace.Columns` is the ordered
  canonical lane set; a card's `status` is a column id. A board's `Columns` is a
  *subset reference* into those.
- A board adds: the **card types** it shows, a **`default_filter`** (a hard scope
  AND-ed into every query for that board — see the filter DSL in §2), optional
  **`transitions`** (enforced only when `settings.enforce_transitions` is true and
  the write isn't `force:true`), **`wip_limits`**/**`monitors`** (fire the
  condition events in §4), and **`presentation`** (lane grouping, card previews,
  and *named* optional `filters[]` chips — distinct from the hard
  `default_filter`).

### Links — `internal/core/types.go` (`type Link`)

```go
type Link struct {
	TypeID    string    `json:"type_id"`   // e.g. "depends-on", "blocked-by", "related"
	Target    string    `json:"target"`    // target card id
	Note      string    `json:"note,omitempty"` // free-text metadata
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}
```

- **Links are stored on the SOURCE card** (the one in the request path). For
  `depends-on`/`blocked-by`, the **waiting/blocked card holds the link**, pointing
  outward at what it depends on.
- **`?blocked=true`** returns cards that have a `blocked-by` or `depends-on` link
  whose **target is not yet `done`**. When every dependency target reaches `done`,
  the card drops out of the blocked set. (The `card_unblocked` push event for this
  is **[built]**, see §4.)
- Link types are workspace vocabulary (`LinkType`: `directional`|`bidirectional`,
  optional `source_types`/`target_types` constraints). Adding the same
  `(type_id, target)` twice is idempotent.

### Custom fields — `internal/core/types.go` (`FieldDef`)

Ten field types: `string`, `text`, `number`, `date`, `enum`, `tags`, `user`,
`card_link`, `repeating`, `artifact`. A `FieldDef` carries `id`, `type`,
`required`, `default`, `options` (enum), `multiple` (enum/user — see below),
`min`/`max` (number/date), `target_type`/`link_type` (card_link),
`item_fields` (repeating), `artifact_policy`, and a UI `display` hint
(`feed|badge|hidden|link|monospace`).

Validation: `required` enforced at create; `enum` values checked against
`options`; `card_link` targets must exist and match `target_type`; **`repeating`
fields are NOT patchable via `PATCH`** — use the append/update/remove API (§2).
With workspace `strict_fields: true`, unknown field keys are rejected
(`unknown_field`); with it false, they pass through and are stored.

**Multi-value fields [built]** — `"multiple": true` on `enum`/`user` (rejected on
other types and inside `item_fields` at definition load): the value is always a
JSON **array of strings**; an unset optional multiple field is **absent** from
`fields` (never `null`, never `[]`). Duplicates and out-of-set elements are
rejected with structured errors. Filter by membership with **`$has`**
(`internal/sqlite/filter.go`, `json_each`; scalar fields degrade to equality).
CLI: pass a JSON array — `--field 'platforms=["desktop","mobile"]'`. `card_link`
multiple is a documented fast-follow, not built.

---

## 2. HTTP API

Base path `/v1`. Server binds `127.0.0.1` by default; **no built-in auth**
(see §5). Routes are registered in `internal/httpapi/server.go` (`Router()`);
`POST /v1/workspace/reload` and `POST /v1/boards` are wired in
`cmd/cards/reload.go` on the reloadable app wrapper (see §6).

### Endpoint table

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/health` | `{version, workspace_id}` |
| GET | `/v1/workspace` | introspection snapshot (types, boards, columns, versions) |
| GET | `/v1/boards/{id}` | one board's definition (404 for unknown ids) |
| GET | `/v1/breaches` | current breaching conditions (`?board_id=&type=`) — §4 |
| GET | `/v1/cards` | list / filter (cursor-paged) |
| POST | `/v1/cards` | create |
| GET | `/v1/cards/{id}` | fetch one (with links + comments) |
| PATCH | `/v1/cards/{id}` | mutate (optimistic concurrency) |
| DELETE | `/v1/cards/{id}` | delete (tombstoned; `?version=N`) |
| POST | `/v1/cards/{id}/upgrade-schema` | re-pin one card to current schema |
| POST | `/v1/cards/take-next` | atomically claim oldest unowned match |
| POST | `/v1/cards/{id}/claim` | claim a specific card |
| POST | `/v1/cards/{id}/release` | unclaim (owner → "") |
| POST | `/v1/cards/{id}/links` | add link (201) |
| DELETE | `/v1/cards/{id}/links/{typeID}/{target}` | remove link |
| POST | `/v1/cards/{id}/comments` | add comment (201) |
| PATCH | `/v1/cards/{id}/comments/{commentID}` | edit comment |
| POST | `/v1/cards/{id}/artifacts/{field}` | upload artifact bytes to a field (201) |
| GET | `/v1/artifacts/*` | fetch stored artifact bytes (path-confined) |
| POST | `/v1/cards/{id}/fields/{field}/append` | append a repeating-field entry |
| PATCH | `/v1/cards/{id}/fields/{field}/{entryID}` | update a repeating entry |
| DELETE | `/v1/cards/{id}/fields/{field}/{entryID}` | remove a repeating entry (`?version=N`) |
| GET | `/v1/cards/{id}/events` | one card's events |
| GET | `/v1/cards/{id}/history` | rendered timeline (resumption) |
| GET | `/v1/events` | **catch-up feed** (cursor-paged, durable) — §4 |
| GET | `/v1/events/stream` | **SSE live stream** — §4 |
| GET | `/v1/openapi.json` | generated OpenAPI 3.1 |
| POST | `/v1/users` | register a user (open, no auth) |
| POST | `/v1/workspace/reload` | re-read `definitions/` into a new generation — §6 |
| POST | `/v1/boards` | create a board (write-then-reload) — §6 |

UI handlers live under `/ui` (reference consumer; not part of the contract).
**There are NO batch/bulk endpoints** — writes are strictly per-card;
`take-next` claims exactly one card.

### Key request/response shapes

**`POST /v1/cards`** — `{type_id*, title*, status?, fields?, tags?, schema_version?, dry_run?}`.
`type_id` and `title` required; `status` defaults to the type's first
`allowed_columns` (or first workspace column). **You CANNOT set `owner` at
creation.** Returns the full card, `201` (or `200` + header `Dry-Run: true` when
`dry_run`).

**`PATCH /v1/cards/{id}`** — `{version*, title?, status?, owner?, tags?, fields?, force?, dry_run?}`.
Mutable: title, status, owner, tags, scalar fields. `version` must equal the
current version. `status` is checked against board `transitions` when the board
enforces them, unless `force:true`. Repeating fields are **not** patchable here.

**Optimistic-concurrency conflict (`409`)** — the body is the standard error
envelope *with the current card attached* so you can re-read and retry:

```json
{ "error": "version_conflict",
  "message": "Stale version; another mutation has occurred.",
  "card": { "...": "full current Card" } }
```

The error envelope is `{error, message, field?, value?, valid_options?, hint?, card?}`
across all 4xx (e.g. `transition_illegal` includes `valid_options`).

**`POST /v1/cards/take-next`** — `{assign_to?, status?, type_id?, board_id?, filter?}`.
Claims the **oldest unowned matching card**, optionally moving it to `status`,
assigning to `assign_to` (else the actor). On no match → `200 { "card": null }`.
On a match → `200 { "card": {...} }`.

> **No-double-claim guarantee [built, tested].** `claim`/`take-next` run in a
> single `BEGIN IMMEDIATE` transaction with a guard that only claims an unowned
> row, on a single writer connection. Under *N* concurrent callers exactly one
> wins; a card can never be handed to two owners
> (`TestClaimAtomicNoDoubleClaim`). **Current limitation:** a racing *loser* on
> `take-next` receives `{ card: null }` (it can't yet distinguish "raced" from
> "queue empty") — under contention, a caller that got `null` should re-issue.

**`GET /v1/workspace`** — `{workspace, card_types, boards, current_schema_versions}`,
where `workspace` carries `{id, name, columns, tag_set, link_types, users, settings}`.

### List filters — `GET /v1/cards`

Query params actually read by the handler (`internal/httpapi/api.go`,
`apiListCards`): `board_id`, `type_id` (single **or** comma-separated → IN),
`status` (single **or** comma-separated → IN), `owner`, `q` (full-text; also
matches id/short-id), `blocked` (`=true`), `has_link`, `link_target`, `sort`,
`include`, `cursor`, `limit`. Response is
`{ items: [...], next_cursor: "<opaque>" }`.

> **Note:** the Mongo-style **filter DSL** (`$and/$or/$eq/$ne/$in/$nin/$gt/$gte/$lt/$lte/$contains/$has`
> + tag ops) is **not** exposed as free-form on the `GET /v1/cards` query string.
> The DSL is consumed from a board's `default_filter` and from the `take-next`
> request `filter`. "Give me unowned cards of a kind" is reachable via
> `take-next`; `type_id`/`status` CSV covers the common list-scoping cases.

### Actor on writes

Resolution order (`internal/httpapi/middleware.go`, `resolveActor`):
**`X-Work-Cards-Actor` header → `CARDS_USER` env (`envUser`) →
`workspace.settings.default_user`**; empty → `actor_required` error. A body
`actor` field is overwritten by the resolved identity (not a resolution source).
See §5.

---

## 3. MCP surface

Transport: **JSON-RPC 2.0 over stdio** (newline-delimited). Launch:
`cards mcp --workspace <dir>`. Methods: `initialize`,
`notifications/initialized`, `tools/list`, `tools/call`. The MCP adapter
(`internal/mcp/mcp.go`) delegates to the **same `core.Service`** as HTTP, so
validation, events, and the no-double-claim guarantee are identical.

### Tools — `internal/mcp/mcp.go` (`buildTools`)

**Per card type (generated):** `create_<type_id>` and `update_<type_id>` — input
schemas derived from the type's fields (`title`/`status`/`tags` + per-field
props; `update_*` requires `card_id` + `version`). There is **no** generic
`create`/`update`.

**Fixed generic tools:** `workspace`, `get_card`, `list_cards`
(`type_id/status/owner/board_id/q/blocked/limit/cursor`), `search_cards`
(`q/limit`), `claim`, `release`, `take_next`
(`type_id/board_id/assign_to/status/filter`), `append_entry`, `update_entry`,
`remove_entry`, `add_link`, `remove_link`, `add_comment`, `edit_comment`,
`upgrade_schema` (dry-run by default; `confirm:true` applies), `attach_artifact`
(base64), `get_artifact`, `history`, `breaches`, and `events` (durable feed
replay with `since`/`types`/`board_id`).

### Actor binding

**Session-bound, no per-call override.** The actor is fixed at process start from
`CARDS_USER` (→ `default_user` fallback) and injected into every call's context
(`s.actor`). No MCP tool exposes an actor parameter, and there is **no
`X-Work-Cards-Actor`** path over MCP — that header is HTTP-only.

Streaming is **HTTP/SSE only** — there is no MCP subscribe tool. An MCP client
that needs live events polls the `events`/`history` tools or holds a separate
SSE connection to `/v1/events/stream`.

---

## 4. Events

Event shape on every channel: `{ id, type, actor, at, card_id, diff }`. `diff`
is `{ before, after }` for scalar changes (e.g. `status_changed`), with richer
shapes per type (`field_updated`: `{field, before, after}`; `item_updated`:
`{field, entry_id, before, after}`; `comment_*`: `{comment_id, ...}`;
`card_created`: `{card: {...}}`; `schema_upgraded`:
`{from, to, defaults_applied, fields_dropped}`; `link_added`:
`{type_id, target, note}`; `link_removed`: `{type_id, target}`). **It is
`diff.after`, never `diff.to`.**

### Mutation events [built] — `internal/core/types.go`

The durable card facts are `card_created`, `card_deleted`, `field_updated`,
`status_changed`, `owner_changed`, `tags_changed`, `item_appended`,
`item_updated`, `item_removed`, `link_added`, `link_removed`, `comment_added`,
`comment_edited`, `schema_upgraded`, `artifact_added` — synchronous on a write,
card-scoped, persisted, replayable. `artifact_added` emits from the artifact
upload path (`Service.AddArtifact`). `definition_reloaded` /
`definition_reload_failed` are emitted by the reload seam (§6, **[built]**).

### Condition events [built] — `internal/core/types.go`, `events/integration.md`

`wip_exceeded`/`wip_cleared`, `lane_drained`/`lane_refilled`,
`card_blocked`/`card_unblocked`, `transition_rejected` (opt-in), and the
temporal `status_timeout`/`card_idle`. Declared as board **monitors** (data,
not code — `board.monitors` + `wip_limits`), emitted by the core onto the
**same bus** as mutation events. Instant conditions evaluate synchronously on
the triggering mutation; temporal conditions run through a deadline scheduler
armed from `status_since`. By default they are **ephemeral** (SSE-only);
`settings.persist_conditions` escalates named types to the durable feed.

### `GET /v1/breaches` [built]

The on-demand "which conditions are currently true" query — WIP-exceeded
columns, drained lanes, blocked cards, and past-due temporal monitors
(`status_timeout`/`card_idle`). Scope with `?board_id=&type=`. Item scans cap at
500 — check `truncated`/`limit` before trusting an empty result.

### Three ways to consume [built]

1. **Catch-up feed** — `GET /v1/events?since=&cursor=&actor=&owner=&type=&types=&board_id=&limit=`
   → `{ items, next_cursor }`, ordered by id ASC. `since=`/`cursor=` are
   **event-id floors** (events with `id >` value); `cursor=` is the pagination
   continuation and overrides `since=`.
2. **Live SSE** — `GET /v1/events/stream?card_id=&board_id=&types=&actor=&owner=`,
   resumable via `Last-Event-ID` / `since=`. All five filters are built.
3. **Per-card** — `GET /v1/cards/{id}/events` and `/history`.

> **SSE retention / replay guarantee [built] — the load-bearing answer.** The
> persisted `events` table is **append-only and never trimmed**, so the **feed is
> a complete, gap-free durable log** replayable from *any* id, no matter how long
> a consumer was disconnected. The in-memory SSE buffer is **bounded and
> best-effort**: a slow consumer whose buffer fills is dropped with a
> `: dropped, reconnect` comment (it never blocks a writer). **Durable recovery
> therefore goes through the feed, not the stream:** page `GET /v1/events` from
> your last id until `next_cursor` is empty, then open the stream with
> `Last-Event-ID` set to that id. No event is lost between the two.
> *(`event_retention_days` exists in workspace settings as a future knob but is
> not enforced today — retention is currently unbounded.)*

---

## 5. Actor & identity model

- **An actor is any string.** It is recorded as `created_by` and event `actor`.
  It is **not validated** against the user registry for create/patch/comment/
  append — open, no auth. This is deliberate: spawn many short-lived workers,
  each with its own `CARDS_USER`, with no pre-registration.
- **Ownership is mostly registry-backed.** Setting `owner` via PATCH, or using
  `claim` (which makes the actor the owner), requires a registered user
  (`POST /v1/users {id, kind}`, open, no auth) or returns `unknown_user`.
  **`take-next` currently bypasses that user lookup** for `assign_to`/actor
  ownership — an implementation inconsistency, not an auth boundary. Workers that
  only create/comment need no registration; workers that PATCH/`claim` must register
  first.
- **Stable orchestrator vs ephemeral workers:** both are just actor strings. Use a
  fixed `CARDS_USER` (e.g. `orchestrator`) for dispatch-owned writes and a distinct
  one per worker. Workers that claim get registered; the orchestrator is registered
  if it ever owns cards. No rate limits; collision = same actor string = same
  identity (that's the only "auth").
- **Resolution:** HTTP uses `X-Work-Cards-Actor` header → `CARDS_USER` →
  `default_user`. MCP uses `CARDS_USER` → `default_user` (no header).

---

## 6. Workspace & schema (git-defined)

### `definitions/` layout

```
<workspace>/
  work-cards.db                 # the single SQLite file (state + events)
  definitions/                  # git-backed source of truth, loaded at startup
    workspace.json              # columns, tag_set, link_types, settings
    card-types/<id>.json        # one schema per card type (fields, allowed_columns)
    boards/<id>.json            # filtered views (card_type_ids, columns, transitions, ...)
    extensions.{json,yaml}      # optional: hook/service/run declarations
  .cards/
    ext/                        # extension scripts
    logs/                       # supervisor writes <ext>.log here
```

`workspace.json` settings include `enforce_transitions`, `strict_fields`,
`tag_policy`, `default_user`, `event_retention_days`, `persist_conditions`. All
cross-references (board columns/types/transitions, card-type `allowed_columns`,
field types) are validated **at load**; bad references fail startup.

### Single workspace per instance [confirmed, long-term]

**One process serves exactly one workspace** (one SQLite file). This is a
**locked, long-term contract** — not a v1 simplification. Multi-tenancy = run
multiple processes on different ports/paths. Intra-workspace isolation uses
**boards** (filtered views over a shared card pool, scoped by `card_type_ids`),
not multiple workspaces. → *picraft note: one workspace + one board + several
`type_id`s is exactly the intended shape; designing for multi-workspace is
unsupported.*

### Schema versioning & migration

- A card type declares `schema_version` (int); each card is **pinned** to the
  version it was created/upgraded against (`Card.schema_version`).
- **Existing cards are NOT auto-migrated.** They validate lazily against their
  pinned snapshot. The only way a card gains defaults / drops removed fields is
  an explicit **`POST /v1/cards/{id}/upgrade-schema`**, which re-pins one card
  forward (applies `migrations[N].field_defaults`, drops fields absent from the
  target schema, re-validates, emits `schema_upgraded`). It is one-card-at-a-time
  and refuses downgrades.

### Definition reload [built]

Reloading definitions is now implemented (P3a/P3b; `cmd/cards/reload.go`,
`cmd/cards/watch.go`):

- **`POST /v1/workspace/reload`** re-reads `definitions/` and swaps in a new
  `*core.Service` + router **generation**; the prior generation is `Close()`d.
- **`POST /v1/boards`** does a write-then-validate-via-reload with rollback.
- **`cards serve --watch`** polls `definitions/` and calls `reload()` on a stable
  fingerprint (debounced; self-write suppressed).
- **What survives a reload:** the SQLite store (card state untouched), the event
  bus (live SSE subscribers stay connected), and the hook supervisor process.
  **Semantically, reloading never mutates cards** — it only rebuilds in-memory
  config, emitting `definition_reloaded` (or `definition_reload_failed`). Hook
  *declarations* are frozen at supervisor construction (a documented follow-up);
  service declarations are reconciled after each successful swap. See
  [`architecture/reload.md`](../architecture/reload.md).

---

## 7. Extensions

The core **loads no extension code and executes nothing in-process**; extensions
are independent processes that talk to the API. Declared in
`definitions/extensions.{json,yaml}`:
`{id, kind, description?, on?, filter?, run, cwd?, env?, autostart?, expose?}`.

- **`hook` [built]** — reactive subprocess. `on: <event_type>` + optional
  `filter` (`board_id`, `type_id`, `card_id`, `to_status`, `from_status`). The
  supervisor (`cards run-extensions`, or `cards serve --run-extensions`)
  subscribes to the bus and, on a match, spawns `run` (argv array, no shell) with:
  the **event JSON on stdin**, env `CARDS_URL`/`CARDS_WORKSPACE`/`CARDS_USER`/
  `CARDS_EVENT_ID`/`CARDS_EVENT_TYPE`, and `cwd` = workspace root. It is
  **fire-and-forget and at-most-once** — async, never blocks or rolls back the
  write, a non-zero exit is logged not retried.

- **`run` [built]** — one-shot command invoked manually via `cards do <id>
  [--param k=v ...]`. Receives the `--param` flags as **argv** (not event JSON);
  synchronous; child stdout/stderr stream to the parent.

- **`service` [built]** — a long-running process supervised by the same
  supervisor (`internal/hooks/services.go`, `reconcile.go`): `autostart` services
  are started, restarted per `RestartPolicy` with bounded backoff, and
  SIGTERM→grace→SIGKILL'd on shutdown, and are **reconciled after each successful
  reload** (§6). The supervisor manages process lifecycle only — it does **not**
  feed events in-process; a service consumes the API + SSE like any other client.
  This is exactly picraft's persistent operator session shape. The core never
  loads a service in-process.

---

## 8. What cards deliberately does NOT provide

The boundary is **"cards emits signals; your app owns the response"** — the core
never acts on a condition. Out of scope, by design:

- **No lease / mutex / TTL / heartbeat / dead-owner reclaim.** The only
  coordination atomics are `claim` and `take-next`; both set `owner` once and
  **never expire or reclaim**. Model a lease as a card with an `expires_at` field
  and reclaim it yourself.
- **No scheduler / dispatcher / queue.** "Background processing, queues,
  schedulers" are explicitly extension-owned. `take-next` returning `null` is the
  pull signal; the pull *policy* is yours.
- **No dependency auto-promotion / epic rollups.** The `blocked` query and the
  built `card_unblocked` event are signals; promoting a ready card is your
  policy.
- **No in-core execution.** Store executable/structured content as
  `string`/`text`/`artifact` and let an extension validate. The core executes
  nothing in-process.
- **No multi-workspace router**, **no built-in auth** (localhost trust;
  reverse-proxy/auth is a host/extension concern), **no server-managed config
  editing beyond reload** (definitions are git-backed files; reload only re-reads
  them).

---

## Pointers into the cards docs

| Topic | Doc |
|---|---|
| Normative contract (data model, API, errors, events, atomics) | [`spec/index.md`](../spec/index.md) |
| Vocabulary + use-case setups (workspaces, boards, card types) | [`concepts/index.md`](../concepts/index.md) |
| Events & integration design (mutation vs condition, monitors, feed, breaches) | [`events/integration.md`](../events/integration.md) |
| Runtime shape, package boundaries, storage | [`architecture/index.md`](../architecture/index.md) |
| Definition reload contract | [`architecture/reload.md`](../architecture/reload.md) |
| MCP transport & tools | [`extensions/mcp.md`](../extensions/mcp.md) |
| Extension declaration format & worked examples | [`extensions/index.md`](../extensions/index.md) |
| Workspace authoring (definitions, schema versioning) | [`reference/card-definitions.md`](./card-definitions.md), [`reference/workspace-and-boards.md`](./workspace-and-boards.md) |
| Code-verified drift audit (built vs proposed) | [`reference/implementation-status.md`](./implementation-status.md) |
| Design rationale & principles | [`concepts/philosophy.md`](../concepts/philosophy.md) |

*Verified against the source at the time of writing (`internal/core`,
`internal/httpapi`, `internal/mcp`, `internal/hooks`, `internal/config`,
`cmd/cards`). Where this doc and an older narrative doc disagree, this doc (read
from code) wins — and the discrepancy is a bug to file against the narrative
doc.*
