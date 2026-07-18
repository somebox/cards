# Technical-Debt Ledger

**Registry date:** 2026-07-18
**Scope:** `/Users/foz/src/cards` working tree at `HEAD` (branch `main`, 1 commit
ahead of `origin/main` — TUI landing `0421efd`).
**Sources:**
- `inventory.md` (2026-07-18 scan)
- `issues-small.md` (2026-07-18, Area C re-review)
- `docs/archive/2026-07-debt-review/issues-core.md` (2026-07-02)
- `docs/archive/2026-07-debt-review/issues-http-cli.md` (2026-07-02)
- `docs/archive/2026-07-debt-review/issues-small.md` (2026-07-02)
- `docs/archive/2026-07-debt-review/issues-docs.md` (2026-07-02)

**Supersedes:** `docs/archive/2026-07-debt-review/debt-ledger.md` (2026-07-02,
partially reconciled at 2026-07-05 for Sprint A scope). That file remains the
historical record; this file is the live register.

**Status legend:**
- `fixed` — resolved at `HEAD`; verified by `inventory.md` or a follow-up
  `issues-*.md`.
- `pinned` — `fixed` and additionally guarded by a regression test that
  carries the `DEBT-NN` marker (see `inventory.md` §1.2).
- `open` — finding stands at `HEAD`; needs work.
- `unverified` — recorded in 2026-07-02 but not re-checked since; the
  `file:line` anchor may have drifted. Treat as "needs HEAD check before
  trusting the anchor".
- `new` — first recorded in the 2026-07-18 pass.

**ID scheme:** existing `DEBT-01..DEBT-57` are preserved (they are referenced
by `*_test.go` regression guards — see `inventory.md` §1.2). New entries
continue from `DEBT-58`.

---

## 1. Reconciliation summary

| Theme | Range | Open | Fixed / pinned | Unverified | New |
|---|---|---|---|---|---|
| 1 — Error handling (swallowed `_ = …`) | DEBT-01..11 | — | DEBT-01, 02 (pinned), 12, 13, 14, 15, 16 | DEBT-03..11 (rest) | — |
| 2 — Duplication & dead code | DEBT-12..26 | DEBT-21, 22, 23, 24, 25, 26 | DEBT-12, 13, 14, 15, 16, 18, 19, 20 | DEBT-17 (if any) | DEBT-58, 59, 60 |
| 3 — Concurrency / lifetime | DEBT-27, 28 | — | — | DEBT-27, 28 | — |
| 4 — Event contract | DEBT-29..32 | — | DEBT-29, 30 | DEBT-31, 32 | — |
| 5 — CLI behaviour | DEBT-33..37 | — | DEBT-33, 34, 35, 36, 37 (pinned) | — | — |
| 6 — Logging consistency | DEBT-38, 39 | — | — | DEBT-38, 39 | — |
| 7 — Docs drift | DEBT-40..53 | — | — | DEBT-40..53 (all 14) | — |
| 8 — CI / config | DEBT-54..56 | — | DEBT-54, 55 | DEBT-56 | — |
| 9 — Artifacts policy | DEBT-57 | — | DEBT-57 (pinned) | — | — |
| 10 — TUI surface (no prior coverage) | — | — | — | — | DEBT-61 |

**Headline counts:** 57 prior items + 4 new entries = **61 total**.
- `fixed` / `pinned`: **22**
- `unverified`: **39** (the bulk are docs-drift, DEBT-40..53)
- `open`: **6** (DEBT-21, 22, 23, 24, 25, 26)
- `new`: **4** (DEBT-58, 59, 60, 61)

---

## 2. Cross-cutting patterns

The 61 entries collapse to **six recurring patterns**. Each pattern lists the
entries that exhibit it; reviewers should fix the pattern, not just the
instance, where feasible.

### Pattern P1 — Swallowed errors (`_ = …` / `x, _ := …`)

The single largest class. A store or service call returns an `error` that the
caller discards while still committing the surrounding transaction or
returning a 2xx to the client.

