# Technical-Debt Ledger — `github.com/somebox/cards`

Generated: 2026-07-02
Sources: `inventory.md`, `issues-small.md`, `issues-core.md`, `issues-http-cli.md`, `issues-docs.md`

This is a deduplicated, cross-referenced registry of the technical debt surfaced by the
parallel review pass. Items are grouped by theme and given stable IDs so follow-up work
can reference them (`DEBT-XX`). Severity is one of `high`, `medium`, `low`, `cleanup`,
`note`. Where the same root pattern appears in multiple files, the entries are merged
into one row and the affected sites are listed together.

Conventions:
- `file:line` references are exact where the source review gave them.
- "Cross-ref" lists related IDs that share a root cause or should be fixed together.
- The ledger intentionally does **not** edit any source files; it only records findings.

---

## Theme 1 — Error Handling (swallowed / best-effort errors)

The single largest recurring pattern in the codebase is discarding errors with `_`,
especially on secondary persistence (event log, FTS, denormalized link/comment tables,
idempotency cache, UI store reads). These are grouped from most severe to least.

| ID | Severity | Affected files / lines | Description | Cross-ref |
|----|----------|------------------------|-------------|-----------|
| DEBT-01 | high | `internal/sqlite/sqlite.go:445-456` | `ClaimAtomic` discards both `execEventInsert(tx, ev)` and `upsertFTS(tx, claimed)` errors with `_ =` while still committing the transaction. A failed event insert loses a durable audit row for a mutation that already committed; a failed FTS update makes the card unsearchable. The caller has no signal of failure. | DEBT-09, DEBT-14 |
| DEBT-02 | medium | `internal/sqlite/sqlite.go:408-411` | `scanCard(row)` returns `nil, nil, nil` for *any* error, including transient SQL errors, so `ClaimAtomic` masks real DB failures as "no cards available". Should distinguish `sql.ErrNoRows` from other errors. | DEBT-01 |
| DEBT-03 | medium | `internal/core/service.go:704, 734-736, 763, 799` | `AddLink`, `AddComment`, `EditComment`, `RemoveLink` ignore denormalized `links`/`comments` table errors with "non-fatal" comments. The graph table / comment arrays can drift from the event log and card JSON with no operator signal. | DEBT-04 |
| DEBT-04 | low | `internal/core/service.go:217-218` | `ResolveCard` discards `ListLinks` / `ListComments` errors on the short-id unique-resolution path (`_`). A card can be returned with stale/missing links or comments. | DEBT-03 |
| DEBT-05 | medium | `internal/core/service.go:1157-1158` | `checkUserExists` does `users, _ := s.store.ListUsers(ctx)`. If the store fails, every user reference is silently coerced to `unknown_user`. Should return `Internal` on store failure. | — |
| DEBT-06 | medium | `internal/httpapi/httpapi.go:647` | `apiEventStream` discards the replay query error: `evs, _ := s.svc.ListEvents(...)`. A DB failure during SSE reconnect silently misses events. Log or emit a recoverable SSE error comment. | DEBT-07 |
| DEBT-07 | medium | `internal/httpapi/httpapi.go:801` | `idempotent` ignores `s.store.PutIdempotency(...)` error. A replay entry may fail to persist while the client already saw a 2xx, breaking later idempotency replays. | DEBT-06 |
| DEBT-08 | low | `internal/httpapi/httpapi.go:836, 851, 870, 921, 933, 988, 1053, 1279, 1454, 1486, 1498, 1527` | Multiple UI/data helpers (`uiIndex`, etc.) silently ignore errors from `ListCards`, `ListUsers`, `AllLinks`, `CommentCounts`. A failing store renders an empty/partial page with no noise. Either fail the request or document why each drop is safe. | DEBT-06 |
| DEBT-09 | low | `internal/httpapi/httpapi.go` (`writeSSEEvent` helper) | `payload, _ := json.Marshal(m)` ignores the marshal error. Theoretically impossible for the current types, but the assumption is implicit; a comment or `panic`-on-failure would make it explicit. | DEBT-01 |
| DEBT-10 | medium | `cmd/cards/portable.go:61-62` | `exportJSONL` skips cards whose `GetCard` fails (`if err != nil { continue }`). A backup can silently drop data. Log the failure or fail the export. | DEBT-08 |
| DEBT-11 | low | `internal/sqlite/sqlite.go:726-736` | `CommentCounts` does not return `rows.Err()` after the `for rows.Next()` loop, unlike `ListComments` (`sqlite.go:691`) and `AllLinks` (`sqlite.go:782`). A partial read goes undetected. | DEBT-02 |

