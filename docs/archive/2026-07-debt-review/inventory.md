# Inventory (focus: markdown only)

Generated: 2026-07-05
Scope hint: focus only on markdown
Source: scanned `docs/**/*.md` (recursively) and top-level `*.md` at repo root.
Excluded: `.go`, `.json`, `.yml`, `.yaml`, `.sql`, `.html`, `.css`, `.sh`, and other source files.

Total .md files inventoried: 38 (30 under `docs/`, 8 at repo root including this report).

## File list

| Path | Lines | Bytes | In docs/README? | Orphan? |
|------|------:|------:|:----------------|:--------|
| /Users/foz/src/cards/README.md | 222 | 8205 | n/a (root index) | no (root) |
| /Users/foz/src/cards/action-plan.md | 363 | 19621 | no | yes |
| /Users/foz/src/cards/debt-ledger.md | 213 | 24657 | no | yes |
| /Users/foz/src/cards/inventory.md | 202 | 9336 | no | yes (this report) |
| /Users/foz/src/cards/issues-core.md | 223 | 12332 | no | yes |
| /Users/foz/src/cards/issues-http-cli.md | 90 | 10635 | no | yes |
| /Users/foz/src/cards/issues-small.md | 80 | 5717 | no | yes |
| /Users/foz/src/cards/link-status.md | 37 | 2529 | no | yes |
| /Users/foz/src/cards/reorg-plan.md | 139 | 12976 | no | yes |
| /Users/foz/src/cards/docs/README.md | 54 | 3709 | n/a (docs index) | no |
| /Users/foz/src/cards/docs/ROADMAP.md | 234 | 12056 | yes | no |
| /Users/foz/src/cards/docs/NOTES.md | 243 | 12148 | yes | no |
| /Users/foz/src/cards/docs/GH-PAGES-TODO.md | 127 | 10676 | yes | no |
| /Users/foz/src/cards/docs/architecture/ARCHITECTURE.md | 387 | 14113 | yes | no |
| /Users/foz/src/cards/docs/architecture/DESIGN.md | 194 | 10277 | yes | no |
| /Users/foz/src/cards/docs/concepts/CONCEPTS.md | 208 | 9775 | yes | no |
| /Users/foz/src/cards/docs/concepts/PHILOSOPHY.md | 107 | 4438 | yes | no |
| /Users/foz/src/cards/docs/events/EVENTS.md | 599 | 25984 | yes | no |
| /Users/foz/src/cards/docs/events/INTEGRATION.md | 378 | 18885 | yes | no |
| /Users/foz/src/cards/docs/examples/LIFECYCLE-EXAMPLES.md | 30 | 1243 | yes | no |
| /Users/foz/src/cards/docs/examples/LIFECYCLE-EXAMPLES-SETUP.md | 65 | 2042 | yes | no |
| /Users/foz/src/cards/docs/examples/LIFECYCLE-EXAMPLES-SOFTWARE.md | 284 | 7628 | yes | no |
| /Users/foz/src/cards/docs/examples/LIFECYCLE-EXAMPLES-SHOPFLOOR.md | 254 | 7886 | yes | no |
| /Users/foz/src/cards/docs/extensions/EXTENSIONS.md | 345 | 11290 | yes | no |
| /Users/foz/src/cards/docs/extensions/MCP.md | 50 | 2386 | yes | no |
| /Users/foz/src/cards/docs/plans/sprint-2026-07-05.md | 197 | 54305 | no | yes |
| /Users/foz/src/cards/docs/reference/DEVELOPER-REFERENCE.md | 131 | 5355 | yes | no |
| /Users/foz/src/cards/docs/reference/DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md | 181 | 7056 | yes | no |
| /Users/foz/src/cards/docs/reference/DEVELOPER-REFERENCE-TYPES-EXAMPLES.md | 211 | 7339 | yes | no |
| /Users/foz/src/cards/docs/reference/DEVELOPER-REFERENCE-CLI.md | 82 | 3467 | yes | no |
| /Users/foz/src/cards/docs/reference/INTEGRATOR-REFERENCE.md | 518 | 27262 | yes | no |
| /Users/foz/src/cards/docs/spec/SPEC.md | 94 | 4788 | yes | no |
| /Users/foz/src/cards/docs/spec/SPEC-DATA-MODEL.md | 351 | 14760 | yes | no |
| /Users/foz/src/cards/docs/spec/SPEC-API-SURFACE.md | 261 | 13720 | yes | no |
| /Users/foz/src/cards/docs/spec/SPEC-CARDTYPE-EXAMPLES.md | 121 | 4682 | yes | no |
| /Users/foz/src/cards/docs/spec/SPEC-EVENTS-HISTORY.md | 94 | 5115 | yes | no |
| /Users/foz/src/cards/docs/spec/SPEC-QUERY-DSL.md | 123 | 4975 | yes | no |
| /Users/foz/src/cards/docs/archive/2026-07-debt-review/issues-docs.md | 104 | 9422 | no | yes |

