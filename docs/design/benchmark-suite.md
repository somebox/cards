# Plan: `cards-bench` — a versioned benchmark suite

Status: **planning** (no code yet). This doc scopes and sequences the work.
Discussion decisions are captured in "Open decisions" at the bottom; the
numbered steps assume the recommended option for each.

> Related: [`docs/concepts/philosophy.md`](../concepts/philosophy.md),
> [`docs/architecture/index.md`](../architecture/index.md),
> [`docs/reference/implementation-status.md`](../reference/implementation-status.md).

## 1. Goal and non-goals

**Goal.** A small, **versioned, opinionated** suite that answers: *can a room
of agents work against one workspace without the interface layer falling over?*
It pins **correctness under contention** as the primary gate and reports
**throughput/latency** as secondary, machine-labeled numbers.

**Non-goals.**

- Not a SQLite benchmark. We run the production DSN (WAL + `busy_timeout(5000)`
  + `_txlock=immediate` + `MaxOpenConns(1)`) and do not vary pragmas.
- Not an infinitely configurable harness. No `--agents=N --rps=…` matrices.
  Intensities are fixed per scenario and frozen at a `suite_version`.
- Not an HTTP/middleware bakeoff in v1. The primary surface is
  `internal/core.Service` + the real `*sqlite.Store`, so a regression localizes
  to our code, not JSON framing. (An optional HTTP profile is a later add.)
- Not multi-process / multi-workspace (explicit architecture non-goal).
- Not a hook-subprocess storm (extension boundary; process noise).

## 2. What we're testing (the seams, verified in code)

| Seam | Code | Why it matters under contention |
|---|---|---|
| Optimistic concurrency | `Service.PatchCard` checks `req.Version != current.Version` → `VersionConflict` | Agents coordinating on one card must not silently clobber |
| Claim / take-next | `Service.TakeNext` → `Store.ClaimAtomic` (SQLite CAS on unowned rows) → `ErrClaimRaced` + bounded retry in `claimWithRetry` | Multi-agent work distribution; double-claim is a correctness bug |
| Single writer | `sqlite.Open`: `MaxOpenConns(1)`, `_txlock=immediate`, `busy_timeout(5000)` | Writers queue; this is the ceiling we want to *characterize*, not defeat |
| Comments | `Service.AddComment` (append-mostly), `Store.CommentCounts` | High-churn, export-embedded; can make `GetCard` slow |
| Artifacts | `Service.AddArtifact` version-guard + `artifacts.Manager.Stage`→`Commit/Discard` | Raced/failed attaches must not orphan blobs (already unit-tested for 2 writers) |
| Event bus / SSE | `InProcBus` with buffered chan (SSE uses buf=64); slow consumers drop | Coordination memory; our contract is **drop, not block** — measure drops honestly |
| Portable JSONL | `exportJSONL` / `importJSONL` (currently unexported in `cmd/cards`) | Git-portable state; export paginates at `Limit:500` with N+1 `GetCard`; import is restore-into-empty |
| List ceiling | `ListCards` clamps (500); cursor pagination | Agents that forget cursors get a truncated world |

Existing race tests we build on, not duplicate:
`internal/core/artifact_concurrency_test.go`, `internal/core/claimretry_test.go`,
`internal/sqlite/claimatomic_test.go`. The suite sits *above* these.

## 3. Suite shape (frozen as `suite_version: 1`)

### 3.1 Fixtures (shared, deterministic)

One embedded mini-workspace, generated from a fixed seed (not the live
`examples/demo-workspace`, which drifts). Mirrors the shape of `testConfig()`
in `internal/core/service_test.go`:

- Workspace `bench`, 1 board `eng` (transitions `todo→in_progress→review→done`,
  WIP limit on `in_progress`), card types: `task` (text/enum/number/repeating
  fields) + `note` (text) + `task`-with-`screenshot` artifact field variant.
- Users: `agent-A`…`agent-H` + `human`. Seeded via `Store.InsertUser`.
- On-disk SQLite under a per-run temp dir (WAL as production — **not** `:memory:`,
  which hides lock contention). Opened with the real `sqlite.Open`.

**Size classes** (baked in, not free knobs):

| Class | Pre-materialized cards | Used by |
|---|---:|---|
| **S** | 200 | all scenarios, CI-fast |
| **M** | 2 000 | primary compare point |
| **L** | 10 000 | portable + list-ceiling only (nightly tag) |

v1 runs **S + M** by default; **L** only where the scenario name says so.

### 3.2 Scenarios (8, fixed intensity)

