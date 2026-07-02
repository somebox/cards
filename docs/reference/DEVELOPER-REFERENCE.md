# Developer reference — schemas, boards, and transitions

How to define and configure Work Cards for a workspace: what is fixed on every
card, what is schema-defined, how workspace/board/type rules merge, how status
transitions and links work, and how versions evolve.

Principles: [`PHILOSOPHY.md`](../concepts/PHILOSOPHY.md). Extension model:
[`EXTENSIONS.md`](../extensions/EXTENSIONS.md). Normative API details:
[`SPEC.md`](../spec/SPEC.md). Architecture and packaging:
[`ARCHITECTURE.md`](../architecture/ARCHITECTURE.md). Walkthroughs:
[`LIFECYCLE-EXAMPLES.md`](../examples/LIFECYCLE-EXAMPLES.md). Design notes:
[`NOTES.md`](../NOTES.md).

---


## See Also (Extracted Sections)

- [1. Flexibility, 2. Workspace definition & 6. Schema versioning](DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md)
- [3. Card type schemas, 4. Status and transitions, 5. Relations and links & 8. Full type examples](DEVELOPER-REFERENCE-TYPES-EXAMPLES.md)
- [9. CLI & 10. Checklist for a new board](DEVELOPER-REFERENCE-CLI.md)

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
      "programming-task": ["branch"],
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

**[proposed, not yet implemented]** the `View` type is declared in the core
but has no CLI verb, HTTP route, or service wiring yet; the JSON shape below
is the intended contract.

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

## 11. Related documents

| Doc | Contents |
|---|---|
| [`PHILOSOPHY.md`](../concepts/PHILOSOPHY.md) | Why the system stays small |
| [`CONCEPTS.md`](../concepts/CONCEPTS.md) | Vocabulary, mental model, and use-case setups |
| [`INTEGRATOR-REFERENCE.md`](../reference/INTEGRATOR-REFERENCE.md) | Code-verified drift audit of SPEC claims |
| [`EXTENSIONS.md`](../extensions/EXTENSIONS.md) | Hooks, services, runs |
| [`ARCHITECTURE.md`](../architecture/ARCHITECTURE.md) | Go core, packaging, Python/Node integration |
| [`SPEC.md`](../spec/SPEC.md) | API, storage, filter DSL, events |
| [`MCP.md`](../extensions/MCP.md) | MCP tool surface |
| [`LIFECYCLE-EXAMPLES.md`](../examples/LIFECYCLE-EXAMPLES.md) | End-to-end CLI + HTTP scenarios |
| [`NOTES.md`](../NOTES.md) | Design notes (v0.4 changes) |
