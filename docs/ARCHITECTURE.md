# Architecture — Go Core With Python and Node Integration

This document describes the implementation platform for Work Cards. The design
assumes a Go **kernel** distributed as a small `cards` binary, with Python and
Node packages that make it easy to embed in agent harnesses, and an extension
model where behavior is added by independent processes in any language.

Normative product behavior lives in [`SPEC.md`](SPEC.md). The principles
behind these choices live in [`PHILOSOPHY.md`](PHILOSOPHY.md). The extension
contract lives in [`EXTENSIONS.md`](EXTENSIONS.md). Schema authoring lives in
[`DEVELOPER-REFERENCE.md`](DEVELOPER-REFERENCE.md).

---

## Goals

- Run as a single local service or CLI with minimal setup.
- Serve **exactly one workspace per process** (one SQLite file, possibly
  assembled from multiple context files). Multi-workspace deployments run
  multiple processes on different ports/paths — the binary, CLI, and clients
  all take `--workspace`/`--url`, so this is trivial and is the supported
  multi-tenancy path for v1.
- Work as a sidecar for Python, Node/TypeScript, MCP, and other clients.
- Use commonly available libraries and avoid mandatory external services.
- Keep runtime state in SQLite and workspace artifacts on disk.
- Load one or more git-backed config contexts from outside the app and merge
  them into **one** workspace.
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

Recommended dependencies:

- HTTP router: `net/http` plus `chi`.
- CLI: `cobra` or `urfave/cli`.
- SQLite: prefer `modernc.org/sqlite` for pure-Go builds; use CGO SQLite only
  if FTS/build behavior requires it and release packaging covers it.
- YAML: `gopkg.in/yaml.v3`.
- JSON Schema: `github.com/santhosh-tekuri/jsonschema` or equivalent.
- File watching: `fsnotify`.

Keep dependency choices boring. The project should be easy to build and reason
about without a framework.

---

## Runtime Shape

The Go binary provides several entry points:

```bash
cards serve --workspace ./.work-cards --port 8787
cards mcp --workspace ./.work-cards
cards list --board engineering --owner me
cards workspace show
```

Internally, these entry points call the same service layer.

Suggested package boundaries:

```text
cmd/cards/          CLI binary and subcommand wiring
internal/core/      cards, schemas, transitions, validation, events
internal/config/    load/merge/validate JSON/YAML contexts
internal/store/     storage interfaces
internal/sqlite/    SQLite implementation
internal/httpapi/   REST and SSE handlers
internal/mcp/       MCP adapter over core services
internal/artifacts/ workspace file/artifact helpers
```

Public Go library use can be added later by promoting stable packages out of
`internal/`, but v1 can keep API stability focused on HTTP/CLI/MCP.

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

The HTTP layer should not reimplement these rules.

---

## Storage

Use SQLite as the operational store.

Recommended tables:

- `cards`: materialized card snapshot; JSON `fields`; denormalized `type_id`,
  `status`, `owner`, timestamps, `schema_version`, `version`.
- `events`: append-only events with actor, timestamp, type, diff JSON.
- `links`: source card, link type, target card, note, timestamps.
- `comments`: card id, author, markdown body, timestamps.
- `users`: registered users.
- `idempotency_keys`: request key, actor, result hash/body reference.
- `fts_cards`: FTS5 index for title and configured searchable fields/comments.

Definitions are not primarily stored in SQLite. They are loaded from
`definitions/` and cached in memory as normalized config. SQLite can keep a
small `definition_digest` table for reload diagnostics and event metadata, but
git-backed files remain the source of truth.

Large data goes to `artifacts/` or external URIs. Cards hold `artifact`, `path`,
`json`, `yaml`, or `command` metadata depending on the use case.

---

## Config Contexts

Configs are loaded from one or more paths supplied by CLI flags, environment, or
wrapper libraries.

Examples:

```bash
cards serve \
  --context ./cards.workspace.yaml \
  --context ./cards.engineering.yaml \
  --context ./cards.shop.yaml
```

```bash
CARDS_CONTEXT=./cards.workspace.yaml,./cards.engineering.yaml cards workspace show
```

A context may point at:

- a workspace directory containing `definitions/`,
- a single YAML/JSON file,
- a directory of partial config files.

Merge rules should be explicit and deterministic, and produce **exactly one
workspace**:

- Load contexts in command-line order.
- Later contexts can add boards, views, card types, link types, and tags.
- Later contexts can override settings only when the key is present.
- Duplicate ids with different definitions are rejected unless
  `--allow-override` is explicitly set.
- The normalized result is one workspace, exposed through `GET /v1/workspace`.

This lets an app ship a base workspace config while an agent harness layers on
local boards, views, or card types.

---

## Process Modes

### Sidecar HTTP