**Pattern note:** Eleven distinct sites drop errors with `_`. The fix shape is uniform —
propagate, or wrap with an explicit "safe to drop, because …" comment. A project-wide
`errcheck` run with a documented allow-list (see DEBT-30) would prevent regression.

---

## Theme 2 — Dead / Duplicated Code

Two sub-patterns: (a) `var _ = <pkg.Sym>` keep-alive assignments that mask unused imports,
and (b) helper functions duplicated across `internal/core` and `internal/sqlite` (and a
small CLI duplication).

| ID | Severity | Affected files / lines | Description | Cross-ref |
|----|----------|------------------------|-------------|-----------|
| DEBT-12 | cleanup | `internal/mcp/mcp.go:466-467` | Dead `"log"` import kept alive by `var _ = log.Print`. Remove the import and the blank assignment. | DEBT-13, DEBT-15 |
| DEBT-13 | cleanup | `internal/hooks/hooks.go:215-216` | Dead `"io"` import kept alive by `var _ = io.EOF`. The preceding comment ("strings import lives here") is stale — it references `io.EOF`, not `strings`. Remove import + assignment + fix the comment. | DEBT-12, DEBT-15 |
| DEBT-14 | low | `internal/core/bus.go:144` | `var _ = context.Background` keeps the `context` import alive for "future request-scoped subscriptions". Dead today. Re-add when the feature arrives. (Inventory §2 also flagged this.) | DEBT-12, DEBT-13 |
| DEBT-15 | low | `internal/httpapi/httpapi.go:201-203` | `type actorKey struct{}` + `var _ = actorKey{}` are unused; actor state now flows through `core.WithActor`. Remove both. | DEBT-12, DEBT-13 |
| DEBT-16 | low | `internal/seed/seed.go:36, 75` | `now := time.Now().UTC().Format(time.RFC3339)` is computed and immediately discarded with `_ = now`; never referenced in seeded cards. Remove or wire into a `created_at`/notes field. | — |
| DEBT-17 | low | `internal/core/service.go:1495`, `internal/sqlite/sqlite.go:922` | `func strOrEmpty(s string) string { return s }` is a no-op duplicated in both packages. Only used in `OwnerChanged` (`service.go:368`) and the raw event literal in `ClaimAtomic` (`sqlite.go:447`). Delete and pass the string directly. | DEBT-01, DEBT-18 |
| DEBT-18 | low | `internal/core/service.go:1598-1600`, `internal/sqlite/sqlite.go:924-926` | Identical `placeholders(n)` helper in both packages. Move to a shared internal helper package or export from `core`. | DEBT-19 |
| DEBT-19 | low | `internal/core/service.go:1583-1596` (`toAnySlice`), `internal/sqlite/sqlite.go:931-936` (`toAny`) | Same `[]string → []any` conversion in two places. Share one helper. | DEBT-18 |
| DEBT-20 | cleanup | `internal/hooks/hooks_test.go:191-198` | `contains` and `indexOf` duplicate `strings.Contains` / `strings.Index`. Import `strings` and delete the helpers. | — |
| DEBT-21 | low | `internal/mcp/mcp.go` + `internal/openapi/openapi.go` | Both packages define a `fieldSchema` helper mapping `core.FieldDef` → JSON Schema. Signatures differ (`mcp` takes `required bool` and handles `x-required`; `openapi` does not). Conceptual duplication, not a copy-paste bug. Defer consolidation to a shared schema-builder package decision. | — |
| DEBT-22 | low | `internal/httpapi/httpapi.go:1865` | `containsBoard` is a one-line wrapper around `containsStr`, called from only two sites. Inline and delete. | — |
| DEBT-23 | low | `cmd/cards/serve.go:105-111`, `cmd/cards/extensions.go:57-70` | `countHooks` is defined in `serve.go` but `runExtensionsCmd` reimplements the same count-and-list loop inline. Call `countHooks` from `extensions.go`. | — |
| DEBT-24 | low | `cmd/cards/extensions.go:23-26` vs `internal/cli.FlagSet.StringArr` | `stringSlice` (a `flag.Value` for repeatable `--param`) duplicates the concept behind `cli.FlagSet.StringArr`. Reuse or move a shared `StringSlice` into `internal/cli`. | — |
| DEBT-25 | low | `cmd/cards/main.go:66-106` vs `internal/cli.FlagSet` | `peelGlobals`, `hasPrefix`, `val`, `splitEq` are a bespoke global-flag parser that overlaps with `cli.FlagSet` parsing. Consider a single-pass `cli.FlagSet` parse or at least align helper names. | DEBT-40 |
| DEBT-26 | medium | `cmd/cards/serve.go`, `cmd/cards/extensions.go`, `cmd/cards/import.go`, `cmd/cards/export.go`, `cmd/cards/directcli.go` (and `serve.go:mcpCmd`) | The workspace-load + SQLite open + service-build sequence is repeated across `serveCmd`, `mcpCmd`, `runExtensionsCmd`, `importCmd`, `exportCmd`, and `newDirectBackend`. Introduce `openWorkspace(dir) (*sqlite.Store, *core.Service, *config.Result, error)` to centralize setup and reduce drift. | — |

