# API Surface Specification

## 5. Schema versioning

Pure **versioned snapshots**. Each `schema_version` is an immutable field list;
a card pins one and validates against it.

1. Monotonic `schema_version` per `type_id`. Introspection returns
   `current_schema_version` per type. (Serving old-version schemas alongside
   current — e.g. via `GET /workspace/card-types/:type_id?version=` — is
   described in §11 but not yet implemented; see that section's status note.)
2. Each card pins `schema_version` (default: current at create).
3. Validation uses the pinned version on PATCH/append.
4. **Additive (minor):** new optional fields in N+1. Cards on N stay valid;
   upgrade optional.
5. **New required fields:** only in a new version; existing cards are not
   forced until upgraded.
6. **Removed fields:** absent from the new version's snapshot. Old-version
   cards keep the field (they validate against their own snapshot). The
   `deprecated: true` flag is optional **within the current version** for
   advance warning only — it is not how removal works.
7. **Enum changes:** new values allowed in the new version; old cards may
   retain removed values until edited.
8. **Repeating `item_fields`:** new appends validate against the pinned
   version. Existing entries are not re-validated unless the card is upgraded.

### Upgrading

`POST /cards/:id/upgrade-schema` with optional `target_version` (default:
current). Applies `field_defaults` from the type's optional `migrations` block,
bumps `schema_version`, emits `schema_upgraded`. `dry_run` supported.

### Migrations (authoring, optional)

```json
"migrations": {
  "2": { "from": 1, "summary": "Track PR URL before review",
         "field_defaults": { "pull_request_url": null } }
}
```
Runtime applies only `field_defaults`; it does not rewrite history.

Reloading definitions from disk does not mutate cards.

---

## 11. API surface (v1)

Base path: `/v1`. JSON in/out. Mutations accept an `Idempotency-Key`
header. (There is no `idempotency_key` body-field alias in the current
implementation — header only.)

### Workspace and definitions
- `GET /workspace` → workspace + current card types (current version per
  type only) + boards + settings. **Does not currently include `views`.**
- `GET /boards/:board_id` → one board's definition (columns, card types,
  default filter, WIP limits); 404 for unknown ids.
- `GET /workspace/card-types/:type_id?version=` → **not yet implemented**;
  card-type schemas are only available via the `card_types` map in
  `GET /workspace` (current version only).
- `POST /workspace/reload` → **implemented** on `cards serve` (`cmd/cards/reload.go`):
  re-loads definitions, swaps the live Service/router, emits `definition_reloaded`;
  failed reload returns 422, emits `definition_reload_failed`, and keeps the prior
  generation. Optional `cards serve --watch` polls `definitions/` (fingerprint
  hash, no fsnotify) and reloads on the same path. CLI: `cards reload`.
  Contract: `docs/architecture/RELOAD.md`.

### Boards and views
- `GET /boards/:board_id` → **implemented** (see "Workspace and definitions"
  above): one board's definition. Boards are also embedded in the `boards`
  map of `GET /workspace`.
- `GET /views/:id/cards` → **not yet implemented**. Named views
  (`presentation.filters`) are applied by the HTML board UI but have no
  dedicated JSON route.

### Conditions
- `GET /breaches?board_id=&type=` → **implemented**: the current-conditions
  catch-up query — which board columns exceed their WIP limit, which watched
  lanes are drained, and which cards are blocked right now. Returns
  `{as_of, items:[{type, scope, board_id?, card_id?, column?, count?, limit?,
  blockers?}]}`. The counterpart to the ephemeral condition signals on the
  SSE stream (`GET /events/stream`); does not yet include temporal conditions
  (`status_timeout`/`card_idle`). See docs/events/INTEGRATION.md.

### Users
- `POST /users` → register (workspace-scoped).

### Cards (canonical)
- `GET /cards` → search/filter/paginate (primary agent entry). Filter params
  include `board_id`, `type_id`, `status`, `owner`, `blocked`, `has_link`,
  `link_target`, and `q` (FTS). **`sort`** orders the result with a flat
  grammar — one key (`created_at`, `updated_at`, `title`, or `fields.<id>`),
  optional leading `-` for descending; NULLs (cards missing the field) sort
  last; an unsupported key is a `422`. `sort` and `cursor` are **mutually
  exclusive** (`422` if both given): keyset pagination is welded to the default
  `updated_at` order, so a custom sort returns no `next_cursor`. Default order
  (no `sort`) is `updated_at DESC`.
- `POST /cards` → create (`type_id`, `title`, `fields`, `status?`, `tags?`,
  `schema_version?`). `dry_run` supported.