Each: fixed op counts (not wall-time budgets — repeatability across busy
hosts), pass/fail invariants, reported metrics.

1. **`claim-stampede`** — 50 unowned cards, 8 agent goroutines loop `TakeNext`
   until empty. **Invariants:** every card claimed exactly once; no double-owner;
   no error except "pool empty". **Metrics:** wall, attempts/win, `ErrClaimRaced`
   count, p50/p95 claim latency.

2. **`hot-card-patch`** — 1 hot card, 8 agents × 25 read→patch cycles with
   current `Version`, re-reading on `version_conflict` (bounded). **Invariants:**
   final `Version` == successful commits; no non-conflict errors. **Metrics:**
   commits, conflicts, conflicts/commit, p50/p95 success latency.

3. **`mixed-read-write`** — fixed 2k ops: ~70% `ListCards`/`GetCard`/`CountCards`,
   ~25% `PatchCard` on random owned cards (version-refreshed), ~5% `CreateCard`.
   4 writers + 8 readers. **Invariants:** no internal errors; cursor-walked
   pages don't lose/dup under concurrent inserts. **Metrics:** ops/s by kind,
   error rate, p50/p95 by kind.

4. **`comment-burst`** — 20 cards, 8 agents × 50 `AddComment`, concurrent
   `GetCard` on the same cards. **Invariants:** `CommentCounts` == posts;
   stable `created_at`+id order; no dropped rows. **Metrics:** comments/s,
   `GetCard` p95 during burst.

5. **`artifact-race`** — card with `screenshot` field; 4 concurrent
   `AddArtifact` at fixed sizes **1 MiB, 8 MiB, 32 MiB** (the documented upload
   cap area), fresh `version` per round. **Invariants:** exactly one winner per
   race; zero orphan temps/blobs (reuse the `storedBlobs` helper pattern);
   stale `version` never publishes bytes; winner content hash matches.
   **Metrics:** stage time, commit time, discard-correctness (must be 100%).

6. **`event-fanout`** — 2 bus subscribers (all-events / one-type), buf=64
   (production SSE), concurrent create+patch+comment writers. **Invariants:**
   committed event log count == successful mutations' events; delivered ≤ log
   (drops allowed) but drop count reported; no hung subscribers after writers
   stop. **Metrics:** events/s produced/delivered/dropped, max bus lag.
   **Note:** drops are *not* failures; a spike relative to baseline is the
   interface signal.

7. **`portable-roundtrip`** — build class M (optional L tag) via bulk create
   with comments+links; `exportJSONL` (state-only + full) → temp files → fresh
   store `importJSONL`. **Invariants:** card/comment/link counts match;
   ids/versions/timestamps preserved; state-only export id-sorted hash stable
   across two exports. **Metrics:** export s, import s, JSONL bytes, cards/s.

8. **`list-ceiling`** — class L (or M+): `ListCards` default vs full cursor
   walk, concurrent with light patches. **Invariants:** cursor walk covers
   every id exactly once; default page never > clamp. **Metrics:** full-scan
   latency, page p95.

## 4. Scoring & comparison

**Per-run record** (JSON, versioned):

```
suite: "cards-bench"
suite_version: 1
git_sha, go_version, goos/goarch, gomaxprocs, cpu_count, cpu_model
scenarios[]:
  name, size_class
  status: pass|fail
  invariants{ name -> bool }
  metrics{ name -> {value, unit} }
  duration_ms
```

**Comparison model:**

- **Primary: invariant pass/fail.** Must never regress. No flake budget; a flaky
  invariant is a suite bug, not a tolerated failure.
- **Secondary: ratios** that travel across machines better than absolute ms:
  `conflicts/commit` (hot-card), `races/win` (claim), export/import `cards/s`,
  event `drop_rate` under the fixed writer load.
- **Tertiary: absolute wall times** on a *pinned* runner only. CI stores a
  `bench-baseline.json` with soft bands (warn if p95 > 2× baseline); never
  hard-fail CI on absolute ms unless a dedicated runner is pinned.

**CI posture:**

- PR CI: **S** scenarios, correctness only (fast).
- Nightly / manual: **M** (+ L for portable & list-ceiling), record metrics.
- `cards bench` first-class command (agent-friendly JSON output) so "run this on
  your MBP vs CI" is trivial. Test-tag (`-tags=bench`) access too.

## 5. Implementation strategy

### 5.1 Package layout