- DEBT-01 (pinned) — `internal/sqlite/sqlite.go` `ClaimAtomic` event + FTS
  inserts ignored while the txn commits. Pinned by
  `internal/sqlite/claimatomic_test.go:57`.
- DEBT-02 (pinned) — `scanCard` masks SQL errors as "no match". Pinned by
  `internal/sqlite/claimatomic_test.go:84`.
- DEBT-03..11 — the remaining 9 drop sites recorded in 2026-07-02. Per
  `issues-core.md` §1.3–1.5 and `issues-http-cli.md` §2, the concrete
  instances are:
  - `internal/core/service.go:704, 763, 799, 734-736` — `AddLink`,
    `AddComment`, `EditComment`, `RemoveLink` ignore denormalized
    `links`/`comments` table writes ("non-fatal" comment).
  - `internal/core/service.go:217-218` — `ResolveCard` drops
    `ListLinks`/`ListComments` errors on short-id resolution.
  - `internal/core/service.go:1157-1158` — `checkUserExists` ignores
    `ListUsers` error; every user becomes "unknown_user" on store failure.
  - `internal/httpapi/api.go:647` — `apiEventStream` discards the
    replay-query error (`evs, _ := …`); SSE reconnect silently misses events.
  - `internal/httpapi/api.go:801` — `idempotent` ignores
    `PutIdempotency` error; replay cache write fails silently.
  - `internal/httpapi/render.go` (formerly `httpapi.go:836, 851, 870, 921,
    933, 988, 1053, 1279, 1454, 1486, 1498, 1527`) — multiple UI helpers
    drop `ListCards`/`ListUsers`/`AllLinks`/`CommentCounts` errors. **Anchor
    drift:** `httpapi.go` was split into `api.go` / `sse.go` / `middleware.go`
    / `render.go`; line numbers in `issues-http-cli.md` are stale.
  - `cmd/cards/portable.go:61-62` — `exportJSONL` skips cards whose
    `GetCard` fails; can silently drop data from a backup.

**Pattern fix:** define a project rule — secondary-persistence errors inside a
transaction must roll back; read-path errors must either propagate or be
explicitly annotated with a comment naming the degradation. Re-audit all
`_ =` and `, _ :=` sites against this rule.

### Pattern P2 — Dead / keep-alive imports and identifiers

`var _ = <pkg.Sym>` placeholders left behind after a refactor.

- DEBT-12 (fixed) — `internal/mcp/mcp.go:466-467` dead `"log"` import +
  `var _ = log.Print`.
- DEBT-13 (fixed) — `internal/hooks/hooks.go:215-216` dead `"io"` import +
  `var _ = io.EOF`; stale preceding comment.
- DEBT-14, 15 (fixed) — `var _ = <pkg.Sym>` keep-alives; per `inventory.md`
  §1.1 "all gone from the live code".
- DEBT-22 (open) — `internal/httpapi/httpapi.go:1865` `containsBoard`
  one-line wrapper around `containsStr`; dead-weight API. (Classed here
  because the wrapper exists only to satisfy a call site that no longer
  needs indirection.)
- `internal/httpapi/httpapi.go:201-203` `actorKey` type + `var _ =
  actorKey{}` — keep-alive for an actor-state path that now flows through
  `core.WithActor`. Maps to one of DEBT-03..11 (error-handling theme) but
  the *pattern* is dead-code. **Anchor drift:** `httpapi.go` was split;
  `actorKey` may now live in `middleware.go`.

**Pattern fix:** grep for `var _ =` and `var _ <pkg.Sym>` as a CI lint rule.

### Pattern P3 — Duplicated helpers across packages

The same small helper exists in 2+ packages and is at risk of divergence.

- DEBT-21 (open) — `fieldSchema` in `internal/mcp/mcp.go:522` and
  `internal/openapi/openapi.go:259`. Signatures differ (`mcp` takes
  `required bool` and emits `x-required`); conceptual duplication, not a
  copy-paste bug. **Defer to a product/architecture decision** on whether to
  introduce a shared schema-building package.