**Pattern note:** Three `var _ = <pkg.Sym>` keep-alive assignments (DEBT-12, DEBT-13,
DEBT-14) and one unused type keep-alive (DEBT-15) share the same root cause: imports that
were either preemptively added for future work or left behind after a refactor. A periodic
`deadcode`/`unused` linter pass would catch these. The duplicated helpers in
`internal/core` and `internal/sqlite` (DEBT-17, DEBT-18, DEBT-19) are the strongest
argument for a small `internal/dbutil` (or similar) shared package.

---

## Theme 3 — Concurrency / Lifetime

| ID | Severity | Affected files / lines | Description | Cross-ref |
|----|----------|------------------------|-------------|-----------|
| DEBT-27 | medium | `internal/core/service.go:103-107` | `Service.Workspace` mutates shared state (`s.ws.Users = users`) with no mutex, and `WorkspaceSnapshot` returns the same `*Workspace` reference. Concurrent HTTP/CLI requests race on `Workspace.Users`. Copy the workspace into the snapshot or guard `s.ws` with a mutex. | — |
| DEBT-28 | medium | `internal/core/eventlogtest/eventlogtest.go:108-112` | `Mem.Replay` passes `&e` where `e` is a `for _, e := range` loop variable; all callbacks receive aliases to the same stack slot. Observers that retain the pointer see corrupted data. Fix: `for i := range out { if err := fn(&out[i]); … }`. | — |
| DEBT-29 | low | `cmd/cards/serve.go:92-99` | When `--run-extensions` is set, the hook supervisor goroutine is launched before `ListenAndServe` blocks. If `ListenAndServe` errors immediately, the supervisor keeps running until interrupted. Tie the supervisor lifecycle to `httpSrv` shutdown (`RegisterOnShutdown`) or a shared `context.Context`. | — |

---

## Theme 4 — Event Contract / Constructor Discipline

