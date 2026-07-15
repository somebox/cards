# Philosophy

Cards is a small kernel for typed-card coordination, not a framework. These
ten principles decide what goes in the core, what stays out, and why. They are
normative: a change that violates one needs a better reason than convenience.

## Principles

### 1. Minimal core, extensible by composition

The core does cards, fields, events, links, comments, columns, and storage.
Everything else — dispatchers, agents, UIs, sync, reports, richer validation —
is an external process talking to the API or reacting to events. The core
grows reluctantly; the field catalog stays at ten types.

### 2. Cards are files

Definitions are git-backed JSON; board state exports to a JSONL snapshot that
is committed next to them. SQLite is the working store — it exists for
queries, search, and concurrent writes, not as the source of truth you have
to keep. Anything authored or reviewed by humans belongs in a file; anything
operational and queried belongs in the database.

### 3. Schemas are the contract

Behavior comes from explicit, introspectable schemas. A card type's definition
is the web form, the API contract, the CLI surface, and the generated MCP
tools. No hidden context injection, no behind-the-scenes mutation, no implicit
defaults that don't appear in the API response.

### 4. Progressive disclosure

Introspection is scoped. Tool surfaces can be narrow. An agent asks for what
it needs; the core does not push every type, board, and tool into every
session.

### 5. Events and hooks, not a workflow engine

There is no automation engine, rules language, or workflow DSL. There are
events, hooks (subprocess scripts), and external processes subscribing over
SSE. Automation is something you write, not something you configure.

### 6. Extensions are separate processes

Extensions run outside the core, in any language, talking to the HTTP API and
event stream. The core never loads their code — which keeps it small,
language-agnostic, and crash-isolated.

### 7. Local and trusted by default

The default deployment is single-user on `localhost`. There is no built-in
authentication or permission system: isolation belongs to the host (a reverse
proxy, a VPN, an SSH tunnel). See
[users & auth](../guides/users-and-auth.md).

### 8. Stable, documented contracts

The HTTP and event contracts are versioned and meant to outlive any specific
implementation. The CLI and client libraries are layers over the same
contract, never side doors.

### 9. Fail loudly, guide recovery

Every rejection is a structured error naming the field, the value, and what
was allowed. That is what lets agents retry and self-correct instead of
guessing.

### 10. Boring technology

SQLite, JSON, HTTP/SSE, subprocesses. No new languages, protocols, or
databases, and no build steps.

## In practice

- A feature is added to the core only when an extension cannot do the job.
- Anything expressible as `cards events stream` plus a small script stays a
  script.
- The materialized card is the durable work product; the event log is
  coordination memory and may be trimmed.
- "A file you edit" beats "a setting in the API."

## What Cards is not

Not a Jira replacement, not a workflow automation platform, not a generic
document store, not an archive. It is a small substrate; anything richer is
built on top of it, against the contracts in this documentation.

## Related

| Doc | Contents |
|-----|----------|
| [Concepts](CONCEPTS.md) | Vocabulary and the reasoning behind Cards |
| [The workflow](WORKFLOW.md) | How a project runs on Cards day to day |
| [Specification](../spec/SPEC.md) | Normative behavior and API |
| [Built vs proposed](../reference/INTEGRATOR-REFERENCE.md) | Code-verified audit of what's implemented |
| [Extensions](../extensions/EXTENSIONS.md) | Hooks, services, and runs |
| [Architecture](../architecture/ARCHITECTURE.md) | The Go core and packaging |
