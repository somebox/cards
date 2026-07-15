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

### [Concepts](./concepts)

- [CONCEPTS.md](./concepts/CONCEPTS.md) — Vocabulary (workspaces, boards, card types) and use case configurations.
- [PHILOSOPHY.md](./concepts/PHILOSOPHY.md) — Core principles and design constraints behind keeping the system small.

### [Specification](./spec)

- [SPEC.md](./spec/SPEC.md) — Main design specification, index, and principles of the Cards contract.
- [SPEC-DATA-MODEL.md](./spec/SPEC-DATA-MODEL.md) — Storage layouts and core data structures.
- [SPEC-API-SURFACE.md](./spec/SPEC-API-SURFACE.md) — Endpoint routers, client ergonomics, and boundaries.
- [SPEC-EVENTS-HISTORY.md](./spec/SPEC-EVENTS-HISTORY.md) — Core timelines, actors, filters, and history scopes.
- [SPEC-QUERY-DSL.md](./spec/SPEC-QUERY-DSL.md) — JSON-based filters and error-catalog indices.
- [SPEC-CARDTYPE-EXAMPLES.md](./spec/SPEC-CARDTYPE-EXAMPLES.md) — Schemas and schema example sets.

### [Architecture](./architecture)

- [ARCHITECTURE.md](./architecture/ARCHITECTURE.md) — Platform views, thread safety, deployment, and supervisory seams.
- [DESIGN.md](./architecture/DESIGN.md) — Web UI design tokens, component architecture, and CSS variables.
- [LIFECYCLE-SCHEMA.md](./architecture/LIFECYCLE-SCHEMA.md) — Extension lifecycle kinds, autostart, and restart policy (supplementary).
- [RELOAD.md](./architecture/RELOAD.md) — Workspace definition reload contract (supplementary).

### [Reference](./reference)

- [DEVELOPER-REFERENCE.md](./reference/DEVELOPER-REFERENCE.md) — Workspace configuration guide and Board/View landing indices.
- [DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md](./reference/DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md) — Workspace schemas and version control rules.
- [DEVELOPER-REFERENCE-TYPES-EXAMPLES.md](./reference/DEVELOPER-REFERENCE-TYPES-EXAMPLES.md) — Card Type definitions and validation.
- [OPERATIONS.md](./reference/OPERATIONS.md) — Every operation documented once, with CLI, HTTP, and MCP examples side by side.
- [DEVELOPER-REFERENCE-CLI.md](./reference/DEVELOPER-REFERENCE-CLI.md) — `cards` CLI usage, queries, and backups.
- [INTEGRATOR-REFERENCE.md](./reference/INTEGRATOR-REFERENCE.md) — Code-verified drift audit (built vs. proposed) and type mappings.

### [Agents](./agents)

- [mcp.md](./agents/mcp.md) — MCP quickstart: connect an agent to a Cards board.
- [instructions.md](./agents/instructions.md) — Ready-to-paste agent instruction block for harness standing instructions.

### [Events](./events)

- [EVENTS.md](./events/EVENTS.md) — Slim overview/index for the event docs.
- [EVENTS-CORE.md](./events/EVENTS-CORE.md) — Event contract: logs, buses, dispatch seams, observer queues, and built/proposed event types.
- [EVENTS-ROLLOUT.md](./events/EVENTS-ROLLOUT.md) — Staged rollout history and current implementation status.
- [INTEGRATION.md](./events/INTEGRATION.md) — Server-Sent Events (SSE), monitors, and conditional business logic.

### [Extensions](./extensions)

- [EXTENSIONS.md](./extensions/EXTENSIONS.md) — Execution pipelines, hook declarations, and local automation policies.
- [MCP.md](./extensions/MCP.md) — Model Context Protocol architecture, agent tool bindings, and anti-hallucination signatures.

### [Design](./design)

- [THEMES.md](./design/THEMES.md) — Theme contract: tokens, stable hooks, validation, and sharing.
- [STYLE-FIELD.md](./design/STYLE-FIELD.md) — Board-chosen enum drives card visuals (supplementary, not in nav).
- [AUTH.md](./design/AUTH.md) — Identity and attribution design (excluded from the published site).
- [CORE-BOUNDARIES.md](./design/CORE-BOUNDARIES.md) — Core boundary design (excluded from the published site).
- [OUTBOX-GONOGO.md](./design/OUTBOX-GONOGO.md) — Outbox go/no-go gate (excluded from the published site).
- [SUBSCRIBERS.md](./design/SUBSCRIBERS.md) — Durable subscribers RFC (excluded from the published site).

### [Examples](./examples)

- [LIFECYCLE-EXAMPLES.md](./examples/LIFECYCLE-EXAMPLES.md) — Landing index for worked scenarios.
- [LIFECYCLE-EXAMPLES-SETUP.md](./examples/LIFECYCLE-EXAMPLES-SETUP.md) — Common environment details.
- [LIFECYCLE-EXAMPLES-SOFTWARE.md](./examples/LIFECYCLE-EXAMPLES-SOFTWARE.md) — End-to-end walkthrough for software engineering lines.
- [LIFECYCLE-EXAMPLES-SHOPFLOOR.md](./examples/LIFECYCLE-EXAMPLES-SHOPFLOOR.md) — End-to-end CNC/3D-printing fabrications walkthrough.

---

### Flat Reference Files at Docs Root

- [ROADMAP.md](./ROADMAP.md) — Forward-looking work relocated from the board backlog (auth, storage, attachments, events, API, extensions).
- [NOTES.md](./NOTES.md) — Historical rationale and stable design decisions (D1–D18).
- [GH-PAGES-TODO.md](./GH-PAGES-TODO.md) — Roadmap and plan for building GitHub Pages & integrations.

For source code level specifics, you can also consult package-level READMEs:
- [internal/hooks/README.md](../internal/hooks/README.md)
- [internal/mcp/README.md](../internal/mcp/README.md)
- [internal/artifacts/README.md](../internal/artifacts/README.md)
