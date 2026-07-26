# Cards — Integrator Reference

A single-page, **code-verified** reference for building on the cards service:
the data model, the HTTP API, the MCP surface, events, the actor model, the
git-defined workspace, extensions, and the boundary of what cards deliberately
does *not* do.

This document is written from the source (`internal/...`) and the real example
workspace (`examples/demo-workspace/`), not from prose — where the older
narrative docs drift from the code, the drift is flagged inline. For the
normative contract see [`index.md`](../spec/index.md); for the events/integration design
see [`integration.md`](../events/integration.md); for runtime shape see
[`index.md`](../architecture/index.md).

**Status legend:** **[built]** exists in code today · **[proposed]** designed,
not yet implemented · **[drift]** documented elsewhere but *not* in the code.

---

## 1. Data model

The unit is a **card**: a fixed envelope managed by the runtime plus
schema-validated custom `fields`. Cards live in **one workspace**; **boards are
filtered views**, not containers (a card has no `board_id`).

### The card object — `internal/core/types.go:286`

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
	StatusSince   time.Time `json:"status_since,omitempty"` // server-maintained; set at create + every status change
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
- Setting `owner` via PATCH or `claim` requires a registered user (`unknown_user`
  422 otherwise). `take-next` currently bypasses that lookup (see §5).
- `claim` is compare-and-set on `version`; claiming a card already owned by a
  *different* actor → `409 version_conflict`. `release` sets owner back to `""`.

### Boards, columns, statuses — `internal/core/types.go:224`

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

### Links — `internal/core/types.go:113`

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

### Custom fields — `internal/core/types.go:16` / `:32`

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

**Multi-value fields [built]** — `"multiple": true` on `enum`/`user` (v1 scope;
rejected on other types and inside `item_fields` at definition load): the value
is always a JSON **array of strings**; an unset optional multiple field is
**absent** from `fields` (never `null`, never `[]` — writing either unsets the
key; normalized in `internal/core/validate.go` + `PatchCard`). Duplicates and
out-of-set elements are rejected with structured errors. Filter by membership
with **`$has`** (`internal/sqlite/filter.go`, `json_each`; scalar fields
degrade to equality). MCP create/update tools introspect the field as
`{type:"array", items:{type:"string", enum:[...]}}`. CLI: pass a JSON array —
`--field 'platforms=["desktop","mobile"]'`. `card_link` multiple is a
documented fast-follow, not built. (Note: the HTTP `filter=` list param is
still unwired — a pre-existing gap tracked on the board; `$has` works through
board saved filters, `take-next`, and MCP `list_cards`.)

---

## 2. HTTP API

Base path `/v1`. Server binds `127.0.0.1` by default; **no built-in auth**
(see §5). Routes are registered in `internal/httpapi/server.go:248-280`;
`POST /v1/workspace/reload` and `POST /v1/boards` are registered on the parent
mux in `cmd/cards/reload.go:104-112`.

