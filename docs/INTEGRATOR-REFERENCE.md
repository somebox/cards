# Cards — Integrator Reference

A single-page, **code-verified** reference for building on the cards service:
the data model, the HTTP API, the MCP surface, events, the actor model, the
git-defined workspace, extensions, and the boundary of what cards deliberately
does *not* do.

This document is written from the source (`internal/...`) and the real example
workspace (`examples/demo-workspace/`), not from prose — where the older
narrative docs drift from the code, the drift is flagged inline. For the
normative contract see [`SPEC.md`](SPEC.md); for the events/integration design
see [`INTEGRATION.md`](INTEGRATION.md); for runtime shape see
[`ARCHITECTURE.md`](ARCHITECTURE.md).

**Status legend:** **[built]** exists in code today · **[proposed]** designed,
not yet implemented · **[drift]** documented elsewhere but *not* in the code.

---

## 1. Data model

The unit is a **card**: a fixed envelope managed by the runtime plus
schema-validated custom `fields`. Cards live in **one workspace**; **boards are
filtered views**, not containers (a card has no `board_id`).

### The card object — `internal/core/types.go:187`

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
}
```

- **The type discriminator is `type_id`, NOT `card_kind`/`kind`.** Each "kind" of
  card is a full **card type** (a versioned schema with its own fields), declared
  as `definitions/card-types/<id>.json`. `Kind` exists only on `User`
  (`human`|`agent`). → *picraft note: D7's `card_kind` enum maps to cards'
  `type_id`; but unlike a bare enum, each value is a distinct schema. One board +
  several `type_id`s is the supported shape.*
- **No `board_id` on a card.** Board membership is **derived**: a card shows on a
  board when its `type_id` ∈ the board's `card_type_ids` and it matches the
  board scope. There is no "move card to board" operation.
- **`owner` is a validated user reference** (a `string`, but must be a registered
  user — see §5), not a free string or UUID. Empty = unowned.
- **`version`** is an int, starts at `1`, and increments by 1 on every persisted
  mutation. A no-op patch does not bump it.

### `owner` semantics

- Set via `PATCH` (`owner` field), `claim`, or `take-next`. `claim`/`take-next`
  set **only** `owner` (+ optional `status`) — they never touch custom `fields`.
  → *picraft note: D7's "claiming worker in `owner`, body in a custom field" holds
  — claim leaves your custom fields untouched.*
- A non-empty owner must be a registered user (`unknown_user` 422 otherwise).
- `claim` is compare-and-set on `version`; claiming a card already owned by a
  *different* actor → `409 version_conflict`. `release` sets owner back to `""`.

### Boards, columns, statuses — `internal/core/types.go:161`

```go
type Board struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Columns       []string            `json:"columns"`        // subset of workspace columns
	CardTypeIDs   []string            `json:"card_type_ids,omitempty"`
	DefaultFilter map[string]any      `json:"default_filter,omitempty"` // hard scope, AND-ed in
	Transitions   map[string][]string `json:"transitions,omitempty"`    // from -> [allowed to]
	Presentation  *BoardPresentation  `json:"presentation,omitempty"`   // UI hints + named filters
	Settings      struct {
		EnforceTransitions bool `json:"enforce_transitions"`
	} `json:"settings"`
}
```

- **Statuses are workspace-global columns.** `Workspace.Columns` is the ordered
  canonical lane set; a card's `status` is a column id. A board's `Columns` is a
  *subset reference* into those.
- A board adds: the **card types** it shows, a **`default_filter`** (a hard scope
  AND-ed into every query for that board — see the filter DSL in §2), optional
  **`transitions`** (enforced only when `settings.enforce_transitions` is true and
  the write isn't `force:true`), and **`presentation`** (lane grouping, card
  previews, and *named* optional `filters[]` chips — distinct from the hard
  `default_filter`). The demo `engineering.json` uses `presentation.filters[]`;
  `default_filter` is the top-level hard-scope key.

### Links — `internal/core/types.go:87`

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
  is **[proposed]**, see §4.)
- Link types are workspace vocabulary (`LinkType`: `directional`|`bidirectional`,
  optional `source_types`/`target_types` constraints). Adding the same
  `(type_id, target)` twice is idempotent.

### Custom fields — `internal/core/types.go:15` / `:29`

Ten field types: `string`, `text`, `number`, `date`, `enum`, `tags`, `user`,
`card_link`, `repeating`, `artifact`. A `FieldDef` carries `id`, `type`,
`required`, `default`, `options` (enum), `min`/`max` (number/date),
`target_type`/`link_type` (card_link), `item_fields` (repeating),
`artifact_policy`, and a UI `display` hint (`feed|badge|hidden|link|monospace`).

Validation: `required` enforced at create; `enum` values checked against
`options`; `card_link` targets must exist and match `target_type`; **`repeating`
fields are NOT patchable via `PATCH`** — use the append/update/remove API (§2).
With workspace `strict_fields: true`, unknown field keys are rejected
(`unknown_field`); with it false, they pass through and are stored.

---

## 2. HTTP API

Base path `/v1`. Server binds `127.0.0.1` by default; **no built-in auth**
(see §5). All routes registered in `internal/httpapi/httpapi.go:84`.

### Endpoint table

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/health` | `{version, workspace_id}` |
| GET | `/v1/workspace` | introspection snapshot (types, boards, columns, versions) |
| GET | `/v1/cards` | list / filter (cursor-paged) |
| POST | `/v1/cards` | create |
| GET | `/v1/cards/{id}` | fetch one (with links + comments) |
| PATCH | `/v1/cards/{id}` | mutate (optimistic concurrency) |
| POST | `/v1/cards/{id}/upgrade-schema` | re-pin one card to current schema |
| POST | `/v1/cards/take-next` | atomically claim oldest unowned match |
| POST | `/v1/cards/{id}/claim` | claim a specific card |
| POST | `/v1/cards/{id}/release` | unclaim (owner → "") |
| POST | `/v1/cards/{id}/links` | add link (201) |
| DELETE | `/v1/cards/{id}/links/{typeID}/{target}` | remove link |
| POST | `/v1/cards/{id}/comments` | add comment (201) |
| PATCH | `/v1/cards/{id}/comments/{commentID}` | edit comment |
| POST | `/v1/cards/{id}/fields/{field}/append` | append a repeating-field entry |
| PATCH | `/v1/cards/{id}/fields/{field}/{entryID}` | update a repeating entry |
| DELETE | `/v1/cards/{id}/fields/{field}/{entryID}` | remove a repeating entry (`?version=N`) |
| GET | `/v1/cards/{id}/events` | one card's events |
| GET | `/v1/cards/{id}/history` | rendered timeline (resumption) |
| GET | `/v1/events` | **catch-up feed** (cursor-paged, durable) — §4 |
| GET | `/v1/events/stream` | **SSE live stream** — §4 |
| GET | `/v1/openapi.json` | generated OpenAPI 3.1 |
| POST | `/v1/users` | register a user (open, no auth) |

