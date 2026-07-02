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
- `take_next` - Atomically claims and returns the next eligible card of a queue.
- `append_entry` - Typed append to a repeating field (version-checked).
- `add_link` - Relate two cards.
- `add_comment` - Append a comment.
- `history` - Fetch resumption-ready card timeline.

## Concurrency, Idempotency, and Errors
- **Concurrency:** Mutation tools require `version` to prevent write collisions, returning a `version_conflict` error if stale.
- **Idempotency:** Unlike HTTP/CLI surfaces which support `Idempotency-Key` headers, the MCP tool surface does **not** yet forward or honor an idempotency key. Callers expecting retries without duplication should use HTTP.
- **Errors:** Emits structured error payloads corresponding to the core error catalog (with `valid_options` to allow agents to rectify validation issues autonomously).

See also: [MCP Design & Ergonomics](../../docs/extensions/MCP.md)
