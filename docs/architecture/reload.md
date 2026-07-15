# Reload contract — workspace definition swaps

Normative contract for `POST /v1/workspace/reload`, `POST /v1/boards`
(create-board write-then-reload), and `cards serve --watch` (definitions
poller). Implementation: `cmd/cards/reload.go`, `cmd/cards/watch.go`.
Supervisor coupling fixed in P3a (`card_524c5758`); watcher + failure
surface in P3b (`card_ec61b093`).

This note is the shared seam Phase 5's service supervisor will inherit.
Open questions are closed here on purpose.

## What survives a reload

| Resource | Across reload |
|---|---|
| SQLite store | Shared — card state untouched |
| Event bus | Shared — live SSE subscribers stay connected |
| `*core.Service` + HTTP router | Rebuilt per successful load; prior generation `Close()`d |
| Hook supervisor process | Lives outside the swap (same goroutine) |
| Hook *declarations* | **Frozen** at supervisor construction — reload does not re-read hook/run decls (follow-up) |
| Service *declarations* | **Reconciled** after each successful swap (P5c) — see decision table below |

Semantically, reloading never mutates cards. It only swaps in-memory config.

## Mutex serialization

`reloadableApp.mu` serializes:

1. `reload()` / `reloadLocked()` (loader + generation swap)
2. `handleCreateBoard` (file write + validate-via-reload + rollback)

The `--watch` poller **never** takes `mu` in its scan loop. It calls
`reload()`, which acquires `mu` only for the duration of one load+swap.
Self-write suppression uses a separate `selfWriteGate` mutex so the poll
loop cannot block the HTTP write path (and vice versa).

## Supervisor ↔ generation provenance

The hook supervisor must evaluate conditions against the **current**
generation. Pass `reloadableApp.currentService` (an accessor), not the
`*core.Service` pointer captured at serve start. `reloadLocked` closes each
prior generation; a captured pointer leaves `GetCard` / board-membership
reading a closed Service after the first reload.

Hook/run declarations remain frozen at supervisor construction (out of scope
for P5c). Service declarations are reconciled after each successful reload.

## Service reconcile-on-reload (P5c)

`config.Result.Extensions` is rebuilt every successful load. The supervisor
lives **outside** the reloadable seam and receives a **snapshot** of the new
extensions list only after the generation swap completes.

### Identity key

| Concept | Definition |
|---|---|
| Identity | Extension `id` — one managed child per id |
| Declaration fingerprint | SHA-256 over `run[]` (command + args), sorted `env`, `cwd`, and `restart_policy`. Same id + different fingerprint ⇒ *same service, changed declaration* |

`autostart` is membership, not fingerprint: `autostart: false` (or non-service /
absent) removes the id from the desired set.

### Decision table

| Diff vs running set | Action |
|---|---|
| **added** (new autostart service id) | start |
| **removed** (id gone, or no longer autostart service) | drain + stop |
| **unchanged** (same id + same fingerprint) | leave alone |
| **declaration-changed** (same id, different fingerprint) | drain + restart |

Routine board-create reload rewrites only `definitions/boards/*.json`. Extension
decls are unchanged ⇒ every running service is **unchanged** ⇒ **zero churn**.

### Concurrency

1. `reload()` / `handleCreateBoard` hold `reloadableApp.mu` only for
   load + generation swap (`reloadLocked`).
2. After `mu` is released, the app hands the extensions snapshot to
   `Supervisor.Reconcile` (via `afterReload`).
3. The supervise loop **never** acquires `reloadableApp.mu`. Reconcile may
   stop/start children; it does not call back into reload.

This avoids HTTP-handler deadlock (create-board / reload holding `mu` while a
supervise path tried to take it).

## Debounce policy (`--watch`)

Dependency-free poller (no fsnotify), modeled on `scripts/dev-server.sh`'s
fingerprint-hash loop:

1. Each tick hashes every regular file under `definitions/` (sorted paths +
   contents → SHA-256).
2. On fingerprint change, start (or reset) a debounce timer measured from the
   **last** change.
3. When the fingerprint has been stable for `debounce` (default 300ms), call
   `reload()` **once**.

Editor bursts (save-all, atomic unlink+create) therefore coalesce to one
reload. Defaults: poll 500ms, debounce 300ms.

Tests drive `scanOnce()` with an injectable clock — no real sleeps.

## Self-write suppression

`handleCreateBoard` writes a board JSON then calls `reloadLocked` itself.
Without suppression, `--watch` would see the same fingerprint change and
reload again.

Contract:

1. `selfWriteGate.begin()` before the write.
2. On success, `end(fingerprint)` records the resulting tree hash.
3. The poller's `scanOnce` calls `take(fp)`: while `begin` is active, or when
   `fp` matches the recorded hash once, the change is **absorbed** (update
   `lastFP`, no `reload()`).

The gate's mutex is not `reloadableApp.mu`.

Rolled-back create-board failures call `reloadLocked` (not `reload`) and do
not emit `definition_reload_failed` — the file is removed and the HTTP
response carries the error.

## Failure surfacing

A load error **never swaps**. Last-good generation keeps serving.

| Path | Caller feedback | Bus |
|---|---|---|
| `POST /v1/workspace/reload` | HTTP 422 structured body | `definition_reload_failed` |
| `--watch` poller | stderr log (additive) | `definition_reload_failed` |
| Successful reload | 200 / log | `definition_reloaded` (per board id) |

`definition_reload_failed` diff shape:

```json
{ "error": "validation_failed", "message": "<loader error>", "hint": "the previous definitions are still being served" }
```

Events fan out with `board_id` set for each currently-served board so
board-scoped SSE clients receive them. The board UI listens for
`definition_reload_failed` (banner with the message) and clears the banner
on the next `definition_reloaded`. Lane HTML is **not** re-fetched on
failure — last-good is already correct.

After a failed watch reload, the poller advances `lastFP` to the broken
tree so it does not spin-retry until the next edit.

## SSE caveat

Board live streams subscribe with `?board_id=` and filter card events by
type membership (`card_type_ids`), not by a first-class board membership
index. Definition reload events set `BoardID` explicitly when published.
Tests that assert failure/success delivery should subscribe on the bus (or
an unfiltered stream), not assume card-type board filters alone.

## Out of scope here

- Re-reading hook/run declarations on reload (hooks stay frozen; P5c covers
  `kind:service` only)
- fsnotify (deliberately not a dependency)
