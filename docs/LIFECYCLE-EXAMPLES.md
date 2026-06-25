# Card lifecycle walkthroughs

Two end-to-end examples that exercise create → links (dependencies) → claim →
work (append) → status transitions → completion. Each step shows equivalent
**HTTP** (`/v1`) and **CLI** (the `cards` command).

Assumptions:

- Sidecar running: `cards serve --workspace ./demo-workspace --port 8787`
- Base URL: `http://127.0.0.1:8787/v1`
- Actor for writes: register users once; CLI uses `CARDS_USER` or `--as`.
  Every write carries the actor via the **`X-Work-Cards-Actor`** header
  (`--as` sets it). See `SPEC.md` §12.

CLI global flags: `--url`, `--workspace`, `--as <user_id>`, `--json` for raw
output. Concurrency: PATCH/claim/take-next pass `--version` (or `If-Match`).
Pinned to [`SPEC.md`](SPEC.md) v0.4.

---

## Shared setup (both examples)

### Register identities

```http
POST /v1/users
Content-Type: application/json

{ "id": "coder-agent", "kind": "agent", "display_name": "Coder" }
```

```http
POST /v1/users

{ "id": "shop-monitor", "kind": "agent", "display_name": "CNC Monitor" }
```

```bash
cards users register --id coder-agent --kind agent
cards users register --id shop-monitor --kind agent
export CARDS_USER=coder-agent   # or --as on each command
```

### Introspect (one call before work)

```http
GET /v1/workspace
```
```bash
cards workspace show
```
Response includes `columns`, `link_types` (`depends-on`, `blocked-by`, …), card
types with `schema_version`, boards, and saved filters. An agent reads this
once and knows the entire valid vocabulary.

---

## Example A — Software delivery board

**Domain:** a small feature split across two coding tasks and a doc task on
board `engineering`. Board `engineering` has `enforce_transitions: true`:

| From | Allowed to |
|------|------------|
| `backlog` | `todo` |
| `todo` | `in_progress` |
| `in_progress` | `review` |
| `review` | `done`, `in_progress` |
| `done` | _(terminal)_ |

**Story:**

1. **auth-api** — implement API (must finish first).
2. **auth-cli** — CLI client; **depends-on** auth-api.
3. **auth-docs** — documentation; **blocked-by** auth-cli until CLI reaches
   review.

Link types used (both stored on the *waiting* card):

- `depends-on` (directed): source waits for target (ordering convention).
- `blocked-by` (directed): source is hard-blocked while target is not `done`.

### A1 — Create cards

```http
POST /v1/cards
X-Work-Cards-Actor: coder-agent
Content-Type: application/json

{
  "type_id": "programming-task",
  "title": "Add token refresh to auth API",
  "status": "todo",
  "fields": {
    "description": "POST /auth/refresh, rotate refresh tokens",
    "branch": "feature/auth-refresh"
  }
}
```

→ `201` body includes `"id": "card_auth_api"`, `"version": 1`.

```bash
cards create --type programming-task \
  --title "Add token refresh to auth API" \
  --status todo \
  --field description="POST /auth/refresh, rotate refresh tokens" \
  --field branch=feature/auth-refresh \
  --as coder-agent
```

Create the CLI task and the docs card similarly (docs uses `research-goal`).
Save ids as `card_auth_cli` and `card_auth_docs`.

### A2 — Wire dependencies (on the waiting card)

CLI task depends on API; docs is blocked by CLI until CLI reaches review.

```http
POST /v1/cards/card_auth_cli/links
X-Work-Cards-Actor: coder-agent

{ "type_id": "depends-on", "target": "card_auth_api",
  "note": "Needs refresh endpoint and error shapes" }
```

```http
POST /v1/cards/card_auth_docs/links

{ "type_id": "blocked-by", "target": "card_auth_cli",
  "note": "Docs follow CLI UX" }
```

```bash
cards link add card_auth_cli --type depends-on --target card_auth_api \
  --note "Needs refresh endpoint and error shapes"
cards link add card_auth_docs --type blocked-by --target card_auth_cli \
  --note "Docs follow CLI UX"
```

