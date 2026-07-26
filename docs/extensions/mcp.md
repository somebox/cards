# MCP surface

How Work Cards exposes itself to agents over the Model Context Protocol. The
MCP server is one of three surfaces (REST, CLI, MCP) sharing one Go service
layer; it is the primary interface for agents living inside an MCP-aware
harness.

Normative behavior lives in [`index.md`](../spec/index.md); the agent coordination loop
([`api-surface.md` §13](../spec/api-surface.md)) drives tool grouping. See also
[card definitions](../reference/card-definitions.md)
for schema authoring and [`index.md`](../examples/index.md) for
worked flows.

---

## Goals

- Agents see **typed tools**, not raw JSON, so the board's vocabulary is a
  guardrail against hallucinated fields and values.
- Tools are **generated from `GET /workspace`**, so every board gets a bespoke
  tool surface with no per-board code.
- The toolset maps cleanly onto the **coordination loop**: introspect →
  take-next → work → append evidence → transition → comment → resume.

## Running it & Tool Surface

For absolute tool inventories, instructions on running the MCP server, and details on concurrency, idempotency, or actor binding, see [the package-level documentation](https://github.com/somebox/cards/blob/main/internal/mcp/README.md).

For an overview of the key design properties of the MCP surface, see below.

## Why this shape works for agents

- **Orient with `workspace` first.** Its `settings.default_board` names the
  primary board in a multi-board workspace — use it rather than guessing, and
  note `take_next` already defaults to it when given no `board_id`/`type_id`.
- **The tool list is the manual.** `workspace` + the generated create/update
  tools encode every valid field, enum, and tag. There is nothing to guess.
- **Type-per-card-type** makes category errors impossible at the tool boundary.
- **`take_next` + `claim` + `append_entry`** map 1:1 to "take a task, work,
  log evidence" — the loop is three calls.
- **`history`** makes preemption recoverable: resume from the timeline.
  (`subscribe` is planned for live event reaction — not yet implemented; use
  the HTTP SSE feed in the meantime.)
- **Structured errors with `valid_options`** turn mistakes into a single
  retry, not a dead end.

## What MCP does *not* do

- It is a transport over the same core; it does not add behavior. Automation,
  validation beyond the field catalog, and integrations are extensions
  ([`index.md`](../extensions/index.md)).
- It does not execute `command` field specs (the core never executes; the
  `command` field type was dropped — see [`design-notes.md`](../design-notes.md) D2).
- It does not manage auth beyond the trusted-local actor binding.
