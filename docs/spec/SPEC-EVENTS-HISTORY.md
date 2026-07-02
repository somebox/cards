## 8. History, events, and retention

> See §3 Event delivery status note — this section's `history`/feed endpoints
> share the same beta/in-progress caveat.

- Append-only **events** table; materialized **cards** row updated in the same
  transaction.
- Query: per card, per workspace feed, by actor/type/time.
- **Not an archive:** the **materialized card (including repeating fields) is
  the durable work product.** The **event log is the audit/coordination
  layer** and may be trimmed via `event_retention_days` (the card snapshot and
  artifacts are always kept). Note: `event_retention_days` is a declared
  schema field but automatic trimming is **not yet implemented** (no
  background job reads it today). Export to git or the host app for long-term
  records.

### Normative `diff` per event type

| Event type | `diff` |
|------------|--------|
| `card_created` | `{ card: { id, type_id, title, status } }` |
| `field_updated` | `{ field, before, after }` |
| `status_changed` | `{ before, after }` |
| `owner_changed` | `{ before, after }` |
| `tags_changed` | `{ added: [], removed: [] }` |
| `item_appended` | `{ field, entry_id, entry, index }` |
| `item_updated` | `{ field, entry_id, before, after }` |
| `item_removed` | `{ field, entry_id, entry }` |
| `link_added` | `{ type_id, target, note }` |
| `link_removed` | `{ type_id, target }` |
| `comment_added` | `{ comment_id }` |
| `comment_edited` | `{ comment_id, before, after }` |
| `schema_upgraded` | `{ from, to }` |
| `artifact_added` | `{ field, uri, sha256 }` *(reserved for when the artifacts subsystem — §6 — is implemented; not currently emitted)* |
| `definition_reloaded` | `{ kind: "workspace"|"board"|"card_type", id }` *(reserved for when definition reload lands; not currently emitted)* |

### History as agent-resumption context

Because events are structured and faithful, `GET /cards/:id/history` renders a
concise timeline an agent ingests to resume interrupted work. This is the
thing that makes "take a task, get preempted, come back" work across processes
— a unique value vs. traditional ticket tools (which forget) and in-framework
agent state (which is ephemeral).

---

## 12. Actors and authorization

- Every write supplies an actor via the **`X-Work-Cards-Actor`** header (or
  `CARDS_USER` env / workspace `default_user` fallback — see resolution order
  below). **Note:** request bodies also declare an `actor` JSON field on most
  write types for forward-compat, but it is currently **ignored/overwritten**
  by the server on every endpoint — the header/env/default chain is
  authoritative and the body field has no effect. Do not rely on the body field.
  Resolution: header → `CARDS_USER` env → workspace `default_user` → `403 actor_required`.
- The server sets `created_by` and event `actor` from the resolved identity.
  In the trusted-local model, the configured identity is trusted; stronger
  identity binding (signed tokens, per-user keys) is an extension/host concern.
- **The actor is not validated against the user registry.** Any string is
  accepted as an actor for create/patch/comment/append — registration is *open*
  and there is no auth. This is deliberate: a harness can spawn many short-lived
  workers, each with its own `CARDS_USER`, without pre-registering them.
- **Ownership is the exception.** `owner` is a validated `user` reference:
  setting it (including `claim`/`take-next`, which use the actor as the new
  `owner`) requires that id to be a **registered** user, else `unknown_user`. So
  an actor that only creates/comments needs no registration, but a worker that
  *claims* cards must be registered first via `POST /v1/users {id, kind}`
  (open, no auth). Registering each worker once at spawn is the intended pattern.
  (Other `user`-typed *fields* — e.g. a work-log author — are type-checked only,
  not existence-checked.)
- `created_at`, `updated_at`, and event `at` are **server-set only**; clients
  cannot supply them.

---