- DEBT-23, 24, 25 (open) — CLI flag-parsing duplication: `peelGlobals`,
  `hasPrefix`, `val`, `splitEq` in `cmd/cards/main.go:66-106` overlap with
  `internal/cli.FlagSet` parsing (`--flag=val` splitting).
- DEBT-26 (open) — workspace-load + SQLite-open + service-build sequence
  repeated in `serveCmd`, `mcpCmd`, `runExtensionsCmd`, `importCmd`,
  `exportCmd`, `newDirectBackend` (`cmd/cards/serve.go`,
  `extensions.go`, `import.go`, `export.go`, `directcli.go`). Extract
  `openWorkspace(dir) (*sqlite.Store, *core.Service, *config.Result, error)`.
- DEBT-58 (new) — `splitCSV` duplicated in `internal/mcp/mcp.go:432` and
  `internal/httpapi/api.go:540`; differs only in slice pre-allocation.
- DEBT-59 (new) — `strOrEmpty` (no-op identity) duplicated in
  `internal/core/service.go:1495` and `internal/sqlite/sqlite.go:922`; used
  only by `OwnerChanged` and the raw event literal in `ClaimAtomic`. Delete
  and pass the string directly.
- Also duplicated (recorded but not assigned DEBT-NN):
  - `placeholders(n)` in `internal/core/service.go:1598-1600` and
    `internal/sqlite/sqlite.go:924-926`.
  - `[]string → []any` conversion (`toAnySlice` / `toAny`) in
    `internal/core/service.go:1583-1596` and
    `internal/sqlite/sqlite.go:931-936`.
  - `countHooks` in `cmd/cards/serve.go:105-111` vs the inline count loop
    in `cmd/cards/extensions.go:57-70`.
  - `stringSlice` flag.Value in `cmd/cards/extensions.go:23-26` vs
    `internal/cli.FlagSet.StringArr`.

**Pattern fix:** introduce `internal/internalutil` (or extend
`internal/core`) for `placeholders`, `toAnySlice`, `splitCSV`; introduce
`internal/cli.StringSlice` and have `cmd/cards` consume it.

### Pattern P4 — Inconsistent logging / output channels

Mixed use of `log.Printf` (server, stderr) and `fmt.Println`/`fmt.Fprintf`
(user, stdout/stderr) inside the same command family.

- DEBT-38, 39 (unverified) — logging-consistency theme.
- Concrete instances from `issues-http-cli.md` §4:
  - `cmd/cards/extensions.go:64, 66, 69` uses `log.Printf` while
    `extensions.go:146, 168, 175` uses `fmt.Printf`/`fmt.Println` — same
    command family, two conventions.
  - `cmd/cards/serve.go:44-46, 56, 89-101` operational messages via
    `log.Printf` (stderr); fine for server mode, but the convention is
    undocumented.
- Positive pattern (keep): `cmd/cards/import.go:84` and `export.go:73`
  send summaries to `os.Stderr` so stdout stays valid JSONL. Document as
  the project standard.

**Pattern fix:** write a one-paragraph "output conventions" doc and add a
`lint`-style grep rule that forbids `fmt.Println` in `cmd/cards/*` outside
of explicitly user-facing commands.

### Pattern P5 — Stale documentation / forward-looking statements

Prose claims that have drifted from the code.

