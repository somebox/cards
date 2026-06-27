# Work Cards — Design Specification

**Status:** v0.4 — implemented (beta). The v0.4 pass trimmed the field
catalog, locked single-workspace-per-instance, fixed link direction and
concurrency contracts, and made the event/error contracts normative. See
[`NOTES.md`](NOTES.md) for the full change log and rationale. The core
kernel, HTTP API, CLI, MCP server, web UI, and hook supervisor are built and
dogfooded; the API is stable for the current scope.

Work Cards is a small substrate for typed-card coordination. It stores cards,
events, links, and comments; validates writes against versioned schemas;
streams events; and exposes one HTTP/CLI/MCP surface. It is **not** a workflow
engine or a long-term archive — behavior beyond core CRUD lives in
**extensions** (independent processes in any language), see
[`EXTENSIONS.md`](EXTENSIONS.md).

The primary interface is a small, self-describing HTTP API and a CLI that
mirrors the same paths and flags. A web UI is a reference consumer rendered
from definitions; it is not part of the kernel.

For the principles behind these choices, see [`PHILOSOPHY.md`](PHILOSOPHY.md).

---

## 1. Design principles

1. **Schema is the process.** Workflow rules live in card-type definitions,
   authored rarely. The runtime validates, indexes, and records events.
2. **Introspection before action.** One call returns types (with versions),
   fields, columns, views, filters, tags, link types, and users.
3. **Strict on writes, forgiving on shape.** Values are validated strictly;
   comments and `text` fields remain the unstructured escape hatch.
4. **History is automatic and append-only.** Current state is a materialized
   projection; the event log is the coordination record (see §8).
5. **Fail loudly, guide recovery.** Rejections echo valid options and hints.
6. **Idempotent by default.** Mutations accept idempotency keys.
7. **Lightweight unless opted in.** Unconstrained status moves by default;
   transitions, strict field mode, and link-type constraints are opt-in.
8. **Coordination, not archive.** Keep blobs and canonical records in the
   workspace or host app; cards hold structure, links, pointers, and history.
9. **One grammar, two surfaces.** CLI flags and URL/query parameters use the
   same names (`--owner`, `filter`, `limit`, `cursor`).
10. **Raw API first, views as sugar.** Agents depend on `/cards` and
    introspection; domain-shaped URLs are resolved views, not a second model.

---

## 2. Design tensions (and how we resolve them)

### Flexibility vs. overhead
Schemas and board definitions live as JSON/YAML in the workspace; the runtime
is a thin validator plus SQLite for query and FTS. A board can expose one card
type and three columns or many types with enforced transitions — same core.

### Validation vs. openness
Strict values; optional `strict` for unknown fields; comments always available.
Structured-payload validation (JSON/YAML schemas, command specs, path
confinement) is **extension territory**; the core stores such payloads as
`text` or `string` and lets an extension validate and annotate.

### Card transitions / enforcement
Any→any by default; optional `transitions` per board or card type when
`enforce_transitions` is true.

### Discoverability for agents
The introspection endpoint returns the entire valid vocabulary; writes reject
unknown values and echo the valid set; card links validate existence and
optional type constraints; users must be registered; tags are a closed set
with an explicit propose path.

### Dynamic domain URLs vs. stable agent API
**Views** bind path patterns (e.g. `/orders/:order_id/parts`) to a filter
template resolved against the same query engine as `GET /cards`. No duplicate
data per board.

### Single workspace vs. multi-tenancy
One process serves one workspace (assembled from context files). Multi-tenancy
is multiple processes. See §3.

---

## 3. Workspace, storage, and deployment

### One workspace per instance

A `cards serve` process serves **exactly one workspace**. The workspace may be
assembled by merging multiple context files (see
[`ARCHITECTURE.md`](ARCHITECTURE.md)), but the result is a single workspace
with one SQLite file. Cards belong to exactly one workspace and are not moved
in place between workspaces (use export/import, which creates a new card id).

Running multiple workspaces = running multiple processes on different
ports/paths. The binary, CLI, and clients all take `--workspace`/`--url`, so
this is trivial and is the supported multi-tenancy path for v1.

### Workspace layout

