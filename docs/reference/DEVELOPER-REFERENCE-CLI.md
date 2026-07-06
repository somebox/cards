# Developer Reference — CLI (`cards`)

The binary is **`cards`** (avoids clashing with Unix `wc`). It mirrors the HTTP
API.

Client commands run **serverless by default**: with no `CARDS_URL` set they run
the same `/v1` router in-process against the resolved workspace
(`$CARDS_WORKSPACE`, else the nearest `.cards/`, else `~/.cards`) — no `cards
serve` required. Set `CARDS_URL` (or `--url`) to talk to a running server
instead. Prefer the server when one is up: a direct write bypasses that
process's event bus, so its SSE stream and hooks would not observe the change.

**Card references.** Everywhere a command takes a card id — reads *and* writes
(`get`, `patch`, `comment`, `link`, `attach`, `claim`, `delete`, …, including
`link add --target`) — you may pass either the full `card_…` id or its 8-char
short id (the first 8 characters after `card_`, as shown on the board). A
short id matching more than one card is never auto-resolved: the command fails
with the structured `ambiguous` error (HTTP 409, exit code 4) listing every
candidate's full id and title so you can pick one and retry. The reference is
normalized to the full id before anything is written, so events, links, and
comments always record full ids.

**Attachments out of the box.** The starter `task` type ships an `attachment`
artifact field, so a fresh install can run the whole loop immediately:
`cards init proj && cards --workspace ./proj attach <id> attachment ./file.png`
(`--workspace` accepts the project root or its `.cards` child; if both look
like workspaces the command errors with the choices rather than guessing).
Workspaces initialized before this field existed keep their old definitions —
add it to `definitions/card-types/task.json` yourself:
`{ "id": "attachment", "type": "artifact", "artifact_policy": "local" }`
(additive and optional; no schema_version bump required).

```bash
cards serve --workspace ./demo-workspace --port 8787

cards workspace show
cards boards show engineering   # GET /v1/boards/engineering

cards list --board engineering --owner me --status todo,in_progress
cards create --type programming-task --title "..." --status todo \
  --field branch=feature/x --as coder-agent
cards get CARD_ID
cards patch CARD_ID --status review --version 3 \
  --field branch=feat/oauth
cards claim CARD_ID --as coder-agent --status in_progress
# --filter-file points at any JSON file holding a §9 filter object
# (illustrative path; not shipped in examples/demo-workspace)
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
# cards views query <id> --param k=v   # [proposed, not yet implemented]

# Local, no server needed — full-snapshot backup/restore (reads SQLite directly):
cards export --workspace ./demo-workspace --out backup.jsonl
cards import --workspace ./fresh-workspace --in backup.jsonl   # restores into a fresh DB
```

Environment:

| Variable | Purpose |
|---|---|
| `CARDS_URL` | API base; **unset = serverless** (in-process). Set it to target a running server |
| `CARDS_WORKSPACE` | Workspace directory for serverless/embedded mode |
| `CARDS_USER` | Default actor (`me` / `--as`) |

Concurrency: pass `--version` on every PATCH/claim (there is no `If-Match`
header alias — `version` in the body/query is the only mechanism); stale
versions return `409 version_conflict` with the current card.

### Output modes
- `--json` — single JSON object (default for `get`, `create`, `patch`).
- `--jsonl` — newline-delimited JSON (default for `list`, `events`, streams).
- `--quiet` — ids only (for `xargs`).
- Errors go to **stderr** as structured JSON, e.g.
  `{"error":"unknown_enum","field":"status","valid_options":[...]}`.

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

