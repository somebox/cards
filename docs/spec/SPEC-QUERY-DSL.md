# Query and Filter DSL Specification

## 9. Query and filter DSL

### First-class query parameters

| Parameter | Meaning |
|-----------|---------|
| `type_id` | One or more types |
| `status` | Column id(s) |
| `owner` | User id (exact). The `me` alias is resolved by the **board UI** to the viewing actor, not by this API param — `?owner=me` on `GET /cards` matches the literal owner `me`. |
| `tag` | Tag(s) |
| `q` | Full-text search (FTS5) |
| `has_link` | Link type id present |
| `link_target` | Card id linked |
| `blocked` | Shorthand: outgoing `blocked-by`/`depends-on` to a non-`done` card |
| `board_id` | Apply board `default_filter` + type/column scope |

Pagination: `limit` (default 50, max 500), `cursor` (opaque; keyed to the
default `updated_at, id` order).

Ordering is **orthogonal to filtering**: filters select *which* cards; `sort`
selects their *order*. `sort` takes one key (`created_at`, `updated_at`,
`title`, or `fields.<id>`) with an optional leading `-` for descending;
missing-field cards sort last; an unknown key is a `422`. `sort` cannot be
combined with `cursor` (the keyset cursor is welded to the default order) — a
custom sort returns no `next_cursor`.

> **Note:** `updated_before`/`updated_after`/`created_before`/`created_after`
> are **not implemented as separate query params** on `GET /cards`. JSON filter
> DSL is implemented for board `default_filter` and `take-next` / CLI
> `--filter-file`, but not as a `filter=` query parameter on `GET /cards`.

### Filter JSON (board defaults and take-next)

jq-*like*, compiled to SQL safely (not full jq). The DSL is used by board
`default_filter` and by `take-next` / `cards take-next --filter-file`; `GET
/cards?filter=` is not currently wired:

```json
{
  "$and": [
    { "owner": { "$eq": "me" } },
    { "status": { "$nin": ["done", "cancelled"] } },
    { "fields.priority": { "$eq": "high" } }
  ]
}
```
Operators: `$eq`, `$ne`, `$in`, `$nin`, `$gt`, `$gte`, `$lt`, `$lte`,
`$contains`, `$has`, `$and`, `$or`. Paths: `fields.<id>` for typed fields;
top-level keys for `status`, `owner`, `type_id`, `tag`, `updated_at`. CLI:
`cards take-next --filter-file q.json`. Power users: `cards export --format
jsonl` and local jq out of band.

> **`$contains` semantics:** on a string-valued path it is a
> case-insensitive substring match (SQLite `LIKE`); on an array-valued path
> (e.g. `tags`) it is an exact membership test (case-sensitive `=`). `$eq`/
> `$in` string comparisons are case-sensitive (`=`).

> **`$has` semantics (multi-value fields) [built]:** exact membership over a
> `fields.<id>` path — `{"fields.platforms": {"$has": "mobile"}}` matches
> cards whose array contains the value (case-sensitive `=`, via `json_each`).
> On a scalar-valued field it degrades to equality; an absent key never
> matches. Valid only on `fields.<id>` paths (core columns are never arrays —
> `$has` there is a loud error). This is the v1 filtering story for
> `multiple: true` fields: `$eq` on an array compares the whole JSON blob and
> is almost never what you want.

### Recipes
- **Open assigned to me:** `owner=me&status=todo,in_progress`.
- **Blocked stale for take-next:** request body `blocked=true` + `filter={"updated_at":{"$lt":"<now-1h>"}}`.

---

## 10. Validation and anti-hallucination

Rules:

1. **Unknown enum value** → `unknown_enum`, echo `valid_options`.
2. **Unknown tag** → `unknown_tag`, echo `tag_set` (+ `propose_tag` hint).
3. **Unknown user** → `unknown_user`, include registration call.
4. **Unknown field** (strict mode) → `unknown_field`, echo field list.
5. **card_link to missing card** → `target_card_missing`, echo target type +
   search hint.
6. **Link type/source/target mismatch** → `target_card_type_mismatch`, echo
   valid `source_types`/`target_types`.
7. **Missing required field** → `validation_failed`, list missing fields.
8. **Repeating entry missing required sub-field** → per-entry rejection with
   `entry_id`/index.
9. **Schema version mismatch on write** → `schema_version_mismatch`, echo
   `current_schema_version` + upgrade hint.
10. **Optimistic concurrency:** stale `version` → `version_conflict` (`409`)
    with current card.
11. **Illegal transition** (enforced) → `transition_illegal`, echo allowed
    next statuses.
12. **No actor on a write** → `actor_required` (`403`).

`dry_run: true` validates fully and returns the would-be card + warnings,
writing nothing. A successful `dry_run` response returns the would-be card
(or would-be result) with a `Dry-Run: true` response **header**; the response
body is not otherwise marked as a dry run. Errors are structured JSON:

```json
{
  "error": "unknown_enum",
  "field": "status",
  "value": "In-Progress",
  "message": "Unknown status. Use a board column id.",
  "valid_options": ["todo", "in_progress", "review", "done"],
  "hint": "See GET /workspace"
}
```

### Error catalog

| `error` | HTTP | Carries |
|---------|------|---------|
| `validation_failed` | 422 | `field[]`, `message` |
| `unknown_enum` | 422 | `field`, `value`, `valid_options` |
| `unknown_tag` | 422 | `value`, `valid_options` (`tag_set`) |
| `unknown_user` | 422 | `value`, hint |
| `unknown_field` | 422 | `field`, `valid_options` |
| `target_card_missing` | 422 | `value`, `target_type`, hint |
| `target_card_type_mismatch` | 422 | `value`, `valid_options` |
| `transition_illegal` | 422 | `from`, `valid_options` |
| `schema_version_mismatch` | 422 | `current_schema_version`, hint |
| `version_conflict` | 409 | current `card` |
| `actor_required` | 403 | hint |
| `not_found` | 404 | `resource` |

A replayed mutation (same `Idempotency-Key`) returns the original response
body and status with an added `Idempotent-Replay: true` response header —
not a distinct error code.

---

