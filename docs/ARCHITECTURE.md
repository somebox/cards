# Architecture — Go Core With Extension Integration

This document describes the implementation platform for Work Cards. The design
is a Go **kernel** distributed as a small `cards` binary, with an extension
model where behavior is added by independent processes in any language.
Python and Node client packages are planned but not yet built; for now the
binary, CLI, HTTP API, and MCP server are the integration surfaces.

Normative product behavior lives in [`SPEC.md`](SPEC.md); the principles
behind these choices live in [`PHILOSOPHY.md`](PHILOSOPHY.md); the extension
contract lives in [`EXTENSIONS.md`](EXTENSIONS.md); schema authoring lives in
[`DEVELOPER-REFERENCE.md`](DEVELOPER-REFERENCE.md); the vocabulary and mental
model live in [`CONCEPTS.md`](CONCEPTS.md). For a code-verified drift audit of
the claims in this document (which features are built vs. proposed), see
[`INTEGRATOR-REFERENCE.md`](INTEGRATOR-REFERENCE.md); for the events subsystem
design, see [`EVENTS.md`](EVENTS.md).

---

## Goals

- Run as a single local service or CLI with minimal setup.
- Serve **exactly one workspace per process** (one SQLite file, possibly
  assembled from multiple definition files). Multi-workspace deployments run
  multiple processes on different ports/paths — the binary, CLI, and clients
  all take `--workspace`/`--url`, so this is trivial and is the supported
  multi-tenancy path for v1.
- Work as a sidecar for MCP clients, CLIs, scripts, and other HTTP clients.
- Use commonly available libraries and avoid mandatory external services.
- Keep runtime state in SQLite and workspace artifacts on disk.
- Load git-backed config from `definitions/` in the workspace directory and
  merge them into **one** workspace.
- Keep the core logic transport-independent so HTTP, CLI, MCP, and library use
  all share the same validation and storage behavior.

Non-goals:

- High-throughput multi-writer transaction workloads.
- Large payload storage inside cards.
- Distributed clustering or server-managed config editing.
- A multi-tenant router in the kernel (run multiple instances instead).

---

## Platform Choice

The default implementation target is **Go**.

Why Go fits:

- Produces a small portable `cards` binary.
- Standard library HTTP server is sufficient for REST and SSE.
- Cross-compilation is practical for npm/Python packaging.
- Memory use and startup time are good for agent sidecars.
- It can expose the same behavior as CLI, HTTP service, MCP subprocess, or Go
  library without changing the core model.

Recommended dependencies (all in use):

- HTTP router: `net/http` plus `chi`.
- SQLite: `modernc.org/sqlite` (pure-Go, no CGO, FTS5 supported).
- UUID: `github.com/google/uuid`.
- YAML: `gopkg.in/yaml.v3`.

Keep dependency choices boring. The project should be easy to build and reason
about without a framework.

---

## Runtime Shape

The Go binary provides several entry points:

```bash
cards serve --workspace ./examples/demo-workspace --port 8787
cards mcp --workspace ./examples/demo-workspace
cards list --board engineering --owner me
```

Internally, these entry points call the same service layer.

Actual package boundaries:

```text
cmd/cards/          CLI binary and subcommand wiring (serve, mcp, extensions, do)
internal/core/      cards, schemas, transitions, validation, events, Store interface
internal/config/    load/merge/validate JSON/YAML definitions + extensions config
internal/sqlite/    SQLite implementation (FTS5, migrations)
internal/httpapi/   REST, SSE, and htmx web UI handlers
internal/mcp/       MCP adapter over core services
internal/hooks/     hook supervisor (spawns subprocesses on events)
internal/cli/       CLI client (serverless by default — runs the /v1 router
                    in-process; talks to a server only when CARDS_URL is set;
                    see DEVELOPER-REFERENCE.md §9)
internal/seed/      demo workspace seed data
internal/starter/   starter workspace scaffolding (cards init, zero-config seed)
internal/artifacts/ workspace file/artifact helpers
```

Public Go library use can be added later by promoting stable packages out of
`internal/`, but v1 keeps API stability focused on HTTP/CLI/MCP.

---

## Core Service Boundary

All transports call a small internal API, conceptually:

