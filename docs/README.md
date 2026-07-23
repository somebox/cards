# Cards Documentation

Welcome to the Cards documentation. The files have been organized into topical
folders to assist developers, integrators, and agents. The published site is
at <https://somebox.github.io/cards/>; the source files below remain browsable
on GitHub.

For repository-wide code quality, style, and review expectations, see
[`../CONTRIBUTING.md`](../CONTRIBUTING.md).

## Directory Structure

### Site Index & Getting Started

- [index.md](./index.md) — The published site landing page (home).
- [get-started.md](./get-started.md) — Install, create a workspace, serve the board, connect an agent.
- [using-cards.md](./using-cards.md) — How a project runs on Cards, then every operation with CLI, HTTP, and MCP examples.

### [Concepts](./concepts)

- [index.md](./concepts/index.md) — Vocabulary (workspaces, boards, card types) and use case configurations.
- [philosophy.md](./concepts/philosophy.md) — Core principles and design constraints behind keeping the system small.

### [Specification](./spec)

- [index.md](./spec/index.md) — Main design specification, index, and principles of the Cards contract.
- [data-model.md](./spec/data-model.md) — Storage layouts and core data structures.
- [api-surface.md](./spec/api-surface.md) — Endpoint routers, client ergonomics, and boundaries.
- [events-history.md](./spec/events-history.md) — Core timelines, actors, filters, and history scopes.
- [query-dsl.md](./spec/query-dsl.md) — JSON-based filters and error-catalog indices.
- [card-types.md](./spec/card-types.md) — Schemas and schema example sets.

### [Architecture](./architecture)

- [index.md](./architecture/index.md) — Platform views, thread safety, deployment, and supervisory seams.
- [design-system.md](./architecture/design-system.md) — Web UI design tokens, component architecture, and CSS variables.
- [lifecycle-schema.md](./architecture/lifecycle-schema.md) — Extension lifecycle kinds, autostart, and restart policy (supplementary).
- [reload.md](./architecture/reload.md) — Workspace definition reload contract (supplementary).

### [Reference](./reference)

- [workspace-and-boards.md](./reference/workspace-and-boards.md) — Workspace configuration guide and Board/View landing indices.
- [card-definitions.md](./reference/card-definitions.md) — Workspace schemas and version control rules.
- [card-type-examples.md](./reference/card-type-examples.md) — Card Type definitions and validation.
- [cli.md](./reference/cli.md) — `cards` CLI usage, queries, and backups.
- [implementation-status.md](./reference/implementation-status.md) — Code-verified drift audit (built vs. proposed) and type mappings.

### [Agents](./agents)

- [mcp.md](./agents/mcp.md) — MCP quickstart: connect an agent to a Cards board.
- [instructions.md](./agents/instructions.md) — Ready-to-paste agent instruction block for harness standing instructions.

### [Events](./events)

- [index.md](./events/index.md) — Slim overview/index for the event docs.
- [core.md](./events/core.md) — Event contract: logs, buses, dispatch seams, observer queues, and built/proposed event types.
- [rollout.md](./events/rollout.md) — Staged rollout history and current implementation status.
- [integration.md](./events/integration.md) — Server-Sent Events (SSE), monitors, and conditional business logic.

### [Extensions](./extensions)

- [index.md](./extensions/index.md) — Execution pipelines, hook declarations, and local automation policies.
- [mcp.md](./extensions/mcp.md) — Model Context Protocol architecture, agent tool bindings, and anti-hallucination signatures.

### [Design](./design)

- [themes.md](./design/themes.md) — Theme contract: tokens, stable hooks, validation, and sharing.
- [style-field.md](./design/style-field.md) — Board-chosen enum drives card visuals (supplementary, not in nav).
- [auth.md](./design/auth.md) — Identity and attribution design (excluded from the published site).
- [core-boundaries.md](./design/core-boundaries.md) — Core boundary design (excluded from the published site).
- [outbox-gonogo.md](./design/outbox-gonogo.md) — Outbox go/no-go gate (excluded from the published site).
- [fts-vs-like-disposition.md](./design/fts-vs-like-disposition.md) — FTS5 vs LIKE decision at POC scale (excluded from the published site).
- [tui-bus-disposition.md](./design/tui-bus-disposition.md) — TUI bus & surface review disposition (excluded from the published site).
- [subscribers.md](./design/subscribers.md) — Durable subscribers RFC (excluded from the published site).

### [Examples](./examples)

- [index.md](./examples/index.md) — Landing index for worked scenarios.
- [setup.md](./examples/setup.md) — Common environment details.
- [software-delivery.md](./examples/software-delivery.md) — End-to-end walkthrough for software engineering lines.
- [shop-floor.md](./examples/shop-floor.md) — End-to-end CNC/3D-printing fabrications walkthrough.

---

### Flat Reference Files at Docs Root

- [roadmap.md](./roadmap.md) — Forward-looking work relocated from the board backlog (auth, storage, attachments, events, API, extensions).
- [design-notes.md](./design-notes.md) — Historical rationale and stable design decisions (D1–D18).

Historical process docs (sprint plans, handovers, the original GitHub Pages plan) live under [`archive/`](./archive), excluded from the published site.

For source code level specifics, you can also consult package-level READMEs:
- [internal/hooks/README.md](../internal/hooks/README.md)
- [internal/mcp/README.md](../internal/mcp/README.md)
- [internal/artifacts/README.md](../internal/artifacts/README.md)
