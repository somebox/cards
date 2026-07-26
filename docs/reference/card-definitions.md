# Card definitions

A card type is one JSON file under `definitions/card-types/`. That file is the
contract: it drives the web form, the API validation, the CLI flags, and the
generated MCP tools. This page covers authoring them.

<div class="cards-duo" markdown>

```json title="definitions/card-types/programming-task.json"
{
  "id": "programming-task",
  "name": "Programming Task",
  "schema_version": 1,
  "fields": [
    { "id": "description", "type": "text",
      "required": true, "display": "monospace" },
    { "id": "branch", "type": "string",
      "required": true, "display": "badge" },
    { "id": "kind", "type": "enum",
      "options": ["feature", "bug", "design", "infra"] },
    { "id": "work_log", "type": "repeating",
      "display": "feed",
      "item_fields": [
        { "id": "commit_hash", "type": "string",
          "required": true },
        { "id": "notes", "type": "text" }
      ] }
  ],
  "allowed_columns": ["backlog", "todo",
    "in_progress", "review", "done"]
}
```

<figure markdown>
  ![The card detail page this definition renders](../assets/img/card-detail.png){ .cards-shot }
  <figcaption>What that file renders: required text, a branch badge, the enum, a work-log feed, comments.</figcaption>
</figure>

</div>

## The envelope vs. your fields

A schema is not fully free-form: every card shares a universal envelope the
runtime manages, and your card type defines only the shape of `fields`. The
two files relate by id — the `id` at the top of the definition
(`programming-task` above) is what every card created from it carries as its
`type_id`.

| Property | On create | On update | Notes |
|---|---|---|---|
| `id` | Server-generated | Immutable | Stable string; used in URLs and links |
| `type_id` | Required | Immutable | The definition's `id` (e.g. `programming-task`) |
| `schema_version` | Defaults to current | Via `upgrade-schema` only | Pins validation rules |
| `title` | Required | PATCH | Not part of `fields`; always indexed for search |
| `status` | Required (or first allowed column) | PATCH | Always a workspace **column id** |
| `fields` | Per type schema | PATCH / entry APIs | Your typed custom data |
| `owner` | Optional | PATCH | The canonical assignment field (`claim`, `take-next`, `owner=me`) |
| `tags` | Optional | PATCH | Subset of the workspace `tag_set` |
| `links` / `comments` | — | Link / comment APIs | Envelope features, not schema fields |
| `version` | `1` | Increments per mutation | Optimistic concurrency |

Don't redefine `title` or `status` inside `fields`. Multiple assignees or
reviewer roles are extra schema fields (`user`, or `repeating` with a `user`
item).

## Defining a field

```json
{
  "id": "machine_key",
  "label": "Human label",
  "type": "string",
  "required": false,
  "default": null,
  "description": "Shown in introspection — write it for the agent."
}
```

The ten field types, with their extra keys:

| Type | Holds | Extra keys |
|---|---|---|
| `string` | Single line | — |
| `text` | Multi-line, rendered as markdown | — |
| `number` | Numeric | `min`, `max` |
| `date` | RFC3339 timestamp | `min`, `max` (as Unix seconds UTC — a date string here is a load error) |
| `enum` | One of a fixed set | `options: [...]`, `multiple: true`, `option_themes` |
| `tags` | Workspace tags | uses the workspace `tag_set` |
| `user` | A registered user id | `multiple: true` |
| `card_link` | Reference to another card | `target_type`, `link_type` |
| `repeating` | An append-only feed of typed entries | `item_fields: [FieldDef, ...]` (no nesting); entries get a server `entry_id` |
| `artifact` | A stored file or URI | `artifact_policy` — `"local"` or `"uri"` |

Details worth knowing:

- **Multi-value** (`enum`/`user` with `multiple: true`) — the value is always
  an array when present and absent when unset (never `null` or `[]`).
  `required` means non-empty; filter with `$has`. Not available inside
  `repeating` items.
- **Display hints** — a field may carry `display: "badge" | "monospace" |
  "feed" | "hidden" | "link"` to shape how the UI renders it, and enum fields
  may map values to icons and colors with `option_themes`. Presentation only;
  no effect on validation.