```go
type Service interface {
    Workspace(ctx context.Context, name string) (*WorkspaceSnapshot, error)
    ListCards(ctx context.Context, q CardQuery) (*Page[Card], error)
    CreateCard(ctx context.Context, req CreateCardRequest) (*Card, error)
    PatchCard(ctx context.Context, id string, req PatchCardRequest) (*Card, error)
    AppendField(ctx context.Context, id, field string, entry any) (*Card, error)
    AddLink(ctx context.Context, id string, link LinkInput) (*Card, error)
    AddComment(ctx context.Context, id string, body string) (*Comment, error)
    ClaimCard(ctx context.Context, id string, req ClaimRequest) (*Card, error)
    Events(ctx context.Context, q EventQuery) (EventStream, error)
}
```

The service layer owns:

- schema lookup and pinned `schema_version` validation,
- transition evaluation,
- filter compilation,
- optimistic concurrency,
- idempotency handling,
- event writing,
- artifact metadata validation.

The HTTP layer does not reimplement these rules.

---

## Storage

SQLite is the operational store.

Tables:

- `cards`: materialized card snapshot; JSON `fields`; denormalized `type_id`,
  `status`, `owner`, timestamps, `schema_version`, `version`.
- `events`: append-only events with actor, timestamp, type, diff JSON.
- `links`: source card, link type, target card, note, timestamps.
- `comments`: card id, author, markdown body, timestamps.
- `users`: registered users.
- `idempotency_keys`: request key, actor, status, body, created_at
  (composite PK `key, actor`).
- `fts_cards`: FTS5 index for title and searchable text fields.

Definitions are not stored in SQLite. They are loaded from `definitions/`
and cached in memory as normalized config. Git-backed files remain the source
of truth.

---

## Workspace Loading

A workspace is a directory with `definitions/` loaded at startup. The binary,
CLI, and clients all take `--workspace <dir>`:

```bash
cards serve --workspace ./examples/demo-workspace --port 8787
cards list --workspace ./examples/demo-workspace
```

The `--workspace` flag points at a directory containing `definitions/`
(workspace.json, card-types/, boards/, extensions.json). The loader reads
and merges these into **exactly one** in-memory workspace, exposed through
`GET /v1/workspace`.

Remote/CLI clients use `--url` (or `CARDS_URL`) to point at a running server
and `--as` (or `CARDS_USER`) to set the actor.

This lets an app ship a base workspace config while an agent harness layers on
local boards, views, or card types.

---

## Process Modes

### Sidecar HTTP

Primary mode for Python and Node harnesses:

```bash
cards serve --workspace ./examples/demo-workspace --port 8787
```

Clients connect to `http://127.0.0.1:8787/v1`. SSE streams use the same process.

### CLI

Useful for scripts and humans:

```bash
cards list --owner me --status todo,in_progress
cards patch CARD_ID --status review --version 3
```

CLI commands call the same service layer directly when local, or optionally call
`CARDS_URL` when configured for remote/sidecar mode.

### MCP

The Go binary can expose MCP over stdio:

```bash
cards mcp --workspace ./examples/demo-workspace
```

MCP tools should be generated from the same normalized workspace introspection
used by HTTP clients. The MCP adapter should delegate mutations to the service
layer, not bypass it.

### Embedded Go

Go consumers can eventually import the core as a library, but this is secondary
to the binary/sidecar contract for v1.

---

## Extension Supervisor

The Go binary includes an **optional** supervisor for declared extensions
(`cards run-extensions`). The supervisor is part of the kernel only because
hook dispatch and service lifecycle benefit from sharing the same event bus
that already exists for SSE.

Responsibilities:

- Read `definitions/extensions.yaml` (or `extensions:` in `workspace.yaml`).
- Subscribe to the internal event bus.
- For `kind: hook` extensions whose `filter` matches a fired event, spawn the
  declared `run` command with the event JSON on stdin and standard
  environment variables (`CARDS_URL`, `CARDS_WORKSPACE`, etc.).
- For `kind: service` extensions with `autostart: true` — **[proposed, not
  yet implemented]** start the process when the supervisor starts and restart
  on crash if `restart: on-failure` is set. (Only `hook` and `run` extensions
  are wired today; see [`INTEGRATOR-REFERENCE.md`](INTEGRATOR-REFERENCE.md) §7
  for the drift note.)
- For `kind: run` extensions, invoke on `cards do <id>`.
- Capture stdout/stderr to per-extension logs in `.cards/logs/`.

The supervisor is not required: extensions can be started by systemd, docker
compose, or by hand. The supervisor exists so a single `cards run-extensions`
in a developer workspace gets the whole declared system running.

The supervisor never loads extension code into the core process. It only
spawns subprocesses and reads events. Crashes are isolated.