- DEBT-40..53 (all unverified) — 14 docs-drift items. Concrete instances
  from `issues-docs.md`:
  - `docs/EVENTS.md` §10 (~line 273) and §12 Step 1 — claim the
    event-test seam and acceptance tests don't exist; both are built
    (`internal/core/events_test.go`,
    `internal/core/eventlog_conformance_test.go`,
    `internal/core/eventlogtest/`).
  - `docs/SPEC.md` §3, `docs/ARCHITECTURE.md`,
    `docs/DEVELOPER-REFERENCE.md` §2, `internal/config/config.go` package
    comment — claim JSON *and* YAML support for workspace/card-types/boards;
    only JSON is loaded (`internal/config/config.go`). Only
    `definitions/extensions.{yaml,json}` is parsed.
  - `docs/EXTENSIONS.md` "Event contract for hooks" — example payload shows
    `board_ids`; actual payload (`internal/hooks/hooks.go:133-141`) emits
    `id`, `type`, `card_id`, `actor`, `at`, `diff`, `workspace_id` — no
    `board_ids`.
  - `docs/EXTENSIONS.md` Declaration/CLI surface — implies `service`
    extensions with `autostart: true` are supervised by `cards
    run-extensions`; only `kind: hook` is wired (`cmd/cards/extensions.go`).
    Contradicts `docs/INTEGRATOR-REFERENCE.md` §7.
  - `docs/INTEGRATOR-REFERENCE.md` §4 — "every event is card-scoped" and
    "15 declared, 13 emitted"; code declares 17 (including `wip_exceeded` /
    `wip_cleared` which are board-scoped).
  - `docs/LIFECYCLE-EXAMPLES.md` B6 — uses `updated_before`/`updated_after`
    query params that the list handler and CLI do not implement.
  - `docs/CONCEPTS.md` — zero-config launch described as "Planned" but
    implemented in `cmd/cards/main.go`.
  - `docs/NOTES.md` D5 — claims `If-Match` references remain in
    `SPEC.md`/`DEVELOPER-REFERENCE.md`; both already state `If-Match` is
    not implemented.
  - `docs/ARCHITECTURE.md` — recommends `gopkg.in/yaml.v3` dependency that
    is not in `go.mod`.
  - `examples/demo-workspace/.cards/ext/notify.sh:5` — comment reads
    `# ... SPEC EXTENSIONS.md.` (truncated; should reference
    `EXTENSIONS.md`).

**Pattern fix:** pair each `docs/*.md` change with a code-side anchor in
the same PR; add a `docs/internal/docaudit` check (already exists as
`internal/docaudit/`) to CI for the high-drift files.

### Pattern P6 — Stale CI / toolchain config

- DEBT-54 (fixed) — CI matrix alignment.
- DEBT-55 (fixed) — golangci-lint config.
- DEBT-56 (unverified) — pin golangci-lint to a specific version instead
  of `version: latest`.