```
{workspace}/
  work-cards.db          # SQLite: cards, events, links, FTS, index columns
  definitions/
    workspace.json       # columns, tag_set, link_types, settings
    card-types/          # one file per type (versioned filenames optional)
    boards/              # board.json per board
    views/               # optional view.json per view
    extensions.yaml      # optional; declared hooks, services, runs
    commands/            # optional; markdown-defined saved commands
  artifacts/             # optional; content-addressed or per-card subdirs
  mirror/                # optional; one markdown file per card (see §8)
  .cards/
    ext/                 # optional; extension scripts (any language)
    sessions/            # optional; agent session logs
```

Definitions are JSON or YAML. The loader normalizes both; `serve` mode reloads
on file change.

### Storage model (default)

| Store | Role |
|-------|------|
| **SQLite** | Cards (JSON `fields` + denormalized index fields), events, links, comments, users, idempotency keys, FTS5 |
| **JSON/YAML files** | Source of truth for types, boards, views; loaded and validated at startup |
| **Filesystem** | Artifact bytes; cards store `artifact` metadata only |

No separate document DB, broker, or cluster. Single-file DB is sufficient for
coordination scale (typically <100k active cards).

### Portable export/import

SQLite is authoritative. Two file-based escape hatches make the state portable;
both are **implemented**:

**Full-snapshot JSONL (backup / migration / disaster recovery).**
`cards export --workspace <dir>` dumps the whole workspace — a header line, then
users, cards (with embedded comments + links), then the full event log — as one
JSON object per line. `cards import --workspace <dir>` is the inverse: it
restores that snapshot into a **fresh** workspace DB, preserving card ids,
versions, and timestamps verbatim so links and history stay intact. It is a
restore, not a merge: import refuses a workspace that already holds cards, and a
duplicate card id is a hard error — never a silent overwrite. Both run locally
against SQLite with no server. Commit the JSONL alongside `definitions/` to make
the full workspace state git-portable.

**Markdown mirror (git/PR-style human review).** For per-card review the core
can maintain a markdown mirror (`cards export --mirror`, `cards import
--mirror`). Unlike the snapshot restore, mirror import is a per-file **PATCH**
and is **version-gated**: each file's frontmatter declares the `version` it was
edited from; a stale import is `409 version_conflict`, never a silent overwrite.
An optional `mirror.autoexport: true` setting keeps the mirror in sync on every
write. (Snapshot export/import shipped first; the markdown mirror is planned.)

### Deployment modes

| Mode | Description |
|------|-------------|
| **Embedded library** | Linked into an app or agent harness; in-process API; optional in-memory SQLite |
| **Sidecar** | `cards serve --workspace ./.work-cards`; host app is HTTP client |
| **Plugin** | Same binary: stdio MCP + optional local HTTP |

### Event delivery

- **SSE (v1):** `GET /events/stream?board=&card=&types=&since=`. Supports
  `Last-Event-ID` for resumable replay — a dropped connection replays events
  after the last acknowledged id. Event payload: `type`, `id`, `card_id`,
  `actor`, `at`, `diff`, optional `board_ids`/`view_ids`.
- **Embedded:** in-process subscriber callbacks on mutation (no HTTP).

---

## 4. Core data model

### Workspace

Top-level scope. All cards belong to one workspace. Shared vocabulary:

```
Workspace {
  id              string
  name            string
  columns         Column[]       // canonical status lanes
  tag_set         string[]
  link_types      LinkType[]     // may constrain source/target types
  users           User[]
  settings        WorkspaceSettings
}
```
```
WorkspaceSettings {
  enforce_transitions   bool (default false)
  strict_fields         bool (default true)
  tag_policy            enum { open, propose, locked }  // default propose
  event_retention_days  int (optional)  // trim old events; card snapshot kept
  default_user          string (optional)  // CLI/API alias "me"
}
```

### Board

A Kanban lens: a column subset, the card types shown, a default filter,
optional transitions, and UI hints. It does **not** own cards.