See [`EXTENSIONS.md`](EXTENSIONS.md) for the declaration format and worked
examples.

---

## Event Taxonomy: Mutation vs Condition

Events have two origins. **Mutation events** (`status_changed`, `comment_added`,
…) are the synchronous consequence of a write and are always card-scoped — this
is what exists today. **Condition events** (planned) are emitted when a declared
threshold crosses: instant ones (`wip_exceeded`, `lane_drained`, `card_blocked`,
`transition_rejected`) evaluated right after the triggering mutation, and
temporal ones (`status_timeout`, `card_idle`) emitted by a **monitor evaluator**
goroutine that sits beside the extension supervisor and is driven by a deadline
min-heap (it sleeps until the next deadline rather than polling on a fixed tick,
and only schedules deadlines a live consumer is listening for).

Condition events are declared as `monitors` on a board (data, not code) and
publish onto the same bus as mutation events, so SSE/webhooks/hooks consume one
unified stream. Two model changes enable board-level conditions: events gain a
`scope` (`card` | `board`) with a nullable `card_id` and a recorded `board_id`.
Critically, the core only **emits** condition signals — it never acts on them;
reprioritizing, escalating, and reassigning are the integrator's policy. See
[`INTEGRATION.md`](INTEGRATION.md) for the full contract.

---

## Planned Integrations

Python and Node client packages are **planned** but not yet built. They will
be thin clients and launchers around the Go binary, so agent harnesses can
embed Work Cards without managing a separate process.

**Python** (`work-cards`):

```python
from work_cards import Cards

cards = Cards.start(workspace="./examples/demo-workspace")
cards.create(type_id="programming-task", title="Update docs", status="todo",
             fields={"description": "Clarify setup", "branch": "docs/setup"})
```

**Node/TypeScript** (`@work-cards/client`):

```ts
import { Cards } from "@work-cards/client";
const cards = await Cards.start({ workspace: "./examples/demo-workspace" });
await cards.create({ type_id: "programming-task", title: "Update docs", status: "todo",
                     fields: { description: "Clarify setup", branch: "docs/setup" } });
```

Both will provide `connect(url)` for an existing server and `start(workspace)`
to launch the bundled binary. Binary distribution will use platform wheels /
npm platform packages so no Go toolchain is required at install time.

For MCP-heavy TypeScript harnesses, launch `cards mcp` directly as the MCP
server — it keeps one source of tool behavior.

Until these land, use the HTTP API, CLI, or MCP server directly.

---

## Release and Packaging Pipeline

CI builds the Go binary for common targets:

```text
darwin-arm64
darwin-amd64
linux-amd64
linux-arm64
windows-amd64
```

Release artifacts (planned):

- `cards_<version>_<os>_<arch>.tar.gz`
- checksums (`SHA256SUMS`)
- optional SBOM
- npm platform packages (when Node client lands)
- Python wheels (when Python client lands)

The same binary supports `serve`, `mcp`, and CLI commands. No separate
server and CLI binaries.

---

## Port and Lifecycle Management

The server binds to `127.0.0.1` by default and serves one workspace per
process. Health endpoint:

```http
GET /v1/health
```

Response includes version, **workspace id** (singular — one process serves one
workspace), config digest, and SQLite path.

Future Python/Node launchers will prefer `CARDS_URL` if set, otherwise start
the bundled binary on `127.0.0.1` and read the selected port from the health
endpoint.

---

## Security and Trust Boundary

Work Cards is designed for local single-instance coordination. The server
binds to `127.0.0.1` by default.

Important boundaries:

- `command` field type was **removed** from the core (see `NOTES.md` D2);
  extensions own execution contracts. The core never executes anything.
- `path`/`json`/`yaml` field types were also removed; store such content as
  `string`/`text`/`artifact` and let an extension validate and annotate.
- If exposed beyond localhost, put the service behind a reverse proxy or an
  auth extension. There is no baked-in auth; Work Cards is treated as an
  internal tool.
- Mirror import is version-gated: each markdown file declares the `version` it
  was edited from; stale imports are `409 version_conflict`, never a silent
  overwrite (see `SPEC.md` §3). **[planned, not yet implemented]**

---

## Summary

The core is built in Go, distributed as a self-contained `cards` binary, with
Python/Node packages planned as thin clients plus launchers. State lives in
SQLite, definitions in git-backed JSON/YAML, artifacts on disk, and all
business logic inside the Go service layer.

This gives agent harnesses a low-friction local dependency while preserving a
clean API boundary for other applications.
