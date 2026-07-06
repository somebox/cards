# Developer Reference — Schema Authoring

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
- Agents authoring card types (core definitions are human/harness-owned JSON; extension declarations may use YAML where supported).

### How workspace, board, and type rules merge

Validation is layered (workspace → card type → board → card instance), with
later layers only adding restrictions. For the normative merge/precedence
rules and resolution order, see
[`SPEC.md` §4 “Definition merge and precedence”](../spec/SPEC-DATA-MODEL.md#definition-merge-and-precedence).
This section adds only the authoring-relevant consequences below.

---

## 2. Workspace definition

File: `definitions/workspace.json` (JSON only; of the definition files, only `extensions.{yaml,json}` accepts YAML)

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

Reload **[proposed, not yet implemented]**: a `POST /v1/workspace/reload`
endpoint and `cards workspace reload` CLI verb are designed but not built;
today, reloading definitions means restarting the server. See
[`INTEGRATOR-REFERENCE.md`](../reference/INTEGRATOR-REFERENCE.md) for the drift note.

### JSON vs YAML authoring

Core workspace, board, and card-type definitions are JSON-only. YAML is accepted
only for extension declarations (`definitions/extensions.{yaml,yml,json}`), where
supported; use JSON when machine-generating core definitions.

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

Schema versioning is pure versioned snapshots; each `schema_version` is an
immutable field list a card pins and validates against. For the normative
change rules (add/remove/enum/repeating handling) and the migrations JSON
shape, see [`SPEC.md` §5 “Schema versioning”](../spec/SPEC-API-SURFACE.md#5-schema-versioning).
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

