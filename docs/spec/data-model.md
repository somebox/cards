# Data Model Specification

## 3. Workspace, storage, and deployment

### One workspace per instance

A `cards serve` process serves **exactly one workspace**. The workspace may be
assembled by merging multiple context files (see
[`index.md`](../architecture/index.md)), but the result is a single workspace
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

Definitions are JSON (`workspace.json`, `card-types/*.json`, `boards/*.json`).
Only `definitions/extensions.{yaml,json}` also accepts YAML. (`serve` mode
does not currently watch/reload on file change — restart the process to
pick up definition edits; an explicit reload endpoint is planned — see §11.)

### Storage model (default)

| Store | Role |
|-------|------|
| **SQLite** | Cards (JSON `fields` + denormalized index fields), events, links, comments, users, idempotency keys, FTS5 |
| **JSON files** | Source of truth for workspace, types, boards, and views; loaded and validated at startup. Extension declarations may also use YAML. |
| **Filesystem** | Artifact bytes; cards store `artifact` metadata only |

No separate document DB, broker, or cluster. Single-file DB is sufficient for
coordination scale (typically <100k active cards).

### Portable export/import

SQLite is authoritative. Two file-based escape hatches make the state
portable: a full-snapshot JSONL export/import (**implemented**) and a
markdown mirror for human review (**planned, not yet implemented**).

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

**Markdown mirror (planned, not yet implemented).** For per-card review the
design calls for a markdown mirror (`cards export --mirror`, `cards import
--mirror`, neither of which exist yet). Unlike the snapshot restore, mirror
import would be a per-file **PATCH** and **version-gated**: each file's
frontmatter declares the `version` it was edited from; a stale import would
be `409 version_conflict`, never a silent overwrite. An optional
`mirror.autoexport: true` setting would keep the mirror in sync on every
write. (Snapshot export/import shipped first; the markdown mirror is planned.)

### Deployment modes

| Mode | Description |
|------|-------------|
| **Embedded library** | Linked into an app or agent harness; in-process API; optional in-memory SQLite |
| **Sidecar** | `cards serve --workspace ./.work-cards`; host app is HTTP client |
| **Plugin** | Same binary: stdio MCP + optional local HTTP |

### Event delivery

> **Beta / in-progress — not yet stable.** The routes and payloads below
> are the target contract implemented in `internal/httpapi`/`internal/core`;
> the event-log seam is under active refactor (see
> [`index.md`](../events/index.md)). Treat this subsection as the design contract,
> not a certification of a finished build.

- **SSE (v1 design):** `GET /v1/events/stream?card_id=&board_id=&types=&actor=&owner=`.
  Supports `Last-Event-ID` (or `since=`) for resumable replay — a dropped
  connection replays events after the last acknowledged id. Filters: `card_id`,
  `board_id`, `types` (CSV), `actor` (events a user caused), `owner` (events on a
  user's cards). Event payload: `type`, `id`, `card_id`, `actor`, `at`, `diff`.
  (`board_ids`/`view_ids` are a proposed future enrichment — see index.md
  §11.3/Step 2 — not present in the current `Event` struct or wire format.)
  The live stream is **best-effort**: a slow
  consumer whose buffer fills is dropped (a `: dropped, reconnect` comment is
  sent); durable catch-up is the feed below, not the stream.
- **Catch-up feed (v1 design):** `GET /v1/events?actor=&owner=&type=&types=&board_id=&since=&cursor=&limit=`
  → `{ "items": [...], "next_cursor": "<id>" }`. A cursor-paged query over the
  append-only events table; the log of durable **facts** (mutation events plus
  any condition/monitor signal whose type is listed in `persist_conditions`)
  ordered by event id ascending. `since=` and `cursor=`
  are both event-id floors (`id >` value); `cursor=` is the pagination
  continuation and overrides `since=`. `next_cursor` is the last item's id, or
  empty when the feed is exhausted. `limit` defaults to 100, max 500.
  **Retention guarantee:** the events table is append-only and never trimmed, so
  the feed is a *complete*, gap-free durable log replayable from any id regardless
  of how long a consumer was disconnected. Recovery = page the feed from your last
  id until `next_cursor` is empty, then open the stream with `Last-Event-ID` set
  to that id.
- **Embedded:** in-process subscriber callbacks on mutation (no HTTP).

---

## 4. Core data model

### Workspace

Top-level scope. All cards belong to one workspace. **One process serves
exactly one workspace** — this is a locked, long-term contract, not a v1
limitation. Multi-tenancy is "run multiple processes" (each with its own
`--workspace`/port/SQLite file); there is no multi-workspace router in the
kernel and integrators should not design for one. Within a single workspace,
isolation between concerns is achieved with **boards** (filtered views over a
shared card pool, scoped by `card_type_ids`), not multiple workspaces. Shared
vocabulary:

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
  event_retention_days  int (optional)  // schema field exists; automatic trimming is not yet implemented (no background job reads it)
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
  wip_limits      map[column]int (optional)  // column -> max; crossing fires wip_exceeded/wip_cleared
  monitors        BoardMonitors (optional)   // reactive-condition watchers (see below)
  presentation    BoardPresentation (optional)
  theme           map[string]string (optional) // design-token overrides, whitelisted (docs/design-system.md §Theming)
  settings        { enforce_transitions: bool }
}