- **`searchable_fields`** — an optional type-level list of field ids (usually
  `text`/`string`) indexed for full-text search alongside `title`. Declaring it
  **restricts** the index to those fields; omitting it indexes every field
  value. `title` is always searchable either way. Narrowing (or widening) the
  list re-indexes existing cards on the next load, so a field you exclude stops
  matching immediately rather than lingering in the index.
- **`allowed_columns`** — an optional type-level subset of workspace columns;
  `status` must stay inside it even when no transition graph is enforced.
- Richer payload validation (JSON schemas, file paths, commands) is not a
  field type — store as `text`/`artifact` and validate in an
  [extension](../extensions/index.md).

!!! tip "It's just JSON — pipe it"
    Definitions and card output are both plain JSON, so ad-hoc questions are
    one-liners. What enums does this type have? How is in-flight work
    distributed?

    ```console
    $ jq -r '.fields[] | select(.type=="enum") | "\(.id): \(.options|join(", "))"' \
        definitions/card-types/programming-task.json
    kind: feature, bug, design, infra

    $ cards list --board engineering | jq -r .status | sort | uniq -c | sort -rn
       2 in_progress
       1 todo
       1 review
       1 backlog
    ```

## Layered validation

Rules merge workspace → card type → board → card, and later layers only add
restrictions — a board can tighten a type's rules, never loosen them. The
normative merge order lives in
[the data-model spec](../spec/data-model.md#definition-merge-and-precedence).

## Schema versioning

Every card records a `schema_version`, and reloading definitions never migrates
existing cards automatically. The runtime currently loads only the latest type
definition, however, so ordinary writes validate against that current schema —
not an immutable historical snapshot. True pinned-version validation and
serving old type definitions remain unbuilt.

To evolve a type, bump `schema_version` and describe the step in
`migrations`. The current definition is the complete target field list — a
field you leave out is dropped when a card explicitly upgrades (the dry-run
shows exactly what would be lost), so carry forward everything you keep:

```json
{
  "id": "programming-task",
  "name": "Programming Task",
  "schema_version": 2,
  "migrations": {
    "2": { "from": 1, "summary": "Completed cards carry a visual proof where appropriate",
           "field_defaults": { "screenshot": null } }
  },
  "fields": [
    { "id": "description", "type": "text", "required": true, "display": "monospace" },
    { "id": "branch", "type": "string", "required": true, "display": "badge" },
    { "id": "kind", "type": "enum",
      "options": ["feature", "bug", "design", "infra"] },
    { "id": "screenshot", "type": "artifact", "artifact_policy": "local" },
    { "id": "work_log", "type": "repeating", "display": "feed",
      "item_fields": [
        { "id": "commit_hash", "type": "string", "required": true },
        { "id": "notes", "type": "text" }
      ] }
  ],
  "allowed_columns": ["backlog", "todo", "in_progress", "review", "done"]
}
```

Cards upgrade explicitly, per card (dry-run first):

```console
$ cards upgrade-schema 4430ab22 --target 2 --dry-run
$ cards upgrade-schema 4430ab22 --target 2
```

Over MCP the `upgrade_schema` tool defaults to dry-run (`confirm: true`
applies). Each upgrade emits a `schema_upgraded` event. A field may be marked
`deprecated: true` within a version as advance warning; actual removal is a
new version. Normative change rules:
[schema versioning in the spec](../spec/api-surface.md#5-schema-versioning).

## What you don't get (v1)

- Per-type column *names* — columns are workspace-wide; types only restrict
  the subset.
- Nested `repeating` inside repeating items.
- Structured-payload field types (`json`, `path`, `command`) — extension
  territory.

## Next

- [Workspace & boards](workspace-and-boards.md) — columns, link types,
  transitions, and board configuration.
- [Card type examples](card-type-examples.md) — complete
  worked schemas (research goal, fabrication job).
- [Using Cards](../using-cards.md) — creating and updating cards against these
  schemas from CLI, HTTP, and MCP.