```
Board {
  id              string
  name            string
  description     text (optional)
  columns         string[]       // subset of workspace.columns
  card_type_ids   string[]       // sugar; merged into default_filter
  default_filter  Filter (optional)
  transitions     object (optional)  // from status -> [next statuses]
  presentation    BoardPresentation (optional)
  settings        { enforce_transitions: bool }
}
```
`card_type_ids` is sugar: it is merged into `default_filter` as
`type_id $in [...]`. Either may be used; both may appear.

### View

A named filter plus optional URL binding — same cards as `/cards`.

```
View {
  id           string
  board_id     string (optional)
  path         string             // e.g. "/orders/:order_id/parts"
  bind         object             // path param -> field constraint
  filter       Filter             // merged with bind params
  methods      string[] (default ["GET"])
}
```
Read-only in v1; writes go to `/cards/:id`.

### Column, User

```
Column { id: string, name: string }
User   { id: string, display_name?: string, kind: "human"|"agent", created_at: timestamp }
```
Open registration: claim a unique id. No auth, no roles in v1.

### LinkType, Link

```
LinkType {
  id            string
  name          string
  type          "directional" | "bidirectional"
  source_types  string[] (optional)  // card type ids allowed on the source
  target_types  string[] (optional)  // card type ids allowed on the target
}
```
```
Link {
  type_id     string
  target      string          // target card id
  note        string (optional)
  created_by  string
  created_at  timestamp
}
```
`type_id` must exist in workspace `link_types`. If the link type declares
`source_types`/`target_types`, both endpoints' card types must match; else
`target_card_type_mismatch` with the valid set echoed.

### Default link vocabulary

| id | Direction | Meaning (source → target) |
|----|-----------|---------------------------|
| `depends-on` | directional | source waits for target (ordering) |
| `blocked-by` | directional | source is hard-blocked by target |
| `related` | bidirectional | loose association |
| `sent-to` | directional | source dispatched to target asset |

Both `depends-on` and `blocked-by` are stored on the *waiting* card, so a
card's outgoing edges answer "what am I waiting on?".

### Comment

Universal on every card.

```
Comment { id: string, author: string, body: text, created_at: timestamp, edited_at?: timestamp }
```

### Event

Append-only. Every mutation produces an event with a normative `diff` (§8).

```
Event {
  id         string
  card_id    string
  type       EventType
  actor      string          // user id
  at         timestamp       // server-set
  diff       object          // shape per type, see §8
}
```
`EventType`: `card_created`, `field_updated`, `status_changed`, `owner_changed`,
`tags_changed`, `item_appended`, `item_updated`, `item_removed`, `link_added`,
`link_removed`, `comment_added`, `comment_edited`, `schema_upgraded`,
`artifact_added`, `definition_reloaded`.

### CardType (schema)

Types are defined at **workspace** level so multiple boards share them.

```
CardType {
  id              string
  name            string
  description     text (optional)
  schema_version  int             // monotonic per type_id; starts at 1
  fields          FieldDef[]
  allowed_columns string[] (optional)
  icon            string (optional)
  searchable_fields string[] (optional)
}
```
Versioned files (convention): `programming-task.json` (current),
`programming-task.v1.json` (immutable snapshot for old pins).

### Card

```
Card {
  id              string
  workspace_id    string
  type_id         string
  schema_version  int             // pinned; validation uses this
  title           string
  status          string          // workspace column id
  fields          object
  owner           string (optional)
  tags            string[]
  links           Link[]
  comments        Comment[]
  version         int             // optimistic concurrency; increments per mutation
  created_at      timestamp       // server-set
  updated_at      timestamp       // server-set
  created_by      string
}
```

**Universal envelope** (not in `fields`): `id`, `workspace_id`, `type_id`,
`schema_version`, `title`, `status`, `owner`, `tags`, `links`, `comments`,
`version`, timestamps, `created_by`. Custom data lives in `fields` only.

### Definition merge and precedence

Validation layers add restrictions; higher layers do not replace lower.

1. **Workspace**: columns, users, tags, link types, defaults.
2. **Card type**: `fields`, `allowed_columns`, optional type `transitions`.
3. **Board** (when `board_id` context applies): board column subset,
   `default_filter`, optional board `transitions`, board enforcement.
4. **Card instance**: pinned `schema_version`, current values, `version`.

