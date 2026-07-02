# Review findings — `internal/core/` + `internal/sqlite/`

Review scope: domain + events layer (`internal/core/` types/service/store/bus/events/errors/tests,
eventlogtest harness, testdata) and SQLite implementation (`internal/sqlite/` event log,
conformance + eventscope tests). Focus: TODOs, dead code, debug statements, half-finished error
handling, duplicated helpers, and correctness issues in the recent events-seam work.

---

## 1. Half-finished / swallowed error handling

### 1.1 `sqlite.ClaimAtomic` silently drops event-log and FTS failures
- **File:line:** `internal/sqlite/sqlite.go:445-456`
- **Issue:** The atomic take-next path builds `owner_changed` / `status_changed` events and then
  persists them with `_ = execEventInsert(tx, ev)`, and updates the FTS index with
  `_ = upsertFTS(tx, claimed)`. Both errors are discarded.
- **Risk:** A failed event insert means the durable audit log loses a real card mutation that
  already committed; a failed FTS update means the card becomes unsearchable. Because the
  transaction still commits, the caller believes the operation succeeded.
- **Recommendation:** Return / wrap these errors and roll back the transaction so the caller can
  retry.

### 1.2 `sqlite.ClaimAtomic` masks SQL scan errors as "nothing matched"
- **File:line:** `internal/sqlite/sqlite.go:408-411`
- **Issue:** `scanCard(row)` returns `nil, nil, nil` for any error, including transient SQL errors.
- **Risk:** Real database failures become silent "no cards available" responses, hiding outages.
- **Recommendation:** Distinguish `sql.ErrNoRows` from SQL errors and propagate the latter.

### 1.3 Secondary persistence in `core.Service` is treated as best-effort / ignored
- **File:line:** `internal/core/service.go:704, 763, 799, 734-736`
- **Issue:** `AddLink`, `AddComment`, `EditComment`, and `RemoveLink` update the card row and emit
  events transactionally (good), but ignore errors from the denormalized `links` / `comments`
  tables with comments like "non-fatal".
- **Risk:** The graph table and card-level comments arrays become inconsistent with the event log
  and card JSON, breaking link graph queries and comment feeds, with no signal to operators.
- **Recommendation:** Surface these errors; ideally perform link/comment writes inside
  `UpdateCard`’s transaction, or at least log/metric them.

### 1.4 `ResolveCard` ignores link/comment load errors
- **File:line:** `internal/core/service.go:217-218`
- **Issue:** On the short-id unique-resolution path, `ListLinks` / `ListComments` errors are discarded
  with `_`.
- **Risk:** A card can be returned to the caller with stale/missing links or comments and no
  indication of failure.
- **Recommendation:** Propagate the error or wrap it; if partial load is intentional, document it.

### 1.5 `checkUserExists` ignores the user-list error
- **File:line:** `internal/core/service.go:1157-1158`
- **Issue:** `users, _ := s.store.ListUsers(ctx)` discards the error. If the store fails, every
  user reference becomes "unknown_user".
- **Recommendation:** Return `Internal` when `ListUsers` fails.

---

## 2. Concurrency / lifetime issues

### 2.1 `Service.Workspace` mutates shared state without synchronization
- **File:line:** `internal/core/service.go:103-107`
- **Issue:** `s.ws.Users = users` writes the service’s shared `Workspace` struct. Multiple HTTP/CLI
  requests can call `Workspace()` concurrently; there is no mutex around `s.ws`, and
  `WorkspaceSnapshot` returns the same `*Workspace` reference.
- **Risk:** Data race on `Workspace.Users` and non-deterministic reads for concurrent clients.
- **Recommendation:** Copy the workspace into the snapshot instead of mutating the shared object,
  or guard `s.ws` with a mutex.

### 2.2 In-memory event log `Replay` returns a pointer to the loop variable
- **File:line:** `internal/core/eventlogtest/eventlogtest.go:108-112`
- **Issue:** `for _, e := range m.filtered(...) { if err := fn(&e); err != nil { ... } }` passes the
  address of the iteration variable. All callbacks receive aliases to the same stack slot; the
  final value overwrites earlier pointers when the loop moves on.
- **Risk:** Observers or replay consumers that retain event pointers see corrupted data.
- **Recommendation:** Index the slice explicitly: `for i := range out { if err := fn(&out[i]); ... }`.

