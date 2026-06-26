<div align="center">

# Work Cards

**A card-based planning system for agents and humans.**

API-first. Lightweight. No dependencies. Schema-driven.

</div>

<p align="center">
  <img src="media/board.png" alt="Work Cards board UI" width="720">
</p>

---

Work Cards turns typed, schema-validated cards into a coordination surface for
agents, CLIs, and humans. Define card types with structured fields, walk them
through enforced transitions, and let multiple agents share the same board —
whether they're a Python script, a Claude extension, or a human in the web UI.

It's a single Go binary with one SQLite file. No database server, no Node
runtime, no container required. Drop it into a project, run `cards serve`,
and start coordinating work.

---

## The problem

Every planning tool — Vikunja, Linear, Planka, Jira — ships with an
opinionated workflow and a UI baked into the product. The schema is fixed, the
states are fixed, and adapting them to fit your work means fighting the tool.

Work Cards takes the opposite stance, born from a minimalist philosophy:

- **The schema is the product.** You define what a card *is* — its fields,
  types, valid values, and transitions. The system validates against it.
- **The UI is a view, not the source of truth.** A lightweight web board
  renders whatever schema you define. It's a convenience, not a constraint.
- **The API is the primary interface.** CLI, MCP, and HTTP all share one
  grammar. The web UI is just another consumer.
- **Behavior is an extension, not a feature.** Hooks, notifications,
  integrations — declared in config, run as external processes. Override or
  replace any of them. The kernel stays small.

### Why not just use the todo list in your agent harness?

Claude, Hermes, and other agent harnesses have built-in todo management. But
those todos are local to one agent in one session. Work Cards gives you:

- **Granular control** — structured fields, enforced transitions, validation
- **Multi-agent coordination** — multiple agents claim, take-next, and share
  the same board, across frameworks
- **Durable history** — every change is an event; an interrupted agent can
  resume from the card's structured log
- **Structured collaboration** — cards are meaningful data structures, not
  free text. Validated, typed, and compatible with the structured outputs LLMs
  return.

---

## Use cases

- **Agent coordination** — multiple agents (Claude, Hermes, pi-agent, custom
  scripts) work from a shared board, claiming cards and appending evidence
- **Project task tracking** — embed `cards serve` alongside your codebase;
  developers and CI use the CLI; PMs use the web UI
- **Research pipelines** — define a card type per pipeline stage; each stage
  is a card with typed inputs, outputs, and status transitions
- **Content workflows** — track drafts, reviews, and publications as cards
  with custom fields and tags
- **Ops runbooks** — structured incident cards with repeating work logs,
  owner handoff, and status enforcement
- **Anything you'd put in a spreadsheet** — but with validation,
  transitions, and an API

---

## Features

- **Typed schemas** — `text`, `enum`, `number`, `date`, `user`, `tags`,
  `card_link`, `repeating` (structured entries with their own field defs),
  `artifact` (file references)
- **Enforced transitions** — boards define legal status flows; illegal moves
  are rejected with recoverable errors
- **Optimistic concurrency** — every write carries a version; conflicts
  surface as `version_conflict` with the current card attached
- **Idempotency** — `Idempotency-Key` header for safe retries, scoped per actor
- **Event log + SSE** — every change is an event; subscribe via
  `GET /v1/events/stream` with `Last-Event-ID` replay
- **Filter DSL** — jq-like queries (`{"status": {"$eq": "todo"}, "owner":
  {"$eq": "me"}}`) across all surfaces
- **Full-text search** — FTS5 across card titles and text fields
- **Hooks** — reactive behavior declared in config, run as external
  subprocesses (any language)
- **MCP server** — generated per-type tools for Claude, Cursor, and any MCP
  client
- **CLI** — `cards list`, `cards create`, `cards claim`, `cards take-next`,
  `cards append`, `cards comment`, `cards history`, and more
- **Web UI** — drag-and-drop board, card modal, live SSE updates, dark mode

---

## Quick start

```bash
# Build
go build -o cards ./cmd/cards

# Serve a workspace
cards serve --workspace ./examples/demo-workspace --port 8787

# Open the board
open http://127.0.0.1:8787/
```

Create a card via the API:

```bash
curl -X POST http://127.0.0.1:8787/v1/cards \
  -H "Content-Type: application/json" \
  -H "X-Work-Cards-Actor: alice" \
  -d '{
    "type_id": "programming-task",
    "title": "Implement OAuth flow",
    "status": "todo",
    "fields": {"description": "Add GitHub OAuth", "branch": "feat/oauth"}
  }'
```

Or via the CLI:

```bash
export CARDS_URL=http://127.0.0.1:8787
export CARDS_USER=alice
cards create --type programming-task --title "Implement OAuth flow" --status todo
cards list --status todo
cards take-next --type programming-task
```

Or via MCP (Claude, Cursor, etc.):

```
# Configure in your MCP client:
cards mcp
```

---

## Design principles

1. **The schema is the UI contract.** Every surface — web, CLI, MCP — renders
   from the introspected card-type definitions. Add a field, it appears
   everywhere.
2. **No baked-in auth.** Treated like an internal tool. Auth is an extension
   (put it behind a reverse proxy, or add a hook).
3. **No external dependencies.** Pure-Go SQLite (modernc.org/sqlite). One
   binary, one file. Deploy by copying.
4. **Embeddable.** Run it as a sidecar, a library, or a standalone server.
   Ship it inside your project.
5. **API-first.** The web UI is a reference consumer, not the product. The API
   is the source of truth.
6. **Portable.** Pure Go, no CGO. Runs anywhere Go runs.

---

## Documentation

| Doc | What's in it |
|-----|--------------|
| [**Philosophy**](docs/PHILOSOPHY.md) | Why this exists, the minimalist stance, what's deliberately left out |
| [**Architecture**](docs/ARCHITECTURE.md) | Components, storage, event log, extension model |
| [**Developer Reference**](docs/DEVELOPER-REFERENCE.md) | Schemas, transitions, links, versioning, field types |
| [**Spec**](docs/SPEC.md) | Full API + data model specification (v0.4) |
| [**MCP**](docs/MCP.md) | MCP server setup and generated tools |
| [**Extensions**](docs/EXTENSIONS.md) | Hooks, filters, and custom extensions |
| [**Lifecycle Examples**](docs/LIFECYCLE-EXAMPLES.md) | Walkthroughs: create → claim → work → transition → done |
| [**Notes**](docs/NOTES.md) | Design notes and changelog |

---

## Status

**Beta.** The core kernel, API, CLI, MCP server, web UI, and hooks system are
implemented and dogfooded — this project uses Work Cards to track its own
development. The API is stable for the current scope; rough edges remain.

Open to feedback and PRs. See the [board](http://127.0.0.1:8787/) (when
running locally) or the [issues](https://github.com/somebox/cards/issues).

---

## Integrations

| Integration | Status |
|-------------|--------|
| **CLI** (`cards`) | ✅ Available |
| **MCP server** (Claude, Cursor) | ✅ Available |
| **HTTP REST API** | ✅ Available |
| **Web UI** | ✅ Available (reference consumer) |
| **Claude extension** | 🔜 Planned |
| **pi-agent extension** | 🔜 Planned |
| **Hermes** | 🔜 Planned |

---

## License

MIT
