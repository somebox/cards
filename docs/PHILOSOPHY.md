# Philosophy

Work Cards aims to be a small kernel for typed-card coordination, not a
framework. The design follows a handful of principles borrowed from minimal
Unix tools and modern minimal agent harnesses.

## Principles

### 1. Small core, big composition

The core does cards, fields, events, links, comments, columns, and storage.
Everything else — dispatchers, agents, UIs, sync, reports, validations — is an
external process talking to the API or reacting to events. The core grows
reluctantly. The field catalog stays small (ten types); richer payload
validation (JSON/YAML schemas, path confinement, command execution) is
extension territory.

### 2. Files where they help

Definitions are git-backed JSON or YAML. An optional markdown mirror keeps
cards human-reviewable. Anything authored, reviewed, or versioned by humans
belongs in a file. Anything operational and queried belongs in SQLite.

### 3. Schemas, not magic

Behavior comes from explicit typed schemas the agent or human can introspect.
No hidden context injection, no behind-the-scenes mutation, no implicit
defaults that aren't visible in the API response.

### 4. Progressive disclosure

Introspection can be scoped. Tool surfaces (MCP) can be narrow. An agent asks
for what it needs; the core does not push every type, board, and tool into
every session.

### 5. Hooks, not engines

There is no automation engine, workflow DSL, or rules language. There are
events, hooks (subprocess scripts), and external processes that subscribe via
SSE. If you need automation, write an extension.

### 6. Extensions over plugins

Extensions are independent processes in any language. They communicate via the
HTTP API and event stream; they do not load into the core process. This keeps
the core small, language-agnostic, and crash-isolated.

### 7. YOLO defaults

The default deployment is local-only, single-tenant, trusted environment. We
do not ship permission theater. Authentication and isolation are the host's
responsibility.

### 8. Stable, documented contracts

The HTTP and event contracts are versioned and meant to outlive any specific
implementation. Wrapper libraries (Python/Node) and the CLI are layered on top
of the same contract.

### 9. Fail loudly, guide recovery

Every rejection includes a structured error: which field, which value, what
was allowed. Agents retry; agents self-correct.

### 10. Boring tech

SQLite, JSON/YAML, HTTP/SSE, subprocess. No new languages, no new protocols,
no new databases.

## What this means in practice

- Features are added only when an external extension cannot do the job well.
- We resist building anything that can be expressed as `cards events stream`
  plus a small script.
- The materialized card is the durable work product; the event log is
  coordination memory that may be trimmed. The core is a coordinator, not an
  archive.
- We prefer "this is a file you edit" over "this is a setting in the API."
- We prefer "run an extension" over "configure a built-in."
- We document conventions and seams, not "the right way" to use the system.

## What this is NOT

- Not a Jira replacement.
- Not a workflow automation platform.
- Not a generic document store.
- Not an archive.

It is a small substrate. Anything richer is something built on top of it using
the contracts described in this repository.

## Related documents

| Doc | Contents |
|-----|----------|
| [`NOTES.md`](NOTES.md) | Design notes (v0.4 changes and rationale) |
| [`SPEC.md`](SPEC.md) | Normative product behavior and API |
| [`MCP.md`](MCP.md) | MCP tool surface |
| [`EXTENSIONS.md`](EXTENSIONS.md) | Extension model (hooks, services, runs) |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | Go core, packaging, Python/Node integration |
| [`DEVELOPER-REFERENCE.md`](DEVELOPER-REFERENCE.md) | Schemas, transitions, links, versioning |
| [`LIFECYCLE-EXAMPLES.md`](LIFECYCLE-EXAMPLES.md) | End-to-end card lifecycles |
