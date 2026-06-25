# MCP surface

How Work Cards exposes itself to agents over the Model Context Protocol. The
MCP server is one of three surfaces (REST, CLI, MCP) sharing one Go service
layer; it is the primary interface for agents living inside an MCP-aware
harness.

Normative behavior lives in [`SPEC.md`](SPEC.md). The agent coordination loop
(§13) drives tool grouping. See also [`DEVELOPER-REFERENCE.md`](DEVELOPER-REFERENCE.md)
for schema authoring and [`LIFECYCLE-EXAMPLES.md`](LIFECYCLE-EXAMPLES.md) for
worked flows.

---

## Goals

- Agents see **typed tools**, not raw JSON, so the board's vocabulary is a
  guardrail against hallucinated fields and values.
- Tools are **generated from `GET /workspace`**, so every board gets a bespoke
  tool surface with no per-board code.
- The toolset maps cleanly onto the **coordination loop**: introspect →
  take-next → work → append evidence → transition → comment → resume.

## Running it

```bash
cards mcp --workspace ./.work-cards
```

stdio MCP. The same binary can run alongside a local HTTP server if a harness
wants both; mutations delegate to the same service layer as HTTP/CLI, so
validation, idempotency, and events are identical.

## Tool surface

### Introspection

| Tool | Input | Returns |
|---|---|---|
| `workspace` | — | workspace: columns, card types (with `schema_version` + field schemas), boards, views, `tag_set`, `link_types`, users, settings |
| `card_type` | `type_id`, `version?` | one card type schema |

`workspace` is the one call an agent makes before doing anything else. The
returned schemas drive the typed create/update tools below.

### Card lifecycle (per card type, generated)

For **each** card type `T`, the server generates:

- `create_<T>` — typed input derived from `T.fields` + universal `title`,
  `status?`, `tags?`, `schema_version?`. Unknown fields rejected by the schema
  itself (the tool's input schema is the board's vocabulary).
- `update_<T>` — typed patch for `T.fields` plus `status`/`owner`/`tags`;
  requires `version`.

Using one create/update tool per type means an agent can't send a
`programming-task` field set to a `research-goal` — the tool won't accept it.
This is the strongest anti-hallucination property: the *tool signature* is the
schema.

> Implementation note: for boards with many card types, the server may offer a
> `scoped_tools` session option that exposes only a requested subset, to keep
> the tool list small (progressive disclosure — see `PHILOSOPHY.md`).

### Generic coordination tools

These are the same for every board:

| Tool | Input | Returns |
|---|---|---|
| `get_card` | `card_id` | full card + `version` |
| `list_cards` | filter params / `filter` object, `board_id?`, `limit`, `cursor` | page of cards |
| `search_cards` | `q`, plus optional filters | page of cards (FTS) |
| `claim` | `card_id`, `status?`, `version` | updated card |
| `take_next` | `filter`, `assign_to`, `status?` | a claimed card, or `card: null` |
| `append_entry` | `card_id`, `field`, `entry` (typed from `item_fields`), `version` | updated card + `entry_id` |
| `update_entry` | `card_id`, `field`, `entry_id`, `entry`, `version` | updated card |
| `add_link` | `card_id`, `type_id`, `target`, `note?` | updated card |
| `add_comment` | `card_id`, `body` | updated card |
| `upgrade_schema` | `card_id`, `target_version?` | updated card |
| `history` | `card_id` | resumption-ready timeline |

`append_entry`/`update_entry` inputs are typed from the field's `item_fields`
in the *pinned* `schema_version`, so an agent can't append a malformed
telemetry entry either.

### Events

| Tool | Input | Returns |
|---|---|---|
| `card_events` | `card_id`, `types?`, `limit` | recent events (normative `diff` per `SPEC.md` §8) |
| `subscribe` | `board_id?`, `card_id?`, `types?` | SSE-like stream; supports `Last-Event-ID` for resumption |

## Concurrency, idempotency, errors

- **Concurrency:** mutation tools take `version`; a stale version returns
  `version_conflict` (`409`) with the current card. The agent re-reads and
  retries.
- **Idempotency:** every mutation tool accepts an `idempotency_key`. Retries
  with the same key return the original result. `take_next` with the same key
  returns the *same* card (not a new pick).
- **Errors:** structured per the [`SPEC.md`](SPEC.md) §10 catalog. MCP exposes
  them as tool errors carrying the same JSON (including `valid_options`), so
  the agent self-corrects: read `valid_options`, retry.

## Actor

Every mutation tool resolves an actor from the session (the harness sets
`CARDS_USER` or a per-session identity), surfaced as `X-Work-Cards-Actor` to
the service layer. The agent does not pass an actor in each call; the session
binds it. See `SPEC.md` §12.

## Why this shape works for agents

- **The tool list is the manual.** `workspace` + the generated create/update
  tools encode every valid field, enum, and tag. There is nothing to guess.
- **Type-per-card-type** makes category errors impossible at the tool boundary.
- **`take_next` + `claim` + `append_entry`** map 1:1 to "take a task, work,
  log evidence" — the loop is three calls.
- **`history` + `subscribe`** make preemption recoverable: resume from the
  timeline, or react to a peer's event.
- **Structured errors with `valid_options`** turn mistakes into a single
  retry, not a dead end.

## What MCP does *not* do

- It is a transport over the same core; it does not add behavior. Automation,
  validation beyond the field catalog, and integrations are extensions
  ([`EXTENSIONS.md`](EXTENSIONS.md)).
- It does not execute `command` field specs (the core never executes; the
  `command` field type was dropped — see [`NOTES.md`](NOTES.md) D2).
- It does not manage auth beyond the trusted-local actor binding.