- Open concrete finding from `issues-docs.md` §1: `.golangci.yml:21`
  `errcheck.exclude-functions` references the old module path
  `(*github.com/foz/work-cards/internal/sqlite.Store).Close`; current module
  is `github.com/somebox/cards`. The exclusion is inert. (Likely covered by
  DEBT-55's fix; re-verify.)
- Open concrete finding: `.github/workflows/ci.yml:19` matrix includes
  Go `1.25` while `go.mod` declares `go 1.26.4`; the `go` directive is a
  minimum, so the 1.25 job will fail to build. (Likely covered by DEBT-54's
  fix; re-verify.)

---

## 3. Individual entries

Severity scale: `blocker` > `high` > `medium` > `low` > `cleanup`.

### DEBT-01 — `ClaimAtomic` silently drops event-log and FTS errors
- **Severity:** high
- **Status:** pinned (fixed)
- **Location:** `internal/sqlite/sqlite.go` (originally `:445-456`; moved to
  `~643-728` after the events-seam work per `inventory.md` §5)
- **Pattern:** P1
- **Fix evidence:** `internal/sqlite/claimatomic_test.go:57` (`DEBT-01` pin)
  asserts that an FTS failure rolls back the claim.

### DEBT-02 — `scanCard` masks SQL errors as "no match"
- **Severity:** medium
- **Status:** pinned (fixed)
- **Location:** `internal/sqlite/sqlite.go:408-411` (original)
- **Pattern:** P1
- **Fix evidence:** `internal/sqlite/claimatomic_test.go:84` (`DEBT-02` pin)
  asserts real errors propagate (not `ErrNoRows`).

### DEBT-03..11 — Remaining swallowed-error sites
- **Severity:** medium → low
- **Status:** unverified
- **Locations:** see Pattern P1 for the full list. **Anchor drift** for the
  `httpapi` sites (file split).
- **Pattern:** P1
- **Action:** re-verify each site at `HEAD`; either propagate the error,
  roll back, or annotate with a comment naming the degradation.

### DEBT-12 — Dead `"log"` import in MCP
- **Severity:** cleanup
- **Status:** fixed (verified `issues-small.md` 2026-07-18)
- **Location:** `internal/mcp/mcp.go:466-467`
- **Pattern:** P2

### DEBT-13 — Dead `"io"` import in hooks
- **Severity:** cleanup
- **Status:** fixed (verified `issues-small.md` 2026-07-18)
- **Location:** `internal/hooks/hooks.go:215-216`
- **Pattern:** P2

### DEBT-14, 15 — Other `var _ =` keep-alives
- **Severity:** cleanup
- **Status:** fixed (per `inventory.md` §1.1: "all gone from the live code")
- **Pattern:** P2

### DEBT-16 — Unused `now` variable in seed
- **Severity:** cleanup
- **Status:** fixed (verified `issues-small.md` 2026-07-18)
- **Location:** `internal/seed/seed.go:36, 75`
- **Pattern:** P3 (dead code)

### DEBT-18, 19 — (recorded in prior ledger; specifics not in 2026-07-02
issues files)
- **Status:** unverified
- **Action:** consult `docs/archive/2026-07-debt-review/debt-ledger.md` for
  the original anchors; re-verify at `HEAD`.

### DEBT-20 — Redundant `contains` / `indexOf` test helpers in hooks
- **Severity:** cleanup
- **Status:** fixed (verified `issues-small.md` 2026-07-18)
- **Location:** `internal/hooks/hooks_test.go:191-198` (original)
- **Pattern:** P3

### DEBT-21 — `fieldSchema` duplicated in `mcp` and `openapi`
- **Severity:** low
- **Status:** open
- **Location:** `internal/mcp/mcp.go:522`, `internal/openapi/openapi.go:259`
- **Pattern:** P3
- **Action:** defer to a product/architecture decision on a shared
  schema-building package. Until then, the two helpers are allowed to
  diverge; flag any divergence in code review.

### DEBT-22 — `containsBoard` is a no-op wrapper
- **Severity:** cleanup
- **Status:** open
- **Location:** `internal/httpapi/httpapi.go:1865` (original; **anchor
  drift** — file was split)
- **Pattern:** P2
- **Action:** inline `containsStr` at the two call sites; delete
  `containsBoard`.

### DEBT-23, 24, 25 — CLI flag-parsing duplication
- **Severity:** low
- **Status:** open
- **Location:** `cmd/cards/main.go:66-106` (`peelGlobals`, `hasPrefix`,
  `val`, `splitEq`)
- **Pattern:** P3
- **Action:** either use `internal/cli.FlagSet` for a single-pass parse, or
  keep the helper names aligned with `cli.FlagSet`.

### DEBT-26 — Workspace-open wiring duplicated across `cmd/cards`
- **Severity:** medium
- **Status:** open
- **Location:** `cmd/cards/serve.go`, `extensions.go`, `import.go`,
  `export.go`, `directcli.go` (the workspace-load + SQLite-open +
  service-build sequence)
- **Pattern:** P3
- **Action:** extract `openWorkspace(dir) (*sqlite.Store, *core.Service,
  *config.Result, error)` into `cmd/cards`. This is the largest single
  source of future drift in the CLI surface.

### DEBT-27 — Race on `Service.Workspace` mutating shared state
- **Severity:** medium
- **Status:** unverified
- **Location:** `internal/core/service.go:103-107` (`s.ws.Users = users`
  with no mutex; `WorkspaceSnapshot` returns the same `*Workspace` ref)