### Endpoint table

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/health` | `{version, workspace_id}` |
| GET | `/v1/workspace` | introspection snapshot (types, boards, columns, versions) |
| GET | `/v1/boards/{id}` | one board's definition (404 for unknown ids) |
| GET | `/v1/cards` | list / filter (cursor-paged) |
| POST | `/v1/cards` | create |
| GET | `/v1/cards/{id}` | fetch one (with links + comments) |
| PATCH | `/v1/cards/{id}` | mutate (optimistic concurrency) |
| DELETE | `/v1/cards/{id}` | delete one |
| POST | `/v1/cards/{id}/upgrade-schema` | re-pin one card to current schema |
| POST | `/v1/cards/take-next` | atomically claim oldest unowned match |
| POST | `/v1/cards/{id}/claim` | claim a specific card |
| POST | `/v1/cards/{id}/release` | unclaim (owner → "") |
| POST | `/v1/cards/{id}/links` | add link (201) |
| DELETE | `/v1/cards/{id}/links/{typeID}/{target}` | remove link |
| POST | `/v1/cards/{id}/comments` | add comment (201) |
| PATCH | `/v1/cards/{id}/comments/{commentID}` | edit comment |
| POST | `/v1/cards/{id}/artifacts/{field}` | attach an artifact blob to a file field |
| GET | `/v1/artifacts/*` | serve an artifact blob |
| POST | `/v1/cards/{id}/fields/{field}/append` | append a repeating-field entry |
| PATCH | `/v1/cards/{id}/fields/{field}/{entryID}` | update a repeating entry |
| DELETE | `/v1/cards/{id}/fields/{field}/{entryID}` | remove a repeating entry (`?version=N`) |
| GET | `/v1/cards/{id}/events` | one card's events |
| GET | `/v1/cards/{id}/history` | rendered timeline (resumption) |
| GET | `/v1/events` | **catch-up feed** (cursor-paged, durable) — §4 |
| GET | `/v1/events/stream` | **SSE live stream** — §4 |
| GET | `/v1/breaches` | active condition breaches (WIP / lane / blocked / temporal — §4) |
| GET | `/v1/openapi.json` | generated OpenAPI 3.1 — covers every row in this table (see note below) |
| POST | `/v1/users` | register a user (open, no auth) |
| POST | `/v1/workspace/reload` | re-run the definitions loader, swap the composition |
| POST | `/v1/boards` | write + validate a board definition (see `cmd/cards/reload.go`) |

UI handlers live under `/ui` (reference consumer; not part of the contract).
**There are NO batch/bulk endpoints** — writes are strictly per-card;
`take-next` claims exactly one card.

> **OpenAPI coverage [built].** `GET /v1/openapi.json` documents **every**
> operation in the table above — 25 paths / 29 operations — including the
> coordination atomics (`claim`, `release`, `take-next`), links, comments,
> repeating entries, the durable `/v1/events` feed, `/v1/users`, and the two
> reload-seam routes (`/v1/workspace/reload`, `POST /v1/boards`, both flagged
> `cards serve` only, since they live on the parent mux in
> `cmd/cards/reload.go` rather than the `/v1` router). `/v1/openapi.json` is the
> single deliberate omission — the document does not describe itself.
> Mutating operations carry the `Idempotency-Key` header and the structured
> `403`/`409`/`422` envelopes; the `Event.type` enum is generated from
> `core.EventTypes()` rather than restated. Pinned by
> `TestOpenAPICoversEveryRoute` (`internal/httpapi`), which walks the chi route
> table against the document in **both** directions — an undocumented route and
> a documented phantom both fail — plus the shape tests in `internal/openapi`.
> *(Prior state: 11 paths / 13 operations, with `claim`, `release`, and the
> event feed absent, and `take-next` documented as returning a bare `Card`
> rather than `{card: …|null}`.)*

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
> **Race retry [built].** A losing CAS surfaces `ErrClaimRaced`
> (`internal/core/errors.go:141-145`, raised from the CAS path at
> `internal/sqlite/sqlite.go:746` <!-- guard: internal/sqlite/sqlite.go:746 symbol=ErrClaimRaced -->);
> `take-next`/`claim` wrap the attempt in `claimWithRetry`
> (`internal/core/service.go:1587` <!-- guard: internal/core/service.go:1587 symbol=claimWithRetry -->,
> called at `:1552` <!-- guard: internal/core/service.go:1552 symbol=claimWithRetry -->),
> which retries the next candidate up to 3 times within one call before
> returning `{ card: null }`. Verified by `internal/core/claimretry_test.go`.

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

> **CSV list filters [built].** `status` and `type_id` accept a
> **comma-separated list** matched as ANY (IN) — e.g.
> `GET /v1/cards?status=todo,in_progress` — mapped to `CardQuery.StatusIn` /
> `TypeIDIn` in the handler (`internal/httpapi/api.go:76-89`; landed `8bb59bb`).
> A bare single value keeps the scalar-equality path.
>
> **Still not exposed:** the Mongo-style **filter DSL**
> (`$and/$or/$eq/$ne/$in/$nin/$gt/$gte/$lt/$lte/$contains` + tag ops) and an
> `unowned` dimension are **not** on the `GET /v1/cards` query string. The DSL
> is only consumed from a board's `default_filter` and from the `take-next`
> request `filter`. "Give me unowned cards of a kind" is reachable via
> `take-next`, not list.

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

### Tools — `internal/mcp/mcp.go:218` (`buildTools`)

**Per card type (generated):** `create_<type_id>` and `update_<type_id>` — input
schemas derived from the type's fields (`title`/`status`/`tags` + per-field
props; `update_*` requires `card_id` + `version`). There is **no** generic
`create`/`update`.

**Fixed generic tools:** `workspace`, `get_card`, `list_cards`
(`type_id/status/owner/board_id/q/blocked/limit/cursor`), `search_cards`
(`q/limit`), `claim` (`card_id/version/status`), `release`
(`card_id/version/status?/force?`), `take_next`
(`type_id/board_id/assign_to/status/filter`), `append_entry`
(`card_id/field/version/entry`), `update_entry`
(`card_id/field/entry_id/version/entry`), `remove_entry`
(`card_id/field/entry_id/version`), `add_link` (`card_id/type_id/target/note`),
`remove_link` (`card_id/type_id/target`), `add_comment` (`card_id/body`),
`edit_comment` (`card_id/comment_id/body`), `upgrade_schema`
(`card_id/target_version?/confirm?` — dry-run unless `confirm:true`),
`attach_artifact` (`card_id/field/content_base64`), `get_artifact` (`uri`),
`history` (`card_id`), `breaches` (`board_id/type`),
`events` (`types/board_id/since/limit`). Authoritative short list also in
`internal/mcp/README.md`; narrative in `docs/extensions/mcp.md`.

### Actor binding

**Session-bound, no per-call override.** The actor is fixed at process start from
`CARDS_USER` (→ `default_user` fallback) and injected into every call's context.
No MCP tool exposes an actor parameter. Note: over MCP there is **no
`X-Work-Cards-Actor`** path — that header is HTTP-only.

Streaming is **HTTP/SSE only** — there is no MCP `subscribe` tool; an MCP-only
client polls `history`/`events`/`list_cards` or holds a separate SSE connection.
MCP does not forward idempotency keys or dry-run (except `upgrade_schema`'s
`confirm` gate) — see SPEC-API-SURFACE §13.

---

## 3a. Terminal UI (TUI) [built]

Interactive terminal UI in `internal/tui/` (bubbletea v2 + lipgloss v2 +
glamour). **Entry point:** a bare `cards` with stdin+stdout as TTYs (and no
`--json`/`--jsonl`) opens it — `cmd/cards/main.go:run` (the `interactive()`
guard) → `cmd/cards/tui.go:tuiCmd`. Non-interactive callers keep the old
usage-text behavior, so scripts/agents are unaffected.

**Composition root:** identical to the serverless CLI — `resolveWorkspaceDir`
→ `initWorkspace` → `openWorkspace` → `core.Service` in-process
(`cmd/cards/tui.go:tuiCmd`). No server required. Live refresh comes from the
**in-process event bus** (`svc.Bus().Subscribe`, re-armed as a `tea.Cmd` per
event).

**Model:** board columns (`board.Columns`, in order) render as tabs — no
per-lane colors/icons assumed, active tab highlighted. Lanes list cards from
`svc.ListCards({BoardID, Include: ["links","comments"]})`. The detail pane
renders the selected card as a **markdown document** via glamour: schema
fields (from the type's `FieldDef`s), **outbound** links from the card plus
**inbound** from `svc.ListCards({LinkTarget})`, comments, and activity from
`svc.ListEvents({CardID})`.

**State machine:** three focus zones (list / header / detail) × three detail
visibilities (hidden / split / fullscreen) — `enter` reveals and focuses the
detail and again fullscreens it; `esc` steps fullscreen → split → list-only;
`tab` toggles panes; `shift+tab` switches boards; `k` at the list top focuses
the tab bar. Legal transitions come from `board.Transitions` when
`enforce_transitions` is on (numbered picker), otherwise any column.

**Writes** go through the same service calls as the CLI (`PatchCard`,
`AddComment`, `Claim`/`Release`, `CreateCard`) with version-based optimistic
concurrency; the actor is bound via `core.WithActor(ctx, actor)` — the same
context mechanism as `X-Work-Cards-Actor` on HTTP. Headless tests in
`internal/tui/tui_test.go`: focus/detail-mode state machines, scroll
clamping, transition legality, and mutation flows against a temp copy of the
demo workspace.

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

### Mutation events [built] — `internal/core/types.go:310`

Canonical enumeration — `internal/core/types.go` declares **26** event types:
17 card/state events plus the 9 condition events (§ below). The **15 durable
card facts** are `card_created`, `card_deleted`, `field_updated`,
`status_changed`, `owner_changed`, `tags_changed`, `item_appended`,
`item_updated`, `item_removed`, `link_added`, `link_removed`, `comment_added`,
`comment_edited`, `schema_upgraded`, `artifact_added` — synchronous on a write,
card-scoped, persisted, replayable. `artifact_added` emits from the attachments
upload path (`Service.AddArtifact`, **[built]**). `definition_reloaded` is
**[built]** — emitted by `POST /v1/workspace/reload` and by `cards serve
--watch` after a successful definitions reload (`cmd/cards/reload.go`).
`definition_reload_failed` is **[built]** — emitted when a reload keeps
last-good serving (HTTP 422 or watch poller); the board UI shows a banner.
Contract: [`docs/architecture/reload.md`](../architecture/reload.md).

### Condition events [built] — `internal/core/types.go`, `integration.md`

`wip_exceeded`/`wip_cleared`, `lane_drained`/`lane_refilled`,
`card_blocked`/`card_unblocked`, `transition_rejected` (opt-in), and the
temporal `status_timeout`/`card_idle`. Declared as board **monitors** (data,
not code — `board.monitors` + `wip_limits`), emitted by the core onto the
**same bus** as mutation events, and evaluated through one seam
(`Emitter.Condition`). Instant conditions evaluate synchronously on the
triggering mutation; temporal conditions run through a tickless deadline
scheduler armed from `status_since`. By default they are **ephemeral**
(live bus/SSE/hooks only); `settings.persist_conditions` escalates named types
to the durable feed. See `docs/events/rollout.md` §12 and `integration.md`.

### `GET /v1/breaches` [built]

The on-demand "which conditions are currently true" query — WIP-exceeded
columns, drained lanes, blocked cards, and **past-due temporal monitors** —
the catch-up path for the (ephemeral) condition events. `?board_id=&type=`.
Temporal projection (`status_timeout`/`card_idle`) is cold: the same deadline
math as rebuild/verify (`statusTimeoutDeadline`/`cardIdleDeadline`,
`internal/core/service.go`), filtered to `At <= now`, read-only — it never
arms or marks conditions fired (`internal/core/breaches.go`). Type→fields:
`status_timeout` → `status`/`since`/`max`; `card_idle` → `since`/`threshold`;
`card_blocked` → `blockers` (nested); WIP/lane → `column`/`count`/`limit`.
Item scans (blocked + temporal) inherit the `ListCards` **500 ceiling** —
the report echoes `limit` and sets `truncated: true` when a scan hit it, so
**catch-up is partial, never tagged "complete"**; WIP/lane counts are
uncapped (`CountCards`). Pinned by clock-injected tests in
`internal/core/breaches_temporal_test.go` (projection, golden-vs-verify,
read-only, truncation) and `internal/httpapi/breaches_temporal_test.go`
(wire shape + UI row text).

### Three ways to consume [built]

1. **Catch-up feed** — `GET /v1/events?since=&cursor=&actor=&owner=&type=&types=&board_id=&limit=`
   → `{ items, next_cursor }`, ordered by id ASC. `since=`/`cursor=` are **event-id
   floors** (events with `id >` value); `cursor=` is the pagination continuation
   and overrides `since=`. Filters: `actor`, `owner` (current card owner),
   `type`/`types` (CSV), `board_id` (board's card types). `limit` default 100, max
   500.
2. **Live SSE** — `GET /v1/events/stream?card_id=&board_id=&types=&actor=&owner=`,
   with bounded replay via `Last-Event-ID` / `since=`. All five filters are built.
3. **Per-card** — `GET /v1/cards/{id}/events` and `/history`.

> **SSE retention / replay guarantee [built] — the load-bearing answer.** The
> persisted `events` table is **append-only and never trimmed**, so the **feed is
> a complete, gap-free durable log** replayable from *any* id, no matter how long
> a consumer was disconnected. The in-memory SSE buffer is **bounded and
> best-effort**: a slow consumer whose buffer fills is dropped with a
> `: dropped, reconnect` comment (it never blocks a writer). **Durable recovery
> therefore goes through the feed, not the stream:** page `GET /v1/events` from
> your last id until `next_cursor` is empty. The SSE handler replays at most
> 500 events and subscribes after replay, so opening it is not an atomic
> feed-to-live handoff; strict consumers should open the stream and then
> reconcile the feed once more from their last processed durable id.
> *(`event_retention_days` exists in workspace settings as a future knob but is
> not enforced today — retention is currently unbounded.)*

`actor`/`owner` filters on both feed and stream are **[built]**. Board-scoped
events are **[built]**: `Event` carries `Scope` (`card|board`) and `BoardID`,
and the feed/stream filter by `board_id`; today the board-scoped emitters are
the ephemeral WIP and lane signals. Definition reload success/failure also
publishes live events per affected board; those lifecycle signals are not
durable feed entries.

---

## 5. Actor & identity model

- **An actor is any string.** It is recorded as `created_by` and event `actor`.
  It is **not validated** against the user registry for create/patch/comment/
  append — open, no auth. This is deliberate: spawn many short-lived workers,
  each with its own `CARDS_USER`, with no pre-registration.
- **Ownership is mostly registry-backed.** Setting `owner` via PATCH, or using
  `claim` (which makes the actor the owner), requires a registered user
  (`POST /v1/users {id, kind}`) or returns `unknown_user`. `take-next` currently
  bypasses that user lookup for `assign_to`/actor ownership; this is an
  implementation inconsistency, not an authentication boundary.
- **Stable orchestrator vs ephemeral workers:** both are just actor strings. Use a
  fixed `CARDS_USER` (e.g. `orchestrator`) for dispatch-owned writes and a distinct
  one per worker. Register identities used with direct owner PATCH/`claim`.
  No rate limits; collision = same actor string = same identity (that's the
  only "auth").
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
validated **at load**; bad references fail startup. Semantic load checks also
cover number/date `min`≤`max`, known icon aliases, and dangling presentation/filter field refs (ambiguous legacy keys warn via `Result.Warnings`).

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
  version it was created/upgraded against (`Card.schema_version`), but that pin
  is recorded metadata rather than a historical validation lookup today.
- **Existing cards are NOT auto-migrated.** Only the current type definition is
  loaded, so ordinary PATCH/append operations validate against the current
  schema even when a card records an older version. Historical snapshots and
  true pinned-version validation are unbuilt.
- Explicit **`POST /v1/cards/{id}/upgrade-schema`** re-pins one card forward:
  it applies `migrations[N].field_defaults`, drops fields absent from the
  current target schema, re-validates, and emits `schema_upgraded`. It is
  one-card-at-a-time, refuses downgrades, and today the target must be the
  type's currently loaded version.
- **Reload definitions [built].** `POST /v1/workspace/reload` (and CLI
  `cards reload`) is implemented on `cards serve` via `reloadableApp` in
  `cmd/cards/reload.go`: re-loads definitions, swaps the Service + HTTP router
  around the same SQLite store and event bus, emits `definition_reloaded` per
  board, and on loader failure returns **422**, emits
  `definition_reload_failed`, and keeps the previous generation. Semantically,
  reloading never mutates cards (in-memory config only). **`cards serve
  --watch` [built]** polls `definitions/` with a dependency-free fingerprint
  hash (no fsnotify), debounces, and reloads on the same path — see
  [`docs/architecture/reload.md`](../architecture/reload.md). `POST /v1/boards`
  (create-board) also writes a board JSON then reloads, with self-write
  suppression so the poller does not double-fire.

---

## 7. Extensions

The core **loads no extension code and executes nothing in-process**; extensions
are independent processes that talk to the API. Declared in
`definitions/extensions.json`:
`{id, kind, description?, on?, filter?, run, cwd?, env?, autostart?, restart_policy?, expose?}`.

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

- **`service` [built]** — long-running supervised process when `autostart: true`
  under `cards serve --run-extensions` (supported home) or standalone
  `cards run-extensions`. Shared construction path (`cmd/cards/supervisor.go`).
  Listener-ready gate on serve: bind first, then start children. Env:
  `CARDS_URL` (loopback base), `CARDS_WORKSPACE`, `CARDS_USER`. Restart per
  `restart_policy` (`on-failure` default / `always` / `never`) with bounded
  backoff and min-healthy-uptime streak reset. Drain: SIGTERM → grace →
  SIGKILL process group. **No in-process event feeding** — services dial
  `/v1/events/stream` themselves. `expose` still parsed but unconsumed.
  **Reconcile-on-reload [built]** (P5c): identity = extension `id`; declaration
  fingerprint = hash of `run`+`env`+`cwd`+`restart_policy`; decision table
  added→start / removed→drain+stop / unchanged→leave alone /
  declaration-changed→drain+restart. Snapshot handed off after
  `reloadableApp.mu` release (board-create reload ⇒ zero service churn). See
  `docs/architecture/reload.md`.

  ```json
  { "id": "dropbox", "kind": "service", "autostart": true,
    "restart_policy": "on-failure",
    "run": ["node", ".cards/ext/dropbox.mjs"] }
  ```

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
  built `card_unblocked` event are signals; promoting a ready card is your
  policy.
- **No in-core execution.** The `command` field type and `path`/`json`/`yaml`
  field types were removed; store such content as `string`/`text`/`artifact` and
  let an extension validate. The core executes nothing.
- **No multi-workspace router**, **no built-in auth** (localhost trust;
  reverse-proxy/auth is an extension/host concern), **no server-managed config
  editing** (definitions are git-backed files).

---

## Audit changelog

This doc is *code-verified*: each entry below records the `git log` evidence
for the source paths a section describes, over the range since the doc was
last verified. **Current boundary:** `e25797c` **→ HEAD (`25cc1d0`,
2026-07-26 — the boundary roll itself)**. The range actually audited is
`67e613f`→`e25797c` (20 commits), recorded in the entry below; the commits
after `e25797c` are this doc roll and carry no code.
Reproduce any line with `git log --oneline 67e613f..e25797c -- <paths>`.

> **Note on this line's first commit.** `TestImplStatusBoundaryCommit` measures
> lag from the **first** commit cited here, so a roll that cites the *start* of
> a 20-commit range is already at the limit the moment the roll commit lands —
> it fails on its own commit. Cite the **end** of the audited range here and
> record the range in the entry heading.

### Entry 2026-07-26 (`67e613f` → `e25797c`)

Headline: 20 commits. Two behavior-contract changes the doc had to absorb
(fail-closed board transitions + the ephemeral-SSE cursor fix in `7cd5bd1`,
which landed before this pass), and the contract-truth work: full OpenAPI
route coverage with a both-directions drift guard (`635d2c9`), and three
declared-but-unread workspace settings wired up (`ba3b6f3`). §1–§4 re-audited
against the source; §5–§8 reviewed with the changes noted below.

This entry also clears a standing failure: `TestImplStatusBoundaryCommit` had
been red in the strict CI job since before `4ec0fa3`, at 23 commits behind.
The lesson is recorded rather than just fixed — the guard warns in an ordinary
`go test` run but is **strict in its own CI job**, so a warning seen locally is
already a red build.

| § | Section | Change | Evidence |
|---|---|---|---|
| 2 | HTTP API | `GET /v1/openapi.json` now documents **every** row of the endpoint table (25 paths / 29 operations, up from 11/13); coverage note added under the table | `internal/openapi/openapi.go`; `TestOpenAPICoversEveryRoute` (`internal/httpapi`) |
| 2 | List filters | ceiling corrected to **500** (was documented as 200; `clampCardLimit` fixed the old ">200 → 50" bug); `sort` and `include` added to the read-params list | `internal/sqlite/sqlite.go` (`clampCardLimit`); `internal/httpapi/api.go` |
| 2 | take-next | pool is scoped to `settings.default_board` when neither `board_id` nor `type_id` is given; an explicit `type_id` is never additionally board-scoped | `internal/core/service.go` (`TakeNext`); `internal/core/defaultboard_test.go` |
| 2 | take-next | anchors re-pinned twice as the claim path moved — `internal/sqlite/sqlite.go:746`, `internal/core/service.go:1587` / `:1552` | `TestImplStatusAnchorsResolve` caught each move |
| 4 | Events | ephemeral condition signals omit the SSE `id:` field so an `EventSource` reconnect keeps its last **durable** cursor; the canonical event catalog is now `core.EventTypes()` and the OpenAPI enum reads it rather than restating it | `internal/httpapi/sse.go`; `internal/core/types.go` (`EventTypes`); commit `7cd5bd1` |
| 6 | Workspace & schema | `tag_policy` is a two-value dial (`open`\|`locked`) the core actually reads — it was declared, validated and ignored, so a fresh `cards init` rejected every tag; `propose` is dropped and **rejected at load**; unset now defaults to `locked` | `internal/core/validate.go`; `internal/config/config.go`; `internal/core/tagpolicy_test.go` |
| 6 | Workspace & schema | `settings.default_board` added — resolves the TUI initial board, the `/ui/cards/new` landing and take-next scope through one helper; a dangling id fails load | `internal/core/helpers.go` (`DefaultBoardID`) |
| 6 | Workspace & schema | `searchable_fields` is honored by the FTS indexer; a changed declaration rebuilds the index once, gated on a digest in a new `meta` table | `internal/sqlite/sqlite.go`; `internal/sqlite/searchable_test.go` |
| 6 | Workspace & schema | load now **warns** on declared-but-inert knobs; two remain (`event_retention_days`, `extensions[].expose`) | `internal/config/inert.go` |
| 5 | Actor & identity | reviewed, no change needed — `take-next` still bypasses the user-registry check that PATCH-owner and `claim` enforce. Documented here since 2026-07-18 and still undecided; tracked on the board | `internal/core/service.go` (`TakeNext`) |
| 7–8 | Extensions / non-goals | reviewed, no change needed | — |

### Entry 2026-07-20 (`bb6ffc5` → `67e613f`)

Headline: 6 commits — the sprint 07-19 landing (`276800b`: TUI filter/sort
parity via the new shared `internal/uioptions`, the review-bot extension
seed, snapshot-contract tests, and the docaudit guards this changelog is
checked by), portable artifact bundles (`0f3529a`: `cards export/import
--with-artifacts`, sha256-verified, in `cmd/cards/bundle.go`), headless TUI
screenshots (`fe9db62`: `tui.Snapshot` + `cmd/tui-shot`), version surfacing
in the web nav and `--help` (`c348037`), and CI hygiene (`1dd54d0`). This
entry rolls the boundary; the anchor guards stayed green across the range
(the moved-symbol tripwire this doc gained in `276800b` did its job), and
§4–§8 were not re-audited.

| § | Section | Change | Evidence |
|---|---|---|---|
| 3 | CLI | `export`/`import` gained `--with-artifacts` (content-addressed bundle beside the JSONL snapshot; pointer-only remains the default) | `cmd/cards/bundle.go`; `cmd/cards/portable_test.go` (round-trip, tamper, self-safe, missing-blob) |
| 3a | TUI | filter/sort landed: `f`/`F`/`T` bindings, sort presets shared with the web UI via `internal/uioptions` (compile-time parity), `me` substitution | `internal/tui/tui.go`; `internal/uioptions/uioptions.go` |

### Entry 2026-07-19 (`0421efd` → `bb6ffc5`)

Headline: 17 commits since the last verification — the temporal-breaches
cold catch-up (`1247e3b`), the sqlite shared-cache memory-test harness and
migration-test routing, TUI inbound-link rendering, the project-board move
to `.cards/`, and docs/plans churn. Scope discipline (sprint 2026-07-19
Phase 1): §4–§8 were **not** re-audited across this range. This entry rolls
the boundary, splits the `1247e3b` evidence row out of the previous entry
(whose stated HEAD it post-dated — the row contradicted that entry's
range), and re-pins the three §2 take-next anchors, which had rotted to
stale line numbers. The re-pinned anchors now carry explicit
`<!-- guard: <path>:<line> symbol=<ident> -->` markers, machine-checked by
`TestImplStatusAnchorsResolve` in `internal/docaudit` (strict in every
`go test` run), so the moved-and-renumbered rot class now fails loudly
instead of green-lighting.

| § | Section | Change | Evidence |
|---|---|---|---|
| 4 | Events | temporal `/breaches` projection landed: `status_timeout`/`card_idle` cold catch-up, additive `BreachItem` fields, `limit`/`truncated` clamp echo — row split out of Entry 2026-07-18, whose stated range (`b3bfed5` → `0421efd`) the commit post-dates | `internal/core/breaches.go`; `internal/core/service.go` (shared deadline helpers); commit `1247e3b` |
| 2 | take-next | anchors re-pinned against HEAD `bb6ffc5` (`internal/sqlite/sqlite.go:746`, `internal/core/service.go:1526`, called at `:1491`) and annotated with guard markers; `internal/docaudit` gained `TestImplStatusAnchorsResolve`, `TestCodeCommentDocPathsResolve`, and the boundary-commit tripwire (warning in normal `go test`, strict under `-tags=strictdoc`) | `internal/docaudit/docaudit_test.go` |

### Entry 2026-07-18 (`b3bfed5` → `0421efd`)

Headline: 46 commits since the last verification, with real `/v1` landings the
doc had missed — CSV `status`/`type_id` list filters (`8bb59bb`), take-next
race retry (`ErrClaimRaced` / `claimWithRetry`), card delete + artifacts +
breaches + reload/create-board routes absent from the endpoint table, a ghost
`internal/httpapi/httpapi.go` citation, and the TUI transport (§3a). This pass
refreshed §1–§3 anchors and the endpoint table and rolled the boundary;
§5–§8 reviewed with no change needed.

| § | Section | Change | Evidence |
|---|---|---|---|
| 1 | Data model | anchors moved to live structs; `StatusSince` added to the published Card envelope | `internal/core/types.go:113`, `:224`, `:286`, `:302-306` |
| 2 | HTTP API | ghost `httpapi.go:84` removed (routes register in `internal/httpapi/server.go:248-280`); endpoint table gained `DELETE /v1/cards/{id}`, artifact upload/serve, `GET /v1/breaches`, `POST /v1/workspace/reload`, `POST /v1/boards` | `internal/httpapi/server.go:248-280`; `cmd/cards/reload.go:104-112` |
| 2 | List filters | comma-separated `status` / `type_id` (→ `StatusIn` / `TypeIDIn`) documented [built] | `internal/httpapi/api.go:76-89`; commit `8bb59bb` |
| 2 | take-next | race loser signals `ErrClaimRaced`; service retries the next candidate in-call (up to 3) — "not yet shipped" claim removed | `internal/core/errors.go:141-145`; `internal/core/service.go:1472`, `:1500-1510`; `internal/core/claimretry_test.go` |
| 3 | MCP | `buildTools` anchor corrected | `internal/mcp/mcp.go:218` |
| 4 | Events | EventType anchor moved to the live declaration | `internal/core/types.go:310` |
| 5–8 | Actor / workspace / extensions / non-goals | reviewed, no change needed — auth is still the trusted-actor model (§5); reload + service-kind supervision already [built] (§7) | — |

### Entry 2026-07-10 (`8d043ea` → `b3bfed5`)

**Boundary:** `8d043ea` (rebuild Phase 3 — multi-value fields;
the doc's previous verification point, `b8dda45` before the frontend-rebuild
branch was rebased into `main`) **→ HEAD (`b3bfed5`, 2026-07-10)**.

Headline: the only post-Phase-3 code under this doc's purview was the `/ui`
**reference client** (design-system.md's domain, not this doc — §2 documents zero
`/ui` routes) and an SSE **keepalive** (liveness, not a consumer contract).
The `/v1` API, MCP surface, event payloads, actor model, workspace schema, and
extension model are **unchanged** since the last verification.

| § | Section | Paths audited | Result (`8d043ea..HEAD`) |
|---|---|---|---|
| 1 | Data model | `internal/core/types.go` | reviewed, no change needed — no commits in range (Phase 3 multi-value fields are the boundary, already reflected) |
| 2 | HTTP API | `internal/httpapi/api.go`, `filters.go`, `internal/sqlite/filter.go` | `/v1` contract reviewed, no change needed — `api.go`/`filters.go` unchanged in range. The Phase 4–10 httpapi churn is all `/ui` reference-client (`render.go`, `ui.go`, `server.go` route registration), which §2 does not document |
| 3 | MCP surface | `internal/mcp/` | reviewed, no change needed — no commits in range |
| 4 | Events | `internal/core/events.go`, `breaches.go`, `monitor.go`; `internal/httpapi/sse.go` | event payloads/breaches reviewed, no change needed. SSE transport gained a keepalive at `44012f4` (Phase 9, `sse.go`) — liveness only, no change to event shape or the consumer contract |
| 5 | Actor & identity | `internal/core/service.go`, `internal/httpapi/middleware.go` | reviewed, no change needed — no commits in range. (The `docs/design/auth.md` direction is *proposed*/unbuilt; §5 still describes the built trusted-actor model) |
| 6 | Workspace & schema | `internal/config/` | reviewed, no change needed — no commits in range |
| 7 | Extensions | `internal/hooks/` | **updated 2026-07-11 (P5c):** reconcile-on-reload for `kind:service` — identity key = extension id; fingerprint = run/env/cwd/restart_policy; decision table in `docs/architecture/reload.md`; board-create reload zero-churn. Prior P5b (service supervisor) already [built]. Hook/run decls remain frozen across reload. |
| 8 | What cards does NOT provide | (prose boundary, no owned source path) | n/a |

## Pointers into the cards docs

| Topic | Doc |
|---|---|
| Normative contract (data model, API, errors, events, atomics) | [`index.md`](../spec/index.md) |
| Vocabulary + use-case setups (workspaces, boards, card types) | [`index.md`](../concepts/index.md) |
| Events & integration design (mutation vs condition, monitors, feed, breaches) | [`integration.md`](../events/integration.md) |
| Runtime shape, package boundaries, storage | [`index.md`](../architecture/index.md) |
| MCP transport & tools (note the drift in §3 above) | [`mcp.md`](../extensions/mcp.md) |
| Extension declaration format & worked examples | [`index.md`](../extensions/index.md) |
| Workspace authoring (definitions, schema versioning, reload) | [`workspace-and-boards.md`](../reference/workspace-and-boards.md) |
| Design rationale & decisions (D-numbers) | [`philosophy.md`](../concepts/philosophy.md), [`design-notes.md`](../design-notes.md) |

*Verified against the source at the time of writing (see the Audit changelog
above for the per-section `git log` evidence through HEAD). Where this doc and
an older narrative doc disagree, this doc (read from code) wins — and the
discrepancy is a bug to file against the narrative doc.*