UI handlers live under `/ui` (reference consumer; not part of the contract).
**There are NO batch/bulk endpoints** — writes are strictly per-card;
`take-next` claims exactly one card.

### Key request/response shapes

**`POST /v1/cards`** — `{type_id*, title*, status?, fields?, tags?, schema_version?, dry_run?}`.
`type_id` and `title` required; `status` defaults to the type's first
`allowed_columns` (or first workspace column). **You CANNOT set `owner` at
creation** — owner is only set later via patch/claim/take-next. Returns the full
card, `201` (or `200` + header `Dry-Run: true` when `dry_run`).

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

The error envelope (`internal/core/errors.go:14`) is
`{error, message, field?, value?, valid_options?, hint?, card?}` across all
4xx (e.g. `transition_illegal` includes `valid_options`).

**`POST /v1/cards/take-next`** — `{assign_to?, status?, type_id?, board_id?, filter?}`.
Claims the **oldest unowned matching card** (`ORDER BY updated_at ASC, id ASC`),
optionally moving it to `status`, assigning to `assign_to` (else the actor). On
no match → `200 { "card": null }`. On a match → `200 { "card": {...} }`.

> **No-double-claim guarantee [built, tested].** `claim`/`take-next` run in a
> single `BEGIN IMMEDIATE` transaction with the guard
> `UPDATE … WHERE id=? AND (owner IS NULL OR owner='')`, on a single writer
> connection. Under *N* concurrent callers exactly one wins; a card can never be
> handed to two owners. Proven by `TestClaimAtomicNoDoubleClaim` (50 claimants /
> 20 cards, race-tested → exactly 20 successes, zero duplicates).
> **Current limitation:** a racing *loser* on `take-next` receives
> `{ card: null }` (it can't yet distinguish "raced" from "queue empty") — under
> contention, a caller that got `null` should re-issue. Retry-to-next-candidate
> within one call is a tracked enhancement, not yet shipped.

**`POST /v1/cards/{id}/comments`** — `{body}`. Returns the updated card (`201`),
bumps `version`, and emits `comment_added`.

**`GET /v1/cards/{id}/history`** — `{items: [{at, actor, type, summary}]}`; a
human-readable timeline projected from the card's events.

**`GET /v1/workspace`** — `{workspace, card_types, boards, current_schema_versions}`,
where `workspace` carries `{id, name, columns, tag_set, link_types, users, settings}`.

### List filters — `GET /v1/cards`

Query params actually read by the handler: `board_id`, `type_id`, `status`,
`owner`, `q` (full-text), `blocked` (`=true`), `has_link`, `link_target`,
`cursor`, `limit` (default 50, max 200). Response is
`{ items: [...], next_cursor: "<opaque>" }`; ordering is `updated_at DESC, id DESC`.

> **Important:** the Mongo-style **filter DSL** (`$and/$or/$eq/$ne/$in/$nin/$gt/$gte/$lt/$lte/$contains`
> + tag ops) and the `unowned`/`status_in`/`type_id`-CSV dimensions are **not**
> exposed on the `GET /v1/cards` query string. The DSL is only consumed from a
> board's `default_filter` and from the `take-next` request `filter`. "Give me
> unowned cards of a kind" is reachable via `take-next`, not list.

### Actor on writes

Resolution order: **`X-Work-Cards-Actor` header → `CARDS_USER` env →
`workspace.settings.default_user`**; empty → `403 actor_required`. A body
`actor` field is overwritten by the resolved identity (not a resolution source).
See §5.

---

## 3. MCP surface

Transport: **JSON-RPC 2.0 over stdio** (newline-delimited). Launch:
`cards mcp --workspace <dir>`. Methods: `initialize`,
`notifications/initialized`, `tools/list`, `tools/call`. The MCP adapter
delegates to the **same `core.Service`** as HTTP, so validation, events, and the
no-double-claim guarantee are identical.

### Tools — `internal/mcp/mcp.go:204`

**Per card type (generated):** `create_<type_id>` and `update_<type_id>` — input
schemas derived from the type's fields (`title`/`status`/`tags` + per-field
props; `update_*` requires `card_id` + `version`). There is **no** generic
`create`/`update`.

