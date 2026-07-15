# Lifecycle Examples — Shared Setup

## Register identities

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

## Introspect (one call before work)

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
| Discovery | `blocked`, `owner=me` | view + client-side `updated_at` filter |
| Reactivity | SSE on engineering board (replayable) | SSE on fabrication board |
| Idempotency | `Idempotency-Key: claim-auth-api-1` on claim | Same on append during retry |

Dry-run before a risky transition:

```http
PATCH /v1/cards/card_auth_cli
X-Work-Cards-Actor: coder-agent
{ "status": "in_progress", "version": 1, "dry_run": true }
```
```bash
cards patch card_auth_cli --status in_progress --version 1 --dry-run
```

These walkthroughs are **spec exercises**; exact flag names may shift slightly
during implementation, but paths and semantics match [`SPEC.md`](../spec/SPEC.md) v0.4.