---

## 3. Event-contract / constructor discipline

### 3.1 `sqlite.ClaimAtomic` builds raw events with `Version: 0`
- **File:line:** `internal/sqlite/sqlite.go:445-450`
- **Issue:** The store builds `&core.Event{CardID: ..., Type: ..., Actor: ..., At: ..., Diff: ...}`
  directly instead of using `core.OwnerChanged` / `core.StatusChanged`. It therefore leaves
  `Version` at 0, while every built-in constructor sets `Version: 1` and the golden-fixture test
  asserts `Version == 1`.
- **Risk:** Event consumers that rely on the contract version see inconsistent events from
  take-next compared with every other mutation path.
- **Recommendation:** Use the constructors and call `Emitter.Emit` / have the service layer emit,
  or at least set `Version: 1`.

---

## 4. Duplicated / dead helpers

### 4.1 `strOrEmpty` is a no-op and duplicated
- **File:line:** `internal/core/service.go:1495` and `internal/sqlite/sqlite.go:922`
- **Issue:** `func strOrEmpty(s string) string { return s }` exists in both packages and is only
  used in `OwnerChanged` (`service.go:368`) and in the raw event literal inside
  `sqlite.ClaimAtomic` (`sqlite.go:447`).
- **Risk:** Noise; signals that an earlier coercion (nil/empty distinction?) was removed and the
  helper was left behind.
- **Recommendation:** Delete the helper and pass the string directly.

### 4.2 `placeholders` duplicated
- **File:line:** `internal/core/service.go:1598-1600` and `internal/sqlite/sqlite.go:924-926`
- **Issue:** Identical `placeholders(n)` helper in both packages.
- **Risk:** Divergence if one copy changes; violates DRY.
- **Recommendation:** Move to a shared internal helper package or export from `core`.

### 4.3 `[string] → []any` conversion duplicated
- **File:line:** `internal/core/service.go:1583-1596` (`toAnySlice`) and
  `internal/sqlite/sqlite.go:931-936` (`toAny`).
- **Issue:** Same logic in two places.
- **Recommendation:** Share a single helper.

### 4.4 `bus.go` keeps an artificial `context` import
- **File:line:** `internal/bus.go:144` — `var _ = context.Background`
- **Issue:** `context` is imported only to satisfy the compiler; the comment says it is reserved for
  future request-scoped subscriptions. This is dead code today.
- **Recommendation:** Remove the import; re-add when the feature arrives.

---

## 5. Migration / schema redundancies

### 5.1 `migrateEventsScope` recreates indexes that `Init` already created
- **File:line:** `internal/sqlite/sqlite.go:185-186` (Init) and `sqlite.go:199-200` (migration)
- **Issue:** Init creates `idx_events_card` and `idx_events_at`. The migration then drops/recreates
  them (and adds `idx_events_scope` again). This is idempotent but redundant.
- **Risk:** Migration steps do extra work; future index changes must be made in two places.
- **Recommendation:** Let the migration focus on schema/scope changes and create only the
  scope-specific index there; let `Init` own the card/at indexes.

---

## 6. Minor correctness / smell items

### 6.1 `sqlite.CommentCounts` may miss iteration errors
- **File:line:** `internal/sqlite/sqlite.go:726-736`
- **Issue:** The function does not return `rows.Err()` after the `for rows.Next()` loop, unlike
  `ListComments` (`sqlite.go:691`) and `AllLinks` (`sqlite.go:782`) which do.
- **Risk:** A partial read due to a SQLite error goes undetected.
- **Recommendation:** `return out, rows.Err()`.

### 6.2 `checkColumn` accepts a `Board` argument but ignores it
- **File:line:** `internal/core/service.go:1151-1157`
- **Issue:** Signature `checkColumn(status string, ct *CardType, _ *Board)`; the board is never
  used.
- **Risk:** Misleading API; future column checks scoped to a board cannot use the parameter.
- **Recommendation:** Either use the board to enforce board-level column sets or remove the
  parameter.

---

## Summary table

