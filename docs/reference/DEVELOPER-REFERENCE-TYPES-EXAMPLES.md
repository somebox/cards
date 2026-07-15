# Card type examples

Worked card type definitions across several domains. The field-by-field
authoring rules live in
[card definitions](DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md); board and
transition configuration in
[workspace & boards](DEVELOPER-REFERENCE.md).

## Minimal: a note

The smallest useful type — one markdown body:

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

## Software: programming task

The type this documentation uses throughout — required description, a branch
badge, an enum with themed options, and a work-log feed. See it with its
rendered result in [card definitions](DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md),
and as shipped in
[`examples/demo-workspace`](https://github.com/somebox/cards/tree/main/examples/demo-workspace/definitions/card-types).

## Research: research goal

A hypothesis, an optional researcher, and a `repeating` feed of sources — the
shape agents fill in as they investigate:

```json
{
  "id": "research-goal", "name": "Research Goal", "schema_version": 1,
  "fields": [
    { "id": "hypothesis", "type": "text", "required": true },
    { "id": "researcher", "type": "user" },
    {
      "id": "sources", "type": "repeating", "display": "feed",
      "item_fields": [
        { "id": "url", "type": "string", "required": true },
        { "id": "query", "type": "string" },
        { "id": "findings", "type": "text" },
        { "id": "checked_at", "type": "date", "required": true }
      ]
    },
    { "id": "conclusion", "type": "text" }
  ],
  "searchable_fields": ["hypothesis", "conclusion"]
}
```

`searchable_fields` puts the hypothesis and conclusion into full-text search
alongside the title.

## Fabrication: printer job

Cards aren't only for software. A print-farm workspace might use columns
`queued`, `printing`, `qa`, `done`, and a job type carrying an artifact, an
enum, a bounded number, a typed link to a printer card, and a telemetry feed:

```json
{
  "id": "printer-job", "name": "Printer Job", "schema_version": 1,
  "fields": [
    { "id": "gcode_ref", "type": "artifact", "required": true,
      "description": "Pointer to g-code in workspace artifacts/" },
    { "id": "material", "type": "enum", "required": true,
      "options": ["PLA", "PETG", "ABS", "TPU"] },
    { "id": "quantity", "type": "number", "required": true, "min": 1 },
    { "id": "assigned_printer", "type": "card_link",
      "target_type": "printer" },
    {
      "id": "status_updates", "type": "repeating",
      "item_fields": [
        { "id": "state", "type": "enum", "required": true,
          "options": ["queued", "printing", "paused", "completed", "failed"] },
        { "id": "reported_at", "type": "date", "required": true },
        { "id": "note", "type": "text" }
      ]
    }
  ],
  "allowed_columns": ["queued", "printing", "qa", "done"]
}
```

Two conventions worth copying:

- The repeating `state` is **machine telemetry**; the card's `status` is the
  **workflow column**. They're aligned by convention (the dispatcher appends
  `printing` to both when the job starts), not by magic.
- Machine-specific validation — g-code well-formedness, machine profiles,
  dispatch commands — is [extension](../extensions/EXTENSIONS.md) territory.
  The card holds the pointer and the log; an extension validates payloads and
  writes findings back as entries or comments.

## Next

- [Walkthroughs](../examples/LIFECYCLE-EXAMPLES.md) — these types exercised
  end to end (software delivery, shop floor).
- [Card definitions](DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md) — the authoring
  rules these examples follow.