- Column validity: workspace → type `allowed_columns` → board subset.
- Transition validity: if enforcement off, no graph check; if on, board
  `transitions` override type `transitions`.
- Link validity: workspace `link_types` (+ `source/target_types`); `card_link`
  fields may add tighter `target_type`.

---

## 5. Schema versioning

Pure **versioned snapshots**. Each `schema_version` is an immutable field list;
a card pins one and validates against it.

1. Monotonic `schema_version` per `type_id`. Introspection returns
   `current_schema_version` and, when available, old-version schemas.
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

## 6. Field types

Core v1 catalog (see [`NOTES.md`](NOTES.md) D2 for what was trimmed and why):

| Type | Description | Validation |
|------|-------------|------------|
| `string` | Single-line text | Non-empty if required |
| `text` | Multi-line; rendered as markdown | Non-empty if required |
| `number` | Int/float | Numeric; optional `min`/`max` |
| `date` | ISO date/datetime | Parseable; optional `min`/`max` |
| `enum` | Single-select | Must be in `options`; else rejected with options |
| `tags` | Multi-select | Each must be in workspace `tag_set` |
| `user` | User reference | Must exist; else rejected with registration hint |
| `card_link` | Card reference | Target exists; optional `target_type`, `link_type` |
| `repeating` | Array of typed entries | Each entry validated against `item_fields` (no nested `repeating` in v1); entries have stable server-generated `entry_id` |
| `artifact` | Pointer to blob in workspace or external URI | `{ uri, mime?, size?, sha256? }`; local `uri` must resolve under workspace artifacts root when `artifact_policy: local` |

```
FieldDef {
  id          string
  label       string
  type        FieldType
  required    bool
  default     any (optional)
  description string (optional)
  // type-specific: enum.options; number/date min,max;
  // card_link target_type, link_type; repeating item_fields;
  // artifact artifact_policy
}
```

Use `artifact` for g-code, logs, exports; keep card JSON small.

> **Beyond core is extension territory.** JSON/YAML payload validation, path
> confinement, and executable command specs are not core field types. Store
> such content as `text`/`string`/`artifact` and let an extension validate and
> annotate (see [`EXTENSIONS.md`](EXTENSIONS.md)).

---

## 7. Card-type examples

### Programming task

```json
{
  "id": "programming-task",
  "name": "Programming Task",
  "schema_version": 1,
  "fields": [
    { "id": "description", "type": "text", "required": true },
    { "id": "branch", "type": "string", "required": true },
    { "id": "pull_request_url", "type": "string", "required": false },
    { "id": "assignee", "type": "user", "required": false },
    {
      "id": "work_log", "type": "repeating", "required": false,
      "item_fields": [
        { "id": "commit_hash", "type": "string", "required": true },
        { "id": "notes", "type": "text", "required": false },
        { "id": "author", "type": "user", "required": true },
        { "id": "timestamp", "type": "date", "required": true }
      ]
    },
    { "id": "definition_of_done", "type": "text", "required": false }
  ],
  "allowed_columns": ["todo", "in_progress", "review", "done"]
}
```

### Research goal

```json
{
  "id": "research-goal", "name": "Research Goal", "schema_version": 1,
  "fields": [
    { "id": "hypothesis", "type": "text", "required": true },
    { "id": "researcher", "type": "user", "required": false },
    {
      "id": "sources", "type": "repeating", "required": false,
      "item_fields": [
        { "id": "url", "type": "string", "required": true },
        { "id": "query", "type": "string", "required": false },
        { "id": "findings", "type": "text", "required": false },
        { "id": "checked_at", "type": "date", "required": true }
      ]
    },
    { "id": "conclusion", "type": "text", "required": false }
  ],
  "searchable_fields": ["hypothesis", "conclusion"]
}
```

### Printer job (fabrication)