| ID | Severity | Affected files / lines | Description | Cross-ref |
|----|----------|------------------------|-------------|-----------|
| DEBT-30 | medium | `internal/sqlite/sqlite.go:445-450` | `ClaimAtomic` builds `&core.Event{CardID:…, Type:…, Actor:…, At:…, Diff:…}` directly instead of using `core.OwnerChanged` / `core.StatusChanged`, leaving `Version: 0`. Every built-in constructor sets `Version: 1` and the golden-fixture test asserts `Version == 1`. Event consumers see inconsistent events from take-next vs. every other mutation path. Use the constructors or at minimum set `Version: 1`. | DEBT-01, DEBT-17 |
| DEBT-31 | low | `internal/core/service.go:1151-1157` | `checkColumn(status string, ct *CardType, _ *Board)` accepts a `Board` it never uses. Misleading API; either enforce board-level column sets or drop the parameter. | — |
| DEBT-32 | low | `internal/sqlite/sqlite.go:185-186` (Init) vs `sqlite.go:199-200` (migration) | `migrateEventsScope` drops/recreates `idx_events_card` and `idx_events_at` that `Init` already created, then re-adds `idx_events_scope`. Idempotent but redundant; future index changes must be made in two places. Let `Init` own card/at indexes and the migration own only scope-specific changes. | — |

---

## Theme 5 — CLI Behaviour / Correctness

These are user-visible behaviour nits, not pure debt, but they belong in the registry so
they are not lost.

| ID | Severity | Affected files / lines | Description | Cross-ref |
|----|----------|------------------------|-------------|-----------|
| DEBT-33 | medium | `internal/cli/client.go:169, 224` + `internal/cli/commands.go:282` | `Client.Print` accepts a dotted `idPath` for `--quiet` mode, but `idOf` only looks up a top-level map key. `cmdTakeNext` passes `"card.id"`, which never matches, so `--quiet take-next` falls back to printing the whole JSON blob instead of the card id. Implement dotted-path lookup or have `take-next` extract and print the id itself. | — |
| DEBT-34 | medium | `internal/cli/commands.go:172` (vs `cmdCreate` ~`commands.go:125`) | `cmdPatch` writes `body["tags"] = *tags` whenever `tags != nil`. Because `fs.StringArr` always returns a non-nil pointer (even for an empty default), an unset `--tag` produces `"tags": null` in the request body, unlike `cmdCreate` which guards with `len(*tags) > 0`. Mirror `cmdCreate`. | — |
| DEBT-35 | low | `internal/cli/commands.go:514-532` | `boards show <board_id>` returns the entire workspace because there is no per-board API endpoint. The comment is honest but the UX is misleading. Add `/v1/boards/{id}` (or `/v1/workspace?board_id=…`) and/or warn the user. | DEBT-43 |
| DEBT-36 | low | `cmd/cards/main.go:25-31` | `peelGlobals` only strips global flags from the *front* of the command line; `cards list --json` fails because `--json` is not a `list` flag. Either document the position restriction more loudly or parse globals in a pre-pass over the whole slice. | DEBT-25 |
| DEBT-37 | low | `cmd/cards/main.go:121-122` | The command lookup loop calls `cli.Commands()` on every iteration; it should be called once before the loop (`cmds := cli.Commands()`). | — |

---

## Theme 6 — Logging / Output-Style Inconsistency

| ID | Severity | Affected files / lines | Description | Cross-ref |
|----|----------|------------------------|-------------|-----------|
| DEBT-38 | low | `cmd/cards/serve.go:44-46, 56, 89-101` vs `cmd/cards/extensions.go:146, 168, 175` | `serveCmd` and `runExtensionsCmd` use `log.Printf` (stderr) for operational messages, while `extensionsCmd` uses `fmt.Printf`/`fmt.Println` (stdout) for user-facing output. Same command family, two conventions. Pick one convention per category (operational → log; user-facing → stdout/stderr) and document it. | — |
| DEBT-39 | note | `cmd/cards/import.go:84`, `cmd/cards/export.go:73`, `cmd/cards/init.go:46-53` | `import`/`export` correctly send summaries to `os.Stderr` so stdout stays valid JSONL; `init` uses `fmt.Println` for post-init instructions. Positive pattern — keep, but consider a `--quiet` flag for `init` for symmetry. | — |

