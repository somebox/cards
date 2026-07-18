# Lifecycle Example — Shop Floor / CNC Board

**Domain:** board `fabrication` with columns `queued` → `printing` → `qa` →
`done`. Card types: `part-spec`, `printer` (asset), `printer-job`. Link types:
`depends-on`, `sent-to`.

**Story:**

1. **part-42-spec** — approved part program (artifact).
2. **job-run-9001** — print run; **depends-on** spec; **sent-to** printer
   `printer-x1`.
3. Monitor agent appends `status_updates` and drives transitions when the
   machine reports state.

### B1 — Create spec and printer asset

```http
POST /v1/cards
X-Work-Cards-Actor: shop-monitor

{ "type_id": "part-spec", "title": "Bracket rev C", "status": "done",
  "fields": { "part_number": "BRK-42C", "material": "PETG" } }
```

→ `card_spec_42`. Attach the g-code `artifact`:

```http
POST /v1/cards/card_spec_42/artifacts/gcode_ref
Content-Type: application/octet-stream
X-Work-Cards-Actor: shop-monitor

<raw bytes of ./programs/brk-42c.gcode>
```

(JSON alternative if the file is already in the workspace: PATCH `gcode_ref`
with `{ "uri": "artifacts/card_spec_42/brk-42c.gcode", "mime": "text/x-gcode" }`.)

```http
POST /v1/cards

{ "type_id": "printer", "title": "X1 Carbon #2", "status": "done",
  "fields": { "serial": "X1-002", "location": "bay-3" } }
```

→ `card_printer_x1`.

```bash
cards create --type part-spec --title "Bracket rev C" --status done \
  --field part_number=BRK-42C --field material=PETG --as shop-monitor
cards attach card_spec_42 gcode_ref ./programs/brk-42c.gcode
cards create --type printer --title "X1 Carbon #2" --status done \
  --field serial=X1-002 --field location=bay-3
```

Machine-specific g-code validation is an extension's job; the card just holds
the `artifact` pointer.

### B2 — Create job in queue with dependencies

```http
POST /v1/cards
X-Work-Cards-Actor: shop-monitor

{ "type_id": "printer-job", "title": "Run 9001 — 4× bracket", "status": "queued",
  "fields": { "material": "PETG", "quantity": 4 } }
```

→ `card_job_9001`.

```http
POST /v1/cards/card_job_9001/links
{ "type_id": "depends-on", "target": "card_spec_42" }
```

```http
POST /v1/cards/card_job_9001/links
{ "type_id": "sent-to", "target": "card_printer_x1", "note": "Scheduled overnight" }
```

```http
PATCH /v1/cards/card_job_9001
{ "fields": { "assigned_printer": "card_printer_x1" }, "version": 1 }
```

(`assigned_printer` is a `card_link` field on `printer-job`; the `sent-to` link
duplicates the semantics for graph queries. The `sent-to` link type may
declare `target_types: ["printer"]`, so linking to a non-printer is rejected.)

```bash
cards create --type printer-job --title "Run 9001 — 4× bracket" \
  --status queued --field material=PETG --field quantity=4
cards link add card_job_9001 --type depends-on --target card_spec_42
cards link add card_job_9001 --type sent-to --target card_printer_x1 \
  --note "Scheduled overnight"
cards patch card_job_9001 --field assigned_printer=card_printer_x1 --version 1
```

### B3 — Assign operator and capture context comment

```http
PATCH /v1/cards/card_job_9001
X-Work-Cards-Actor: shop-monitor
{ "owner": "shop-monitor", "version": 2 }
```

```http
POST /v1/cards/card_job_9001/comments
X-Work-Cards-Actor: shop-monitor
{ "body": "Queued for overnight run. Operator checks first layer at T+10m." }
```

```bash
cards patch card_job_9001 --owner shop-monitor --version 2
cards comment add card_job_9001 \
  --body "Queued for overnight run. Operator checks first layer at T+10m."
```

### B4 — View: jobs for this printer (domain URL)

> **[proposed]** The `GET /v1/views/…` route and the `cards views query`
> CLI subcommand are not yet implemented. The view-definition format shown
> below is the intended contract; until the serving path ships, use
> `GET /v1/cards?board_id=…&type_id=…` with query filters to achieve the
> same result.