```json
{
  "id": "printer-job", "name": "Printer Job", "schema_version": 1,
  "fields": [
    { "id": "gcode_ref", "type": "artifact", "required": true,
      "description": "Pointer to g-code in workspace artifacts/" },
    { "id": "material", "type": "enum", "required": true,
      "options": ["PLA", "PETG", "ABS", "TPU"] },
    { "id": "quantity", "type": "number", "required": true, "min": 1 },
    { "id": "assigned_printer", "type": "card_link", "required": false,
      "target_type": "printer" },
    {
      "id": "status_updates", "type": "repeating", "required": false,
      "item_fields": [
        { "id": "state", "type": "enum", "required": true,
          "options": ["queued", "printing", "paused", "completed", "failed"] },
        { "id": "reported_at", "type": "date", "required": true },
        { "id": "note", "type": "text", "required": false }
      ]
    }
  ],
  "allowed_columns": ["queued", "printing", "qa", "done"]
}
```
Machine-specific payload validation (g-code well-formedness, machine profile
schemas) is extension territory; the card holds the `artifact` pointer and a
`repeating` telemetry log.

---

## 8. History, events, and retention

- Append-only **events** table; materialized **cards** row updated in the same
  transaction.
- Query: per card, per workspace feed, by actor/type/time.
- **Not an archive:** the **materialized card (including repeating fields) is
  the durable work product.** The **event log is the audit/coordination
  layer** and may be trimmed via `event_retention_days` (the card snapshot and
  artifacts are always kept). Export to git or the host app for long-term
  records.

### Normative `diff` per event type

| Event type | `diff` |
|------------|--------|
| `card_created` | `{ card: { id, type_id, title, status } }` |
| `field_updated` | `{ field, before, after }` |
| `status_changed` | `{ before, after }` |
| `owner_changed` | `{ before, after }` |
| `tags_changed` | `{ added: [], removed: [] }` |
| `item_appended` | `{ field, entry_id, entry, index }` |
| `item_updated` | `{ field, entry_id, before, after }` |
| `item_removed` | `{ field, entry_id, entry }` |
| `link_added` | `{ type_id, target, note }` |
| `link_removed` | `{ type_id, target }` |
| `comment_added` | `{ comment_id }` |
| `comment_edited` | `{ comment_id, before, after }` |
| `schema_upgraded` | `{ from, to }` |
| `artifact_added` | `{ field, uri, sha256 }` |
| `definition_reloaded` | `{ kind: "workspace"|"board"|"card_type", id }` |

### History as agent-resumption context

Because events are structured and faithful, `GET /cards/:id/history` renders a
concise timeline an agent ingests to resume interrupted work. This is the
thing that makes "take a task, get preempted, come back" work across processes
— a unique value vs. traditional ticket tools (which forget) and in-framework
agent state (which is ephemeral).

---

## 9. Query and filter DSL

### First-class query parameters

| Parameter | Meaning |
|-----------|---------|
| `type_id` | One or more types |
| `status` | Column id(s) |
| `owner` | User id; alias `me` → `default_user` |
| `tag` | Tag(s) |
| `q` | Full-text search (FTS5) |
| `updated_before`, `updated_after` | ISO timestamps |
| `created_before`, `created_after` | ISO timestamps |
| `has_link` | Link type id present |
| `link_target` | Card id linked |
| `blocked` | Shorthand: outgoing `blocked-by`/`depends-on` to a non-`done` card |
| `board_id` | Apply board `default_filter` + type/column scope |

Pagination: `limit` (default 50, max 200), `cursor` (opaque; sort
`updated_at`, `id`).

### Filter JSON (`filter=`)

jq-*like*, compiled to SQL safely (not full jq):

```json
{
  "$and": [
    { "owner": { "$eq": "me" } },
    { "status": { "$nin": ["done", "cancelled"] } },
    { "fields.priority": { "$eq": "high" } }
  ]
}
```
Operators: `$eq`, `$ne`, `$in`, `$nin`, `$gt`, `$gte`, `$lt`, `$lte`,
`$contains`, `$and`, `$or`. Paths: `fields.<id>` for typed fields; top-level
keys for `status`, `owner`, `type_id`, `tag`, `updated_at`. CLI:
`cards list --filter-file q.json`. Power users: `cards export --format jsonl`
and local jq out of band.

### Recipes
- **Open assigned to me:** `owner=me&status=todo,in_progress`.
- **Blocked stale:** `blocked=true&updated_before=<now-1h>`.

---

## 10. Validation and anti-hallucination

Rules:

