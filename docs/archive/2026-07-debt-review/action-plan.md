# Technical Debt — Action Plan
Source: debt-ledger.md (57 items). No code was modified to produce this plan.

## Executive summary
One `high`-severity item (DEBT-01, ClaimAtomic swallowing errors). Everything else is
medium/low/cleanup/doc-drift. Highest leverage moves: fix ClaimAtomic's transaction
integrity, fix the CI Go-version mismatch (risks breaking all builds), then work down
through error-handling, concurrency, CLI correctness, and doc-drift batches. Doc-only
batches carry zero code risk and can run in parallel with anything.

## Priority × Effort matrix

| Batch | Priority | Effort | DEBT-IDs |
|---|---|---|---|
| 1. ClaimAtomic hardening | **High** | M | 01, 02, 30, 17 |
| 2. CI / config hygiene | **High** | S | 54, 55, 56 |
| 3. Secondary error-handling sweep | **High/Medium** | L | 03, 04, 05, 06, 07, 08, 09, 10, 11 |
| 4. Shared `internal/dbutil` package | Medium | S | 17(cont.), 18, 19 |
| 5. Concurrency / lifetime fixes | Medium | M | 27, 28, 29 |
| 6. CLI correctness fixes | Medium | M | 33, 34, 35, 36, 37 |
| 7. YAML doc correction | Medium | S | 40, 41, 53 |
| 8. Hooks/extensions doc reconciliation | Medium | S | 45, 46 |
| 9. Artifacts policy (TODO) | Medium | L | 57 |
| 10. Duplication cluster (CLI/workspace) | Low/Medium | M | 21, 23, 24, 25, 26 |
| 11. Event contract leftovers | Low | S | 31, 32 |
| 12. Dead-import / dead-code sweep | Low | S | 12, 13, 14, 15, 16, 20, 22 |
| 13. Logging/output consistency | Low | S | 38, 39 |
| 14. Event catalog refresh | Low | S | 42, 43, 44 |
| 15. Misc doc drift | Low | S | 47, 48, 49, 50, 51, 52 |

Recommended sequencing: **2 → 1 → 4 → 3 → 5 → 6 → 9 → (7, 8, 14, 15, 10, 11, 12, 13 in any order/parallel)**.
Batch 2 goes first because a broken CI matrix blocks verifying every other batch.

---

## Batch 1 — ClaimAtomic hardening cluster
**Priority:** High **Effort:** M **IDs:** DEBT-01, DEBT-02, DEBT-30, DEBT-17
**Files:** `internal/sqlite/sqlite.go:408-456`