- **Pattern:** (concurrency)
- **Action:** copy the workspace into the snapshot or guard `s.ws` with a
  mutex. Re-verify the anchor at `HEAD` — `service.go` is 54,605 bytes and
  has been touched repeatedly.

### DEBT-28 — `Mem.Replay` passes pointer to loop variable
- **Severity:** medium
- **Status:** unverified
- **Location:** `internal/core/eventlogtest/eventlogtest.go:108-112`
- **Pattern:** (concurrency / correctness)
- **Action:** index the slice explicitly: `for i := range out { if err :=
  fn(&out[i]); … }`. Test-harness bug; may be latent under current tests.

### DEBT-29 — Hook supervisor lifecycle not tied to `httpSrv` shutdown
- **Severity:** low
- **Status:** fixed (Phase 3; ties to `cmd/cards/serve.go:92-99` and
  `cmd/cards/supervisor.go`)
- **Pattern:** (lifetime)

### DEBT-30 — Event-contract seam (partial)
- **Severity:** medium
- **Status:** fixed
- **Location:** `internal/core/types.go` event constructors
- **Pattern:** (event contract)

### DEBT-31, 32 — Event-contract remaining items
- **Severity:** medium → low
- **Status:** unverified
- **Locations:**
  - DEBT-31 — `internal/sqlite/sqlite.go:445-450` (original): `ClaimAtomic`
    builds raw `core.Event{}` with `Version: 0` instead of using
    `core.OwnerChanged` / `core.StatusChanged` constructors (which set
    `Version: 1`). Golden-fixture test asserts `Version == 1`. **Anchor
    drift** — `ClaimAtomic` moved to `~643-728`.
  - DEBT-32 — `internal/sqlite/sqlite.go:219` `migrateEventsScope` comment:
    implementation note (Init owns all event indexes; migration owns
    scope-specific changes). Listed in `inventory.md` §1.2 as "not debt";
    reclassify as `wontfix`/`note` on next reconciliation.
- **Pattern:** (event contract)

### DEBT-33..37 — CLI behaviour items
- **Severity:** medium → low
- **Status:** pinned (fixed)
- **Locations / fix evidence:**
  - DEBT-33 — `internal/cli/cli_test.go:144` — `--quiet take-next` prints
    the card id. (Original finding: `internal/cli/client.go:224` `idOf`
    only looked up top-level keys; `take-next` passed `"card.id"`.)
  - DEBT-34 — `internal/cli/cli_test.go:158` — `cmdPatch` without `--tag`
    does not send `tags`. (Original finding: `internal/cli/commands.go:172`
    wrote `body["tags"] = *tags` unconditionally because `fs.StringArr`
    returns a non-nil pointer.)
  - DEBT-35 — `internal/cli/cli_test.go:196` — `boards show <id>` returns
    that board, not the whole workspace.
  - DEBT-36 — `cmd/cards/main_test.go:5` — `peelGlobals` works for global
    flags at any position.
  - DEBT-37 — (recorded in prior ledger; not in 2026-07-02 issues files.
    Consult `docs/archive/2026-07-debt-review/debt-ledger.md`.)
- **Pattern:** (CLI correctness)

### DEBT-38, 39 — Logging consistency
- **Severity:** low
- **Status:** unverified
- **Pattern:** P4
- **Action:** see Pattern P4 for concrete instances and the proposed
  output-conventions doc.

### DEBT-40..53 — Docs drift (14 items)
- **Severity:** warning → note
- **Status:** unverified (all 14)
- **Pattern:** P5
- **Action:** see Pattern P5 for the per-file list. Each item needs a
  side-by-side check of the prose against the current code.

### DEBT-54 — CI Go matrix alignment
- **Severity:** warning
- **Status:** fixed (verified in `sprint-2026-07-06.md` Phase 0)
- **Location:** `.github/workflows/ci.yml:19`
- **Note:** `issues-docs.md` §1 still flags a `1.25` vs `go 1.26.4`
  mismatch; re-confirm the fix actually landed and didn't regress.