> **Direction note.** `depends-on` and `blocked-by` are stored on the card
> that is waiting/blocked. A card's outgoing edges answer "what am I waiting
> on?" The old `blocks` type was removed because agents wired it backwards —
> see [`NOTES.md`](NOTES.md) D3.

### A2.5 — Assign ownership and add a kickoff comment

```http
PATCH /v1/cards/card_auth_cli
X-Work-Cards-Actor: coder-agent

{ "owner": "coder-agent", "version": 1 }
```

```http
POST /v1/cards/card_auth_cli/comments
X-Work-Cards-Actor: coder-agent

{ "body": "Waiting on auth-api card before implementation starts." }
```

```bash
cards patch card_auth_cli --owner coder-agent --version 1
cards comment add card_auth_cli \
  --body "Waiting on auth-api card before implementation starts."
```

### A3 — Discover blocked / ready work

Blocked docs (outgoing `blocked-by` to a non-done card):

```http
GET /v1/cards?board_id=engineering&blocked=true&type_id=research-goal
```
```bash
cards list --board engineering --blocked --type research-goal
```

Open todo items assigned to me:

```http
GET /v1/cards?board_id=engineering&owner=me&status=todo,in_progress
```
```bash
cards list --board engineering --owner me --status todo,in_progress
```

### A4 — Claim API task and move to in progress

```http
POST /v1/cards/card_auth_api/claim
X-Work-Cards-Actor: coder-agent

{ "status": "in_progress", "version": 1 }
```
```bash
cards claim card_auth_api --as coder-agent --status in_progress --version 1
```

Illegal transition (enforced board) — jump `todo` → `review`:

```http
PATCH /v1/cards/card_auth_cli
X-Work-Cards-Actor: coder-agent

{ "status": "review", "version": 1 }
```
→ `422 transition_illegal` with `valid_options: ["in_progress"]`.

```bash
cards patch card_auth_cli --status review --version 1
# same validation error (structured JSON to stderr)
```

### A5 — Log work (append) and advance API to review

Appending to a `repeating` field returns a stable `entry_id`; address later
updates by that id, not array index.

```http
POST /v1/cards/card_auth_api/fields/work_log/append
X-Work-Cards-Actor: coder-agent

{ "entry": {
    "commit_hash": "a1b2c3d",
    "notes": "Refresh handler + tests",
    "author": "coder-agent",
    "timestamp": "2026-06-25T14:30:00Z"
  }
}
```
→ `200` includes `"entry_id": "ent_01HXYZ"`.

```http
PATCH /v1/cards/card_auth_api
X-Work-Cards-Actor: coder-agent

{
  "status": "review",
  "version": 2,
  "fields": { "pull_request_url": "https://github.com/org/repo/pull/42" }
}
```

```bash
cards append card_auth_api work_log \
  --entry-json '{"commit_hash":"a1b2c3d","notes":"Refresh handler + tests","author":"coder-agent","timestamp":"2026-06-25T14:30:00Z"}'
cards patch card_auth_api --status review --version 2 \
  --field pull_request_url=https://github.com/org/repo/pull/42
```

### A6 — Complete API; unblocks dependency chain

```http
PATCH /v1/cards/card_auth_api
X-Work-Cards-Actor: coder-agent

{ "status": "done", "version": 3 }
```
```bash
cards patch card_auth_api --status done --version 3
```

Now `blocked-by` for docs resolves (target `card_auth_cli` is not yet `review`,
so docs stays blocked until CLI reaches `review`). Use `take-next` to pick the
next eligible CLI task atomically:

```http
POST /v1/cards/take-next
X-Work-Cards-Actor: coder-agent

{
  "board_id": "engineering",
  "filter": {
    "$and": [
      { "type_id": { "$eq": "programming-task" } },
      { "status": { "$eq": "todo" } },
      { "has_link": { "$eq": "depends-on" } },
      { "link_target": { "$eq": "card_auth_api" } }
    ]
  },
  "assign_to": "coder-agent",
  "status": "in_progress"
}
```
```bash
cards take-next --board engineering --filter-file ./filters/cli-after-api.json \
  --as coder-agent --status in_progress
```

