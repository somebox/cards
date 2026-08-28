# Agent instructions

Cards serves its own agent guidance. There is nothing to hand-paste and keep in
sync.

Two channels, with different reach:

- **MCP is harness-neutral.** Any MCP client receives the short coordination
  instructions in the `initialize` handshake. This is the path that works
  everywhere.
- **The installed skill is Claude Code-shaped.** `cards init` writes
  `.claude/skills/cards/`, which Claude Code and compatible harnesses discover.
  It carries the fuller playbook. A harness that does not read
  `.claude/skills/` should use MCP, or paste the output of
  `cards mcp --print-instructions` into wherever it keeps standing instructions.

## Over MCP — automatic

The server returns its instructions in the `initialize` handshake, so any MCP
client picks them up on connect. Wire the server in
([config snippets](mcp.md)) and you are done:

```bash
cards mcp --workspace /abs/path
```

The text covers what the tool schemas cannot — the coordination loop, optimistic
concurrency and retry discipline, evidence norms, honest status moves, who owns
card bookkeeping, and session-end persistence. It is deliberately short and
size-capped, because it sits in every session's prompt prefix.

To read it, or to paste it into a harness that does not surface MCP
instructions:

```bash
cards mcp --print-instructions
```

That needs no workspace and no running server, so it works from a bare install.

## Over the CLI — an installed skill (Claude Code and compatible)

Agents with shell access but no MCP client use the same operations with the same
validation. `cards init` installs a skill covering board discovery, the CLI's
flag-order and short-id rules, mapping questions to queries, sprint planning
against a board, and recording work as it lands — plus a `project-practices`
reference for setting a board up, designing card types, migrating a backlog in,
and review/snapshot/release conventions:

```bash
cards init                    # installs .claude/skills/cards/ beside .cards/
cards init --global           # installs into ~/.claude/skills/cards/
cards init --no-skill         # workspace only
```

Running `init` in a project that already has a board installs the skill without
touching the workspace. An existing skill is never overwritten — you are told it
was left alone.

The skill states the same invariants as the MCP handshake, so an agent driving
the board over the CLI and one driving it over MCP behave identically. The skill
is the larger document by design: it loads on demand, whereas the handshake sits
in every session's prompt prefix.

`.claude/skills/` is currently the only install target. Support for another
harness's skill location is a follow-up, not a promise — until then, MCP is the
neutral path.

## Two notes for the human setting this up

- Set `CARDS_USER` in the MCP server's environment to a distinct actor id per
  agent (for example `agent-claude`, `agent-pi`) so the event history shows who
  did what.
- The MCP surface has no idempotency keys yet. If your workflow retries
  aggressively, route those writes through the
  [REST API](../spec/api-surface.md) instead.

## Exact request shapes

For a running server, `GET /v1/openapi.json` is an OpenAPI 3.1 document
generated from the live workspace, so the field schemas in it are your card
types. The authoritative MCP tool inventory is
[`internal/mcp/README.md`](https://github.com/somebox/cards/blob/main/internal/mcp/README.md).