- `GET /cards/:id` → full card + `version`.
- `PATCH /cards/:id` → fields/status/owner/tags; requires `version` in
  the request body (optimistic concurrency). (There is no `If-Match` header
  alias in the current implementation.) `dry_run` supported (body field;
  signaled back via a `Dry-Run: true` response header, not a body field).
- `DELETE /cards/:id` → remove a card, appending a `card_deleted` tombstone to
  the append-only event log (history survives; dependent cards are re-evaluated
  for `card_unblocked`). Optional optimistic-concurrency guard via `?version=`
  (409 on mismatch); omit it to delete unconditionally. Honors `Idempotency-Key`.
  Returns the deleted card; a second delete is a `404`.
- `POST /cards/:id/upgrade-schema` → bump pinned version.

### Coordination atomics
These ship in core because they need atomicity hard to replicate from outside.
- `POST /cards/:id/claim` → set `owner` (+ optional `status`) via
  compare-and-set on `version`; `409` if already owned by another actor.
- `POST /cards/take-next` → body `{ filter?, assign_to, status?,
  type_id?, board_id? }`. `type_id`/`board_id` narrow the candidate pool in
  addition to `filter`. Picks the
  oldest matching unowned card (`updated_at ASC, id ASC`), atomically claims
  it, returns it. `200 { card: null }` when nothing matches. Same
  `Idempotency-Key` returns the same card.
- **No-double-claim guarantee.** `claim`/`take-next` run inside a single
  `BEGIN IMMEDIATE` transaction and update with a guard —
  `UPDATE … WHERE id=? AND (owner IS NULL OR owner='')`. Under *N* concurrent
  callers exactly one update affects a row; the rest see zero rows affected and
  do **not** claim. A single card can never be handed to two owners, regardless
  of concurrency (the single writer connection serializes commits as well).
  Note the current limitation: a racing loser on `take-next` receives
  `200 { card: null }` (it cannot yet distinguish "raced" from "queue empty");
  retrying to the *next* candidate within the same call is a tracked
  enhancement, not yet shipped — until then a caller that got `null` under
  contention should re-issue `take-next`.

### Repeating fields (addressed by `entry_id`)
- `POST /cards/:id/fields/:field/append` → append; returns `entry_id`.
- `PATCH /cards/:id/fields/:field/:entry_id` → update entry.
- `DELETE /cards/:id/fields/:field/:entry_id` → remove entry.

(`version` travels in the JSON body for `append`/`PATCH`; for `DELETE` —
which has no body per HTTP convention — it is a `?version=` query parameter
instead.)

### Links, comments, artifacts
- `POST /cards/:id/links` / `DELETE /cards/:id/links/:type_id/:target`.
- `POST /cards/:id/comments` / `PATCH /cards/:id/comments/:comment_id`.
- `POST /cards/:id/artifacts` → store file, set/update an `artifact` field.
  **[not yet implemented — no route registered; see §6]**

### Batch (proposed, not implemented)
A future `POST /cards/batch` may accept an array of mutations with shared
idempotency scope and `mode: all_or_nothing | partial`. **No such route
exists in the current router.**

### History and streams
- `GET /cards/:id/events?…`
- `GET /cards/:id/history` → resumption-ready timeline projection.
- `GET /events?actor=&owner=&type=&types=&board_id=&since=&cursor=&limit=` → cursor-paged
  catch-up feed (append-only, gap-free; see §3 Event delivery).
- `GET /events/stream?…` → SSE with `Last-Event-ID` replay.

Both `/cards/:id/events` and `/cards/:id/history` return `{"items":[...]}`
with a default/max `limit` but **no `next_cursor`** — there is currently no
way to page past the first page of a single card's event/history list.
(Contrast with the workspace-wide catch-up feed `GET /events`, which is
properly cursor-paginated.)

Write responses include the updated card (or batch results) to avoid extra
GETs.

---

## 13. Agent ergonomics and the coordination loop

The **agent coordination loop** is the system's organizing concept:

> **introspect** (`GET /workspace`) → **take-next** (claim a task) → **work**
> (append evidence to `repeating` fields, add artifacts) → **transition**
> (move status) → **comment** (handoff) → repeat; **resume** from history
> after interruption.

The API is shaped so each step is one call, with self-correcting errors. The
loop drives MCP tool grouping ([`MCP.md`](../extensions/MCP.md)), reference skills, and the
lifecycle examples ([`LIFECYCLE-EXAMPLES.md`](../examples/LIFECYCLE-EXAMPLES.md)).