---

## Theme 7 — Docs / Code Drift

The largest drift cluster: YAML support for core definitions is claimed in four docs but
the loader only reads JSON. A second cluster: event counts and test-seam status in
`EVENTS.md` / `INTEGRATOR-REFERENCE.md` are stale.

| ID | Severity | Affected files / lines | Description | Cross-ref |
|----|----------|------------------------|-------------|-----------|
| DEBT-40 | warning | `docs/SPEC.md` §3; `docs/ARCHITECTURE.md`; `docs/DEVELOPER-REFERENCE.md` §2; `internal/config/config.go` package comment | Multiple docs state workspace/card-type/board definitions may be JSON *or* YAML and that the loader normalizes both. `internal/config/config.go` only reads JSON for `workspace.json`, `card-types/*.json`, `boards/*.json`; only `definitions/extensions.{yaml,json}` is parsed (with a minimal inline YAML parser). The `config` package comment also claims "reload-on-change is handled in serve mode", which is not implemented. Correct the docs or implement YAML + reload. | DEBT-41 |
| DEBT-41 | note | `docs/ARCHITECTURE.md` | Recommends `gopkg.in/yaml.v3` as a dependency, but it is not in `go.mod` and is not used by the loader. Drop the recommendation or add the dependency if full YAML support is intended. | DEBT-40 |
| DEBT-42 | warning | `docs/EVENTS.md` §10 (~line 273), §12 Step 1 | States `internal/core/events_test.go` does not exist and the test fakes/fixtures from §10 are not built. In fact the file exists with `TestEventContracts_GoldenFixtures` and `TestNoRawEventLiterals`, and `internal/core/eventlogtest/` provides `Mem`, `Recorder`, and a `Conformance` suite. §12 Step 1 ("Add test fakes + seam acceptance tests" not done) is also stale — the seam acceptance test exists via `internal/core/eventlog_conformance_test.go`. Update both. | DEBT-44 |
| DEBT-43 | warning | `docs/INTEGRATOR-REFERENCE.md` §4 | "Today every event is card-scoped" and board-scoped events are only proposed. `internal/core/types.go:223-230` has `Scope` and `BoardID` on `Event`, and `internal/core/wip_test.go` emits `wip_exceeded` / `wip_cleared` with `Scope: "board"`, `BoardID: "eng"`. Ephemeral, but board-scoped events exist. Update the doc. | DEBT-44, DEBT-35 |
| DEBT-44 | warning | `docs/INTEGRATOR-REFERENCE.md` §4 | "Canonical enumeration (15 declared, 13 emitted today)" is stale. `internal/core/types.go:209-230` declares 17 constants (13 durable facts + `artifact_added` + `definition_reloaded` + `wip_exceeded` + `wip_cleared`). Update or remove the count. | DEBT-42, DEBT-43 |
| DEBT-45 | warning | `docs/EXTENSIONS.md` ("Event contract for hooks") vs `internal/hooks/hooks.go:133-141` | The hook event JSON example shows `"board_ids": ["engineering"]`. The actual payload built in `hooks.go:133-141` includes `id`, `type`, `card_id`, `actor`, `at`, `diff`, `workspace_id`; there is no `board_ids` field. Either emit the board(s) a card belongs to or correct the doc. | DEBT-46 |
| DEBT-46 | warning | `docs/EXTENSIONS.md` ("Declaration" / "CLI surface") vs `cmd/cards/extensions.go` vs `docs/INTEGRATOR-REFERENCE.md` §7 | `EXTENSIONS.md` implies `service` extensions with `autostart: true` are supervised by `cards run-extensions`. `runExtensionsCmd` only wires `kind: hook`; `service` extensions are parsed but never started. `INTEGRATOR-REFERENCE.md` §7 says this correctly, so the two docs contradict each other. Reconcile. | DEBT-45 |
| DEBT-47 | warning | `docs/LIFECYCLE-EXAMPLES.md` B6 vs `internal/httpapi/httpapi.go` (list handler), `internal/cli/commands.go` (`list`) | Example B6 uses `GET /v1/cards?board_id=fabrication&status=queued&updated_before=…` and `cards list --updated-before …`. Neither the list handler nor the CLI reads `updated_before`/`updated_after`/`created_before`/`created_after`. `SPEC.md` §9 already notes these are not implemented; the example contradicts it. | — |
| DEBT-48 | note | `docs/SLICE3-REFLECTION.md` (resolution summary) vs `docs/INTEGRATION.md` | Says `INTEGRATION.md`'s fuller `card_ready`/`card_unblocked` design supersedes F1. `INTEGRATION.md` actually talks about `card_unblocked`; `card_ready` is not in the code or event catalog. Slightly imprecise reference. | — |
| DEBT-49 | note | `docs/NOTES.md` (D5 status note) | Claims `SPEC.md` and `DEVELOPER-REFERENCE.md` still mention an `If-Match` header alias and should be updated. Both docs already state `If-Match` is not implemented and only body/query `version` is used. The D5 follow-up note can be removed/updated. | — |
| DEBT-50 | note | `docs/CONCEPTS.md` | The zero-config launch path (`cards` with no args serves `~/.cards`) is described as "Planned" but is implemented in `cmd/cards/main.go`. Update the doc. | — |
| DEBT-51 | note | `docs/DEVELOPER-REFERENCE.md` §9 (`take-next`) | Example mentions `--filter-file ./filters/todo.json`, but no such file is checked in at `examples/demo-workspace/filters/todo.json`. Illustrative only; note for copy-paste users. | — |
| DEBT-52 | note | `examples/demo-workspace/.cards/ext/notify.sh:5` | Comment reads `# ... SPEC EXTENSIONS.md.` — likely meant `See EXTENSIONS.md`. | — |
| DEBT-53 | note | `internal/config/config.go` package comment | Package doc says "JSON/YAML files in definitions/" and "Reload-on-change is handled in serve mode". The `Loader` comment already says "JSON only, single context, no file watching, no merge". Reconcile the package comment with the `Loader` comment. | DEBT-40 |

