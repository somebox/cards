# Card Type Field Catalog and Examples

Core v1 catalog (see [`design-notes.md`](../design-notes.md) D2 for what was trimmed and why):

| Type | Description | Validation |
|------|-------------|------------|
| `string` | Single-line text | Non-empty if required |
| `text` | Multi-line; rendered as markdown | Non-empty if required |
| `number` | Int/float | Numeric; optional `min`/`max` |
| `date` | ISO date/datetime | Parseable; optional `min`/`max` |
| `enum` | Single-select | Must be in `options`; else rejected with options |
| `tags` | Multi-select | Each must be in workspace `tag_set` |
| `user` | User reference | Must exist; else rejected with registration hint. **Exception:** `owner` is existence-checked; other `user`-typed fields (e.g. a repeating entry's `author`) are currently type-checked only, not existence-checked — see §12. |
| `card_link` | Card reference | Target exists; optional `target_type`, `link_type` |
| `repeating` | Array of typed entries | Each entry validated against `item_fields` (no nested `repeating` in v1); entries have stable server-generated `entry_id` |
| `artifact` | Pointer to blob in workspace or external URI | `{ uri, mime?, size?, sha256? }`; local `uri` must resolve under workspace artifacts root when `artifact_policy: local`. (Fully implemented with path confinement; see [internal/artifacts/README.md](https://github.com/somebox/cards/blob/main/internal/artifacts/README.md) for details). |

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
> annotate (see [`index.md`](../extensions/index.md)).

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