Notes on "In docs/README?": "yes" means the file is referenced by `/Users/foz/src/cards/docs/README.md`. The two index files (`/Users/foz/src/cards/README.md` and `/Users/foz/src/cards/docs/README.md`) are marked n/a. `internal/hooks/README.md`, `internal/mcp/README.md`, and `internal/artifacts/README.md` are linked from `docs/README.md` but are outside this audit's scope (not under `docs/` and not top-level project files).

## Bulky files (>500 lines)

- /Users/foz/src/cards/docs/events/EVENTS.md: 599 lines — proposed split surface. Roughly half of the file is the events subsystem spec (emission seam, log, bus, observer queues); could split into `docs/events/EVENTS-CORE.md` (design contract) and `docs/events/EVENTS-CONSUMER-GUIDE.md` (consumer-side usage / integration pattern), leaving the index slim.
- /Users/foz/src/cards/docs/reference/INTEGRATOR-REFERENCE.md: 518 lines — proposed split surface. Already 6 numbered sections; the §3 MCP surface, §5 Actor & identity model, and §6 Workspace & schema each read like separable units that could move to their own files under `docs/reference/`.

## Self-described outdated / superseded

- /Users/foz/src/cards/docs/NOTES.md:3 — "A record of design decisions and why they were made. This is a **historical rationale log**, not a status report — for current implementation status see [`SPEC.md`](spec/SPEC.md) and [`EVENTS.md`](events/EVENTS.md)."
- /Users/foz/src/cards/docs/NOTES.md:4 — "Other docs cite the D-numbered entries below (D1–D18) for rationale not restated elsewhere; those anchors are stable and must not be renumbered." (Self-described as historical, intentionally retained as decision log.)
- /Users/foz/src/cards/docs/archive/2026-07-debt-review/issues-docs.md — file lives in `docs/archive/2026-07-debt-review/` (an archive folder); per `reorg-plan.md:119` "Explicitly flagged outdated in inventory.json; most findings resolved in commit `9fe8fb9`". Path itself is the self-description.
- /Users/foz/src/cards/docs/plans/sprint-2026-07-05.md:1 — "Sprint plan — Events as composition substrate — durable delivery on a verified core, with the human surface in the loop" (working artifact, not user-facing doc; already confined to `docs/plans/`).

No file uses an explicit "Deprecated:" header or "Superseded by:" header. The only "Deprecated" tokens are the in-doc mentions of the `deprecated: true` field flag (a feature spec, not a doc-status marker): `docs/spec/SPEC-API-SURFACE.md:18`, `docs/NOTES.md:132`, `docs/reference/DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md:166`.

## Working artifacts at root (not docs)

- /Users/foz/src/cards/action-plan.md — one-off prioritised task list generated from `debt-ledger.md`. Not user documentation; should move to `docs/archive/2026-07-debt-review/` once remediation lands.
- /Users/foz/src/cards/debt-ledger.md — deduplicated registry of technical debt from a 2026-07-02 review pass. Not user documentation; belongs in an archive or `.pi/` working area.
- /Users/foz/src/cards/inventory.md — this very report (regenerated by docs-audit pipeline).
- /Users/foz/src/cards/issues-core.md — review findings for `internal/core/` + `internal/sqlite/`. Working artifact of the 2026-07 debt review, not documentation.
- /Users/foz/src/cards/issues-http-cli.md — review findings for HTTP/CLI surface. Working artifact, not documentation.
- /Users/foz/src/cards/issues-small.md — review findings for small packages. Working artifact, not documentation.
- /Users/foz/src/cards/link-status.md — link-audit status report ("Documentation Link Audit & Verification Status"). Working artifact, not documentation.
- /Users/foz/src/cards/reorg-plan.md — proposal for reorganising `docs/`. Working artifact, not documentation.
- /Users/foz/src/cards/docs/plans/sprint-2026-07-05.md — sprint planning artifact ("Sprint plan — Events as composition substrate"). Not user documentation; should move to `.pi/` or `docs/archive/`.
- /Users/foz/src/cards/docs/archive/2026-07-debt-review/issues-docs.md — already in an archive folder, but it is itself a working artifact, not a doc page.