### DEBT-55 — golangci-lint config
- **Severity:** warning
- **Status:** fixed (verified in `sprint-2026-07-06.md` Phase 0)
- **Location:** `.golangci.yml`
- **Note:** `issues-docs.md` §1 still flags an old-module-path
  `errcheck.exclude-functions` entry
  (`(*github.com/foz/work-cards/internal/sqlite.Store).Close`). Re-confirm
  whether DEBT-55's fix covered this; if not, file a new entry.

### DEBT-56 — Pin golangci-lint version in CI
- **Severity:** note
- **Status:** unverified
- **Location:** `.github/workflows/ci.yml` (`version: latest`)
- **Action:** pin to a specific golangci-lint version to avoid unrelated
  PR failures.

### DEBT-57 — Artifacts policy unimplemented
- **Severity:** high (if exposed to untrusted input)
- **Status:** pinned (fixed — artifacts end-to-end landed)
- **Location:** `internal/artifacts/artifacts.go:11-12` (original `TODO`);
  pin at `internal/artifacts/artifacts_test.go:89`
- **Note:** the original `TODO` (content-addressed subdirs, SHA-256, MIME
  sniff, path-confinement) is resolved; the test pin documents the prior
  state. Re-verify the policy is actually enforced before exposing
  artifact upload to untrusted paths.

### DEBT-58 (new) — `splitCSV` duplicated in `mcp` and `httpapi`
- **Severity:** low
- **Status:** new (2026-07-18)
- **Location:** `internal/mcp/mcp.go:432`, `internal/httpapi/api.go:540`
- **Pattern:** P3
- **Action:** extract a shared helper to `internal/core` or
  `internal/config`; the two versions differ only in slice pre-allocation.

### DEBT-59 (new) — `strOrEmpty` identity helper duplicated
- **Severity:** cleanup
- **Status:** new (2026-07-18)
- **Location:** `internal/core/service.go:1495`, `internal/sqlite/sqlite.go:922`
- **Pattern:** P3
- **Action:** delete both copies; pass the string directly. Used only by
  `OwnerChanged` (`service.go:368`) and the raw event literal in
  `ClaimAtomic`.

### DEBT-60 (new) — `strReq` / `intReq` aliases in MCP
- **Severity:** cleanup
- **Status:** new (2026-07-18)
- **Location:** `internal/mcp/mcp.go:574-577`
- **Pattern:** P3
- **Action:** the MCP tool schema already expresses required fields through
  the top-level `required` array; the `*Req` names add no enforcement.
  Replace the call sites in `create_<T>` / `update_<T>` input schemas with
  `str()` / `intSchema()` and delete `strReq` / `intReq`.

### DEBT-61 (new) — TUI surface has no debt-ledger coverage
- **Severity:** medium (review gap, not a defect)
- **Status:** **disposition recorded 2026-07-18** — see
  [`docs/design/tui-bus-disposition.md`](docs/design/tui-bus-disposition.md).
  Deps/teardown/actor sound; one real defect fixed (inbound links never
  rendered — `syncExtras` now eager-loads `Include:["links"]`); render tests
  made hermetic (snapshot, not live DB); non-TTY regression pinned
  (`cmd/cards/interactive_test.go`); in-process bus ruled load-bearing
  (wontfix, boundary documented).
- **Location:** `internal/tui/tui.go` (1,619 lines), `internal/tui/tui_test.go`
  (556 lines), `cmd/cards/tui.go` (63 lines), wiring in
  `cmd/cards/main.go:run` (`interactive()` guard)
