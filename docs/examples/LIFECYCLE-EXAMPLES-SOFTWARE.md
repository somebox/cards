# Lifecycle Example — Software Delivery Board

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
> see [`NOTES.md`](../NOTES.md) D3.

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
  "version": 2
}
```

```bash
cards append card_auth_api work_log \
  --entry-json '{"commit_hash":"a1b2c3d","notes":"Refresh handler + tests","author":"coder-agent","timestamp":"2026-06-25T14:30:00Z"}'
cards patch card_auth_api --status review --version 2
```

> A real deployment might add a `pull_request_url` field (type `string`) to
> its `programming-task` definition and pass `--field pull_request_url=…`
> here; the bundled demo workspace does not include it, so the example omits
> it to stay runnable as-is.

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
# illustrative filter-file path; not shipped in examples/demo-workspace
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
cards events stream --board engineering --types status_changed,item_appended  # [HTTP only, no CLI]
```

`history` returns a resumption-ready timeline an agent ingests to continue
interrupted work — the unique value of structured, faithful events
(see `SPEC.md` §8).

**Lifecycle summary (A):** create → link (`depends-on`, `blocked-by`, stored
on the waiting card) → list blocked/owned → claim → append `work_log` (stable
`entry_id`) → enforced transitions → `done` → `take-next` on dependent → docs
unblocked → append `sources` → `done` → events/SSE/history.

---

