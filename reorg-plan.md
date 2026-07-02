# Cards Docs Reorg Plan

## 1. Current state summary

- `docs/` has 14 files, 5,297 lines. Top level has 8 `.md` files (README + 7 working artifacts from one 2026-07 debt-review pass), 1,496 lines.
- Three files exceed 500 lines: `docs/SPEC.md` (967), `docs/LIFECYCLE-EXAMPLES.md` (627), `docs/DEVELOPER-REFERENCE.md` (557).
- `docs/INTEGRATOR-REFERENCE.md` (514 lines) is close to the limit but out of scope per the task (not listed as exceeding); still worth watching post-split.
- `docs/SLICE3-REFLECTION.md` is explicitly self-described "Historical — superseded," not linked from README, findings duplicated in `issues-docs.md`/`debt-ledger.md`.
- `docs/DESIGN.md` is orphaned (not in README's doc index) — fold into a topic dir rather than delete.
- Seven top-level working artifacts (`action-plan.md`, `debt-ledger.md`, `inventory.md`, `issues-core.md`, `issues-docs.md`, `issues-http-cli.md`, `issues-small.md`) are one review-pass cluster; they clutter the repo root next to `README.md`.

## 2. Proposed `docs/` topical layout

```
docs/
  README.md                       # new: doc-set index (mirrors README's doc links)
  concepts/
    CONCEPTS.md
    PHILOSOPHY.md
  spec/
    SPEC.md                       # split, see §3
    SPEC-DATA-MODEL.md
    SPEC-API-SURFACE.md
    SPEC-EVENTS-HISTORY.md
    SPEC-QUERY-DSL.md
    SPEC-CARDTYPE-EXAMPLES.md
  architecture/
    ARCHITECTURE.md
    DESIGN.md                     # moved in (web-UI design system), linked from README
  reference/
    DEVELOPER-REFERENCE.md        # split, see §3
    DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md
    DEVELOPER-REFERENCE-TYPES-EXAMPLES.md
    DEVELOPER-REFERENCE-CLI.md
    INTEGRATOR-REFERENCE.md
  events/
    EVENTS.md
    INTEGRATION.md
  extensions/
    EXTENSIONS.md
    MCP.md                        # candidate for internal/mcp/README.md too, see §4
  examples/
    LIFECYCLE-EXAMPLES.md         # split, see §3
    LIFECYCLE-EXAMPLES-SETUP.md
    LIFECYCLE-EXAMPLES-SOFTWARE.md
    LIFECYCLE-EXAMPLES-SHOPFLOOR.md
  NOTES.md                        # stays flat at docs/ root — cross-cutting decision log, not topical
```

Rationale for grouping:
- `concepts/`: onboarding/vocabulary/principles, read first.
- `spec/`: the normative contract and its natural sub-parts.
- `architecture/`: platform/runtime/deployment view, plus the previously-orphaned `DESIGN.md`.
- `reference/`: authoring + code-verified audit docs, read by developers/integrators.
- `events/`: event core design + integration patterns (two docs already cross-link heavily).
- `extensions/`: extension model + MCP (both describe "how outside processes talk to core").
- `examples/`: worked walkthroughs.
- `NOTES.md` stays at `docs/` root: it's a stable historical decision log (D1–D18) referenced from many docs; moving it into a subfolder would break the most anchors for no topical benefit.

Do **not** create a subdirectory with only one file where nothing else fits (e.g., don't force `NOTES.md` into its own dir).

## 3. Splitting the three oversized files

### `docs/SPEC.md` (967 → target ~200 lines root + 5 subfiles, each <500)

Keep `SPEC.md` as a short index/contract-status page (§1 Design principles, §2 Design tensions, ~80 lines) plus a table of contents linking to the new files. Extract:

| New file | Sections moved | Approx. lines |
|---|---|---|
| `spec/SPEC-DATA-MODEL.md` | §3 Workspace/storage/deployment (87–204), §4 Core data model (205–417) | ~330 |
| `spec/SPEC-CARDTYPE-EXAMPLES.md` | §6 Field types (462–501), §7 Card-type examples (502–582) | ~120 |
| `spec/SPEC-EVENTS-HISTORY.md` | §8 History/events/retention (583–628), §12 Actors/authz (844–872) | ~90 |
| `spec/SPEC-QUERY-DSL.md` | §9 Query/filter DSL (629–682), §10 Validation/error catalog (683–744) | ~115 |
| `spec/SPEC-API-SURFACE.md` | §5 Schema versioning (418–461), §11 API surface (745–843), §13 Agent ergonomics (873–903), §14 Open questions (904–920), §15 Core vs extensions (921–967) | ~230 |

All five stay under 500 lines individually. `SPEC.md` root retains §1–2 and a "See also" table replacing the extracted sections; every internal anchor link (`SPEC.md#5-schema-versioning`, `#6-field-types`, `#default-link-vocabulary`, `#definition-merge-and-precedence`) referenced from `DEVELOPER-REFERENCE.md`/`INTEGRATOR-REFERENCE.md` must be updated to point at the new file+anchor.

### `docs/DEVELOPER-REFERENCE.md` (557 → root + 3 subfiles)

| New file | Sections moved | Approx. lines |
|---|---|---|
| `reference/DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md` | §1 Flexibility (16–79), §2 Workspace definition (80–134), §6 Schema versioning (278–339) | ~245 |
| `reference/DEVELOPER-REFERENCE-TYPES-EXAMPLES.md` | §3 Card type schemas (135–191), §4 Status/transitions (192–247), §5 Relations/links (248–277), §8 Full type examples (395–462) | ~330 |
| `reference/DEVELOPER-REFERENCE-CLI.md` | §9 CLI (463–529), §10 Checklist (530–544) | ~85 |

Root `DEVELOPER-REFERENCE.md` keeps §7 Board/view config (340–394) plus §11 Related documents, and becomes a short landing page with links to the three subfiles — under 200 lines.

### `docs/LIFECYCLE-EXAMPLES.md` (627 → root + 3 subfiles)

| New file | Sections moved | Approx. lines |
|---|---|---|
| `examples/LIFECYCLE-EXAMPLES-SETUP.md` | "Shared setup" (25–61) + "Cross-cutting behaviors" (600–627) | ~55 |
| `examples/LIFECYCLE-EXAMPLES-SOFTWARE.md` | Example A, A1–A7 (62–345) | ~285 |
| `examples/LIFECYCLE-EXAMPLES-SHOPFLOOR.md` | Example B, B1–B6 (346–599) | ~255 |

Root `LIFECYCLE-EXAMPLES.md` becomes a ~20-line index pointing at the three, preserving the title and the "pinned to SPEC v0.4" note.

All resulting files are comfortably under the 500-line ceiling with margin for future growth.

## 4. Localize as package-level `README.md`

Move implementation-adjacent narrative out of `docs/` into `internal/*/README.md`, keeping `docs/` for cross-cutting design (each `internal/*/README.md` should be short: what the package does, key types, and a link back to the relevant `docs/` topic for full context — not a copy).

| Source content | New location | What moves |
|---|---|---|
| `docs/EXTENSIONS.md` "CLI surface", "Distribution patterns", Example 5 (bash hook), supervisor mechanics currently duplicated from `docs/ARCHITECTURE.md` §Extension Supervisor | `internal/hooks/README.md` | Extension supervisor responsibilities (spawn hook on event match, `.cards/logs/`, crash isolation), package-level "how it runs" — link back to `docs/extensions/EXTENSIONS.md` for the declaration format/worked examples, which stay in docs. |
| `docs/MCP.md` "Running it", "Tool surface" tables (Introspection/Card lifecycle/Generic coordination/Events), "Concurrency, idempotency, errors" | `internal/mcp/README.md` | Package-level tool inventory tied to `mcp.go` generation logic. `docs/extensions/MCP.md` keeps the *why* (goals, agent ergonomics, philosophy tie-in) and links to the package README for the current tool table so the two don't drift independently — recommend `docs/MCP.md` link to `internal/mcp/README.md` as the authoritative tool list, since that's closer to the code and cheaper to keep in sync. |
| `docs/SPEC.md` §6 `artifact` field description + confinement/`artifact_policy` details | `internal/artifacts/README.md` | Implementation-specific description of the local artifact policy and confinement enforcement (this is now built, per commit `0578312`, so the doc-vs-code drift noted in inventory.json should be fixed here, not in SPEC.md prose). SPEC.md keeps only the field-type contract row. |

Localization principle: `docs/` keeps *design intent and cross-surface contracts* (what a field type means across HTTP/CLI/MCP); `internal/*/README.md` keeps *this package's current behavior* (what's implemented, what's a stub, package-specific gotchas). Each package README should end with "See also: docs/<topic>" rather than restating shared prose.

No changes proposed for `internal/core`, `internal/httpapi`, `internal/cli`, `internal/sqlite`, `internal/config`, `internal/openapi`, `internal/seed`, `internal/starter` — inventory.json shows no docs/ content specific enough to those packages to localize; adding READMEs there would be scope creep unless a future pass identifies content.

## 5. Deletions / archival

| File | Action | Reason |
|---|---|---|
| `docs/SLICE3-REFLECTION.md` | **Delete** | Self-labeled "Historical — superseded"; not linked from README or any current doc; its findings (B1, F1–F7) are already tracked in `debt-ledger.md`/`issues-docs.md`. Before deleting, port the one still-useful correction (INTEGRATION.md's `card_ready` should be `card_unblocked` — currently INTEGRATION.md doesn't even use that term, so this is likely already resolved; verify then delete). |
| `issues-docs.md` | **Archive, don't delete outright** — move to `docs/archive/2026-07-debt-review/issues-docs.md` or delete if `debt-ledger.md`/`action-plan.md` fully supersede it | Explicitly flagged outdated in inventory.json; most findings resolved in commit `9fe8fb9`. Two findings remain unresolved (YAML-support overstatement, run-extensions claim) — port those two into `debt-ledger.md` before removing this file so they aren't lost. |
| `action-plan.md`, `debt-ledger.md`, `inventory.md`, `issues-core.md`, `issues-http-cli.md`, `issues-small.md` | **Consolidate, then archive** (not this pass's blocking work, but flag) | One review-pass cluster with overlapping content (e.g., dead-import findings appear in 4 places). Once remediation lands, fold into a single `debt-ledger.md` and move the rest to `docs/archive/2026-07-debt-review/`. Out of scope for the immediate docs/ reorg — call out as phase 4, optional. |

Do not delete `docs/DESIGN.md` — it's orphaned from the README index, not outdated; the fix is linking it (folded into `architecture/`), not removing it.

## 6. Cross-link and README updates required

- `README.md` "Documentation And Development" section: update all 12 linked paths to new topical locations (`docs/concepts/CONCEPTS.md`, `docs/reference/DEVELOPER-REFERENCE.md`, `docs/spec/SPEC.md`, `docs/architecture/PHILOSOPHY.md`... note `PHILOSOPHY.md` moves to `concepts/` not `architecture/`, `docs/architecture/ARCHITECTURE.md`, `docs/extensions/MCP.md`, `docs/extensions/EXTENSIONS.md`, `docs/events/INTEGRATION.md`, `docs/events/EVENTS.md`, `docs/reference/INTEGRATOR-REFERENCE.md`, `docs/examples/LIFECYCLE-EXAMPLES.md`, `docs/NOTES.md`). Also add a link to `docs/architecture/DESIGN.md` (currently missing — this is the orphan fix).
- Add new `docs/README.md` as a doc-set index/sitemap, since files are no longer flat.
- Every doc's own "Related documents" table (found in `PHILOSOPHY.md`, `DEVELOPER-REFERENCE.md` §11, `EXTENSIONS.md`, `MCP.md`) needs path updates to match new locations.
- All anchor-based cross-refs into `SPEC.md`/`DEVELOPER-REFERENCE.md`/`LIFECYCLE-EXAMPLES.md` sections that moved need updating to the correct new file (this is the highest-risk mechanical step — a broken-link check should run after every move).

## 7. Execution phases

1. **Phase 0 — util-tier, safety net.** Run a link inventory (grep all `.md` files for `\]\(.*\.md` and anchors) to produce a full before/after mapping table. Confirm no other files reference `docs/SLICE3-REFLECTION.md` before deletion.
2. **Phase 1 — util-tier, mechanical moves.** Create `docs/{concepts,spec,architecture,reference,events,extensions,examples}/` and `git mv` files per §2 (no content edits yet, just moves + git history preservation). Update path-only links (no anchor changes) via search/replace.
3. **Phase 2 — util-tier, splits.** Perform the three splits in §3 exactly as specified (section ranges, target files, line counts). Verify each output file <500 lines with `wc -l`. Update all internal anchors accordingly.
4. **Phase 3 — util-tier, localization.** Create the three `internal/*/README.md` files per §4, extracting (not duplicating) the specified sections; add "See also" backlinks from the corresponding `docs/` topic file.
5. **Phase 4 — util-tier, deletions.** Delete `docs/SLICE3-REFLECTION.md` (after porting the one open correction); move `issues-docs.md` to archive after porting its 2 unresolved findings into `debt-ledger.md`.
6. **Phase 5 — high-tier, review.** Re-run link-check, confirm all files <500 lines, confirm README.md and docs/README.md render a coherent index, spot-check that no content was lost (diff line counts old total vs. new total ± the small amount of net-new index/glue text).
7. **Phase 6 — optional, out of scope for this pass.** Consolidate the 2026-07 debt-review artifact cluster (`action-plan.md`, `debt-ledger.md`, `inventory.md`, `issues-core.md`, `issues-http-cli.md`, `issues-small.md`) into one ledger + archive. Flag to orchestrator as a separate task; don't bundle into the docs/ reorg to avoid scope creep.
