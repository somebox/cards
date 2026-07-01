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
validation and events are identical. Idempotency differs by surface — see
the "Concurrency, idempotency, errors" section below.

## Tool surface

### Introspection

| Tool | Input | Returns |
|---|---|---|
| `workspace` | — | workspace: columns, card types (with `schema_version` + field schemas), boards, views, `tag_set`, `link_types`, users, settings |

`workspace` is the one call an agent makes before doing anything else. The
returned schemas drive the typed create/update tools below.

> A `card_type` tool (returning one card-type schema by `type_id`/`version`)
> is planned but **not yet implemented**. Use `workspace` to introspect
> card-type schemas in the meantime.

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
| `append_entry` | `card_id`, `field`, `entry` (typed from `item_fields`), `version` | updated card (the `entry_id` is inside the appended item in the returned card, not a separate top-level field) |
| `add_link` | `card_id`, `type_id`, `target`, `note?` | updated card |
| `add_comment` | `card_id`, `body` | updated card |
| `history` | `card_id` | resumption-ready timeline |

`append_entry` inputs are typed from the field's `item_fields` in the *pinned*
`schema_version`, so an agent can't append a malformed telemetry entry
either.

> `update_entry` (`svc.UpdateEntry`) and `upgrade_schema` (`svc.UpgradeSchema`)
> are available over the HTTP and CLI surfaces but **not yet exposed as MCP
> tools**. Use `PATCH /v1/cards/{id}/fields/{field}/{entryID}` and
> `POST /v1/cards/{id}/upgrade-schema` over HTTP, or `cards patch-entry` /
> `cards upgrade-schema` on the CLI, in the meantime.

### Events

> `card_events` and `subscribe` are designed but **not yet exposed as MCP
> tools**. Use `GET /v1/cards/{id}/events` (recent events for a card) and
> `GET /v1/events/stream` (live SSE feed with `Last-Event-ID` resumption)
> over HTTP in the meantime.

| Tool | Input | Returns | Status |
|---|---|---|---|
| `card_events` | `card_id`, `types?`, `limit` | recent events (normative `diff` per `SPEC.md` §8) | `[proposed]` |
| `subscribe` | `board_id?`, `card_id?`, `types?` | SSE-like stream; supports `Last-Event-ID` for resumption | `[proposed]` |

## Concurrency, idempotency, errors

- **Concurrency:** mutation tools take `version`; a stale version returns
  `version_conflict` (`409`) with the current card. The agent re-reads and
  retries.
- **Idempotency:** the HTTP and CLI surfaces support an `Idempotency-Key`/
  `idempotency_key` for safe retries (the HTTP middleware reads the
  `Idempotency-Key` header and deduplicates via the store). The MCP tool
  surface does **not** currently forward or honor an idempotency key — no MCP
  tool accepts an `idempotency_key` parameter, and retries of `create_<T>`,
  `append_entry`, `take_next`, etc. are not deduplicated. Callers needing
  idempotent retries should use the HTTP API with `Idempotency-Key` instead.
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
- **`history`** makes preemption recoverable: resume from the timeline.
  (`subscribe` is planned for live event reaction — not yet implemented; use
  the HTTP SSE feed in the meantime.)
- **Structured errors with `valid_options`** turn mistakes into a single
  retry, not a dead end.

## What MCP does *not* do

- It is a transport over the same core; it does not add behavior. Automation,
  validation beyond the field catalog, and integrations are extensions
  ([`EXTENSIONS.md`](EXTENSIONS.md)).
- It does not execute `command` field specs (the core never executes; the
  `command` field type was dropped — see [`NOTES.md`](NOTES.md) D2).
- It does not manage auth beyond the trusted-local actor binding.