BoardMonitors {
  alert_when_empty    string[] (optional)     // columns to watch for lane_drained/lane_refilled
  emit_rejections     bool (optional)         // fire transition_rejected on refused status moves
  max_time_in_status  map[column]duration (optional)  // arm status_timeout (e.g. {"review":"168h"})
  idle_after          duration (optional)     // arm card_idle after no mutation for this long
}
```

Durations use Go's `time.ParseDuration` syntax plus a `d` (days) suffix it
lacks — e.g. `"168h"`, `"7d"`, `"72h"`. See docs/events/index.md for the
condition semantics.
`card_type_ids` is sugar: it is merged into `default_filter` as
`type_id $in [...]`. Either may be used; both may appear.

### View

A named filter plus optional URL binding — same cards as `/cards`. **Type
defined, not yet wired to a route** — `GET /views/:id/cards` described in
§11 is aspirational; there is no `/v1/views` route in the current router
(`internal/httpapi`). Treat View as a forthcoming feature.

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
| `parent` | directional | source belongs to target (membership/hierarchy) |

Both `depends-on` and `blocked-by` are stored on the *waiting* card, so a
card's outgoing edges answer "what am I waiting on?".

#### Modeling hierarchy (epic → story → task)

Only `depends-on` and `blocked-by` participate in blocked-ness; every other link
type is inert for it. So `parent` cleanly expresses **membership** without
conflating it with **prerequisite** — an epic is not a blocker of its own story.
Model a tree by pointing each child at its parent:

```bash
cards link add <story-id> --type parent --target <epic-id>
cards link add <task-id>  --type parent --target <story-id>
```

`parent` ships in the demo workspace's `link_types`; add it (or an `epic-of`
variant, optionally `source_types`/`target_types`-scoped) to any workspace. A
first-class parent field and a tree-rendering UI are on the roadmap; today the
hierarchy lives as ordinary typed edges you can already query and traverse.

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
`card_deleted`, `artifact_added`, `definition_reloaded`.

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

### Multi-value fields (`multiple: true`) **[built]**

An `enum` or `user` field may declare `"multiple": true`, making its value a
JSON **array of strings** instead of one scalar (v1 scope: enum + user only;
`card_link` multiple is a documented fast-follow). Normative contract:

- **A present multiple field is always a JSON array** — each element validated
  as the scalar would be (enum membership with `valid_options` on rejection;
  duplicates rejected loudly, never silently deduped). A scalar value for a
  multiple field (and an array for a single-value field) is a validation error.
- **An unset optional multiple field is ABSENT on the wire** — the key does not
  appear in `fields`; it is never `null` and never `[]`. Writing `null` or `[]`
  **unsets** the key (normalized once, in core, so every transport inherits
  it). `required: true` therefore means present **and** non-empty.
- A `default` for a multiple field must itself be a non-empty string array
  (elements checked against `options` for enums) — enforced at definition load.
- `multiple` is not supported inside repeating `item_fields` (v1).
- Filter by **membership** with the `$has` operator (see the query DSL §9).

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
  updated_at      timestamp       // server-set (any mutation)
  status_since    timestamp       // server-set; when the card entered its current status
  created_by      string
}
```

`status_since` is maintained server-side: set at creation and reset only on a
real status change (unaffected by other mutations), so it — not `updated_at` —
answers "how long has this been in review?". It arms the temporal condition
monitors (`status_timeout`; see §events). Server-set, never client-writable.

**Universal envelope** (not in `fields`): `id`, `workspace_id`, `type_id`,
`schema_version`, `title`, `status`, `owner`, `tags`, `links`, `comments`,
`version`, timestamps, `status_since`, `created_by`. Custom data lives in
`fields` only.

> **Note:** `GET /cards` (list) responses omit `links`/`comments` for
> performance (not loaded on the list path); `GET /cards/:id` includes them.
> Do not assume `links`/`comments` are present on list-page items.

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