**Decision:** ClaimAtomic must fail the claim (roll back) if the event-log insert or FTS
upsert fails — a durable audit row and searchability cannot be optional side effects of a
committed mutation. Also: stop building a raw `&core.Event{...}` literal; use
`core.OwnerChanged`/`core.StatusChanged` constructors so `Version` is set to 1 like every
other mutation path. `scanCard` must distinguish `sql.ErrNoRows` (legitimately "no card
available") from real DB errors.

**Execution steps:**
1. In `scanCard` (`sqlite.go:408-411`), return the underlying error unless it is
   `sql.ErrNoRows`; only the latter should become `(nil, nil, nil)`.
2. In `ClaimAtomic` (`sqlite.go:445-456`), replace `_ = execEventInsert(...)` and
   `_ = upsertFTS(...)` with real error propagation; on error, return before commit (or
   roll back) so the caller gets a definitive failure instead of a silently degraded
   success.
3. Replace the raw `&core.Event{...}` literal with `core.OwnerChanged(...)` /
   `core.StatusChanged(...)` as appropriate, which removes the need for `strOrEmpty` at
   this call site (DEBT-17) and fixes `Version: 0` (DEBT-30).
4. Add/extend a test that asserts: (a) a forced FTS-upsert failure prevents commit, (b)
   the emitted event has `Version: 1`, (c) `scanCard` returns a non-nil error for a
   non-`ErrNoRows` scan failure.
5. Update the golden-fixture test referenced in the ledger to confirm it still passes.

---

## Batch 2 — CI / config hygiene
**Priority:** High **Effort:** S **IDs:** DEBT-54, DEBT-55, DEBT-56
**Files:** `.github/workflows/ci.yml:19`, `.golangci.yml:21,33`

**Decision:** Fix now, independent of everything else — a broken CI matrix or an inert
lint exclusion silently erodes confidence in every other change landing this quarter.

**Execution steps:**
1. `.github/workflows/ci.yml:19` — drop Go `1.25` from the matrix (incompatible with
   `go.mod`'s `go 1.26.4` directive); test on `1.26.4` and optionally `1.27`.
2. `.golangci.yml:21` — update the stale module path in `errcheck.exclude-functions`
   from `github.com/foz/work-cards/...` to `github.com/somebox/cards/...` so the
   `Store.Close` exclusion actually takes effect.
3. `.golangci.yml:33` — pin `golangci-lint` to a specific version instead of `latest`
   to stop unrelated PRs from failing due to new lint rules landing upstream.
4. Re-run CI once to confirm the matrix builds and lint passes with the corrected
   exclusion.

---

## Batch 3 — Secondary error-handling sweep
**Priority:** High/Medium **Effort:** L **IDs:** DEBT-03..11
**Files:** `internal/core/service.go`, `internal/httpapi/httpapi.go`, `cmd/cards/portable.go`, `internal/sqlite/sqlite.go`

**Decision:** For each site, make an explicit per-site call: **propagate the error**
(preferred default for anything that affects data integrity or backup completeness) or
**keep silent but document why** (only for genuinely non-critical reads where a stale UI
render is acceptable). No more bare `_ =` with no comment.

**Execution steps:**
1. `service.go:704,734-736,763,799` (DEBT-03) — `AddLink`/`AddComment`/`EditComment`/
   `RemoveLink`: propagate denormalized-table errors instead of "non-fatal" comments, or
   log at `error` level with the mutation ID so drift is detectable.
2. `service.go:217-218` (DEBT-04) — `ResolveCard`: propagate `ListLinks`/`ListComments`
   errors on the short-id path.
3. `service.go:1157-1158` (DEBT-05) — `checkUserExists`: return `Internal` on
   `ListUsers` store failure instead of coercing to `unknown_user`.
4. `httpapi.go:647` (DEBT-06) and `:801` (DEBT-07) — SSE replay and idempotency-write
   failures: log at minimum; consider surfacing a recoverable SSE comment / retry signal.
5. `httpapi.go:836,851,870,921,933,988,1053,1279,1454,1486,1498,1527` (DEBT-08) — UI
   helpers: choose fail-the-request vs. documented-safe-drop per call site; add a one-line
   comment at each surviving silent drop.
6. `httpapi.go` `writeSSEEvent` (DEBT-09) — replace bare `_, _ := json.Marshal(m)` with an
   explicit comment or a panic-on-failure guard, since the type set is closed.
7. `cmd/cards/portable.go:61-62` (DEBT-10) — `exportJSONL`: log skipped cards (with card
   ID) instead of silently `continue`-ing, or fail the export outright.
8. `sqlite.go:726-736` (DEBT-11) — `CommentCounts`: check `rows.Err()` after the loop,
   matching `ListComments`/`AllLinks`.
9. Add/extend tests that force each store-layer failure and assert the new behavior
   (error returned, or a log line emitted) rather than silent success.

---

## Batch 4 — Shared `internal/dbutil` package
**Priority:** Medium **Effort:** S **IDs:** DEBT-17 (remainder), DEBT-18, DEBT-19

**Decision:** Create one small internal package to hold the three duplicated helpers
currently living in both `internal/core` and `internal/sqlite`. Do this *after* Batch 1,
since Batch 1 already removes the `strOrEmpty` call at the ClaimAtomic site.

**Execution steps:**
1. Create `internal/dbutil` (or similar) with `Placeholders(n int) string` and
   `ToAnySlice(ss []string) []any`.
2. Delete `service.go:1598-1600` / `sqlite.go:924-926` (`placeholders`) and
   `service.go:1583-1596` / `sqlite.go:931-936` (`toAny`/`toAnySlice`); replace call
   sites with the shared package.
3. Delete the now-fully-unused `strOrEmpty` (`service.go:1495`, `sqlite.go:922`) and its
   remaining call site in `OwnerChanged` (`service.go:368`) — pass the string directly.
4. Run existing unit tests for both packages to confirm behavior parity.

---

## Batch 5 — Concurrency / lifetime fixes
**Priority:** Medium **Effort:** M **IDs:** DEBT-27, DEBT-28, DEBT-29

**Decision:** Fix the two real bugs (race on `Workspace.Users`; aliased loop-variable
pointer in test helper) and the one lifecycle looseness (orphaned supervisor goroutine).

**Execution steps:**
1. `internal/core/service.go:103-107` (DEBT-27) — guard `s.ws` with a mutex, or have
   `WorkspaceSnapshot` return a deep copy instead of the live `*Workspace` pointer.
2. `internal/core/eventlogtest/eventlogtest.go:108-112` (DEBT-28) — change
   `for _, e := range out { fn(&e) }` to `for i := range out { fn(&out[i]) }` so callbacks
   don't alias one stack slot.
3. `cmd/cards/serve.go:92-99` (DEBT-29) — tie the hook-supervisor goroutine's lifecycle to
   `httpSrv` shutdown via `RegisterOnShutdown` or a shared `context.Context`, so it doesn't
   outlive an immediately-failed `ListenAndServe`.
4. Add a regression test for DEBT-28 (assert distinct addresses/values are observed across
   replayed callbacks).

---

## Batch 6 — CLI correctness fixes
**Priority:** Medium **Effort:** M **IDs:** DEBT-33, DEBT-34, DEBT-35, DEBT-36, DEBT-37

**Decision:** These are user-visible bugs, not stylistic debt — fix as a batch since they
all live in `cmd/cards/*` and `internal/cli/*`.

**Execution steps:**
1. `internal/cli/client.go:169,224` + `commands.go:282` (DEBT-33) — implement dotted-path
   lookup in `idOf`, or have `cmdTakeNext` extract/print the id itself so `--quiet
   take-next` works.
2. `commands.go:172` vs `commands.go:125` (DEBT-34) — mirror `cmdCreate`'s
   `len(*tags) > 0` guard in `cmdPatch` so unset `--tag` doesn't send `"tags": null`.
3. `commands.go:514-532` (DEBT-35) — either add a per-board endpoint (`/v1/boards/{id}`)
   or make `boards show` print an explicit warning that it's returning the whole
   workspace. (Note: cross-ref DEBT-43 — board-scoped concepts already exist in events.)
4. `cmd/cards/main.go:25-31` (DEBT-36) — parse global flags in a pre-pass over the whole
   argument slice instead of only the front, so `cards list --json` works positionally.
5. `cmd/cards/main.go:121-122` (DEBT-37) — hoist `cli.Commands()` out of the lookup loop
   into a single `cmds := cli.Commands()` before iterating.
6. Add CLI integration tests for `--quiet take-next`, `patch` without `--tag`, and
   `list --json` in non-leading position.

---

## Batch 7 — YAML doc correction
**Priority:** Medium **Effort:** S **IDs:** DEBT-40, DEBT-41, DEBT-53

**Decision (needs a product call):** Either (a) correct the docs to say JSON-only for
workspace/card-type/board definitions and drop the `gopkg.in/yaml.v3` recommendation, or
(b) commit to implementing YAML support + reload-on-change and add the dependency. Given
no current code path reads YAML for these files, **(a) — correct the docs — is the
lower-cost default** unless YAML support is already roadmapped.

**Execution steps:**
1. `docs/SPEC.md` §3, `docs/ARCHITECTURE.md`, `docs/DEVELOPER-REFERENCE.md` §2 — state
   JSON-only for `workspace.json`/`card-types/*.json`/`boards/*.json`; keep YAML support
   scoped to `definitions/extensions.{yaml,json}` only.
2. `internal/config/config.go` package comment — remove "reload-on-change is handled in
   serve mode" (not implemented) and reconcile with the `Loader` comment (DEBT-53).
3. `docs/ARCHITECTURE.md` — drop the `gopkg.in/yaml.v3` recommendation (DEBT-41) unless
   decision (b) above is taken, in which case add it to `go.mod` instead.

---

## Batch 8 — Hooks/extensions doc reconciliation
**Priority:** Medium **Effort:** S **IDs:** DEBT-45, DEBT-46

**Decision (needs a product call):** `EXTENSIONS.md` and `INTEGRATOR-REFERENCE.md`
directly contradict each other on whether `service`-kind extensions are supervised.
Ground truth per the ledger: `runExtensionsCmd` only wires `kind: hook`. Decide whether to
(a) implement `service` extension supervision, or (b) fix `EXTENSIONS.md` to match current
behavior. Also decide whether hook payloads should gain a `board_ids` field or whether the
doc's example should drop it.

**Execution steps:**
1. Pick (a) or (b) for service-extension supervision; if (b), edit `EXTENSIONS.md`
   "Declaration"/"CLI surface" to match `INTEGRATOR-REFERENCE.md` §7.
2. Decide on `board_ids` in hook payloads; if not adding it, remove it from the
   `EXTENSIONS.md` "Event contract for hooks" example (`hooks.go:133-141` is the source of
   truth for actual fields: `id, type, card_id, actor, at, diff, workspace_id`).

---

## Batch 9 — Artifacts policy (TODO)
**Priority:** Medium **Effort:** L **IDs:** DEBT-57
**Files:** `internal/artifacts/artifacts.go:11-12`

**Decision:** This is the only literal `TODO` in the repo and the only debt item with a
concrete security-exposure risk (untrusted path handling) once artifact handling gains
callers. Treat as "must implement before exposing artifact endpoints," not urgent today
since `Manager` has no current callers.

**Execution steps:**
1. Implement content-addressed subdirectory storage.
2. Implement SHA-256 hashing of stored content.
3. Implement MIME sniffing on ingest.
4. Implement path-confinement validation (reject `..`/absolute paths/symlink escapes).
5. Add tests specifically targeting path-traversal attempts before any endpoint calls
   into this package.

---

## Batch 10 — Duplication cluster (CLI / workspace setup)
**Priority:** Low/Medium **Effort:** M **IDs:** DEBT-21, DEBT-23, DEBT-24, DEBT-25, DEBT-26

**Decision:** Consolidate repeated setup/parsing logic; defer the `fieldSchema`
consolidation (DEBT-21) since `mcp`/`openapi` signatures genuinely differ — flag it as a
future package decision rather than an in-batch fix.

**Execution steps:**
1. `cmd/cards/serve.go` / `extensions.go` / `import.go` / `export.go` / `directcli.go`
   (DEBT-26) — introduce `openWorkspace(dir) (*sqlite.Store, *core.Service,
   *config.Result, error)` and call it from each command instead of repeating the
   workspace-load + open + service-build sequence.
2. `cmd/cards/extensions.go:57-70` (DEBT-23) — call the existing `countHooks` from
   `serve.go` instead of reimplementing the loop.
3. `cmd/cards/extensions.go:23-26` (DEBT-24) — replace the bespoke `stringSlice` with
   `internal/cli.FlagSet.StringArr` (or promote a shared `StringSlice` into `internal/cli`).
4. `cmd/cards/main.go:66-106` (DEBT-25) — evaluate collapsing `peelGlobals` et al. into a
   single-pass `cli.FlagSet` parse; at minimum align naming with `cli.FlagSet` (cross-ref
   DEBT-36, fixed in Batch 6 — do this after Batch 6 so the flag-parsing fix isn't
   double-touched).
5. `internal/httpapi/httpapi.go:1865` (DEBT-22 — cross-listed) — inline
   `containsBoard` into its two call sites and delete it.
6. Log DEBT-21 (`fieldSchema` duplication) as a backlog note only — no action this batch.

---

## Batch 11 — Event contract leftovers
**Priority:** Low **Effort:** S **IDs:** DEBT-31, DEBT-32

**Execution steps:**
1. `service.go:1151-1157` — `checkColumn(status, ct, _ *Board)`: either use the `Board`
   parameter to enforce board-level column sets, or drop it from the signature.
2. `sqlite.go:185-186` vs `199-200` — let `Init` own the `idx_events_card`/`idx_events_at`
   indexes; have `migrateEventsScope` only add `idx_events_scope`, removing the
   drop/recreate redundancy.

---

## Batch 12 — Dead-import / dead-code sweep
**Priority:** Low **Effort:** S **IDs:** DEBT-12, DEBT-13, DEBT-14, DEBT-15, DEBT-16, DEBT-20, DEBT-22

**Decision:** Pure cleanup, zero behavior change. Safe to batch entirely and run through
a `deadcode`/`unused` linter pass to catch anything else of the same shape.

**Execution steps:**
1. `internal/mcp/mcp.go:466-467` — remove dead `"log"` import + `var _ = log.Print`.
2. `internal/hooks/hooks.go:215-216` — remove dead `"io"` import + `var _ = io.EOF`; fix
   the stale comment that references `strings` instead of `io.EOF`.
3. `internal/core/bus.go:144` — remove `var _ = context.Background` (re-add the import
   only when request-scoped subscriptions actually land).
4. `internal/httpapi/httpapi.go:201-203` — remove unused `actorKey` type + `var _`.
5. `internal/seed/seed.go:36,75` — remove the discarded `now` variable, or wire it into a
   `created_at`/notes field if that was the intent.
6. `internal/hooks/hooks_test.go:191-198` — delete `contains`/`indexOf`; use
   `strings.Contains`/`strings.Index`.
7. Run a `deadcode`/`unused` linter pass over the whole repo to confirm no other instances
   of this pattern remain.

---

## Batch 13 — Logging/output consistency
**Priority:** Low **Effort:** S **IDs:** DEBT-38, DEBT-39

**Decision:** Standardize: operational messages → `log.Printf` (stderr); user-facing
output → `fmt.Print*`. Document the convention once, applied consistently.

**Execution steps:**
1. `cmd/cards/serve.go:44-46,56,89-101` vs `cmd/cards/extensions.go:146,168,175` — align
   `runExtensionsCmd` and `extensionsCmd` on the same convention per the rule above.
2. Document the convention in a short code comment or `CONTRIBUTING` note.
3. Optional: add a `--quiet` flag to `init` (DEBT-39) for symmetry with `import`/`export`.

---

## Batch 14 — Event catalog refresh
**Priority:** Low **Effort:** S **IDs:** DEBT-42, DEBT-43, DEBT-44

**Decision:** Single editing pass; no code changes, only bringing `EVENTS.md` and
`INTEGRATOR-REFERENCE.md` in line with `internal/core/types.go` and existing tests.

**Execution steps:**
1. `docs/EVENTS.md` §10/§12 — remove claims that `events_test.go` and test fakes don't
   exist; they do (`TestEventContracts_GoldenFixtures`, `TestNoRawEventLiterals`,
   `internal/core/eventlogtest`, `eventlog_conformance_test.go`).
2. `docs/INTEGRATOR-REFERENCE.md` §4 — update "every event is card-scoped" to acknowledge
   `Scope`/`BoardID` on `Event` and the existing `wip_exceeded`/`wip_cleared` board-scoped
   events (`wip_test.go`).
3. Same section — update the "15 declared, 13 emitted" count to reflect the current 17
   constants in `internal/core/types.go:209-230`.

---

## Batch 15 — Misc doc drift
**Priority:** Low **Effort:** S **IDs:** DEBT-47, DEBT-48, DEBT-49, DEBT-50, DEBT-51, DEBT-52

**Execution steps:**
1. `docs/LIFECYCLE-EXAMPLES.md` B6 (DEBT-47) — remove/flag the
   `updated_before`/`created_before` filter example since neither the HTTP list handler
   nor the CLI implements it (SPEC.md §9 already says so).
2. `docs/SLICE3-REFLECTION.md` (DEBT-48) — correct the `card_ready` reference to
   `card_unblocked`.
3. `docs/NOTES.md` D5 (DEBT-49) — remove the stale follow-up; `SPEC.md`/
   `DEVELOPER-REFERENCE.md` already correctly state `If-Match` is unimplemented.
4. `docs/CONCEPTS.md` (DEBT-50) — change zero-config launch from "Planned" to
   "Implemented" (it's live in `cmd/cards/main.go`).
5. `docs/DEVELOPER-REFERENCE.md` §9 (DEBT-51) — either add the missing
   `examples/demo-workspace/filters/todo.json` or note the example is illustrative only.
6. `examples/demo-workspace/.cards/ext/notify.sh:5` (DEBT-52) — fix the comment to read
   "See EXTENSIONS.md".

---

## Batches explicitly requiring a product/human decision before execution
- **Batch 7 (YAML docs):** correct docs vs. implement YAML support + reload — pick one.
- **Batch 8 (hooks/extensions):** implement `service` extension supervision vs. fix docs;
  add `board_ids` to hook payloads vs. fix the example.
- **Batch 6, item 3 (DEBT-35):** add a per-board API endpoint vs. warn-only UX fix.

## Cost/scale note
This plan spans 15 batches touching ~20 files plus ~15 doc files. If executed as
individual high-tier-reviewed changes, that is too many high-tier review cycles for the
value delivered — **recommend util-tier execution for all doc-only batches (7, 8's doc
half, 11, 13, 14, 15) and the mechanical cleanup batches (2, 4, 12)**, reserving high-tier
review for the behavior-changing batches with test risk: **1 (ClaimAtomic), 3
(error-handling sweep), 5 (concurrency), 6 (CLI correctness), 9 (artifacts policy)** — 5
high-tier review passes, not 15.