**Fixed generic tools:** `workspace`, `get_card`, `list_cards`
(`type_id/status/owner/board_id/q/blocked/limit/cursor`), `search_cards`
(`q/limit`), `claim` (`card_id/version/status`), `take_next`
(`type_id/board_id/assign_to/status/filter`), `append_entry`
(`card_id/field/version/entry`), `add_link` (`card_id/type_id/target/note`),
`add_comment` (`card_id/body`), `history` (`card_id`).

### Actor binding

**Session-bound, no per-call override.** The actor is fixed at process start from
`CARDS_USER` (→ `default_user` fallback) and injected into every call's context.
No MCP tool exposes an actor parameter. Note: over MCP there is **no
`X-Work-Cards-Actor`** path — that header is HTTP-only.

> **[drift] `docs/MCP.md` over-documents the surface.** `update_entry`,
> `subscribe`, `card_events`, `upgrade_schema`, `card_type`, a create
> `schema_version` param, per-call `idempotency_key`, and `scoped_tools` are
> described there but are **not implemented** in `internal/mcp/mcp.go`. Treat the
> tool list above (read from code) as authoritative. Streaming is **HTTP/SSE
> only** — there is no MCP subscribe tool; an MCP-only client polls
> `history`/`list_cards` or holds a separate SSE connection.

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

### Mutation events [built] — `internal/core/types.go:209`

