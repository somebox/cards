# Work Cards

> A small substrate for typed cards, events, and extensions. Built to
> coordinate agents and tools — not to be a ticket archive.

Work Cards is a tiny Go kernel that stores typed cards, validates writes
against versioned schemas, records every change as an event, and exposes one
HTTP/CLI/MCP surface. Everything else — dispatchers, agents, UIs, sync,
reports, custom validators — is an independent process talking to the API.

The core idea: **card types are versioned schemas, every change is an event,
and behavior is added via external extensions, not built into the kernel.**
Agents use one introspection call plus a stable `/cards` API; domain paths
like `/orders/:id/parts` are resolved views, and reactive behavior comes from
hooks declared in the workspace.

---

## Why not traditional ticket tools?

Agents need discoverable structure, validated enums and tags, repeating
structured entries (work logs, sources, machine status), faithful update
history, and semantic links — not a single mutating description field.

Work Cards rejects invalid writes with recoverable errors, pins **schema
version** per card so types can evolve safely, and stays embeddable as a
library, sidecar, or MCP plugin beside your app and filesystem.

## What makes Work Cards different

1. **Small kernel, big composition.** The core does cards, events, schemas,
   and storage. Everything else is an external extension (any language).
2. **Versioned JSON schemas** for card types (`schema_version` on type and card).
3. **Trimmed field catalog** — `string`, `text`, `number`, `date`, `enum`,
   `tags`, `user`, `card_link`, `repeating`, `artifact`. Structured-payload
   validation (JSON/YAML schemas, path confinement, command specs) is
   extension territory; the core stores such content as `text`/`string`/
   `artifact` and lets an extension validate.
4. **Append-only events** with materialized card rows; optional retention. The
   materialized card (including repeating fields) is the durable work product;
   the event log is the coordination memory.
5. **Single workspace per instance; easy multi-instance.** One process serves
   one workspace (one SQLite file, possibly assembled from several context
   files). Multiple workspaces = multiple processes on different ports/paths.
6. **Boards vs views** — boards drive the Kanban UI; views add path-bound
   filters without duplicating data.
7. **API, CLI, MCP share one grammar** — filters, pagination, SSE streams;
   `--json` / `--jsonl` output everywhere.
8. **Hooks, not engines.** Reactive behavior is declared in the workspace and
   delivered as subprocesses with event JSON on stdin.
9. **The agent coordination loop** is the organizing concept: introspect →
   take-next → work → append evidence → transition → comment → (resume).

## Intended components

| Component | Role |
|-----------|------|
| **Core API** | REST `/v1`: workspace introspection, `/cards`, views, events/SSE |
| **SQLite + FTS5** | Index, search, pagination (default storage) |
| **CLI** | Same paths and flags as the API; `--json` / `--jsonl` output |
| **MCP server** | Tools generated from workspace/card-type introspection |
| **Extensions** | Independent processes (hooks, services, runs) declared in the workspace |
| **Web UI** | Schema-driven board (reference consumer, not part of the kernel) |

Start with the philosophy doc; it explains why everything else is small.

- Philosophy: [`docs/PHILOSOPHY.md`](docs/PHILOSOPHY.md)
- Design notes (v0.4 changes): [`docs/NOTES.md`](docs/NOTES.md)
- Full design: [`docs/SPEC.md`](docs/SPEC.md) (v0.4)
- Architecture and packaging: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- Lifecycle walkthroughs: [`docs/LIFECYCLE-EXAMPLES.md`](docs/LIFECYCLE-EXAMPLES.md)
- MCP surface: [`docs/MCP.md`](docs/MCP.md)
- Schemas, transitions, links, versioning: [`docs/DEVELOPER-REFERENCE.md`](docs/DEVELOPER-REFERENCE.md)

## Status

**Spec stage (v0.4).** Design targets a Go `cards` binary with Python and Node
wrapper packages: easy to ship inside an agent harness or run as
`cards serve` next to another app. CLI binary: **`cards`** (avoids clashing
with Unix `wc`).

## Goals

- One introspection call (`GET /workspace`) yields types, versions, columns,
  views, and valid vocabularies.
- **Open cards assigned to me** and **blocked stale cards** are first-class
  filter scenarios.
- State changes can **push** via SSE (with `Last-Event-ID` replay) so workers
  react without polling.
- Artifacts live on disk; cards hold references.
- An interrupted agent can **resume** from a card's structured event history.

## Non-goals (for v1)

- Long-term archive or compliance retention (export elsewhere if needed).
- Full jq on the server (export + local jq instead).
- Roles/SSO; users are open identifiers with optional `kind: human | agent`.
- Graphical schema designer (edit JSON in `definitions/`).
- Structured-payload field types (`json`/`yaml`/`path`/`command`); validate via
  extensions.
