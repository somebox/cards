# MCP quickstart

Cards exposes its full model to agents over the
[Model Context Protocol](https://modelcontextprotocol.io) — the same typed
service layer the web UI and CLI use, so an agent's writes are validated,
versioned, and event-emitting exactly like a human's.

!!! note "Scope"
    This page gets an agent connected and coordinating. For *why* the tool
    surface is shaped the way it is (types as tools, the loop mapping), see
    [MCP design & ergonomics](../extensions/MCP.md).

## Run the server

The MCP server runs over **stdio**:

```bash
cards mcp --workspace ./examples/demo-workspace
```

The actor for writes is resolved from `CARDS_USER`, falling back to the
workspace's `default_user`. Point it at whatever workspace you want the agent to
coordinate on.

## Wire it into a harness

Most harnesses take a small JSON entry naming the command and args. A generic
stdio MCP server config:

```json
{
  "mcpServers": {
    "cards": {
      "command": "cards",
      "args": ["mcp", "--workspace", "/absolute/path/to/your-workspace"],
      "env": { "CARDS_USER": "agent-1" }
    }
  }
}
```

- **Claude Code:** `claude mcp add cards -- cards mcp --workspace /abs/path`
  (or drop the block above into your MCP settings).
- **Other harnesses (pi, Cursor, custom):** use the same `command`/`args`/`env`
  shape their MCP client expects.

Use an absolute `--workspace` path — the agent's working directory may differ
from yours.

## The tool surface

On connect, the agent should call **`workspace`** first — it returns the
columns, card types, boards, tag sets, link types, and users. Drive every other
decision from that schema rather than guessing.

For each card type `T`, Cards publishes typed **`create_<T>`** and
**`update_<T>`** tools (e.g. `create_programming-task`) whose inputs are derived
from the schema — unknown fields are rejected at the protocol layer. Alongside
them, a fixed set of coordination tools:

| Tool | Purpose |
|---|---|
| `workspace` | Introspect the whole schema. Call this first. |
| `list_cards` / `search_cards` / `get_card` | Query and read cards. |
| `take_next` | Atomically claim the next eligible card of a queue. |
| `claim` / `release` | Take or drop ownership + transition status (version-checked). |
| `append_entry` / `update_entry` / `remove_entry` | Edit a `repeating` field entry. |
| `add_comment` / `edit_comment` | Discussion. |
| `add_link` / `remove_link` | Relate cards. |
| `history` | Resumption-ready card timeline. |
| `breaches` | Current WIP / blocked / drained-lane conditions. |
| `events` | Durable workspace event feed with replay. |
| `upgrade_schema` | Preview/apply a schema migration (dry-run by default). |
| `attach_artifact` / `get_artifact` | Store/fetch artifact bytes (base64). |

The authoritative, always-current inventory is
[`internal/mcp/README.md`](https://github.com/somebox/cards/blob/main/internal/mcp/README.md);
a parity test keeps it honest against the code.

## The coordination loop

The tools are grouped to support one loop an agent runs repeatedly:

1. **Introspect** — `workspace` to learn the types, columns, and boards.
2. **Take** — `take_next` (or `list_cards` → `claim`) to get a card and a
   `version`.
3. **Work** — do the task; record evidence with `append_entry` /
   `add_comment` / `attach_artifact`.
4. **Transition** — `update_<T>` / `claim` to move status (carrying the current
   `version`).
5. **Resume** — `history` to pick up where a prior session left off.

## Things to know

- **Concurrency is version-checked.** Mutations that patch typed state require
  the current `version`; a stale write returns `version_conflict` with the
  current card attached. Read it, retry — don't paper over it.
- **No idempotency keys over MCP (yet).** Unlike the HTTP surface, the MCP tools
  don't forward an `Idempotency-Key`. If you need retry-safe writes without
  duplication, use the [REST API](../spec/SPEC-API-SURFACE.md) for those calls.