Canonical enumeration (15 declared, 13 emitted today): `card_created`,
`field_updated`, `status_changed`, `owner_changed`, `tags_changed`,
`item_appended`, `item_updated`, `item_removed`, `link_added`, `link_removed`,
`comment_added`, `comment_edited`, `schema_upgraded` — these 13 are **facts**:
synchronous on a write, card-scoped, persisted, replayable. The remaining two
are **[drift]**: `artifact_added` (constant declared; no artifact-upload route
or emit site exists yet — see `INTEGRATION.md` Act/Build-order #8) and
`definition_reloaded` (constant declared; no reload handler or file watching —
see §6 below). Both are [proposed], not built.

### Condition events [proposed] — `INTEGRATION.md`

`status_timeout`, `card_idle`, `wip_exceeded`/`wip_cleared`,
`lane_drained`/`lane_refilled`, `card_blocked`/`card_unblocked`,
`transition_rejected`. Declared as board **monitors** (data, not code), emitted
by the core onto the **same bus** as mutation events. **Not yet in the code** —
none of these constants exist today; monitors and the deadline evaluator are
designed in `INTEGRATION.md`, not built. `persist: true` (record a monitor's
events in the durable feed) is part of that design and **[proposed]**.

### `GET /v1/breaches` [proposed]

The on-demand "which cards currently violate which monitors" query — the
catch-up path for the (ephemeral) condition events. Designed, not built.

### Three ways to consume [built]

1. **Catch-up feed** — `GET /v1/events?since=&cursor=&actor=&owner=&type=&types=&board_id=&limit=`
   → `{ items, next_cursor }`, ordered by id ASC. `since=`/`cursor=` are **event-id
   floors** (events with `id >` value); `cursor=` is the pagination continuation
   and overrides `since=`. Filters: `actor`, `owner` (current card owner),
   `type`/`types` (CSV), `board_id` (board's card types). `limit` default 100, max
   500.
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

`actor`/`owner` filters on both feed and stream are **[built]**. Board-scoped
events (`scope: card|board`) are **[proposed]** (today every event is card-scoped).

---

## 5. Actor & identity model

- **An actor is any string.** It is recorded as `created_by` and event `actor`.
  It is **not validated** against the user registry for create/patch/comment/
  append — open, no auth. This is deliberate: spawn many short-lived workers,
  each with its own `CARDS_USER`, with no pre-registration.
- **Ownership is the one exception.** `owner` is a validated `user` reference, so
  setting it — including `claim`/`take-next`, which make the actor the owner —
  requires that id to be a **registered** user (`POST /v1/users {id, kind}`, open,
  no auth), else `unknown_user`. **A worker that only creates/comments needs no
  registration; a worker that *claims* must be registered first.** Register each
  worker once at spawn.
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
`tag_policy`, `default_user`, `event_retention_days`. All cross-references
(board columns/types/transitions, card-type `allowed_columns`, field types) are
validated **at load**; bad references fail startup.

### Single workspace per instance [confirmed, long-term]

**One process serves exactly one workspace** (one SQLite file). This is a
**locked, long-term contract** — not a v1 simplification. Multi-tenancy = run
multiple processes on different ports/paths. Intra-workspace isolation uses
**boards** (filtered views over a shared card pool, scoped by `card_type_ids`),
not multiple workspaces. → *picraft note: one workspace + one board + several
`type_id`s (D7) is exactly the intended shape; designing for multi-workspace is
unsupported.*

### Schema versioning & migration

- A card type declares `schema_version` (int); each card is **pinned** to the
  version it was created/upgraded against (`Card.schema_version`).
- **Existing cards are NOT auto-migrated.** They validate lazily against their
  pinned snapshot: adding an optional field leaves old cards valid; a new
  *required* field never retro-invalidates old cards; a removed field stays on
  old-version cards. The only way a card gains defaults / drops removed fields is
  an explicit **`POST /v1/cards/{id}/upgrade-schema`**, which re-pins one card
  forward (applies `migrations[N].field_defaults`, drops fields absent from the
  target schema, re-validates, emits `schema_upgraded`). It is one-card-at-a-time
  and refuses downgrades; today the target must be the type's currently-loaded
  version.
- **"Reload definitions" caveat [drift].** A `definition_reloaded` event type and
  a `POST /v1/workspace/reload` are *specced*, but in the current code only the
  event constant exists — **there is no reload handler and no file watching**.
  Definitions are loaded once at process start, so **"reload" in practice means
  restart the server**. Semantically, reloading definitions never mutates cards
  (it only rebuilds in-memory config).

---

## 7. Extensions

The core **loads no extension code and executes nothing in-process**; extensions
are independent processes that talk to the API. Declared in
`definitions/extensions.json`:
`{id, kind, description?, on?, filter?, run, cwd?, env?, autostart?, expose?}`.

- **`hook` [built]** — reactive subprocess. `on: <event_type>` + optional
  `filter` (`board_id`, `type_id`, `card_id`, `to_status`, `from_status`). The
  supervisor (`cards run-extensions`, or `cards serve --run-extensions`)
  subscribes to the bus and, on a match, spawns `run` (argv array, no shell) with:
  the **event JSON on stdin**, env `CARDS_URL`/`CARDS_WORKSPACE`/`CARDS_USER`/
  `CARDS_EVENT_ID`/`CARDS_EVENT_TYPE`, and `cwd` = workspace root. It is
  **fire-and-forget and at-most-once** — async, never blocks or rolls back the
  write, a non-zero exit is logged not retried. Hooks write back via the ordinary
  HTTP API (they are just clients reacting after the fact).

  ```json
  { "id": "review-notify", "kind": "hook", "on": "status_changed",
    "filter": { "board_id": "engineering", "to_status": "review" },
    "run": ["bash", ".cards/ext/notify.sh"] }
  ```

- **`run` [built]** — one-shot command invoked manually via `cards do <id>
  [--param k=v ...]`. Receives the `--param` flags as **argv** (not event JSON);
  synchronous; child stdout/stderr stream to the parent.

- **`service` [drift / not implemented]** — a long-running SSE-subscriber is
  *designed* (`autostart`, restart policy, `expose`), and the fields parse, but
  **nothing in the current code starts or supervises a `service`**; only `hook`
  and `run` are wired. Run long-lived consumers yourself (systemd/compose/by-hand)
  as ordinary API+SSE clients — which is exactly picraft's persistent operator
  session. Either way a "service" can only write back as an API client; the core
  never loads it in-process.

---

## 8. What cards deliberately does NOT provide

The boundary is **"cards emits signals; your app owns the response"** — the core
never acts on a condition. Out of scope, by design:

- **No lease / mutex / TTL / heartbeat / dead-owner reclaim.** The only
  coordination atomics are `claim` and `take-next`; both set `owner` once and
  **never expire or reclaim**. Model a lease as a card with an `expires_at` field
  and reclaim it yourself. → *picraft note: the D6′ boundary is correct.*
- **No scheduler / dispatcher / queue.** "Background processing, queues,
  schedulers" are explicitly extension-owned. `take-next` returning `null` is the
  pull signal; the pull *policy* is yours.
- **No dependency auto-promotion / epic rollups.** The `blocked` query and
  (proposed) `card_unblocked` event are signals; promoting a ready card is your
  policy.
- **No in-core execution.** The `command` field type and `path`/`json`/`yaml`
  field types were removed; store such content as `string`/`text`/`artifact` and
  let an extension validate. The core executes nothing.
- **No multi-workspace router**, **no built-in auth** (localhost trust;
  reverse-proxy/auth is an extension/host concern), **no server-managed config
  editing** (definitions are git-backed files).

---

## Pointers into the cards docs

| Topic | Doc |
|---|---|
| Normative contract (data model, API, errors, events, atomics) | [`SPEC.md`](SPEC.md) |
| Vocabulary + use-case setups (workspaces, boards, card types) | [`CONCEPTS.md`](CONCEPTS.md) |
| Events & integration design (mutation vs condition, monitors, feed, breaches) | [`INTEGRATION.md`](INTEGRATION.md) |
| Runtime shape, package boundaries, storage | [`ARCHITECTURE.md`](ARCHITECTURE.md) |
| MCP transport & tools (note the drift in §3 above) | [`MCP.md`](MCP.md) |
| Extension declaration format & worked examples | [`EXTENSIONS.md`](EXTENSIONS.md) |
| Workspace authoring (definitions, schema versioning, reload) | [`DEVELOPER-REFERENCE.md`](DEVELOPER-REFERENCE.md) |
| Design rationale & decisions (D-numbers) | [`PHILOSOPHY.md`](PHILOSOPHY.md), [`NOTES.md`](NOTES.md) |

*Verified against the source at the time of writing. Where this doc and an older
narrative doc disagree, this doc (read from code) wins — and the discrepancy is a
bug to file against the narrative doc.*
