# Developer Reference — Card Type Schemas and Examples

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

For the full field-type catalog including validation rules, see
[`SPEC-CARDTYPE-EXAMPLES.md` field catalog](../spec/SPEC-CARDTYPE-EXAMPLES.md#card-type-field-catalog-and-examples).

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
Link types are declared in `workspace.json` (`link_types`); each has `id`,
`name`, `type` (`directional` | `bidirectional`), and optional
`source_types`/`target_types`. For the default link vocabulary
(`depends-on`, `blocked-by`, `related`, `sent-to`) and the direction/storage
convention (both stored on the waiting/blocked card), see
[`SPEC.md` §4 “Default link vocabulary”](../spec/SPEC-DATA-MODEL.md#default-link-vocabulary).
The old `blocks` type was removed because agents consistently wired it
backwards — see [`NOTES.md`](../NOTES.md) D3.

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

