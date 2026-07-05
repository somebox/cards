# Link status (v3) — independent re-verification

Generated: 2026-07-05

## Scope
- Files scanned: 42 .md files in `docs/` (recursive) + 2 root `README.md`-class files
- Total relative-path links extracted: 164 .md-relative links
- Anchor-bearing .md links extracted: 6 (all 6 resolve)

## Broken links

Strict path-resolution count: **4 candidates**. Practical navigation-broken count: **0**.

| Where | Status | Reason |
|---|---|---|
| `docs/archive/2026-07-debt-review/inventory.md:62` `[`SPEC.md`](spec/SPEC.md)` | false positive | Quoted verbatim from `docs/NOTES.md:3`. Path `spec/SPEC.md` is correct *relative to NOTES.md*; renders inside a transcribed Obsidian backlink log entry. Not a nav link from `inventory.md`'s perspective. |
| `docs/archive/2026-07-debt-review/inventory.md:62` `[`EVENTS.md`](events/EVENTS.md)` | false positive | Same line as above; same reason. Path correct from `NOTES.md`'s directory. |
| `docs/archive/2026-07-debt-review/link-status-v2.md:12` `[EVENTS-ROLLOUT.md §12 Step 4](EVENTS-ROLLOUT.md#step-4-optional-outboxtailer-evolution-future)` | strict-broken / practical-OK | Inside inline backticks — renders as code, not a clickable link. Path `EVENTS-ROLLOUT.md` from `docs/archive/2026-07-debt-review/` resolves to a missing file; the real file is at `docs/events/EVENTS-ROLLOUT.md`. v2 is quoting the patched-link text for documentation; not a nav link. |
| `docs/archive/2026-07-debt-review/link-status-v2.md:31` `[NOTES.md](../NOTES.md)` | strict-broken / practical-OK | Inside inline backticks — renders as code. Path `../NOTES.md` from `docs/archive/2026-07-debt-review/` resolves to `docs/archive/NOTES.md` (missing); real file is at `docs/NOTES.md` (needs `../../NOTES.md`). v2 self-referential example; not navigable. |

No other broken .md-relative or anchor links were found in the corpus.

## Specific spot-checks

- **Root working artifacts**: 0 remaining. Only `README.md` and `reorg-plan.md` exist at the repo root. Confirmed.
- **`docs/archive/2026-07-debt-review/README.md` exists**: yes. Inbound links: none (the only mentions are in `reorg-plan.md` lines 36 and 57, both as inline backticks, not markdown links). No breakage caused by its existence.
- **EVENTS-CORE.md → EVENTS-ROLLOUT.md** (the §8 patch): resolves. File `docs/events/EVENTS-ROLLOUT.md` exists; anchor slug `step-4-optional-outboxtailer-evolution-future` matches the H3 heading "Step 4 — optional outbox/tailer evolution `[future]`" (GitHub-style slug: em-dash stripped, double-space collapsed to single hyphen).
- **EVENTS-ROLLOUT.md → EVENTS-CORE.md (inverse)**: no link exists. `EVENTS-ROLLOUT.md` contains zero references to `EVENTS-CORE.md` or any sibling `EVENTS-*.md`. Not a broken link (nothing to break) but a missing cross-reference. The `EVENTS.md` slim index already links to both, so navigation works from the index.
- **EVENTS.md index → both**: resolves. `docs/events/EVENTS.md` line 104 links to `EVENTS-CORE.md`; line 105 links to `EVENTS-ROLLOUT.md`. Both target files exist.

## Patches applied
- None required. The 4 strict-criterion "broken" links are all non-issues for rendered navigation.

## Verdict
- **ALL CLEAR** for documentation navigation. The structural reorg + EVENTS split is clean from a rendered-navigation standpoint. The v2 "0 broken links" claim holds for actual document navigation; v3 is a stricter audit that catches non-rendering edge cases.
