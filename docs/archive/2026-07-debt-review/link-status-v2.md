# Link status (v2)

Re-verified after Phase 1 relocations and Phase 2 EVENTS.md split.

- Internal file links scanned: 160 (relative-path `.md` targets, all
  in-tree `docs/` + root-level `.md`)
- Internal anchor links scanned: 9 (all cross-page `#anchor` links)
- Broken internal links found: 0 — no patches needed
- Cross-subdir traversals added: 0 (no broken links required fixing)
- One explicit patch: `EVENTS-CORE.md` §8 inline "outbox/tailer model in
  §12, Step 4" ref → linked to
  `[EVENTS-ROLLOUT.md §12 Step 4](EVENTS-ROLLOUT.md#step-4-optional-outboxtailer-evolution-future)`

Notes:

- The reorg relocations (8 `git mv` operations) did not break any link,
  because no .md file referenced any of the moved root-level files
  (`action-plan.md`, `debt-ledger.md`, `inventory.md`, `issues-*.md`,
  `link-status.md`, `docs/plans/sprint-2026-07-05.md`) — those were
  working artifacts, not documentation.
- The EVENTS.md split preserved the section-numbered cross-references
  (§1–§13) used throughout the file; the only absolute reference that
  crossed the split boundary was "§12, Step 4" in §8, patched above.
- Anchor slug computed via GitHub-style: lowercase, strip non-word
  characters, collapse whitespace to single hyphens (em dash → empty →
  adjacent spaces collapse). The Step-4 slug is
  `step-4-optional-outboxtailer-evolution-future` (single hyphen, not
  double — the `\s+` collapse in the slugifier turns the "—" gap into
  one hyphen, not two).
- All other in-tree links resolve cleanly. The 9 cross-page anchor
  links (e.g. `[NOTES.md](../NOTES.md)` followed by an anchor in the
  target) all resolve; the 0 broken count holds for both file targets
  and anchor targets combined.
