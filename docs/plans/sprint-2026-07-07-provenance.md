# Sprint 2026-07-07 — provenance

Process artifacts behind [sprint-2026-07-07.md](sprint-2026-07-07.md).
Nothing here is normative; the plan supersedes it where they disagree.

## How this plan was made (and why it differs from the last one)

Two `sprint-plan` workflow runs were attempted (run `wf_add35e59-6eb`, fresh +
resumed; 19 agents each). Both produced **real, useful discovery** and both
failed in the same interesting way, so the final plan was **hand-authored**
from the groomed board cards + the two design docs
([THEMES.md](../design/THEMES.md), [STYLE-FIELD.md](../design/STYLE-FIELD.md)),
with the workflow's outputs used as inputs rather than as the draft.

### Failure 1 — the probe that became the answer

The `synthesize-state` agent hit a structured-output submission problem,
decided to *"isolate whether it's a parsing issue by submitting a minimal
version first"* — and its probe (`{"summary": "test summary", "momentum":
["one","two"], …}`) was accepted by the StructuredOutput tool, which **ends
the agent's turn**. The debug probe became the phase's final answer and
poisoned everything downstream (the alignment agent correctly flagged it).
The resumed run, with a fence added to the prompt, still degraded to a
size-bisection probe (`"Test size hypothesis."`) with terse-but-real fields.
Lesson recorded in the workflow script: *the first StructuredOutput call is
final; never submit test payloads* — and a length guard on the synthesis is
worth adding.

### Failure 2 — the focus override (the interesting one)

Given the explicit focus "UI and frontend tasks", the workflow's
vision-alignment and strategy phases **overrode it**: they ranked the SQLite
read pool as "the north star", called both theme candidates "UI-framework
territory" drift risks, and produced a complete sprint plan for
**read contention in `cards serve`** instead. That plan is measured, gated,
and honest (it distinguishes in-process contention a Go pool can fix from
cross-process file locking it cannot; it makes "not needed yet" a legitimate
verdict). It was too good to discard and the wrong sprint to run: the full
plan is attached to card `fec18019` (evidence artifact + summary comment).

### The dissent, taken seriously

The workflow's core arguments and how the hand-authored plan answers them:

| Workflow's claim | Plan's answer |
|---|---|
| "Agents don't consume themes; UI is cosmetic for the primary audience" | The sprint's P1/P2 are not cosmetic: they close the *human half of the coordination loop* (comment, append evidence, create cards) that today forces humans through the CLI. |
| "style_field pushes presentation into the board contract" | Agreed enough to act: `8b3e83d9` stays **out of scope** pending its four open design questions; the granular types (07-06) relieved its motivating pain. |
| "Read pool is the highest-value core work" | Preserved intact on `fec18019` with an explicit pull-forward trigger in the plan's Risks: real board latency during this sprint's dogfooding. |
| "Momentum is meta-work; the coordination model stands still" | Fair warning, noted — this sprint ships user-facing capability (editing, creation), not more workflow-about-the-workflow. |

### Also fixed en route

A dropped `}` in `style.css` (from a stash-conflict resolution two days ago)
had silently swallowed the entire labels theme — nothing validates CSS. The
plan's cross-cutting test section adds a docaudit-pattern CSS sanity test
(brace balance + theme-prefix discipline) so this class of failure gets
caught by `go test ./...`.

## Run metadata

- Run 1: 19 agents, ~836k tokens (2026-07-06, machine contract sprint — for
  comparison, that run's focus held).
- Run 2 (this sprint, fresh): 19 agents, ~819k tokens — survey facets real,
  synthesis probe-poisoned, candidates off-focus.
- Run 2 (resumed after prompt fence): 19 agents, ~593k tokens — synthesis
  degraded again (size probe), read-pool plan produced.
- Workflow script: `~/.claude/workflows/sprint-plan.js` (user-maintained;
  gained `robustAgent` degraded-fallback wrappers on 07-06 and the
  first-call-is-final fence on 07-07).
