# Developer reference — schemas, boards, and transitions

How to define and configure Work Cards for a workspace: what is fixed on every
card, what is schema-defined, how workspace/board/type rules merge, how status
transitions and links work, and how versions evolve.

Principles: [`PHILOSOPHY.md`](PHILOSOPHY.md). Extension model:
[`EXTENSIONS.md`](EXTENSIONS.md). Normative API details:
[`SPEC.md`](SPEC.md). Architecture and packaging:
[`ARCHITECTURE.md`](ARCHITECTURE.md). Walkthroughs:
[`LIFECYCLE-EXAMPLES.md`](LIFECYCLE-EXAMPLES.md). Design notes:
[`NOTES.md`](NOTES.md).

---

## 1. Is a card schema fully flexible?

**No.** Every card shares a **universal envelope** managed by the runtime.
Custom behavior lives in **`fields`** (and optional board/type rules). Card
types only define the shape of `fields` plus type-level constraints.

### Universal (every card)

| Property | On create | On update | Notes |
|---|---|---|---|
| `id` | Server-generated | Immutable | Stable string; URLs and links |
| `workspace_id` | Set from workspace | Immutable | One workspace per instance (v1) |
| `type_id` | Required | Immutable | Must match a defined card type |
| `schema_version` | Defaults to current | Via `upgrade-schema` only | Pins validation rules |
| `title` | Required | Optional PATCH | Search, UI, FTS; not in `fields` |
| `status` | Required (or first allowed column) | PATCH | Always a workspace **column id** |
| `fields` | Per type schema | PATCH / append APIs | Typed custom data |
| `owner` | Optional | PATCH | Registered user id; assignment field |
| `tags` | Optional | PATCH | Subset of workspace `tag_set` |
| `links` | Optional | Link APIs | Typed edges to other cards |
| `comments` | — | Comment APIs | Markdown; not part of type schema |
| `version` | `1` | Increments per mutation | Optimistic concurrency |
| `created_at`, `updated_at`, `created_by` | System | System | Server-set only |

`owner` is the canonical assignment field used by built-in filters (`owner=me`)
and `claim`/`take-next`. Multiple assignees or reviewer roles are modeled as
additional schema fields (`user` or `repeating` with `user` entries).

Agents introspect **card types** for `fields`; they introspect **workspace**
for `columns`, `link_types`, and `tag_set`. Do not redefine `title` or
`status` inside `fields`.

### Schema-defined (per card type)

Everything in the card type's `fields[]` array: ids, labels, types, required
flags, enums, repeating `item_fields`. Values live under `card.fields` keyed by
field id.

### Flexibility you get

- Any number of custom fields and repeating sequences.
- Per-type column subset (`allowed_columns`).
- Per-board or per-type transition graphs (optional; `transitions`).
- Board-specific presentation without changing types.

### What you do not get (v1)

- Per-type status machines with different column *names* on one workspace
  (columns are workspace-wide; types restrict **subset** only).
- Nested `repeating` inside repeating items.
- Structured-payload field types (`json`/`yaml`/`path`/`command`) — extension
  territory. Store as `text`/`string`/`artifact`; validate via extension.
- Agents authoring card types (definitions are human/harness-owned JSON/YAML).

### How workspace, board, and type rules merge

Validation is layered; later layers add restrictions:

1. **Workspace**: columns, users, tags, link types, defaults.
2. **Card type**: field schema, `allowed_columns`, optional type `transitions`.
3. **Board** (when `board_id` is used): board columns subset, default filter,
   optional board `transitions`, board enforcement settings.
4. **Card instance**: pinned `schema_version`, current values, optimistic
   `version`.

Resolution rules:

- Column must exist in workspace, then pass type `allowed_columns`, then pass
  board `columns` if board-scoped.
- If transition enforcement is on, board `transitions` are used when present;
  otherwise type `transitions`.
- Link `type_id` must exist in workspace `link_types`; `card_link` fields may
  add tighter `target_type`; link types may constrain `source_types`/
  `target_types`.

---

## 2. Workspace definition

File: `definitions/workspace.json` (or `.yaml`)

```json
{
  "id": "demo",
  "name": "Demo workspace",
  "columns": [
    { "id": "backlog", "name": "Backlog" },
    { "id": "todo", "name": "To Do" },
    { "id": "in_progress", "name": "In Progress" },
    { "id": "review", "name": "Review" },
    { "id": "done", "name": "Done" }
  ],
  "tag_set": ["urgent", "bug", "feature"],
  "link_types": [
    { "id": "depends-on", "name": "Depends on", "type": "directional" },
    { "id": "blocked-by", "name": "Blocked by", "type": "directional" },
    { "id": "related", "name": "Related", "type": "bidirectional" },
    { "id": "sent-to", "name": "Sent to", "type": "directional",
      "target_types": ["printer"] }
  ],
  "settings": {
    "enforce_transitions": false,
    "strict_fields": true,
    "tag_policy": "propose",
    "default_user": "local-dev"
  }
}
```