After CLI reaches `review`, the docs `blocked` query shrinks; docs move
`backlog` → `todo` → `in_progress`, append `sources`, write `conclusion`:

```http
POST /v1/cards/card_auth_docs/fields/sources/append
X-Work-Cards-Actor: coder-agent

{ "entry": {
    "url": "https://github.com/org/repo/pull/42",
    "query": "Readme auth section",
    "findings": "Matches implementation",
    "checked_at": "2026-06-25T16:00:00Z"
  }
}
```

```http
PATCH /v1/cards/card_auth_docs
X-Work-Cards-Actor: coder-agent

{ "status": "done", "version": 4,
  "fields": { "conclusion": "Published docs/auth-refresh.md" } }
```

### A7 — Audit trail and resume

```http
GET /v1/cards/card_auth_api/events?limit=20
GET /v1/cards/card_auth_api/history
GET /v1/events/stream?board_id=engineering&types=status_changed,item_appended
```
```bash
cards events card_auth_api --limit 20
cards history card_auth_api
cards events stream --board engineering --types status_changed,item_appended
```

`history` returns a resumption-ready timeline an agent ingests to continue
interrupted work — the unique value of structured, faithful events
(see `SPEC.md` §8).

**Lifecycle summary (A):** create → link (`depends-on`, `blocked-by`, stored
on the waiting card) → list blocked/owned → claim → append `work_log` (stable
`entry_id`) → enforced transitions → `done` → `take-next` on dependent → docs
unblocked → append `sources` → `done` → events/SSE/history.

---

## Example B — Shop floor / CNC board

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
POST /v1/cards/card_spec_42/artifacts
Content-Type: multipart/form-data
X-Work-Cards-Actor: shop-monitor

field=gcode_ref&file=@./programs/brk-42c.gcode
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
cards artifact upload card_spec_42 gcode_ref --file ./programs/brk-42c.gcode
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

### B2.5 — Assign operator and capture context comment

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

### B3 — View: jobs for this printer (domain URL)

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

### B4 — Start print (transition + telemetry)

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
cards events stream --board fabrication --types=status_changed,item_appended &
```
```http
GET /v1/events/stream?board_id=fabrication&types=status_changed,item_appended
```

### B5 — Failure path then recovery

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

### B6 — Query stale blocked queue (ops)

Jobs still `queued` not updated in 1 hour:

```http
GET /v1/cards?board_id=fabrication&status=queued&updated_before=2026-06-25T21:00:00Z
```
```bash
cards list --board fabrication --status queued \
  --updated-before 2026-06-25T21:00:00Z
```

**Lifecycle summary (B):** spec + artifact → printer asset → job with
`depends-on` + `sent-to` → view by printer → append `status_updates` (stable
`entry_id`s) + status transitions → SSE (with replay) for monitors →
failure/requeue path → `qa` → `done`.

---

## Cross-cutting behaviors both examples use

| Concern | Example A | Example B |
|---|---|---|
| Dependencies | `depends-on`, `blocked-by` (on waiting card) | `depends-on`, `sent-to` |
| Transitions | Strict on `engineering` | Typical fabrication column line |
| Structured progress | `work_log` append | `status_updates` append |
| Assignment | `owner` set + `claim` | `owner` set to monitor |
| Collaboration | comments for handoff notes | comments for runbook notes |
| Concurrency | `version` / `--version` on every PATCH | Same |
| Repeating entries | stable `entry_id`; update by id | same |
| Discovery | `blocked`, `owner=me` | view + `updated_before` |
| Reactivity | SSE on engineering board (replayable) | SSE on fabrication board |
| Idempotency | `Idempotency-Key: claim-auth-api-1` on claim | Same on append during retry |

Dry-run before a risky transition:

```http
PATCH /v1/cards/card_auth_cli?dry_run=true
X-Work-Cards-Actor: coder-agent
{ "status": "in_progress", "version": 1 }
```
```bash
cards patch card_auth_cli --status in_progress --version 1 --dry-run
```

These walkthroughs are **spec exercises**; exact flag names may shift slightly
during implementation, but paths and semantics match [`SPEC.md`](SPEC.md) v0.4.
