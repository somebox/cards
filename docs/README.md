# Work Cards Documentation

Welcome to the Work Cards documentation. The files have been organized into topical folders to assist developers, integrators, and agents.

## Directory Structure

### [Concepts](./concepts)
- [CONCEPTS.md](./concepts/CONCEPTS.md) — Vocabulary (workspaces, boards, card types) and use case configurations.
- [PHILOSOPHY.md](./concepts/PHILOSOPHY.md) — Core principles and design constraints behind keeping the system small.

### [Specification](./spec)
- [SPEC.md](./spec/SPEC.md) — Main design specification, index, and principles of the Work Cards contract.
- [SPEC-DATA-MODEL.md](./spec/SPEC-DATA-MODEL.md) — Storage layouts and core data structures.
- [SPEC-API-SURFACE.md](./spec/SPEC-API-SURFACE.md) — Endpoint routers, client ergonomics, and boundaries.
- [SPEC-EVENTS-HISTORY.md](./spec/SPEC-EVENTS-HISTORY.md) — Core timelines, actors, filters, and history scopes.
- [SPEC-QUERY-DSL.md](./spec/SPEC-QUERY-DSL.md) — JSON-based filters and error-catalog indices.
- [SPEC-CARDTYPE-EXAMPLES.md](./spec/SPEC-CARDTYPE-EXAMPLES.md) — Schemas and schema example sets.

### [Architecture](./architecture)
- [ARCHITECTURE.md](./architecture/ARCHITECTURE.md) — Platform views, thread safety, deployment, and supervisory seams.
- [DESIGN.md](./architecture/DESIGN.md) — Web UI design tokens, component architecture, and CSS variables.

### [Reference](./reference)
- [DEVELOPER-REFERENCE.md](./reference/DEVELOPER-REFERENCE.md) — Workspace configuration guide and Board/View landing indices.
- [DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md](./reference/DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md) — Workspace schemas and version control rules.
- [DEVELOPER-REFERENCE-TYPES-EXAMPLES.md](./reference/DEVELOPER-REFERENCE-TYPES-EXAMPLES.md) — Card Type definitions and validation.
- [DEVELOPER-REFERENCE-CLI.md](./reference/DEVELOPER-REFERENCE-CLI.md) — `cards` CLI usage, queries, and backups.
- [INTEGRATOR-REFERENCE.md](./reference/INTEGRATOR-REFERENCE.md) — Drifts, codebase bindings, and type mappings.

### [Events](./events)
- [EVENTS.md](./events/EVENTS.md) — Event logs, buses, dispatch seams, and observer queues.
- [INTEGRATION.md](./events/INTEGRATION.md) — Server-Sent Events (SSE), monitors, and conditional business logic.

### [Extensions](./extensions)
- [EXTENSIONS.md](./extensions/EXTENSIONS.md) — Execution pipelines, hook declarations, and local automation policies.
- [MCP.md](./extensions/MCP.md) — Model Context Protocol architecture, agent tool bindings, and anti-hallucination signatures.

### [Examples](./examples)
- [LIFECYCLE-EXAMPLES.md](./examples/LIFECYCLE-EXAMPLES.md) — Landing index for worked scenarios.
- [LIFECYCLE-EXAMPLES-SETUP.md](./examples/LIFECYCLE-EXAMPLES-SETUP.md) — Common environment details.
- [LIFECYCLE-EXAMPLES-SOFTWARE.md](./examples/LIFECYCLE-EXAMPLES-SOFTWARE.md) — End-to-end walkthrough for software engineering lines.
- [LIFECYCLE-EXAMPLES-SHOPFLOOR.md](./examples/LIFECYCLE-EXAMPLES-SHOPFLOOR.md) — End-to-end CNC/3D-printing fabrications walkthrough.

---

### Flat Reference Files at Docs Root
- [NOTES.md](./NOTES.md) — Historical rationale and stable design decisions (D1–D18).

For source code level specifics, you can also consult package-level READMEs:
- [internal/hooks/README.md](../internal/hooks/README.md)
- [internal/mcp/README.md](../internal/mcp/README.md)
- [internal/artifacts/README.md](../internal/artifacts/README.md)