**Columns** define the only valid `status` values (by column `id`). Array
order is the lane order. APIs/CLI use ids (`in_progress`, not "In Progress").

**Link types** are workspace-level vocabulary. `source_types`/`target_types`
are optional arrays of card type ids; mismatched links are rejected with the
valid set echoed.

**Workspace scope:** cards belong to exactly one workspace in v1 (one instance
= one workspace). Use export/import to move (new card id, optional
source-reference link).

Reload: `POST /v1/workspace/reload` or `cards workspace reload`.

### JSON vs YAML authoring

Both supported. Use one consistently per project. YAML is shorter and allows
comments; JSON is stricter for machine generation.

---

## 3. Card type schemas

File: `definitions/card-types/<type_id>.json`

### Minimal type

```json
{
  "id": "note", "name": "Note", "schema_version": 1,
  "description": "Single body field for ad-hoc items.",
  "fields": [
    { "id": "body", "label": "Body", "type": "text", "required": true,
      "description": "Markdown content." }
  ]
}
```

### Field definition shape

```json
{
  "id": "machine_key",
  "label": "Human label",
  "type": "string | text | number | date | enum | tags | user | card_link | repeating | artifact",
  "required": false,
  "default": null,
  "description": "Shown in introspection for agents."
}
```

Type-specific options:

| Type | Extra keys |
|---|---|
| `enum` | `options`: string[] |
| `number` / `date` | optional `min`, `max` |
| `tags` | uses workspace `tag_set` |
| `user` | must reference a registered user |
| `card_link` | optional `target_type`, `link_type` |
| `repeating` | `item_fields`: FieldDef[] (no nested `repeating` in v1); entries get stable server-generated `entry_id` |
| `artifact` | optional `artifact_policy`: `"local" \| "uri"` |

`text` is rendered as markdown. `string` is single-line.

### `searchable_fields`
Optional list of field ids (usually `text`/`string`) indexed in FTS with
`title`. Omit to index title only.

### `allowed_columns`
Optional list of column ids. If set, `status` on create/PATCH must be in this
subset even when transitions are unconstrained.

---

## 4. Status and transitions

### Status is always required
Every card has exactly one `status`: a workspace column id. Default on create:
first column in `allowed_columns`, else first workspace column.

### Three layers of control (per status change)
1. **Column validity** — must exist in `workspace.columns`.
2. **Type column subset** — if `allowed_columns` is set, new status must be
   listed.
3. **Transition graph** — only if enforcement is on.

Failure → `422` with `valid_options` (allowed statuses or next steps).

### Transition enforcement (opt-in)

| Scope | Config | Effect |
|---|---|---|
| Workspace | `settings.enforce_transitions: true` | Default for all boards unless overridden |
| Board | `settings.enforce_transitions` + `transitions` | Board-specific graph |
| Card type | `transitions` (optional) | Tighter graph for that type only |

When **off** (default): any valid column id → any other (subject to layers
1–2). When **on**: `transitions` maps current status → allowed next status ids.

```json
{
  "id": "engineering", "name": "Engineering",
  "columns": ["backlog", "todo", "in_progress", "review", "done"],
  "card_type_ids": ["programming-task", "research-goal"],
  "settings": { "enforce_transitions": true },
  "transitions": {
    "backlog": ["todo"],
    "todo": ["in_progress"],
    "in_progress": ["review"],
    "review": ["done", "in_progress"],
    "done": []
  }
}
```

Illegal move `todo` → `done` while enforced → error echoes `["in_progress"]`.

**Board vs type:** if both define graphs, board graph is used when present
(board owns the process for that lens); otherwise type graph.

### Transitions vs links
Transitions gate `status` changes only. `depends-on` / `blocked-by` do **not**
automatically block PATCH in v1 — use queries (`blocked=true`), skills, or
`take-next` filters. Optional future: `enforce_links_on_transition`.

### Events
Each legal status change emits `status_changed` with `diff: { before, after }`.

---

## 5. Relations and links

### Link types (workspace)
Declared in `workspace.json` (`link_types`). Each has `id`, `name`, `type`
(`directional` | `bidirectional`), and optional `source_types`/`target_types`
(card type ids).