- **Pattern:** (review gap)
- **Action:** run a dedicated review pass on the TUI. Open sub-questions
  (from `inventory.md` Area E):
  1. Is the bubbletea v2 / lipgloss v2 / glamour dependency set healthy
     (no `nolint` exclusions; binary-size budget)?
  2. Are live-refresh subscriptions (`svc.Bus().Subscribe`) cleaned up on
     model teardown?
  3. Does actor resolution match the rest of the surface
     (`core.WithActor(ctx, actor)` only)?
  4. Does the markdown rendering of the detail pane handle
     malicious/surprising field values (long strings, multi-line, code
     blocks in user-typed text)?
  5. Are the three focus zones × three detail visibilities state machine
     transitions exhaustively tested in `tui_test.go`?
- **Note:** this is a *review-gap* entry, not a finding of debt. The TUI
  may be clean; the ledger just records that nobody has checked yet.

---

## 4. Anchor-drift warnings

The following `file:line` references in the 2026-07-02 issues files are
**known stale** and must be re-resolved before any fix work:

| Original anchor | Drift cause | Re-resolve to |
|---|---|---|
| `internal/httpapi/httpapi.go:647, 801, 836, 851, 870, 921, 933, 988, 1053, 1279, 1454, 1486, 1498, 1527, 1865, 201-203` | `httpapi.go` was split into `api.go` / `sse.go` / `middleware.go` / `render.go` | grep by symbol name (`apiEventStream`, `idempotent`, `uiIndex`, `containsBoard`, `actorKey`) |
| `internal/sqlite/sqlite.go:408-411, 445-456, 445-450, 726-736` | `ClaimAtomic` moved from `~445` to `~643-728` | grep by symbol name (`ClaimAtomic`, `scanCard`, `CommentCounts`) |
| `internal/core/service.go:103-107, 217-218, 704, 734-736, 763, 799, 1151-1157, 1157-1158, 1495, 1583-1596, 1598-1600` | `service.go` is 54,605 bytes and frequently touched | grep by symbol name (`Workspace`, `ResolveCard`, `AddLink`, `AddComment`, `EditComment`, `RemoveLink`, `checkColumn`, `checkUserExists`, `strOrEmpty`, `toAnySlice`, `placeholders`) |
| `internal/bus.go:144` | likely now `internal/core/bus.go` or `internal/bus/bus.go` | `find` for `var _ = context.Background` |

---

## 5. Recommended next-pass priorities

Ordered by leverage, not by ID:

1. **DEBT-26** — extract `openWorkspace`. Removes the largest drift source in
   `cmd/cards` and unblocks future CLI commands.
2. **DEBT-21, 58, 59, 60** — consolidate the schema/CSV/string helpers into a
   shared internal package. One PR, four entries closed.
3. **DEBT-40..53** — docs-drift sweep. Distinct skill set; pairs well with a
   `internal/docaudit` CI integration.
4. **DEBT-27, 28** — concurrency items. Latent today; will surface under
   load. Cheap to fix.
5. **DEBT-56** — pin golangci-lint version. Two-minute change; prevents
   unrelated PR failures.
6. **DEBT-61** — TUI review pass. Fresh surface, no coverage; schedule
   before the TUI accumulates more behavior.
7. **DEBT-03..11** — re-verify the swallowed-error sites at `HEAD`. The
   pattern fix (P1) closes the cluster.
8. **DEBT-31, 32** — re-verify event-contract items; reclassify DEBT-32 as
   `wontfix`/`note`.

---

## 6. Escalation notes

- **DEBT-21** (shared schema-builder package) and **DEBT-60** (MCP `*Req`
  aliases) both touch the question of whether `mcp` and `openapi` should
  share a JSON-Schema construction package. This is an architecture call,
  not a cleanup call. **Recommend escalating to high-tier** before
  introducing `internal/schema` or similar.
- **DEBT-61** (TUI review) raises the question of whether the TUI's
  in-process `core.Service` bus subscription is a load-bearing architectural
  choice (TUI must work without a server) or an implementation convenience.
  If the former, the bus contract needs documentation. **Recommend
  escalating the bus-subscription-lifetime question to high-tier.**

---

*End of ledger. No source files were modified; this is a registry only.*
