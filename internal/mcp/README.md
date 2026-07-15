# Model Context Protocol (MCP) Package

This package implements the Model Context Protocol (MCP) server for Work Cards, enabling AI agents to interact with cards, boards, and schemas using standardized, auto-generated tools.

## Running the MCP Server

```bash
cards mcp --workspace ./.work-cards
```

The MCP server runs over stdio. Mutations delegate to the same service layer as HTTP/CLI, ensuring event emission and validation match the core contract.

## Tool Surface

### Introspection
- `workspace` - Returns current workspace definition: columns, card types, boards, views, tag sets, link types, and users. Drive agent decision-making from this schema.

### Card Lifecycle (Dynamic per Card Type)
For each card type `T` defined in the workspace, the MCP server dynamically publishes:
- `create_<T>` - Typed input derived from the card type schema. Unknown fields are rejected at the protocol layer.
- `update_<T>` - Typed patch for `T.fields`, `status`, `owner`, and `tags`. Requires `version`.

### Generic Coordination Tools
- `get_card` - Retrieve full card info + version.
- `list_cards` - Page-based querying and list filters.
- `search_cards` - Full-text search (FTS).
- `claim` - Claim ownership and transition status.
- `release` - Release ownership (optional status); requires `version`.
- `take_next` - Atomically claims and returns the next eligible card of a queue.
- `append_entry` - Typed append to a repeating field (version-checked).
- `update_entry` - Patch a repeating-field entry by `entry_id` (version-checked).
- `remove_entry` - Remove a repeating-field entry by `entry_id` (version-checked).
- `add_link` - Relate two cards.
- `remove_link` - Remove a typed link.
- `add_comment` - Append a comment.
- `edit_comment` - Edit an existing comment body.
- `upgrade_schema` - Preview or apply a schema upgrade. Defaults to dry-run returning `{dry_run, would_drop, would_apply, card}`; set `confirm:true` to apply. (REST's `POST /cards/:id/upgrade-schema` has the opposite default — it applies unless `dry_run:true` is passed.)
- `attach_artifact` - Store bytes for an artifact field from base64-encoded content; returns the updated card.
- `get_artifact` - Fetch stored artifact bytes by uri, returned as base64 with size.
- `history` - Fetch resumption-ready card timeline.
- `breaches` - Current breaching conditions (WIP over limit, drained watched lanes, blocked cards); optional `board_id` / `type`.
- `events` - Durable workspace event feed with `since` replay floor; filter by `types` / `board_id`.

## Concurrency, Idempotency, and Errors
- **Concurrency:** Mutations that patch typed state (`claim`, `release`, `take_next`, `update_<T>`, and the entry tools) require `version` and return `version_conflict` if stale. The link, comment, and `upgrade_schema` tools take no `version` argument and operate on latest state — the store's compare-and-swap still rejects racing lost updates.
- **Idempotency:** Unlike HTTP/CLI surfaces which support `Idempotency-Key` headers, the MCP tool surface does **not** yet forward or honor an idempotency key. Callers expecting retries without duplication should use HTTP.
- **Errors:** Emits structured error payloads corresponding to the core error catalog (with `valid_options` to allow agents to rectify validation issues autonomously).

See also: [MCP Design & Ergonomics](../../docs/extensions/mcp.md)