```
internal/bench/
  suite.go          suite_version, Run(name?), JSON reporter, machine labels
  fixture.go        seededWorkspace(size, seed), on-disk store + Service, users
  scenarios/
    claim_stampede.go
    hot_card_patch.go
    mixed_read_write.go
    comment_burst.go
    artifact_race.go
    event_fanout.go
    portable_roundtrip.go
    list_ceiling.go
cmd/cards/
  bench.go          `cards bench` subcommand wiring (prints JSON + summary)
```

A `Scenario` interface:

```go
type Scenario interface {
    Name() string
    Sizes() []SizeClass          // which size classes apply
    Run(ctx context.Context, env *Env) Result
}
```

`Env` holds the fixture (Service + Store + artifacts root + temp dir) and a
fixed `seed`. Scenarios are registered in one place (`suite.go`); the order is
the suite definition and is versioned.

### 5.2 Reuse, don't rebuild

- **Fixtures:** model on `testConfig()` + `newTestServiceWith()` from
  `internal/core/service_test.go`, but on-disk and seeded at scale. Use
  `coretest.SeedCard` / `coretest.CardID` for deterministic ids where useful.
- **Actors:** `core.WithActor(ctx, "agent-A")` per goroutine (the pattern every
  existing test uses).
- **No-orphan assertion:** reuse the `storedBlobs` walk pattern from
  `internal/core/artifact_concurrency_test.go`.
- **Portable:** call the real `exportJSONL`/`importJSONL` — *after* the
  extraction prerequisite (§5.3).

### 5.3 Prerequisite: extract portable JSONL out of `cmd/cards`

`exportJSONL` and `importJSONL` are unexported in `package main`
(`cmd/cards/portable.go`), so the bench (and any future test) can't reach them.
First mechanics PR, zero behavior change:

- Move `portStats`, `exportJSONL`, `importJSONL`, `portEnvelope` into a new
  `internal/portable` package.
- `cmd/cards/export.go` and `import.go` call `portable.Export(...)` /
  `portable.Import(...)`.
- Verify `cmd/cards/portable_test.go` + `cmd/cards/reconcile_reload_test.go`
  still pass.

This is a cleanup the repo wants anyway (it makes import/export testable from
any package) and unblocks scenario 7 without duplicating the JSONL logic.

### 5.4 Intensity & clock discipline

- **Fixed op counts**, not wall-time targets (a 5s budget does different work
  on a busy laptop vs a quiet CI box). A wall-time budget may appear only as a
  *timeout-fail* guard, never as the work target.
- **Wall clock** for bench (fake clocks hide lock contention — the whole point).
  Do not reuse `clocktest.Fake` here.
- **No jitter** in v1; deterministic pacing (or none). Repeatability first.

### 5.5 What we are *not* building in v1

- HTTP loop scenarios (defer to a `v1.1` `http` profile, same fixtures).
- MCP framing overhead (same Service underneath).
- FTS ranking quality (at most one fixed `search` latency sample inside
  `mixed-read-write`, not a dedicated scenario).
- Hook subprocess storms.

## 6. Sequencing

1. **Extract portable JSONL** → `internal/portable` (prerequisite; no behavior
   change). Verify existing tests.
2. **Harness + fixtures**: `internal/bench/suite.go`, `fixture.go`,
   `Scenario` interface, JSON reporter, machine labels, `cards bench` command
   stub. One trivial placeholder scenario to prove the loop.
3. **Scenarios 1–4 + 7** (claim, hot-patch, mixed, comments, portable) — highest
   multi-agent signal.
4. **Scenarios 5, 6, 8** (artifacts, events, list-ceiling).
5. **Freeze** `suite_version: 1`; add a short `docs/` note on how to read the
   ratios (what regressed when `conflicts/commit` climbs, etc.).

Each step is independently mergeable and lands behind the same `cards bench`
entry point.

## 7. Open decisions (to lock before step 2)

1. **Primary surface.** Recommended: **Service-first v1**, HTTP profile later.
   Confirm.
2. **`cards bench` as first-class command** vs test-tag-only. Recommended:
   **both** — command for humans/agents, tag for CI. Confirm.
3. **"Large attachment" sizes.** Recommended: fixed **1 / 8 / 32 MiB** (matches
   the documented upload cap area); no 64 MiB in v1. Confirm.
4. **Event drops as failures?** Recommended: **no** (contract allows drop);
   **yes** report and soft-band drop_rate vs baseline. Confirm.
5. **L size class scope.** Recommended: only `portable-roundtrip` +
   `list-ceiling`, nightly tag, never PR CI. Confirm.

Once these five are locked, step 1 (portable extraction) is the first real PR.