**Pattern note:** The YAML-support cluster (DEBT-40, DEBT-41, DEBT-53) is the highest-value
doc fix: four docs + a package comment all overstate capability. The event-count/test-seam
cluster (DEBT-42, DEBT-43, DEBT-44) is a single editing pass over two files. The hook
contract drift (DEBT-45, DEBT-46) crosses a doc/code boundary and needs a product call on
whether hooks should emit `board_ids`.

---

## Theme 8 — CI / Config Hygiene

| ID | Severity | Affected files / lines | Description | Cross-ref |
|----|----------|------------------------|-------------|-----------|
| DEBT-54 | warning | `.github/workflows/ci.yml:19` vs `go.mod` (`go 1.26.4`) | CI matrix includes Go `1.25`, but `go.mod` declares `go 1.26.4`. Since Go 1.21+ enforces the `go` directive as a minimum version, CI jobs on Go `1.25` will fail to build the module. Drop `1.25`; test on `1.26.4` and/or `1.27`. | — |
| DEBT-55 | warning | `.golangci.yml:21` | `errcheck.exclude-functions` references `(*github.com/foz/work-cards/internal/sqlite.Store).Close` — the old module path. Current module is `github.com/somebox/cards`, so the exclusion is inert and `errcheck` may still complain about an ignored `Store.Close`. Update the path. | — |
| DEBT-56 | note | `.golangci.yml:33` (workflow `install-mode: goinstall`, `version: latest`) | `version: latest` in CI can introduce unrelated lint failures on unrelated PRs. Pin to a specific `golangci-lint` version. | — |

