# Work Cards — Design Specification

**Status:** v0.4 — beta, in-progress (not yet stable). The v0.4 pass trimmed
the field catalog, locked single-workspace-per-instance, fixed link direction
and concurrency contracts, and made the event/error contracts normative. See
[`NOTES.md`](../NOTES.md) for the full change log and rationale. The core
kernel, HTTP API, CLI, MCP server, web UI, and hook supervisor are largely
built and dogfooded; the API is not yet declared stable — some surfaces
described below are design-only or not yet wired (see per-section status
notes). Treat this document as the target contract, not a certification of a
finished build.

Work Cards is a small substrate for typed-card coordination. It stores cards,
events, links, and comments; validates writes against versioned schemas;
streams events; and exposes one HTTP/CLI/MCP surface. It is **not** a workflow
engine or a long-term archive — behavior beyond core CRUD lives in
**extensions** (independent processes in any language), see
[`EXTENSIONS.md`](../extensions/EXTENSIONS.md).

The primary interface is a small, self-describing HTTP API and a CLI that
mirrors the same paths and flags. A web UI is a reference consumer rendered
from definitions; it is not part of the kernel.

For the principles behind these choices, see [`PHILOSOPHY.md`](../concepts/PHILOSOPHY.md).

---

## 1. Design principles

1. **Schema is the process.** Workflow rules live in card-type definitions,
   authored rarely. The runtime validates, indexes, and records events.
2. **Introspection before action.** One call returns types (with versions),
   fields, columns, views, filters, tags, link types, and users.
3. **Strict on writes, forgiving on shape.** Values are validated strictly;
   comments and `text` fields remain the unstructured escape hatch.
4. **History is automatic and append-only.** Current state is a materialized
   projection; the event log is the coordination record (see §8).
5. **Fail loudly, guide recovery.** Rejections echo valid options and hints.
6. **Idempotent by default.** Mutations accept idempotency keys (all
   POST/PATCH writes; DELETEs are idempotent by HTTP semantics and are not
   separately keyed; `POST /users` registration is currently unkeyed — see §11).
7. **Lightweight unless opted in.** Unconstrained status moves by default;
   transitions, strict field mode, and link-type constraints are opt-in.
8. **Coordination, not archive.** Keep blobs and canonical records in the
   workspace or host app; cards hold structure, links, pointers, and history.
9. **One grammar, two surfaces.** CLI flags and URL/query parameters use the
   same names (`--owner`, `filter`, `limit`, `cursor`).
10. **Raw API first, views as sugar.** Agents depend on `/cards` and
    introspection; domain-shaped URLs are resolved views, not a second model.

---

## 2. Design tensions (and how we resolve them)

### Flexibility vs. overhead
Core schemas and board definitions live as JSON in the workspace; extension
declarations may use YAML where supported. The runtime is a thin validator plus
SQLite for query and FTS. A board can expose one card
type and three columns or many types with enforced transitions — same core.

### Validation vs. openness
Strict values; optional `strict` for unknown fields; comments always available.
Structured-payload validation (JSON/YAML schemas, command specs, path
confinement) is **extension territory**; the core stores such payloads as
`text` or `string` and lets an extension validate and annotate.

### Card transitions / enforcement
Any→any by default; optional `transitions` per board or card type when
`enforce_transitions` is true.

### Discoverability for agents
The introspection endpoint returns the entire valid vocabulary; writes reject
unknown values and echo the valid set; card links validate existence and
optional type constraints; users must be registered; tags are a closed set
with an explicit propose path.

### Dynamic domain URLs vs. stable agent API
**Views** bind path patterns (e.g. `/orders/:order_id/parts`) to a filter
template resolved against the same query engine as `GET /cards`. No duplicate
data per board.

### Single workspace vs. multi-tenancy
One process serves one workspace (assembled from context files). Multi-tenancy
is multiple processes. See §3.

---


## See Also (Extracted Sections)

- [3. Workspace, storage, and deployment & 4. Core data model](SPEC-DATA-MODEL.md)
- [6. Field types & 7. Card-type examples](SPEC-CARDTYPE-EXAMPLES.md)
- [8. History, events, and retention & 12. Actors and authorization](SPEC-EVENTS-HISTORY.md)
- [9. Query and filter DSL & 10. Validation and anti-hallucination](SPEC-QUERY-DSL.md)
- [5. Schema versioning, 11. API surface, 13. Agent ergonomics, 14. Open questions & 15. Core vs extensions](SPEC-API-SURFACE.md)