1. **Unknown enum value** → `unknown_enum`, echo `options`.
2. **Unknown tag** → `unknown_tag`, echo `tag_set` (+ `propose_tag` hint).
3. **Unknown user** → `unknown_user`, include registration call.
4. **Unknown field** (strict mode) → `unknown_field`, echo field list.
5. **card_link to missing card** → `target_card_missing`, echo target type +
   search hint.
6. **Link type/source/target mismatch** → `target_card_type_mismatch`, echo
   valid `source_types`/`target_types`.
7. **Missing required field** → `validation_failed`, list missing fields.
8. **Repeating entry missing required sub-field** → per-entry rejection with
   `entry_id`/index.
9. **Schema version mismatch on write** → `schema_version_mismatch`, echo
   `current_schema_version` + upgrade hint.
10. **Optimistic concurrency:** stale `version` → `version_conflict` (`409`)
    with current card.
11. **Illegal transition** (enforced) → `transition_illegal`, echo allowed
    next statuses.
12. **No actor on a write** → `actor_required` (`403`).

`dry_run: true` validates fully and returns the would-be card + warnings,
writing nothing. Errors are structured JSON:

```json
{
  "error": "unknown_enum",
  "field": "status",
  "value": "In-Progress",
  "message": "Unknown status. Use a board column id.",
  "valid_options": ["todo", "in_progress", "review", "done"],
  "hint": "See GET /workspace"
}
```

### Error catalog

| `error` | HTTP | Carries |
|---------|------|---------|
| `validation_failed` | 422 | `field[]`, `message` |
| `unknown_enum` | 422 | `field`, `value`, `valid_options` |
| `unknown_tag` | 422 | `value`, `valid_options` (`tag_set`) |
| `unknown_user` | 422 | `value`, hint |
| `unknown_field` | 422 | `field`, `valid_options` |
| `target_card_missing` | 422 | `value`, `target_type`, hint |
| `target_card_type_mismatch` | 422 | `value`, `valid_options` |
| `transition_illegal` | 422 | `from`, `valid_options` |
| `schema_version_mismatch` | 422 | `current_schema_version`, hint |
| `version_conflict` | 409 | current `card` |
| `actor_required` | 403 | hint |
| `not_found` | 404 | `resource` |
| `idempotency_replay` | 200 | original response (on retry) |

---

## 11. API surface (v1)

Base path: `/v1`. JSON in/out. Mutations accept `Idempotency-Key` header or
`idempotency_key` body field.

### Workspace and definitions
- `GET /workspace` → workspace + current card types (all versions summary) +
  boards, views, settings.
- `GET /workspace/card-types/:type_id?version=` → schema.
- `POST /workspace/reload` → reload definitions from disk.

### Boards and views
- `GET /boards`, `GET /boards/:id`.
- `GET /views/:id/cards` → paginated cards (filter + bind).

### Users
- `POST /users` → register (workspace-scoped).

### Cards (canonical)
- `GET /cards` → search/filter/paginate (primary agent entry).
- `POST /cards` → create (`type_id`, `title`, `fields`, `status?`, `tags?`,
  `schema_version?`). `dry_run` supported.
- `GET /cards/:id` → full card + `version`.
- `PATCH /cards/:id` → fields/status/owner/tags; requires `version` (body) or
  `If-Match` (alias). `dry_run`.
- `POST /cards/:id/upgrade-schema` → bump pinned version.

### Coordination atomics
These ship in core because they need atomicity hard to replicate from outside.
- `POST /cards/:id/claim` → set `owner` (+ optional `status`) via
  compare-and-set on `version`; `409` if already owned by another actor.
- `POST /cards/take-next` → body `{ filter, assign_to, status? }`. Picks the
  oldest matching unowned card (`updated_at ASC, id ASC`), atomically claims
  it, returns it. `200 { card: null }` when nothing matches. `409` on a race
  → client retries. Same `Idempotency-Key` returns the same card.

### Repeating fields (addressed by `entry_id`)
- `POST /cards/:id/fields/:field/append` → append; returns `entry_id`.
- `PATCH /cards/:id/fields/:field/:entry_id` → update entry.
- `DELETE /cards/:id/fields/:field/:entry_id` → remove entry.