| id | Typical use | Stored on |
|---|---|---|
| `depends-on` | Ordering: source waits for target | the waiting card |
| `blocked-by` | Hard block: source blocked by target | the blocked card |
| `related` | Loose association | either (bidirectional) |
| `sent-to` | Job dispatched to asset card (printer, server) | the job card |

> **Direction note.** `depends-on` and `blocked-by` are both stored on the
> *waiting/blocked* card, so a card's outgoing edges answer "what am I waiting
> on?" The old `blocks` type (source blocks target) was removed because agents
> consistently wired it backwards — see [`NOTES.md`](NOTES.md) D3.

### Two ways to relate cards
1. **`card_link` field** — part of the schema (e.g. `assigned_printer`).
   Validated on PATCH; target must exist; optional `target_type`.
2. **`links` collection** — runtime edges via `POST /cards/:id/links`; same
   validation; historied as `link_added` / `link_removed`.

Use fields when the relation is part of the type's data model; use `links`
when relationships are discovered during work.

### Graph queries
- **Blocked:** `GET /cards?blocked=true` (outgoing `blocked-by`/`depends-on`
  to a non-`done` card).
- **Outgoing `depends-on`:** filter with `has_link` / `link_target`.
- **Jobs for printer X:** view or filter on `fields.assigned_printer` or a
  `sent-to` link to the printer card id.

---

## 6. Schema versioning

Pure **versioned snapshots**: each `schema_version` is an immutable field list;
a card pins one and validates against it.

- Monotonic `schema_version` per `type_id` (integer, starts at 1).
- Each card pins `schema_version` (default: current at create).
- Writes validate `fields` against the **pinned** version.

### Authoring a new version

```json
{
  "id": "programming-task", "name": "Programming Task",
  "schema_version": 2,
  "migrations": {
    "2": { "from": 1, "summary": "Track PR URL before review",
           "field_defaults": { "pull_request_url": null } }
  },
  "fields": [
    { "id": "description", "type": "text", "required": true },
    { "id": "branch", "type": "string", "required": true },
    { "id": "pull_request_url", "type": "string", "required": false },
    {
      "id": "work_log", "type": "repeating", "required": false,
      "item_fields": [
        { "id": "commit_hash", "type": "string", "required": true },
        { "id": "notes", "type": "text", "required": false },
        { "id": "author", "type": "user", "required": true },
        { "id": "timestamp", "type": "date", "required": true }
      ]
    }
  ],
  "allowed_columns": ["todo", "in_progress", "review", "done"]
}
```

Keep immutable snapshots optional: `programming-task.v1.json` for introspection
of old pins.

### Change rules

| Change | Handling |
|---|---|
| Add optional field | New version; old cards unchanged until upgrade |
| Add required field | New version; upgrade applies `field_defaults` |
| Remove field | Absent from the new snapshot; old-version cards keep it |
| Enum: add value | New version |
| Enum: remove value | Old cards may retain value until edited |
| Repeating item shape | New appends use the pinned version's `item_fields` |

A field may be flagged `deprecated: true` **within the current version** for
advance warning; this is informational, not how removal works.

### Upgrading a card

```http
POST /v1/cards/{id}/upgrade-schema
{ "target_version": 2, "dry_run": false }
```
```bash
cards upgrade-schema CARD_ID --target 2
```
Emits `schema_upgraded`. Reloading type files does not auto-upgrade cards.

---

## 7. Board and view configuration

### Board
File: `definitions/boards/<board_id>.json`

```json
{
  "id": "engineering", "name": "Engineering",
  "columns": ["backlog", "todo", "in_progress", "review", "done"],
  "card_type_ids": ["programming-task", "research-goal"],
  "settings": { "enforce_transitions": true },
  "transitions": {
    "todo": ["in_progress"],
    "in_progress": ["review"],
    "review": ["done", "in_progress"],
    "done": []
  },
  "presentation": {
    "lane_group_by": "status",
    "card_preview": {
      "programming-task": ["branch", "pull_request_url"],
      "research-goal": ["hypothesis"]
    },
    "filters": [
      { "id": "mine-open", "label": "My open",
        "filter": { "owner": { "$eq": "me" }, "status": { "$nin": ["done"] } } }
    ]
  }
}
```

`card_type_ids` is sugar merged into `default_filter` as `type_id $in [...]`.
Boards do not own cards; they filter workspace cards for UI and `board_id`
queries.

### View
File: `definitions/views/<view_id>.json`