---

## Theme 9 — Unimplemented Policy (explicit TODO)

| ID | Severity | Affected files / lines | Description | Cross-ref |
|----|----------|------------------------|-------------|-----------|
| DEBT-57 | medium | `internal/artifacts/artifacts.go:11-12` | The only `TODO` in the repo. `Manager` is an empty struct; the comment flags content-addressed subdirs, SHA-256, MIME sniff, and path-confinement validation — none implemented. The local artifact policy is not enforced. Address before artifact handling is exposed to untrusted paths. | — |

---

## Cross-reference summary

The strongest cross-cutting fix clusters (touching multiple themes at once):

1. **`ClaimAtomic` hardening** — DEBT-01 + DEBT-02 + DEBT-30 + DEBT-17 all live in
   `internal/sqlite/sqlite.go:408-456`. One focused pass returns errors from
   `execEventInsert`/`upsertFTS`/`scanCard`, uses the `core.OwnerChanged`/
   `core.StatusChanged` constructors (fixing `Version: 0`), and deletes the no-op
   `strOrEmpty` used only by the raw event literal.
2. **Shared `internal/dbutil` package** — DEBT-17 + DEBT-18 + DEBT-19 (duplicated
   `strOrEmpty`/`placeholders`/`toAnySlice`) collapse into one shared helper package.
3. **Dead-import sweep** — DEBT-12 + DEBT-13 + DEBT-14 + DEBT-15 are four `var _ =`
   keep-alive sites across four packages; a single `deadcode`/`unused` linter pass
   removes them together.
4. **YAML doc correction** — DEBT-40 + DEBT-41 + DEBT-53 are one editing pass over
   `SPEC.md`, `ARCHITECTURE.md`, `DEVELOPER-REFERENCE.md`, and the `config` package
   comment (plus a `go.mod` decision on `gopkg.in/yaml.v3`).
5. **Event catalog refresh** — DEBT-42 + DEBT-43 + DEBT-44 are one editing pass over
   `EVENTS.md` and `INTEGRATOR-REFERENCE.md`.

---

## Severity roll-up

| Severity | Count | IDs |
|----------|-------|-----|
| high | 1 | DEBT-01 |
| medium | 12 | DEBT-02, DEBT-03, DEBT-05, DEBT-06, DEBT-07, DEBT-10, DEBT-26, DEBT-27, DEBT-28, DEBT-30, DEBT-33, DEBT-34 |
| low | 25 | DEBT-04, DEBT-08, DEBT-09, DEBT-11, DEBT-14, DEBT-15, DEBT-16, DEBT-17, DEBT-18, DEBT-19, DEBT-21, DEBT-22, DEBT-23, DEBT-24, DEBT-25, DEBT-29, DEBT-31, DEBT-32, DEBT-35, DEBT-36, DEBT-37, DEBT-38, DEBT-57(*) |
| cleanup | 3 | DEBT-12, DEBT-13, DEBT-20 |
| warning (docs/CI) | 9 | DEBT-40, DEBT-42, DEBT-43, DEBT-44, DEBT-45, DEBT-46, DEBT-47, DEBT-54, DEBT-55 |
| note | 9 | DEBT-39, DEBT-41, DEBT-48, DEBT-49, DEBT-50, DEBT-51, DEBT-52, DEBT-53, DEBT-56 |

(*) DEBT-57 (the artifacts TODO) is rated `medium` for safety exposure but lives in a
skeleton package with no current callers, so the practical risk today is low; the rating
reflects the untrusted-path exposure risk if the policy ships unimplemented.

**Total distinct items: 57.** The single `high` is DEBT-01 (silently dropped event + FTS
errors inside a committing transaction in `ClaimAtomic`).

---

*End of ledger. No source files were modified; only `debt-ledger.md` was written.*