View definition (in `definitions/views/printer-jobs.json`):

```json
{
  "id": "printer-jobs",
  "path": "/printers/:printer_id/jobs",
  "bind": { "printer_id": { "field": "assigned_printer", "op": "eq" } },
  "filter": { "type_id": { "$eq": "printer-job" },
              "status": { "$nin": ["done", "cancelled"] } }
}
```

```http
GET /v1/views/printer-jobs/cards?printer_id=card_printer_x1
```

```bash
cards views query printer-jobs --param printer_id=card_printer_x1
```

### B5 — Start print (transition + telemetry)

Machine starts; monitor appends a `status_updates` entry (getting back an
`entry_id`) and moves the column:

```http
POST /v1/cards/card_job_9001/fields/status_updates/append
X-Work-Cards-Actor: shop-monitor

{ "entry": {
    "state": "printing",
    "reported_at": "2026-06-25T22:05:00Z",
    "note": "Bed 65°C, nozzle 240°C"
  }
}
```

```http
PATCH /v1/cards/card_job_9001
X-Work-Cards-Actor: shop-monitor
{ "status": "printing", "version": 2 }
```

```bash
cards append card_job_9001 status_updates \
  --entry-json '{"state":"printing","reported_at":"2026-06-25T22:05:00Z","note":"Bed 65°C"}'
cards patch card_job_9001 --status printing --version 2
```

Subscribe before the long run (with `Last-Event-ID` so a dropped connection
replays):

```bash
cards events stream --board fabrication --types=status_changed,item_appended &  # [HTTP only, no CLI]
```

```http
GET /v1/events/stream?board_id=fabrication&types=status_changed,item_appended
```

### B6 — Failure path then recovery

```http
POST /v1/cards/card_job_9001/fields/status_updates/append
X-Work-Cards-Actor: shop-monitor

{ "entry": { "state": "failed", "reported_at": "2026-06-25T23:10:00Z",
             "note": "Layer shift at Z=12.4mm" } }
```

If the board allows `printing` → `queued` for re-run:

```http
PATCH /v1/cards/card_job_9001
X-Work-Cards-Actor: shop-monitor
{ "status": "queued", "fields": { "quantity": 2 }, "version": 4 }
```

Re-queue telemetry entry:

```http
POST /v1/cards/card_job_9001/fields/status_updates/append
X-Work-Cards-Actor: shop-monitor

{ "entry": { "state": "queued", "reported_at": "2026-06-25T23:15:00Z",
             "note": "Reprint remaining 2; bed relevelled" } }
```

Second attempt through `printing` → `qa` → `done`:

```http
POST /v1/cards/card_job_9001/fields/status_updates/append
X-Work-Cards-Actor: shop-monitor
{ "entry": { "state": "completed", "reported_at": "2026-06-26T01:00:00Z",
             "note": "4/4 OK" } }
```

```http
PATCH /v1/cards/card_job_9001
{ "status": "qa", "version": 6 }
```

```http
PATCH /v1/cards/card_job_9001
{ "status": "done", "version": 7 }
```

```bash
# append failed / queued / completed states similarly (each returns an entry_id)
cards patch card_job_9001 --status qa --version 6
cards patch card_job_9001 --status done --version 7
```

To correct a telemetry entry, address it by `entry_id` (never by index):

```bash
cards patch-entry card_job_9001 status_updates ent_01HXYZ \
  --entry-json '{"state":"completed","reported_at":"2026-06-26T01:00:00Z","note":"4/4 OK (corrected)"}'
```

### B7 — Query stale blocked queue (ops)

Jobs still `queued` not updated in 1 hour — **illustrative only**:
`updated_before`/`created_before` time filters are **not implemented** in the
list handler or the CLI ([`query-dsl.md` §9](../spec/query-dsl.md)). Today, list `--status queued` and filter on
each card's `updated_at` client-side:

```bash
cards list --board fabrication --status queued --jsonl \
  | jq -r 'select(.updated_at < "2026-06-25T21:00:00Z") | .id'
```

**Lifecycle summary (B):** spec + artifact → printer asset → job with
`depends-on` + `sent-to` → view by printer → append `status_updates` (stable
`entry_id`s) + status transitions → SSE (with replay) for monitors →
failure/requeue path → `qa` → `done`.

---
