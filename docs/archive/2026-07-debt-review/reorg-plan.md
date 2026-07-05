# Cards Docs Reorg Plan (v2)

## Status: 2 bulky files reviewed, 10 orphans queued, 0 broken internal links; ~20 file operations across 4 phases.

## 1. Bulky-file splits

### `docs/events/EVENTS.md` (599 lines) — **SPLIT**

- Rationale: 599 lines is well past the 500-line soft cap; document has 13 sections with a natural core (concepts, schema) vs. rollout (deployment, migration) boundary.
- Proposed split:
  - `docs/events/EVENTS.md` (rewritten) — index/overview, ~70 lines (current §1–§4 with intro).
  - `docs/events/EVENTS-CORE.md` — conceptual reference, ~377 lines (current §1–§11: model, schema, history, semantics, query).
  - `docs/events/EVENTS-ROLLOUT.md` — operations reference, ~170 lines (current §12–§13: deployment, migration/rollout).
- Cross-file link fix: in `EVENTS-CORE.md` §8, change inline ref "see §12, Step 4" → link to `EVENTS-ROLLOUT.md` §12 Step 4.

### `docs/reference/INTEGRATOR-REFERENCE.md` (518 lines) — **KEEP**

- Rationale: file self-describes as a "single-page reference" (line 3). It is 18 lines over the 500-line soft cap. The 8 sections are tightly cross-referenced. Candidate sub-splits (§3 MCP is only 39 lines, §5 actor is only 22 lines) are too thin to stand alone and would force readers to jump between files for related content. Keeping it as a single page preserves its reference-table character.
- Action: no change.

## 2. Orphan file handling

| Path | Action | Target | Rationale |
|------|--------|--------|-----------|
| `action-plan.md` | move | `docs/archive/2026-07-debt-review/action-plan.md` | One-review-pass working artifact |
| `debt-ledger.md` | move | `docs/archive/2026-07-debt-review/debt-ledger.md` | One-review-pass working artifact |
| `inventory.md` | move | `docs/archive/2026-07-debt-review/inventory.md` | One-review-pass working artifact |
| `issues-core.md` | move | `docs/archive/2026-07-debt-review/issues-core.md` | One-review-pass working artifact |
| `issues-http-cli.md` | move | `docs/archive/2026-07-debt-review/issues-http-cli.md` | One-review-pass working artifact |
| `issues-small.md` | move | `docs/archive/2026-07-debt-review/issues-small.md` | One-review-pass working artifact |
| `link-status.md` | move | `docs/archive/2026-07-debt-review/link-status.md` | One-review-pass working artifact |
| `reorg-plan.md` | move (renamed) | `docs/archive/2026-07-debt-review/reorg-plan-v1.md` | This very file (v1) once v2 lands |
| `docs/plans/sprint-2026-07-05.md` | move | `docs/archive/2026-07-debt-review/sprint-2026-07-05.md` | Sprint plan from 2026-07-02 review |
| `docs/archive/2026-07-debt-review/issues-docs.md` | keep | (in place) | Already correctly placed |

Also: write a new `docs/archive/2026-07-debt-review/README.md` that indexes the cluster as the output of the 2026-07-02 docs review pass.

**Constraint:** the `reorg-plan.md` move must happen AFTER the new `reorg-plan.md` is written — i.e. this file's content lives in the active path during execution, and is moved into the archive as `reorg-plan-v1.md` once Phase 1 has moved the other 8 root working artifacts. The current reorg-plan-v1 (the older one written by the previous pipeline run) is already in the archive indirectly via the issues-docs.md narrative; the second copy is the v1 of *this* plan.

To avoid clobbering: rename the current root `reorg-plan.md` to `reorg-plan-v1.md` BEFORE writing the new v2 content. Then write v2 over the original path.

## 3. Phasing

### Phase 1 — Safe relocations (pure `git mv`, no content edits)

1. `git mv reorg-plan.md reorg-plan-v1.md` (rename in place at root).
2. `git mv action-plan.md docs/archive/2026-07-debt-review/`
3. `git mv debt-ledger.md docs/archive/2026-07-debt-review/`
4. `git mv inventory.md docs/archive/2026-07-debt-review/`
5. `git mv issues-core.md docs/archive/2026-07-debt-review/`
6. `git mv issues-http-cli.md docs/archive/2026-07-debt-review/`
7. `git mv issues-small.md docs/archive/2026-07-debt-review/`
8. `git mv link-status.md docs/archive/2026-07-debt-review/`
9. `git mv docs/plans/sprint-2026-07-05.md docs/archive/2026-07-debt-review/`
10. `git mv reorg-plan-v1.md docs/archive/2026-07-debt-review/` (now safe to move the old plan alongside its peers).
11. Write new `reorg-plan.md` at root with this v2 content.
12. Write new `docs/archive/2026-07-debt-review/README.md` indexing the cluster.

### Phase 2 — Bulky-file split: EVENTS.md

1. Read `docs/events/EVENTS.md` in full; identify exact line ranges for §1–§11 (CORE) and §12–§13 (ROLLOUT).
2. Write `docs/events/EVENTS-CORE.md` containing §1–§11 verbatim, with a 2-line preamble pointing to the parent `EVENTS.md`.
3. Write `docs/events/EVENTS-ROLLOUT.md` containing §12–§13 verbatim, with a 2-line preamble.
4. Replace `docs/events/EVENTS.md` with a slim index (~70 lines) covering §1–§4 (intro, scope, model overview, schema overview) and linking to the two new files.
5. Patch the one cross-file link: in `EVENTS-CORE.md` §8, replace inline "see §12, Step 4" text with a markdown link to `EVENTS-ROLLOUT.md#step-4`.

### Phase 3 — Link re-verification (expected no-op)

- Inventory said 0 broken internal links. Re-scan after moves and the EVENTS split to confirm no new broken refs. If any are found, patch in place.
- Write a short `docs/archive/2026-07-debt-review/link-status-v2.md` summarizing the verification (or replace the previous one if it's still in the archive).

### Phase 4 — Commit

- Single conventional commit: `docs: archive 2026-07 debt-review working artifacts; split EVENTS.md`
- Body: list the 9 moves, the 1 archive index, the 3 EVENTS files (rewrite + 2 new), and the link-status note.

## 4. Risk and rollback

- **Pure moves are reversible** with `git mv` until commit.
- **No deletions.** Every original byte remains; only locations and one file (EVENTS.md) are restructured.
- **EVENTS.md rewrite** is the only place where content is removed from its original location. Risk: if a section boundary is misjudged, content could be lost or duplicated. Mitigation: write the new EVENTS-CORE.md and EVENTS-ROLLOUT.md FIRST, verify they contain exactly the original §1–§11 and §12–§13 respectively (grep the original for the section headings to confirm all 13 sections are accounted for), then only then rewrite EVENTS.md.
- **reorg-plan.md rename dance** is the only ordering-sensitive move. It must happen as the first step of Phase 1 so the v2 content has somewhere to land.
- **Rollback:** `git reset --hard HEAD` (before commit) or `git revert <commit-sha>` (after).