```json
{
  "id": "order-parts", "board_id": "fulfillment",
  "path": "/orders/:order_id/parts",
  "bind": { "order_id": { "field": "order_ref", "op": "eq" } },
  "filter": { "type_id": { "$eq": "part-line" },
              "status": { "$nin": ["done", "cancelled"] } }
}
```
Read-only in v1; writes go to `/cards/:id`.

---

## 8. Full type examples

### Programming task
See §6 migration example. Links (`depends-on`, `blocked-by`) are typically
added at runtime.

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
Workspace columns might be `queued`, `printing`, `qa`, `done`.

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

Machine-specific validation (g-code well-formedness, machine profile schemas,
dispatch command specs) is extension territory. The card holds the `artifact`
pointer and a `repeating` telemetry log; an extension validates payloads and
appends findings as comments or `status_updates` entries.

> Repeating `state` describes **machine telemetry**; card `status` uses
> workspace columns. Keep them aligned by convention (an agent appends
> `printing` in both when the job starts).

---

## 9. CLI (`cards`)

The binary is **`cards`** (avoids clashing with Unix `wc`). It mirrors the HTTP
API.

```bash
cards serve --workspace ./demo-workspace --port 8787

cards workspace show
cards boards show engineering

cards list --board engineering --owner me --status todo,in_progress
cards create --type programming-task --title "..." --status todo \
  --field branch=feature/x --as coder-agent
cards get CARD_ID
cards patch CARD_ID --status review --version 3 \
  --field pull_request_url=https://github.com/org/repo/pull/42
cards claim CARD_ID --as coder-agent --status in_progress
cards take-next --board engineering --filter-file ./filters/todo.json \
  --as coder-agent --status in_progress
cards append CARD_ID work_log \
  --entry-json '{"commit_hash":"a1b2c3d","notes":"...","author":"coder-agent","timestamp":"2026-06-25T14:30:00Z"}'
cards patch-entry CARD_ID work_log ENTRY_ID --entry-json '{...}'
cards link add CARD_ID --type depends-on --target OTHER_ID
cards upgrade-schema CARD_ID --target 2
cards events CARD_ID
cards events stream --board engineering
cards history CARD_ID

cards users register --id coder-agent --kind agent
cards views query order-parts --param order_id=34
```

Environment:

| Variable | Purpose |
|---|---|
| `CARDS_URL` | API base (default `http://127.0.0.1:8787/v1`) |
| `CARDS_WORKSPACE` | Workspace directory for embedded/local mode |
| `CARDS_USER` | Default actor (`me` / `--as`) |

Concurrency: pass `--version` on every PATCH/claim (or `If-Match`); stale
versions return `409 version_conflict` with the current card.

### Output modes
- `--json` — single JSON object (default for `get`, `create`, `patch`).
- `--jsonl` — newline-delimited JSON (default for `list`, `events`, streams).
- `--quiet` — ids only (for `xargs`).
- Errors go to **stderr** as structured JSON, e.g.
  `{"error":"unknown_enum","field":"status","valid_options":[...]}`.

### RPC mode
```bash
cards rpc --workspace ./.work-cards
```
Reads newline-delimited JSON-RPC on stdin, writes responses on stdout. Method
names mirror HTTP (`cards.list`, `cards.create`, `events.stream`). Same service
layer as HTTP and MCP.

---

## 10. Checklist for a new board

1. Add or reuse **columns** in `workspace.json` (status ids).
2. Add **link_types** you need (`depends-on`, `sent-to`, …) with optional
   `source_types`/`target_types`.
3. Create a **card type** under `definitions/card-types/`.
4. Create a **board** JSON: `card_type_ids`, optional `transitions`,
   `presentation.card_preview` and saved **filters**.
5. Optional **views** for domain URLs.
6. Register **users** (agents/humans) before assigning `owner`/`user` fields.
7. Hit `GET /v1/workspace` or `cards workspace show` and verify introspection
   before agents run.

---

## 11. Related documents

| Doc | Contents |
|---|---|
| [`PHILOSOPHY.md`](PHILOSOPHY.md) | Why the system stays small |
| [`EXTENSIONS.md`](EXTENSIONS.md) | Hooks, services, runs |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | Go core, packaging, Python/Node integration |
| [`SPEC.md`](SPEC.md) | API, storage, filter DSL, events |
| [`MCP.md`](MCP.md) | MCP tool surface |
| [`LIFECYCLE-EXAMPLES.md`](LIFECYCLE-EXAMPLES.md) | End-to-end CLI + HTTP scenarios |
| [`NOTES.md`](NOTES.md) | Design notes (v0.4 changes) |
