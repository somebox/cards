# Agent instructions

A ready-to-paste instruction block for agents working a Cards board. Put it
where your harness reads standing instructions — `CLAUDE.md` for Claude Code,
the system prompt or project instructions for other harnesses — alongside the
[MCP server config](mcp.md).

## Setup

1. Install the `cards` binary — download it from the
   [latest release](https://github.com/somebox/cards/releases/latest) or build
   with `go install github.com/somebox/cards/cmd/cards@latest`
   ([full install steps](../get-started.md)).
2. Wire the MCP server into your harness: `cards mcp --workspace /abs/path`
   ([config snippets](mcp.md)).
3. For exact request shapes against a running server, fetch
   `GET /v1/openapi.json` — an OpenAPI 3.1 document generated from the live
   workspace, so the field schemas in it are your card types.

Agents with shell access but no MCP client can use the CLI instead — the same
operations with the same validation ([using Cards](../reference/OPERATIONS.md)
shows CLI, HTTP, and MCP side by side).

## The instruction block

The authoritative tool inventory is
[`internal/mcp/README.md`](https://github.com/somebox/cards/blob/main/internal/mcp/README.md).

````markdown
## Working the Cards board

You coordinate work through a Cards board, available via MCP tools.

- Call `workspace` first to learn the columns, card types, boards, and
  registered users. Drive every decision from that schema — do not guess
  field names or status values.
- Pick up work with `take_next` (atomically claims the next eligible card),
  or `list_cards` to survey and `claim` to take a specific card.
- Mutations that change card state require the card's current `version`.
  On a `version_conflict` error, the response includes the current card —
  re-read it and retry with the new version. Do not blind-retry.
- Record what you do on the card as you work:
    - `add_comment` for decisions, questions, and status notes
    - `append_entry` for work-log entries (commits, results, measurements)
    - `attach_artifact` for files
- Validation errors name the failing field and include `valid_options`.
  Correct the value and retry — do not work around the schema.
- Move the card's status as work progresses (`update_<type>`, or `claim` /
  `release` for ownership). Boards may enforce allowed transitions; a
  rejected move names the allowed next columns.
- To resume interrupted work, call `history` on the card — it returns the
  timeline of changes, comments, and entries.
- No MCP connection? The `cards` CLI exposes the same operations with the
  same validation (`cards --help`; set `CARDS_URL` to target the running
  server so the board updates live).
````

Two notes for the human setting this up:

- Set `CARDS_USER` in the MCP server's environment to a distinct actor id per
  agent (for example `agent-claude`, `agent-pi`) so the event history shows
  who did what.
- The MCP surface has no idempotency keys yet. If your workflow retries
  aggressively, route those writes through the [REST API](../spec/SPEC-API-SURFACE.md)
  instead.