| # | Severity | Area | Finding | Evidence |
|---|----------|------|---------|----------|
| 1.1 | **high** | sqlite | Event + FTS errors ignored in `ClaimAtomic` | `sqlite.go:452-456` |
| 1.2 | **medium** | sqlite | Scan errors silently treated as "no match" | `sqlite.go:410-411` |
| 1.3 | **medium** | core | Denormalized link/comment writes ignored | `service.go:704,763,799,734-736` |
| 1.4 | **low** | core | ResolveCard drops link/comment load errors | `service.go:217-218` |
| 1.5 | **medium** | core | `checkUserExists` ignores store error | `service.go:1157-1158` |
| 2.1 | **medium** | core | Race on `s.ws.Users` in `Workspace()` | `service.go:103-107` |
| 2.2 | **medium** | core-test | Pointer to loop variable in `Mem.Replay` | `eventlogtest.go:108-112` |
| 3.1 | **medium** | sqlite | Raw events in `ClaimAtomic` lack `Version=1` | `sqlite.go:445-450` |
| 4.1 | **low** | core/sqlite | Duplicated no-op `strOrEmpty` | `service.go:1495`, `sqlite.go:922` |
| 4.2 | **low** | core/sqlite | Duplicated `placeholders` | `service.go:1598`, `sqlite.go:924` |
| 4.3 | **low** | core/sqlite | Duplicated `[]string → []any` helpers | `service.go:1583`, `sqlite.go:931` |
| 4.4 | **low** | core | Dead `context` import in bus.go | `bus.go:144` |
| 5.1 | **low** | sqlite | Redundant index recreation in migration | `sqlite.go:185-186`, `199-200` |
| 6.1 | **low** | sqlite | `CommentCounts` missing `rows.Err()` | `sqlite.go:726-736` |
| 6.2 | **low** | core | `checkColumn` ignores Board parameter | `service.go:1151-1157` |

---

```acceptance-report
{
  "criteriaSatisfied": [
    {
      "id": "criterion-1",
      "status": "satisfied",
      "evidence": "Reviewed the requested files in internal/core/ and internal/sqlite/ and produced a structured findings document at /home/user/src/cards/issues-core.md; no unrelated code was changed."
    }
  ],
  "changedFiles": [],
  "testsAddedOrUpdated": [],
  "commandsRun": [
    {
      "command": "go test ./internal/core/... ./internal/sqlite/...",
      "result": "passed",
      "summary": "All tests in the review area pass; review produced no code changes."
    },
    {
      "command": "git status --short",
      "result": "passed",
      "summary": "Working tree has no staged changes; only pre-existing untracked inventory.md."
    }
  ],
  "validationOutput": [
    "No compilation errors or test failures in the reviewed packages.",
    "Findings documented with precise file:line references."
  ],
  "residualRisks": [
    "Findings are code-quality/correctness observations; no automated safety net enforces that these issues will be fixed.",
    "A few risks (e.g. ignored event inserts, race on Workspace.Users) are latent and may not reproduce under current test load."
  ],
  "noStagedFiles": true,
  "diffSummary": "No source changes; only added /home/user/src/cards/issues-core.md with review findings.",
  "reviewFindings": [
    "blocker (treat-next audit): sqlite.go:445-456 - ClaimAtomic ignores execEventInsert and upsertFTS errors while still committing",
    "medium: sqlite.go:408-411 - ClaimAtomic masks scanCard errors as nil/nil/nil",
    "medium: service.go:704,763,799,734-736 - link/comment/FTS secondary writes ignored after card commit",
    "medium: service.go:103-107 - unsynchronized mutation of s.ws.Users in Workspace()",
    "medium: eventlogtest/eventlogtest.go:108-112 - Replay passes pointer to for-range loop variable",
    "medium: sqlite.go:445-450 - raw core.Event{} built with Version=0 in ClaimAtomic",
    "low: service.go:1495 & sqlite.go:922 - duplicated/identity strOrEmpty helper",
    "low: service.go:1598 & sqlite.go:924 - duplicated placeholders helper",
    "low: bus.go:144 - dead context import via var _",
    "low: sqlite.go:726-736 - CommentCounts omits rows.Err()",
    "low: service.go:1151-1157 - checkColumn accepts Board but ignores it"
  ],
  "manualNotes": "The inventory.md produced by an earlier scan already flagged the dead context import in bus.go and the artifacts TODO; this review adds core/sqlite-specific correctness findings. Tests pass, so none of the issues above currently fail existing assertions."
}
```
