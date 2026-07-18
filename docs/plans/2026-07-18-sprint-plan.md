# Sprint plan — 2026-07-18

- **Focus:** `none`
- **Candidate count:** 4
- **North star:** Make the shipped kernel trustworthy under multi-client use: honest contracts first, then unstick the concurrent-read spine and close residual event/query seams agents already depend on.

## Sequencing contract

1. **Foundations first** — Re-establish *honest* built-vs-proposed truth (`implementation-status` + roadmap + 07-12 P1 hygiene) before product residue or substrate claims. Without this, agents re-plan shipped work and re-open parked AUTH.
2. **Debts that unblock vs wait** — Doc SoT lag and ghost roadmap ids unblock planning and clear the 07-12 ledger early. Temporal `/breaches` is product residue that does **not** depend on the pool — ship it once SoT is honest. The concurrent-read **test harness** does not fix multi-client runtime latency this sprint; it only makes later P2b measurement runnable. TUI bus review / five-transport docs is seasoning after D1–D3 if capacity remains.
3. **Domain/contract shifts** — Prefer **none** on card/board definitions. The only additive wire surface planned is optional fields on `BreachItem` (`status` / `since` / `max` / `threshold`) for temporal types — computed projection, no tables, no migration. No dual-handle `Open`, no schema catalog growth, no AUTH continue design, no `filter=` on list this sprint.
4. **Partial order + demo cut points** — P1 honesty can start in parallel with technical exploring of P2a (no hard dep). Ship demos after: (D1) SoT falsehoods gone + docaudit green; (D2) temporal projection multi-surface via **named fixture/clock tests** (+ optional scripted smoke); (D3) shared-cache harness isolation test race-green **or** written park-with-evidence. Stop before P2b/P3 unless a verdict artifact is pre-written and time remains. TUI is optional after D1–D3.
5. **Out of this sprint** — See [Out of scope](#out-of-scope). Headline: no production pool; no `filter=` list wiring; no census item-scan deep wash; no core auth; no outbox; no TUI feature parity with web.

## Sprint goal

Restore **planning and agent trust** in what already ships and close the one unfinished cold-query admin seam, while landing a **test-only foundation** for later concurrent-read work:

1. A reviewer can plan from `implementation-status.md` / roadmap at HEAD without rediscovering shipped CSI filters, take-next retry, or reopening AUTH as an impl epic.
2. Cold temporal catch-up works: `type=status_timeout` / `type=card_idle` project past-due cards on `/v1/breaches` (and CLI/MCP/UI) with the same honesty as WIP/lane/blocked — including labels, Type→fields contract, and Limit:500 clamp transparency.
3. A shared-cache SQLite **test harness** exists (or P2a is parked with evidence) so P2b measurement becomes runnable next — **production `MaxOpenConns(1)` + `_txlock=immediate` remain unchanged**. Multi-client runtime serialization is **not** fixed this sprint.

Optional if capacity remains after D1–D3: TUI named and reviewed as a fifth operator surface without substituting the spine.

## Phases (4)

### Phase 1 — Contract SoT + 07-12 P1 hygiene

**Purpose:** Foundations. Kill agent re-plan loops and absorb residual 07-12 P1 bookkeeping into one honesty pass so later phases cite residual gaps, not rediscoveries. Merge overlapping work from `contract-sot-refresh` and the P1 slice of `concurrent-read-spine-start` under one owner.

**Ownership lock (SoT rows):** Phase 1 may refresh boundary SHA and non-breaches inventory freely. On temporal `/breaches` and roadmap §5 condition rows, Phase 1 may **only** leave residual/partial language and update the audit boundary — it must **not** flip temporal catch-up to built. Phase 2 alone owns those row flips and Limit:500 residual wording.

**Steps**

| # | Do | Observable outcome | Likely files |
|---|---|---|---|
| 1.1 | Refresh `implementation-status.md` anchors/inventory to live code; roll audit boundary past `b3bfed5` toward current HEAD (at least `0421efd` family) | Ghost `httpapi.go` gone; Card/Board/Link/EventType citations hit `types.go`; endpoint table lists delete/artifacts/breaches/reload/create-board where built | `docs/reference/implementation-status.md`; live refs `internal/httpapi/server.go`, `api.go`, `internal/core/types.go`, `internal/mcp/mcp.go` |
| 1.2 | Flip shipped falsehoods to **[built]** with file:line or post-boundary commits | CSV `status`/`type_id` filters documented; take-next cites `ErrClaimRaced` / `claimWithRetry` (no “not yet shipped”); TUI section consistent with HEAD | same SoT page; `api.go`; `errors.go`; `service.go` claim retry |
| 1.3 | Roadmap honesty: retag known ships; AUTH cease-fire; read-pool id | Reload / service supervision / GH Pages–MkDocs not “proposed”; §1 **lead** states design frozen at `docs/design/auth.md` (`c6cb17e`) — open questions are historical of the frozen design, not an invitation to investigate this sprint; §3 cites `card_57e1bde9…` not ghost `87903967` | `docs/roadmap.md`; `docs/design/auth.md`; `examples/demo-workspace/backlog.jsonl` |
| 1.4 | Close or update board P1 cards (comment + status) with citations. Prefer **corrective comments** (pinned/latest, with true state + file:line) over body rewrites so comment history and audit trail survive; rewrite a card **body** only if the card is still open and its body actively drives agents toward false contracts | P1a ledger truth (`card_29200d8b…`), P1b AUTH park (`card_7a3f4ebd…` + bearer park `card_350b1bac…`), P1c id reconcile (`card_8f8cbde7…`) advanced or done; stale plan path → archive; cards that still claim take-next unshipped / ghost pool id rewritten | backlog JSONL; `docs/archive/2026-07-sprints/sprint-2026-07-12.md` link hygiene |
| 1.5 | Required: docaudit green. Optional only if first-hour dig proves a greenfield gap | docaudit still green; optional date min/max drift test only if behavior is unpinned (else comment P1a “test already pins X” and stop) | `internal/docaudit/…`; maybe `internal/core/date_minmax_test.go` |

**Demo (third-party runnable)**

```bash
# Structural honesty
test ! -f internal/httpapi/httpapi.go
rg -n 'server.go|types.go|mcp.go' docs/reference/implementation-status.md | head
rg -n 'ErrClaimRaced|claimWithRetry|StatusIn|type_id' docs/reference/implementation-status.md
! rg -n 'Retry-to-next-candidate within one call is a tracked enhancement, not yet shipped' docs/reference/implementation-status.md
rg -n '57e1bde9|design frozen|auth\.md' docs/roadmap.md
# Roadmap short-id ↔ backlog
rg -n '57e1bde9' examples/demo-workspace/backlog.jsonl docs/roadmap.md
go test ./internal/docaudit/ -count=1
git log -1 --oneline   # boundary SHA ≤ HEAD

# Agent-facing acceptance (take-next / CSV truth matches live code)
# After 1.2, the SoT take-next bullet must name file:line or symbol that exists:
rg -n 'claimWithRetry|ErrClaimRaced' internal/core/
rg -n 'StatusIn|TypeID|type_id' internal/httpapi/api.go
# Blind check: SoT no longer instructs planners that intra-call take-next retry is unshipped
```

**Exit criteria (binary)**

- [ ] Audit changelog no longer freezes at `b3bfed5` as current truth without a newer boundary.
- [ ] No remaining prose **in SoT + roadmap + MCP/CLI help** that denies CSV list filters or intra-call take-next retry (archive plans are historical — out of this criterion).
- [ ] SoT take-next / CSV rows cite live symbols (`claimWithRetry` / `ErrClaimRaced` / filter paths) that `rg` finds in tree.
- [ ] `roadmap.md` §3 resolves real short-id present in `backlog.jsonl` (`57e1bde9`); docaudit passes.
- [ ] AUTH wording is **design frozen / impl parked** in §1 lead (not “built”, not “open investigation epic”); bearer park card cited.
- [ ] Temporal `/breaches` and `filter=` remain honestly partial/unwired in SoT (Phase 1 did **not** flip temporal catch-up to built).
- [ ] P1a behavior-test expansion either proven greenfield-and-landed **or** explicitly deferred after first-hour check (not an open sink).
- [ ] Agent-planning acceptance probe: a fresh planning pass (re-run the sprint-plan survey step, or a `cards list` + doc cross-check by an agent that did not do Phase 1) does **not** re-propose CSV list filters, intra-call take-next retry, or AUTH implementation as open work. Grep-able honesty is necessary but not sufficient — the point is that agents stop re-planning shipped work.

---

### Phase 2 — Temporal `/breaches` catch-up (slice A only)

**Purpose:** Close the one named event/query seam agents already depend on: conditions engine emits `status_timeout` / `card_idle`, but cold catch-up (`Service.Breaches`) still projects only WIP/lane/blocked. Finish **one** contract multi-surface with no second evaluator and no scheduler mutation from the HTTP path.

**RACI:** Phase 2 owns `breaches.go`, pure deadline helpers extracted from monitor rebuild/verify, API/CLI/MCP/UI surfaces for breaches, and SoT/roadmap §5 residual language for temporal projection + Limit:500. Phase 3 must not refactor monitor code.

**Past-due projection algorithm (normative)**

1. Extract **shared pure helpers** used by both rebuild *and* Breaches for deadline computation from card + monitor config (`At = StatusSince+max` / `UpdatedAt+idle_after`).
2. `Service.Breaches` temporal path = scan candidates → filter `At <= s.now()` → revalidate identity with **verify** semantics (key still matches status+StatusSince / UpdatedAt; monitor still configured) →.emit `BreachItem`.
3. Explicit **no** from `Service.Breaches`: `Arm`, `MarkConditionFired*`, scheduler heap ops, or any MonitorScheduler mutator.
4. Golden test card IDs = “verify would emit if due now” — **not** “rebuild deadline list length” (rebuild arms everyone; Path is past-due only).

**Wire contract (Type → fields on flat `BreachItem`)**

| `type` | Populated fields (beyond common id/title/board) | Notes |
|---|---|---|
| `wip_exceeded` | `column`, `count`, `limit` | Instant (existing) |
| `lane_limit` | `column`, `count`, `limit` | Instant (existing) |
| `card_blocked` | `blockers` (as today) | Instant; subject to ListCards Limit:500 |
| `status_timeout` | `status`, `since`, `max` | Additive; omitempty on others |
| `card_idle` | `since`, `threshold` | Additive; omitempty on others |

Document this table in `api-surface.md` + MCP/CLI help; provide JSON golden per type. Clients discriminate by `type` — no field meaning shift without type. Note the shape is **not uniform across types**: `card_blocked.blockers` is a nested array; the temporal types are flat scalars on the item — clients must not assume one shape.

**Clamp honesty:** Temporal scan reuses the same ListCards `Limit: 500` inheritance as blocked (`breaches.go` blocked path applies `Limit: 500`). Do **not** tag temporal catch-up “complete/full.” Prose documentation is **not sufficient** — agents don't read prose. Ship a client-visible signal: `BreachReport` echoes the applied scan `limit` and a `truncated: true` flag whenever the candidate scan hit the cap (cheap — the scan already knows the limit and result count). Document the flag + partial clamp on `/v1/breaches` temporal rows in api-surface + SoT (same semantics as blocked). Unclamped scan is out of scope (census B).

**Steps**

| # | Do | Observable outcome | Likely files |
|---|---|---|---|
| 2.1 | Shared pure deadline helpers + past-due filter per algorithm above | No arm-heap; no `MarkConditionFired` from Breaches; golden IDs match verify-if-due-now | `internal/core/breaches.go`; helpers shared with `service.go` rebuild/verify |
| 2.2 | Additive `BreachItem` fields + Type→fields docs + JSON goldens | Fields land with omitempty; contract table published; goldens for timeout/idle/WIP/blocked | `breaches.go`; `events.go` diffs for names; `api-surface.md` |
| 2.3 | Tests with injected clock (`clocktest` / package clock) | Timeout appears after max; vanishes on status change; idle after threshold; vanishes on mutation; `type=` exclusivity (timeout query does not return WIP-only rows) | next to `temporal_test.go`; `internal/httpapi/breaches_test.go` |
| 2.4 | Surface honesty for operators **and** agents | Friendly labels for `status_timeout`/`card_idle` (not raw slug); UI row/secondary line shows status + max (or idle threshold) + since/`<time data-ago>`; empty-state copy mentions temporal; CLI/MCP descriptions not WIP-only (keep MCP char budget short); docs flip temporal **projection** to built-for-projection + Limit:500 partial | `cli/commands.go`; `mcp.go`; `render.go` `conditionLabel`; `templates/breaches.html`; `api-surface.md`; `events/integration.md`; SoT breaches row; `roadmap.md` §5 |
| 2.5 | Demo path that cannot depend on 168h/336h engineering monitors | **Required:** named go tests under `-run 'Breach.*Timeout\|Breach.*Idle'` with clock injection. **Optional live smoke only if** `scripts/demo-temporal-breaches.sh` (or checked-in short-max scratch defs) seeds review/idle past due — otherwise drop live curl/UI from third-party runnable bar | tests preferred; optional script under `scripts/` |

**Demo (third-party runnable)**

```bash
# Required — fixture + clock; does not need multi-day dogfood board
go test ./internal/core/ ./internal/httpapi/ -count=1 \
  -run 'Breach|Temporal|Timeout|Idle|Blocked|WIP'

# Optional live smoke — only if demo script / short-max scratch exists:
# scripts/demo-temporal-breaches.sh   # seeds short thresholds + overdue card
# curl -s 'http://127.0.0.1:8787/v1/breaches?type=status_timeout' | head
# ./cards breaches --type status_timeout
# open 'http://127.0.0.1:8787/ui/breaches'
```

Acceptance: freeze test clock past `max_time_in_status` / `idle_after` → cold projection returns the card; leave status / touch card → report clears. Instant path still green. UI test asserts **row text** (label + deadline context), not only JSON.

**Exit criteria (binary)**

- [ ] Named tests `go test -run 'Breach.*Timeout|Breach.*Idle'` use clock injection and **fail** if an overdue fixture card is omitted; no “workspace happens to be overdue” acceptance.
- [ ] Projection reuses pure deadline helpers + verify revalidation (code review: no divergent second deadline math; no scheduler mutators from `Service.Breaches`).
- [ ] Type→fields table published in api-surface (+ short MCP/CLI help); JSON goldens cover timeout vs idle vs WIP vs blocked.
- [ ] API + CLI + MCP + UI show temporal **labels** and status/max|threshold/since context; empty-state mentions temporal; docs tag projection built-for-projection **and** Limit:500 partial (not “complete catch-up”).
- [ ] Instant path green: existing `breaches_test` WIP + blocked + `type=` exclusivity alongside new temporal tests.
- [ ] **Not** started: full `filter=` list wiring; unclamped rebuild item-scan package (B); outbox.

---

### Phase 3 — Test harness for future concurrent-read work (P2a only)

**Purpose:** Unstick the designed 07-12 substrate **test** path: land a **shared-cache SQLite test harness** that alone makes multi-conn parallel unit tests possible, so later P2b a/b/c measurement is runnable. Explicitly **do not** ship dual-handle production `Open` / reader pool. This phase’s user value is “**P2b becomes runnable**,” not “SSE/MCP/CLI stop serializing.”

**RACI:** Phase 3 owns `internal/sqlite/sqlitetest` (or sibling) + test `Open` call-site migration. No Phase 3 refactors of monitor / breaches code.

**Sequencing rule (normative):** Phase 2 must meet its exit criteria **before** any mass `:memory:` migrate (3.2–3.3) begins. The 3.1 isolation proof may run in parallel with Phase 2; the migrate may not. Two large diffs in flight would put the demo-able temporal-breaches win at risk from substrate churn.

**Production freeze (normative):** `MaxOpenConns(1)` + `_txlock=immediate` **unchanged**. Harness is test-only. Board card `card_77d6c663…` **must be comment-patched** so harness ≠ production pool; “replacing the MaxOpenConns(1) crutch” language is corrected: production single-conn stays until a **written P2b verdict**. Card body and this plan must not contradict.

**Stop gate after 3.1 (park-with-evidence):** If isolation test fails or shows nonzero flake under forced `-parallel`/`-race`, **park P2a with evidence — do not mass-migrate**. D1+D2 still ship. Remaining bare `:memory:` sites stay allowlisted; document residual debt on the P2a card. Binary exist: either full harness + empty main-site inventory **or** written park + zero drive-by dual DSN.

**Migrate strategy (not “~26 by feel”):**

1. Run inventory `rg` at phase entry; pin allowlist in harness package comment / P2a card.
2. Land helper + migrate high-churn `internal/sqlite` + `internal/core` first.
3. Other packages (httpapi/mcp/cli/cmd) optional if capacity; residual sites get an explicit debt note — never silent incomplete.
4. Leave file-backed hooks/tui/starter as-is.
5. Success demo: `rg` of bare `Open(":memory:")` / `sql.Open("sqlite", ":memory:")` outside allowlist is empty (or park letter explains residual).

**Steps**

| # | Do | Observable outcome | Likely files |
|---|---|---|---|
| 3.1 | Prove modernc shared-cache isolation **before** mass migrate. Named trap: `cache=shared` + `mode=memory` under modernc can share state across connections in surprising ways — spell it out, don't rediscover it. Smoke test: open N>1 conns to the same shared-cache name, write via one, read coherent state via another, assert isolation of uncommitted writes, under `-race` with forced `-parallel`; keep-alive connection holds the DB alive (verified dropped in 3.2) | new `internal/sqlite/sqlitetest` (or sibling) |
| 3.1b | **Stop gate** | Pass → continue. Fail/flake → park P2a with evidence; skip 3.2–3.4 migrate; still ship D1+D2 | P2a card comment + evidence |
| 3.2 | Harness constructor + lifetime | Unique `file:test_<uuid>?mode=memory&cache=shared` + keep-alive for DB lifetime; **Close/lifetime test** (DB dropped when keep-alive released; no global name collision) | `sqlitetest` package |
| 3.3 | Inventory-driven migrate in package-sized chunks | `rg` inventory at entry; core+sqlite required; others capacity; zero bare memory outside allowlist **or** residual debt recorded | core/sqlite/httpapi/mcp/cli/cmd tests; leave file-backed alone |
| 3.4 | Green race on touch packages | `go test -race` on sqlite + core (and migrated packages) | CI-local |
| 3.5 | Board truth + stop gate written | Comment-patch `card_77d6c663…` (harness ≠ MaxOpenConns change; P2a done **or** parked); “P3 not opened”; optional stub P2b decision table form on `card_495d2e09…` only | backlog JSONL |

**Demo (third-party runnable)**

```bash
go test ./internal/sqlite/... -count=1
go test -race ./internal/sqlite ./internal/core -count=1
# Isolation + lifetime tests present:
rg -n 'cache=shared|sqlitetest|keep-?alive|Close' internal/sqlite/
# Bare memory inventory (expect empty outside allowlist after successful migrate;
# after park: inventory + park comment exist instead)
rg -n 'Open\(":memory:"\)|sql\.Open\("sqlite", ":memory:"\)' internal cmd || true
```

**Exit criteria (binary)**

- [ ] Either: shared-cache isolation test exists and passes with `-race`, **and** Close/lifetime test exists; **or** written park-with-evidence on P2a, no mass migrate.
- [ ] Production `MaxOpenConns(1)` + `_txlock=immediate` **unchanged** (no dual DSN merge).
- [ ] `card_77d6c663` body/comments agree with plan: harness ≠ production pool.
- [ ] Successful path: migrated call sites use harness; `rg` bare opens outside allowlist empty; suite green. Park path: residual allowlisted, debt named.
- [ ] Board/tracker: P2a advanced or parked; P3 still blocked on P2b measurement.
- [ ] No method-routing audit table treated as “pool shipped.”

---

### Phase 4 — TUI fifth-transport seasoning (capacity-bound)

**Purpose:** Convert tip heat (`0421efd`) into durable operator discoverability and a **closed disposition** for DEBT-61 — **not** feature expansion or bus redesign. Only after D1–D3, or if a second track can run parallel **without** stealing Phase 2/3 owners. If capacity remains after D1–D3, prefer tightening the closed-loop agent path (MCP breaches + take-next truth) over TUI narrative page count.

**Steps**

| # | Do | Observable outcome | Likely files |
|---|---|---|---|
| 4.1 | DEBT-61 written **disposition** as a durable note — `docs/design/tui-bus-disposition.md` (or `docs/archive/`), linked from the debt ledger and the card; a card comment alone gets lost and this is exactly the “why we didn't” future agents need. Findings + **wontfix/defer** (not a new architecture project). Cap: subscribe teardown observe + non-TTY regression; bus = in-process only, ≠ SSE | `docs/design/tui-bus-disposition.md`; debt ledger; `internal/tui/tui.go`; `internal/core/bus.go` |
| 4.2 | Five-transport narrative | CLAUDE / architecture / using-cards name TUI; quit with `q`; in-process bus ≠ multi-process SSE | `CLAUDE.md`; `docs/architecture/index.md`; `docs/using-cards.md` |
| 4.3 | Small safety nubs | Non-TTY / `--json` bare `cards` still usage (regression test); optional ListCards 500 truncation flash | `cmd/cards/main.go` / tests; `tui.go` refresh Limit |
| 4.4 | Dogfood (narrow) | Claim/status/comment + quit clean. **No** second-writer single-writer pain demo as TUI acceptance — link that narrative to the P2b card only | short note or card comment |

**Demo (third-party runnable)**

```bash
cards </dev/null 2>&1 | head          # usage, not full-screen
cards --json </dev/null | head
cd examples/demo-workspace && cards   # TTY: boards, helpers ?, q exits clean
go test ./internal/tui/ -count=1
rg -n 'Terminal UI|internal/tui|fifth' CLAUDE.md docs/using-cards.md docs/architecture/index.md
```

**Exit criteria (binary)**

- [ ] DEBT-61 closed with disposition (resolved / wontfix / defer) recorded as a durable markdown note linked from the debt ledger — not a silent open questions list, not just a card comment.
- [ ] Agent-facing docs produce “how to open TUI and quit” without grepping `internal/tui`.
- [ ] Script-safety regression exists or is explicitly verified in PR description.
- [ ] No remote-URL TUI, field editors, design-system parity, or package-split work landed.
- [ ] No multi-process “prove pool need” dogfood treated as Phase 4 acceptance.

---

## Thread

```text
Phase 1 (honest map)
    │  unblocks re-plan poison; parks AUTH; fixes pool card citation
    ├──────────────► Phase 2 (temporal breaches)     [owns breaches.go + helpers]
    │                   agent cold catch-up + UI honesty;
    │                   Type→fields + Limit:500 partial language
    └──────────────► Phase 3 (test harness / P2a)    [parallel-ok after 1.x; no dep on 2]
                        isolation proof → migrate OR park-with-evidence
                        enables later P2b a/b/c → only then P3 pool
                              │
Phase 4 (TUI season) ◄────────┘ optional; must not become spine substitute
```

- **Composition:** Phase 1 is pure foundation. Phases 2 and 3 both advance the north star after honesty — **breach honesty for agents**, **test substrate unstick for future multi-client work** — and can run as two tracks once 1.1–1.3 are stable. SoT row protocol and RACI prevent dual-write thrash.
- **Why this order vs put ting pool first after SoT:** P2a does not remove production single-conn queue; temporal catch-up is the higher *shipping-week agent value* on an already-built engine. Harness still starts so the week-28 cliff is broken without false pool merch.
- **What this sprint does *not* claim:** multi-client runtime latency fixed; readers free of `MaxOpenConns(1)` serialization; temporal catch-up complete beyond Limit:500; AUTH implemented.
- **Demo narrative arc:** day-of D1 checklist → mid-sprint clock-fixture temporal breaches multi-surface → end-sprint harness isolation+lifetime (or written park) → optional TUI walk.

## Risks

| Risk | Plan confronts via |
|---|---|
| Shallow SoT re-freeze across many commits | Exit: every amended row names post-boundary commit or live file:line; take-next/CSV agent-facing `rg` check |
| Overclaim multi-client runtime fixed | Goal rewritten; Phase 3 title/user value = “P2b runnable”; production freeze restated |
| Empty live temporal demos on 168h/336h board | Required path = clock tests; live curl only with scripted short-max scratch |
| Scope absorption of `filter=` / census B | Phase 2 slice A locked; B/C explicit out of scope |
| Silent P3 pool ship / production dual-conn | Phase 3 forbids dual-handle Open; **card_77d6c663 patched** so harness ≠ MaxOpenConns change |
| Second evaluator / scheduler mutation | Normative algorithm; golden = verify-if-due-now; ban Arm/Mark from Breaches |
| Limit:500 silent “full catch-up” | Document clamp on temporal rows; kill “complete” tags |
| modernc shared-cache flakiness | 3.1 proof first; 3.1b park-with-evidence before mass migrate |
| Dual phase SoT thrash on breaches rows | Phase 1 may not flip temporal built; Phase 2 owns those rows |
| Phase 1.5 becomes product-behavior sink | Optional only after first-hour greenfield proof |
| TUI heat steals spine owners | Phase 4 capacity-bound; prefer agent closed-loop over TUI page count |
| AUTH overcorrection or re-open | Exit: parked language; §1 lead reframes questions as historical of frozen design |
| Harness keep-alive leak / name collision | Close/lifetime test required on success path |
| Parallel 2∥3 merge hazards | RACI: Phase 2 owns breaches/helpers; Phase 3 owns sqlitetest + test Open only |

## Out of scope

| Deferred | One-line reason |
|---|---|
| **P2b file-WAL baseline + a/b/c measurement full run** | Harness (or park) first; measurement is the next milestone |
| **P3 reader pool / write-hold dual Open** | Forbidden until written P2b verdict (c = park with evidence is success) |
| **`GET /v1/cards?filter=`** | Orthogonal query-DSL surface (slice C); steals breach completion |
| **Census item-scan unclamp + DefaultFilter boards (`card_8fc028ad`)** | WIP/lane COUNT(*) already fixed; item scans optional follow-on B |
| **Core AUTH / bearer / ACL hooks** | Philosophy §7 / YOLO; design frozen, impl parked |
| **Outbox / webhooks / subscribers** | Explicit no-go without fresh go signal |
| **Second storage backend / backend-neutral filter AST** | No second backend scheduled |
| **cards-bench suite implementation** | Design-only; harness may be reused later, not this sprint |
| **style_field a11y floor / monotype residual body** | Not north-star; backlog residual |
| **TUI remote `CARDS_URL`, field editors, theme parity, package split** | Accretion vs seasoning; philosophy §1 |
| **Revive htmx UI** | Deliberately killed |
| **Workflow DSL / automation engine** | Soft-events + hooks only |
| **event_retention_days activation** | Dormant residue, not agent catch-up seam |
| **Multi-workspace process router** | One workspace per process |
| **Unclamped temporal /breaches scan** | Same Limit:500 partial as blocked; census B later |

## Traceability

| Phase | Candidate path(s) drawn | Notes |
|---|---|---|
| **Phase 1** | `contract-sot-refresh` (primary); P1a/b/c bookkeeping slice of `concurrent-read-spine-start` | Merge bookkeeping; S effort; P1a behavior test optional/narrowed |
| **Phase 2** | `breaches-query-residue` slice A only | Optional B/C deferred; Type→fields + UI labels + clamp honesty in-band |
| **Phase 3** | `concurrent-read-spine-start` P2a harness only | User value corrected: “P2b becomes runnable,” not “readers stop serializing”; park path added |
| **Phase 4** | `tui-surface-harden` S seasoning | Capacity-bound; DEBT-61 disposition not redesign |

| Deferred (whole or remaining slice) | From path | Reason |
|---|---|---|
| P2b baseline + P3 pool/write-hold | `concurrent-read-spine-start` | Measurement gate; no silent expansion |
| Census B + filter= C | `breaches-query-residue` | Keep single-contract completion; avoid L multi-seam |
| Feature-heavy TUI / remote | `tui-surface-harden` | Tip already works; avoid mission distraction |
| AUTH token impl | board/`roadmap` residual via P1b text only | Park, do not implement |

**Board anchors referenced for shipping team claim/close:**

- Tracker: `card_b3079e56…` (07-12 parent)
- P1: `card_29200d8b…`, `card_7a3f4ebd…`, `card_8f8cbde7…`
- P2a: `card_77d6c663…` (patch body: harness ≠ MaxOpenConns) · P2b `card_495d2e09…` · P3 `card_57e1bde9…` (canonical)
- Temporal breaches: `card_e3c63f21…`
- Defer: `card_8fc028ad…` (census B), `card_c7a70b64…` filter bag piece, `card_350b1bac…` bearer park

**Constraints respected:** no core auth, no outbox, no second backend, no workflow DSL, no htmx revival, core growth limited to projection fields + test harness (not new engines, not production pool).

## Review disposition

Every blocker and major from `review.md`, with disposition.

### Blockers

| # | Review item | Disposition |
|---|---|---|
| 1 | North-star / goal overclaims multi-client runtime fix | **Fixed.** Goal rewritten to three demoable outcomes; Phase 3 title → “Test harness for future concurrent-read work”; user value = “P2b becomes runnable”; explicit non-claims tableline. Candidate mapping corrected in Traceability. |
| 2 | Live Phase 2 demos empty on 168h/336h dogfood board | **Fixed.** Required demopath = clock-injected named tests. Live curl/UI only with optional short-max script/defs; otherwise dropped from third-party bar. Exit no longer “workspace past-due.” |
| 6 | Board P2a card invites production dual-conn vs plan freeze | **Fixed.** Phase 3 production freeze + required comment-patch of `card_77d6c663`; exit criterion that card and plan agree; harness ≠ MaxOpenConns. |
| 7 | “Reuse rebuild arithmetic” underspecifies past-due projection | **Fixed.** Normative algorithm: pure helpers + `At <= now` + verify revalidation; ban Arm/Mark/scheduler mutators from Breaches; golden = verify-if-due-now. |
| 14 | Harness integrate-first; no park-with-evidence path | **Fixed.** Step 3.1b stop gate: fail/flake → park P2a, no mass migrate, D1+D2 still ship; binary exit either full harness or written park. |

### Majors

| # | Review item | Disposition |
|---|---|---|
| 3 | Phase 1 demos prove greps, not agent plan-time behavior | **Fixed.** Agent-facing acceptance: SoT take-next/CSV bullets must cite live symbols that `rg` finds; still keeps greps + docaudit. |
| 4 | Temporal UI can ship API-complete and look WIP-only | **Fixed.** Exit requires labels, row/secondary deadline context, temporal empty-state, and UI/HTTP test of **row text**. |
| 8 | Temporal projection inherits silent Limit:500 | **Fixed.** Clamp honesty required in api-surface + SoT; forbid “complete/full catch-up” tags; unclamp remains OOS. |
| 9 | Wire contract incomplete for Type→fields | **Fixed.** Normative Type→fields table; goldens per type; publish in api-surface + short MCP/CLI help. |
| 10 | Phase 1+2 dual-write SoT breaches rows | **Fixed.** Ownership lock: Phase 1 may only leave temporal partial; Phase 2 alone flips projection rows and residual language. |
| 11 | Phase 1.5 optional date-minmax expands into product work | **Fixed.** Required Phase 1 = SoT/roadmap/AUTH/id only; 1.5 optional after first-hour greenfield proof else comment-and-stop. |
| 15 | `:memory:` inventory scale underspecified | **Fixed.** Inventory-via-`rg` at entry; core+sqlite first; residual debt explicit; success = empty outside allowlist (not “~26”). |
| 16 | Phase 2 exit time/seed-dependent without fixtures | **Fixed.** Exit = named clock tests that fail if overdue fixture omitted; live smoke optional+scripted. |
| 17 | Doc-only “no denying prose” hard under parallel edits / archive | **Fixed.** Scope limited to SoT + roadmap + MCP/CLI help; archive historical; 1.4 rewrites active board card bodies that drive false contracts. |
| 18 | Phase 3 amortizes future work; Phase 4 may not pay debt | **Fixed.** Close/lifetime harness tests required; Phase 4 DEBT-61 must close with disposition not open questions. |
| — | (Reliability) Parallel tracks without owner map | **Fixed.** RACI one-liner in Thread / Phase 2 / Phase 3. |

### Minors / suggestions (non-blocking)

| # | Item | Disposition |
|---|---|---|
| 5 | Phase 4 dogfood of single-writer pain | **Fixed.** Dogfood narrowed to claim/status/comment + quit; second-writer narrative → P2b link only. |
| 12 | AUTH §1 still invites investigation | **Fixed.** 1.3 rewrites §1 lead: questions historical of frozen `auth.md` `c6cb17e`. |
| 13 | DEBT-61 architecture escalation | **Fixed.** Disposition = findings + wontfix/defer; cap at teardown + non-TTY + in-process docs. |
| 19 | Weak instant-path canary | **Fixed.** Exit requires WIP + blocked + type exclusivity + temporal tests. |
| S1 | Prewrite P2b decision table skeleton | **Accepted optional** in 3.5 (form only). |
| S2 | BenchmarkBreachesScan | **Declined.** Not this sprint; avoids p99 theater. |
| S3 | Harness reused by cards-bench later | **Accepted as note only**; cards-bench remains OOS. |
| S4 | MCP/CLI short descriptions | **Accepted** — char budget kept short on surface rewrite. |
| S5 | Prefer agent closed-loop over TUI pages | **Accepted** as capacity rule in Phase 4 purpose. |
| S6 | Roadmap short-id ↔ backlog executable lemma | **Accepted** into Phase 1 demo (`rg` both files). |
| S7 | Rename Phase 3 title | **Accepted.** Title is now “Test harness for future concurrent-read work (P2a only).” |

---

## Post-review amendments (plan-owner pass, 2026-07-18)

Applied after a second review against the live tree (clock seam in `internal/core/clock.go`, verify/rebuild in `service.go`, 23-file `:memory:` inventory — all confirmed). These amend the plan above; the pipeline's original `final_plan` artifact under `.pi/run/sprint-plan-20260718-9c8b68/targets/` predates them.

1. **Phase 1 exit** — added agent-planning acceptance probe (behavioral, not grep-only).
2. **Phase 1.4** — corrective comments preferred over card body rewrites; bodies only for open, actively-misleading cards.
3. **Phase 2 wire contract** — `BreachReport` gains applied-`limit` echo + `truncated` flag; Type→fields table notes `card_blocked.blockers` is nested vs flat temporal scalars.
4. **Phase 3** — normative sequencing rule: no mass migrate before Phase 2 exits.
5. **Phase 3.1** — shared-cache modernc trap and smoke-test shape spelled out.
6. **Phase 4** — DEBT-61 disposition must be a durable markdown note, not just a card comment.