Primary mode for Python and Node harnesses:

```bash
cards serve --workspace ./.work-cards --port 8787
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
cards mcp --workspace ./.work-cards
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
- For `kind: service` extensions with `autostart: true`, start the process
  when the supervisor starts. Restart on crash if `restart: on-failure` is
  set.
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

## Python Integration

The Python package should be a small client and launcher around the Go binary.

Package name example: `work-cards`.

User experience:

```bash
pip install work-cards
```

```python
from work_cards import Cards

cards = Cards.start(
    workspace=".work-cards",
    context=["cards.workspace.yaml", "cards.engineering.yaml"],
)

cards.create(
    type_id="programming-task",
    title="Update docs",
    status="todo",
    fields={"description": "Clarify setup", "branch": "docs/setup"},
)
```

The Python package can provide:

- `Cards.connect(url=...)` for an existing service.
- `Cards.start(workspace=..., context=[...])` to launch the bundled binary.
- `Cards.cli([...])` for simple subprocess use when a long-lived service is not
  needed.

Binary distribution options:

- Preferred: platform wheels that include the matching `cards` binary.
- Acceptable: download the matching binary from GitHub Releases at install time
  or first use.
- Avoid: requiring users to install Go and compile during `pip install`.

Python wheels should not write runtime state into site-packages. The wrapper
passes workspace paths explicitly and stores PID/port metadata in the workspace
or a user cache directory.

---

## Node / TypeScript Integration

The Node package should mirror the Python shape.

Package name example: `@work-cards/client`.

User experience:

```bash
npm install @work-cards/client
```

```ts
import { Cards } from "@work-cards/client";

const cards = await Cards.start({
  workspace: ".work-cards",
  context: ["cards.workspace.yaml", "cards.engineering.yaml"],
});

await cards.create({
  type_id: "programming-task",
  title: "Update docs",
  status: "todo",
  fields: { description: "Clarify setup", branch: "docs/setup" },
});
```

Binary distribution options:

- Preferred: main package plus platform-specific optional dependencies, e.g.
  `@work-cards/cards-darwin-arm64`, `@work-cards/cards-linux-x64`.
- Acceptable: postinstall download from GitHub Releases.
- Avoid: building Go during `npm install`.

The Node client can provide:

- `Cards.connect({ url })`.
- `Cards.start({ workspace, context })`.
- typed request/response helpers generated from the OpenAPI spec later.

For MCP-heavy TypeScript harnesses, either:

- launch `cards mcp` directly as an MCP server, or
- expose a TypeScript MCP server that delegates to the Go HTTP API.

The first option is simpler and keeps one source of tool behavior.

---

## Release and Packaging Pipeline

CI should build the Go binary for common targets:

```text
darwin-arm64
darwin-amd64
linux-amd64
linux-arm64
windows-amd64
```

Release artifacts:

- `cards_<version>_<os>_<arch>.tar.gz`
- checksums (`SHA256SUMS`)
- optional SBOM
- npm platform packages
- Python wheels or downloader package

The same binary should support `serve`, `mcp`, and CLI commands. Avoid separate
server and CLI binaries unless size or dependency isolation becomes necessary.

---

## Port and Lifecycle Management

Python/Node launchers should:

- prefer `CARDS_URL` if set,
- otherwise start the bundled binary on `127.0.0.1` with port `0` or a requested
  port,
- read a startup line or health endpoint to discover the selected port,
- stop the child process when the client closes,
- keep logs in the workspace or user cache,
- avoid leaving orphan processes when possible.

Recommended health endpoint:

```http
GET /v1/health
```

Response includes version, **workspace id** (singular — one process serves one
workspace), config digest, and SQLite path.

---

## Security and Trust Boundary

Work Cards is designed for local single-instance coordination. The first version
should bind to `127.0.0.1` by default.

Important boundaries:

- `command` field type was **removed** from the core (see `NOTES.md` D2);
  extensions own execution contracts. The core never executes anything.
- `path`/`json`/`yaml` field types were also removed; store such content as
  `string`/`text`/`artifact` and let an extension validate and annotate.
- Python/Node wrappers should not pass untrusted config paths silently.
- If exposed beyond localhost, callers should put the service behind their
  application's auth or a local reverse proxy.
- Mirror import is version-gated: each markdown file declares the `version` it
  was edited from; stale imports are `409 version_conflict`, never a silent
  overwrite (see `SPEC.md` §3).

---

## Recommendation

Build the core in Go, distribute `cards` as a self-contained binary, and treat
Python/Node packages as thin clients plus launchers. Keep state in SQLite,
definitions in git-backed JSON/YAML, artifacts on disk, and all business logic
inside the Go service layer.

This gives agent harnesses a low-friction local dependency while preserving a
clean API boundary for other applications.