### Links, comments, artifacts
- `POST /cards/:id/links` / `DELETE /cards/:id/links/:type_id/:target`.
- `POST /cards/:id/comments` / `PATCH /cards/:id/comments/:comment_id`.
- `POST /cards/:id/artifacts` → store file, set/update an `artifact` field.

### Batch
- `POST /cards/batch` → array of mutations; shared idempotency scope;
  `mode: all_or_nothing | partial`.

### History and streams
- `GET /cards/:id/events?…`
- `GET /cards/:id/history` → resumption-ready timeline projection.
- `GET /events?workspace=&since=&limit=` → workspace feed (cursor).
- `GET /events/stream?…` → SSE with `Last-Event-ID` replay.

Write responses include the updated card (or batch results) to avoid extra
GETs.

---

## 12. Actors and authorization

- Every write supplies an actor via the **`X-Work-Cards-Actor`** header (or
  `actor` body field alias). Resolution: header → `CARDS_USER` env → workspace
  `default_user` → `403 actor_required`.
- The server sets `created_by` and event `actor` from the resolved identity.
  In the trusted-local model, the configured identity is trusted; stronger
  identity binding (signed tokens, per-user keys) is an extension/host concern.
- `claim`/`take-next` use the actor as the new `owner`.
- `created_at`, `updated_at`, and event `at` are **server-set only**; clients
  cannot supply them.

---

## 13. Agent ergonomics and the coordination loop

The **agent coordination loop** is the system's organizing concept:

> **introspect** (`GET /workspace`) → **take-next** (claim a task) → **work**
> (append evidence to `repeating` fields, add artifacts) → **transition**
> (move status) → **comment** (handoff) → repeat; **resume** from history
> after interruption.

The API is shaped so each step is one call, with self-correcting errors. The
loop drives MCP tool grouping ([`MCP.md`](MCP.md)), reference skills, and the
lifecycle examples ([`LIFECYCLE-EXAMPLES.md`](LIFECYCLE-EXAMPLES.md)).

| Interface | Notes |
|-----------|-------|
| **REST** | Source of truth; filters and SSE for reactive agents |
| **CLI** | 1:1 with REST |
| **MCP** | Typed tools from workspace introspection (one create tool per card type) |
| **Skills** | `take-and-work`, `append-commit-and-PR`, `upgrade-schema`, `resume-from-history` |
| **Web UI** | Renders from `BoardPresentation` + field types; read-mostly |

Ergonomics guarantees: idempotency keys on all mutations; structured errors
with `valid_options`; dry-run before commit; full card in responses; stable
string ids; `version` for optimistic concurrency; SSE replay via
`Last-Event-ID`.

---

## 14. Open questions

1. **Cross-workspace links.** Defer; v1 is single workspace per instance.
2. **Cross-board column names.** Workspace-wide columns only; alias map later.
3. **Webhook outbound.** SSE covers many cases; signed webhooks for serverless
   workers in a future revision.
4. **Human-only columns.** Opt-in board rule: only `kind: human` users may
   move to listed columns.
5. **Nested repeating fields.** Still deferred for v1.
6. **View write routes.** v1 views are read-only.
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
- Append-only events and SSE streaming (with replay).
- Storage (SQLite + FTS5) and the optional version-gated mirror.
- Idempotency, optimistic concurrency, dry-run.
- HTTP, CLI, MCP, and RPC surfaces sharing one service layer.
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

See [`EXTENSIONS.md`](EXTENSIONS.md).

### Intentionally absent from v1
- Jira-grade permissions, ACLs, SSO.
- Built-in automation engine or workflow DSL (use hooks).
- Graphical schema designer (JSON/YAML in `definitions/`).
- Presence / live cursors.
- Server-side full jq (use `cards export | jq`).
- Unlimited event retention (coordination focus, not archive).
- In-place card moves between workspaces.
- In-process plugins (extensions are external processes).
- Structured-payload field types (`json`/`yaml`/`path`/`command`) —
  extension territory; core stores them as `text`/`string`/`artifact`.

**Thesis:** a small typed kernel, SQLite indexing, JSON/YAML definitions,
event streams for reactions, schema versioning for evolution, views for
domain-shaped reads — and extensions for everything else.