## Linkage summary

- Total internal link references scanned: 172 (resolved by Python script over all 38 .md files; matches found by `grep -E '\[...\]\(...)'` minus any non-relative, non-internal targets).
- Total external link references in markdown-link form (`[text](http...)`): 0. The 10 http/https occurrences across the .md corpus are all inline text or inside code blocks (e.g., `http://127.0.0.1:8787/v1` in README, `https://github.com/org/repo/pull/42` in a JSON example).
- Broken internal links: 0 (every relative-path target resolves to an existing file on disk; anchor-only links such as `#import--export-and-portability`, `#condition-events-are-ephemeral`, `#current-breaches-catch-up-for-conditions`, `#5-schema-versioning`, `#6-field-types`, `#definition-merge-and-precedence`, `#default-link-vocabulary` all match a heading in the target file).
- Cross-doc links between subdirs (`../`): 106 total — 103 single-level (`../spec/...`, `../events/...`, etc.) plus 3 double-level (`../../internal/artifacts/README.md`, `../../internal/hooks/README.md`, `../../internal/mcp/README.md`).

## Duplicates / overlaps

- 2026-07 debt-review cluster (one pass, scattered across root + archive):
  - /Users/foz/src/cards/issues-core.md, /Users/foz/src/cards/issues-http-cli.md, /Users/foz/src/cards/issues-small.md, /Users/foz/src/cards/docs/archive/2026-07-debt-review/issues-docs.md, /Users/foz/src/cards/debt-ledger.md, /Users/foz/src/cards/action-plan.md, /Users/foz/src/cards/reorg-plan.md, /Users/foz/src/cards/inventory.md, /Users/foz/src/cards/link-status.md
  - All nine are artefacts of the same 2026-07-02 review pass. `debt-ledger.md` already consolidates the findings; `action-plan.md` and `reorg-plan.md` are the two plan documents derived from it; `issues-*.md` are the raw facets; `inventory.md` and `link-status.md` are meta-audits. Significant overlap: error-handling and dead-import items appear in at least 4 of the 9 files.
- Design decisions rationale:
  - /Users/foz/src/cards/docs/NOTES.md (D1–D18 historical log) and /Users/foz/src/cards/docs/concepts/PHILOSOPHY.md (principles) overlap on several "why we did it this way" topics; `reorg-plan.md:58` confirms the design intent that `NOTES.md` is the stable D-number anchor and `PHILOSOPHY.md` is the principles essay, but the actual content overlap is non-trivial and worth cross-checking.
- API + extension + events coverage:
  - /Users/foz/src/cards/docs/spec/SPEC-API-SURFACE.md, /Users/foz/src/cards/docs/reference/INTEGRATOR-REFERENCE.md, and /Users/foz/src/cards/docs/extensions/EXTENSIONS.md all describe endpoint shapes and extension declaration patterns; cross-references are explicit but the prose could drift.
- /Users/foz/src/cards/docs/reference/INTEGRATOR-REFERENCE.md — 12 instances of the same "Related documents" table pattern (one block at lines 123-131 in `DEVELOPER-REFERENCE.md` and one at 507-514 in `INTEGRATOR-REFERENCE.md`); partial overlap with the inline `Related:` paragraphs in `concepts/PHILOSOPHY.md` lines 97-107 and `extensions/EXTENSIONS.md` lines 341-345. Not duplicates per se — each is the index for a different doc — but they all encode the same cross-doc navigation graph and would benefit from a single source of truth.