| Interface | Notes |
|-----------|-------|
| **REST** | Source of truth; filters and SSE for reactive agents |
| **CLI** | Mirrors REST paths/flags for most operations; a few REST routes (e.g. `release`) currently have no CLI command, and `--dry-run` coverage is inconsistent across write commands — see DEVELOPER-REFERENCE.md for the current gap list. |
| **MCP** | Typed tools from workspace introspection (one create tool per card type). Fixed tools include mutations (`claim`, `release`, entry/link/comment CRUD, `upgrade_schema` with `confirm:true` apply gate), `history`/`events`/`breaches`, and artifact attach/get. Still a **strict subset** of REST/CLI: no SSE/event streaming or user registration over MCP; no idempotency-key forwarding. See `internal/mcp/README.md` + MCP.md before assuming full parity. |
| **Skills** | `take-and-work`, `append-commit-and-PR`, `upgrade-schema`, `resume-from-history` |
| **Web UI** | Renders from `BoardPresentation` + field types. Inline click-to-edit on the card modal/detail (title/status/owner/tags/scalar fields) saves via `POST /ui/cards/{id}/save` with optimistic-concurrency `version`; drag-drop moves and unclaim call the `/v1` API. Board-scoped theming via `Board.theme` (design-system token overrides). See `docs/DESIGN.md`. |

Ergonomics guarantees (HTTP/CLI): idempotency keys on POST/PATCH mutations
(not DELETE; see §11); structured errors with `valid_options`; dry-run before
commit on create/patch/upgrade-schema; full card in responses; stable string
ids; `version` for optimistic concurrency; SSE replay via `Last-Event-ID`.
**MCP tools currently support none of idempotency-key, dry-run** — see MCP.md
gap list — agents using MCP get none of these two guarantees and must be
written defensively (e.g. check before retry).

---

## 14. Open questions

1. **Cross-workspace links.** Defer; v1 is single workspace per instance.
2. **Cross-board column names.** Workspace-wide columns only; alias map later.
3. **Webhook outbound.** SSE covers many cases; signed webhooks for serverless
   workers in a future revision.
4. **Human-only columns.** Opt-in board rule: only `kind: human` users may
   move to listed columns.
5. **Nested repeating fields.** Still deferred for v1.
6. **View write routes.** Views are read-only by design once implemented
   (writes go to `/cards/:id`) — note views themselves (`GET /views/:id/cards`)
   are not yet implemented; see §11 status note.
7. **Definition-of-Done gating.** Candidate extension: a `repeating` checklist
   + opt-in `enforce_dod` rule blocking `done` until all items checked.

---

## 15. Core vs extensions

The spec describes the **core kernel**: the smallest substrate to coordinate
typed cards across agents and tools. Anything implementable as an external
process talking to the API belongs in an **extension**.

### Core owns
- Cards, fields, links, comments, columns, users.
- Schema validation and versioning.
- Transition rules (opt-in).
- Append-only events and SSE streaming (with replay) — **design complete,
  beta/in-progress; see Status line and §3.**
- Storage (SQLite + FTS5) and the optional version-gated mirror **(mirror:
  planned, not yet implemented — see §3)**.
- Idempotency, optimistic concurrency, dry-run.
- HTTP, CLI, and MCP surfaces sharing one service layer.
- Coordination atomics (`claim`, `take-next`).
- Extension discovery and optional supervision.

### Extensions own
- Workflow automation, plan/approval flows, escalation, SLA timers.
- CI dispatch, deployment, agent session spawning.
- External sync (GitHub, Linear, Slack, Sentry).
- Custom validation beyond the core field catalog (JSON/YAML schemas, path
  confinement, command execution contracts).
- Report generation, document assembly, exports.
- UI backends (a bundled web UI is one example consumer).
- Semantic search, embeddings, similarity.
- Background processing, queues, schedulers.

See [`EXTENSIONS.md`](../extensions/EXTENSIONS.md).

### Intentionally absent from v1
- Jira-grade permissions, ACLs, SSO.
- Built-in automation engine or workflow DSL (use hooks).
- Graphical schema designer (core JSON definitions; extension YAML where supported).
- Presence / live cursors.
- Server-side full jq (use `cards export | jq`).
- Unlimited event retention (coordination focus, not archive).
- In-place card moves between workspaces.
- In-process plugins (extensions are external processes).
- Structured-payload field types (`json`/`yaml`/`path`/`command`) —
  extension territory; core stores them as `text`/`string`/`artifact`.

**Thesis:** a small typed kernel, SQLite indexing, JSON core definitions (plus
extension YAML where supported), event streams for reactions, schema versioning
for evolution, views for domain-shaped reads — and extensions for everything
else.
