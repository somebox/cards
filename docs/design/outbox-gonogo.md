# Outbox go/no-go gate (Sprint A, Phase 6)

Decides whether **Sprint B** (the durable outbox/tailer + webhook delivery) is
built now, or deferred. The criteria below were pre-registered from the sprint
plan *before* the dogfood evidence was gathered, so the verdict is measured
against a fixed bar rather than rationalized after the fact.

Two gates. The **foundation gate** is binary and objective: if it fails, nothing
downstream matters and Sprint B has no safe base. The **needs gate** decides
whether the outbox is worth building *this cycle* given real usage.

---

## Gate 1 — Foundation (binary; must pass)

**Pre-registered criterion:** a fired `persist:true` condition can never be
absent from the event log across an induced failure. Concretely: mark-fired and
the durable event append commit together, or neither does — so a crash between
them cannot leave a fired-but-never-appended condition (which would be lost
forever *and* suppress re-fire, unrecoverable by any later cursor replay). Any
counterexample ⇒ **automatic no-go**; fix the foundation before building on it.

**Result: PASS.** The Phase 2 atomicity fix (`MarkConditionFiredAndAppend`)
commits the fired-marker and the event in one transaction. Proven by
fault-injection tests that force the append to fail mid-transaction:

- `TestMarkConditionFiredAndAppendRollsBackOnAppendFailure` — a forced append
  failure rolls back the mark, so the condition re-fires (no orphaned mark, no
  lost event).
- `TestMarkConditionFiredAndAppendAtomic` — happy path: mark + event commit
  together; a duplicate fire appends nothing.
- `TestClaimAtomicFTSFailureRollsBack` — the same rollback discipline on the
  claim path.
- `TestMonitorScheduler_RestartDoesNotRefire` / `_PersistTrueArmsWithNoSubscribers`
  — end-to-end: a persist:true condition fires once and does not re-fire across
  a restart.

The event log is therefore trustworthy ground truth for any future cursor/outbox
consumer. The foundation gate is satisfied.

---

## Gate 2 — Needs (decides Sprint B)

**Pre-registered criterion:** during real use, does a consumer the team *relies
on* — a shell hook (at-most-once) or a disconnected SSE client without replay —
miss an event that **is** in the log, **and does that miss matter**? This is
pitched at the *delivery* layer, where the residual gap lives once the log
itself is crash-safe (Gate 1). **Go** ⇔ a relied-upon consumer demonstrably
missed (or would have missed) events the log retained, and it mattered. **No-go**
⇔ no such delivery gap was hit or cared about in real use — then Sprint B is cut
and re-planned, and the RFC (Phase 7) still lands.

### Evidence

**1. External dogfood, 2026-07-05 (a real agent-orchestrator driving the board).**
The friction reported was *entirely* CLI machine-contract — workspace targeting,
output modes, exit codes, bulk reads, unknown-flag handling. **Zero** of it was
durable delivery: nobody reported a dropped event, a missed hook, or an SSE
consumer losing data that mattered. This is the strongest available signal and
it points squarely away from the outbox.

**2. Synthetic multi-surface exercise (this phase).** Drove create → transition →
comment → attach → `list --include` → `feed` across CLI, with differentiated
exit codes and strict-field/enum validation. Friction found (see log below) was
again all *interaction contract*, not delivery. No event was dropped; the
reactive path never failed; `feed` returned every fact.

### Friction log (covers the surfaces, not just happy paths)

- **Workspace init/targeting is confusing.** `cards init X` scaffolds `X/.cards`,
  but `--workspace`/`$CARDS_WORKSPACE` want the `.cards` dir *itself*; pointing at
  the parent silently opens an empty workspace. (somebox/cards#17 territory — the
  `--workspace` flag helps, but the init/target relationship still surprises.)
- **Writes require prior introspection.** An agent must read `cards workspace`
  first to learn a type's field ids and a board's valid statuses; blind writes
  are rejected in strict mode. Mitigating positive: the errors are *actionable* —
  `unknown_field (description) [valid: notes]` and `unknown_enum (status) [valid:
  todo, doing, done] — See GET /v1/workspace`. The contract is discoverable once
  you look; it just isn't guessable.
- **`cards attach` needs an artifact-typed field**, and no shipped card type
  declares one, so attachments aren't exercisable out-of-the-box. Follow-up: add
  an `artifact` field to a demo type so the feature is reachable from the box.
- **Positives that worked cleanly:** differentiated exit codes (`get` missing →
  `3`, stale `--version` → `4`), `list --include` (one call, no N+1), `feed`
  (every event present and typed), `--quiet` id output, and clear
  version-conflict errors.

### Verdict (recommended): **NO-GO on the outbox this cycle.**

Both evidence sources — a real external agent-driver and a synthetic sweep —
found the friction is the **interaction contract**, not durable delivery. With
the log made crash-safe (Gate 1) and no relied-upon consumer observed missing
events that mattered, building the XL outbox + webhooks now would be
infrastructure ahead of demonstrated need — exactly the over-investment the
sprint's own risk list warned against.

**What no-go means (per the plan):**
- Sprint B (durable outbox/tailer + webhook delivery) is **deferred**, not
  cancelled. Re-open it on a real go signal: a consumer the team relies on
  losing events that matter during actual use.
- **Phase 7's subscribers.md RFC still lands** — it resolves the naming
  collision, the ephemeral-deliverability default, and the dispatch topology on
  paper, so if the go signal arrives the design is ready and reviewed cold.
- The higher-leverage work the evidence *does* point at (CLI/workspace ergonomics,
  attachment reachability) is captured above and in ROADMAP for the next cycle.

> This verdict is a **recommendation** grounded in the available evidence. The
> plan frames the needs gate as a team judgment from live dogfooding; a human
> work session on the real board would strengthen it, but both independent
> signals so far agree. Confirm or override before Sprint B is formally shelved.
