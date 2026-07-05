# HTTP + CLI surface review findings

**Scope:** `internal/httpapi/`, `internal/cli/`, `cmd/cards/`.  
**Date:** 2026-07-02  
**Reviewer:** dev-tier agent

## Executive summary

No blockers and no `TODO`/`FIXME` markers inside the HTTP + CLI area.  The repo-wide `TODO` lives in `internal/artifacts/artifacts.go` (outside this review).  The surface is functional but has a handful of code-quality rough edges: dead/kept-alive identifiers, duplicated workspace-store wiring, silently discarded errors in UI/SSE paths, an unused dotted `idPath` convention in the CLI printer, and an inconsistent split between `log.Printf` server logging and `fmt.Println`/`fmt.Fprintf(os.Stderr, …)` CLI output.

All findings are cleanup/refactoring candidates; none prevent the code from running.  Existing tests pass (`go test ./internal/httpapi/... ./internal/cli/... ./cmd/cards/...`) and `go vet` is clean for the reviewed packages.

---

## Structured findings

### 1. Dead / kept-alive code

| File | Line | Severity | Finding | Recommendation |
|------|------|----------|---------|----------------|
| `internal/httpapi/httpapi.go` | `201–203` | low | `type actorKey struct{}` and `var _ = actorKey{}` are referenced only by a comment that says actor state now flows through `core.WithActor`.  The type and blank assignment are unused. | Remove the `actorKey` type and the `var _ = actorKey{}` keep-alive assignment. |
| `internal/httpapi/httpapi.go` | `1865` | low | `containsBoard` is a one-line wrapper around `containsStr`.  It adds no domain behaviour and is only called from two places. | Inline `containsStr` at the two call sites and delete `containsBoard`. |

### 2. Silently ignored errors (UI / SSE / idempotency)

| File | Line | Severity | Finding | Recommendation |
|------|------|----------|---------|----------------|
| `internal/httpapi/httpapi.go` | `647` | medium | In `apiEventStream` the replay query error is discarded: `evs, _ := s.svc.ListEvents(...)`; a slow DB failure during reconnect would silently miss events. | Log the error or send a recoverable SSE comment/close with an error indicator. |
| `internal/httpapi/httpapi.go` | `801` | medium | `idempotent` ignores `s.store.PutIdempotency(...)` error. A replay entry may fail to persist while the client already saw a 2xx, breaking later idempotency replays. | Log or return a non-2xx if the replay cache cannot be written. |
| `internal/httpapi/httpapi.go` | `836`, `851` | low | `uiIndex` discards `ListCards` errors (`page, _ := …`, `all, _ := …`). A failing store renders a possibly empty homepage without noise, masking problems. | Return `http.StatusInternalServerError` or surface the error in the page. |
| `internal/httpapi/httpapi.go` | `870`, `921`, `933`, `988`, `1053`, `1279`, `1454`, `1486`, `1498`, `1527` | low | Multiple UI/data helpers silently ignore errors from `ListCards`, `ListUsers`, `AllLinks`, `CommentCounts`, etc.  Examples: `users, _ := s.store.ListUsers(...)`, `edges, _ := s.store.AllLinks(ctx)`. | Either fail the request or, if the failure is truly optional, explicitly document why the error is safe to drop. |
| `internal/httpapi/httpapi.go` | `writeSSEEvent` helper | low | `payload, _ := json.Marshal(m)` ignores the (theoretically impossible) marshal error. | Keep ignoring is acceptable, but a comment or `panic` on failure would make the assumption explicit. |
| `cmd/cards/portable.go` | `61–62` | medium | `exportJSONL` skips cards whose `GetCard` fails (`if err != nil { continue }`).  This can silently drop data from a backup. | Log the failure or fail the export rather than omitting cards. |

### 3. Duplicated / inconsistent helpers

| File | Line | Severity | Finding | Recommendation |
|------|------|----------|---------|----------------|
| `cmd/cards/serve.go` | `105–111` | low | `countHooks` is defined in `serve.go`, but `runExtensionsCmd` in `extensions.go:57–70` reimplements the same count-and-list loop inline. | Export/call `countHooks` from `extensions.go` to remove duplication. |
| `cmd/cards/extensions.go` | `23–26` | low | `stringSlice` (flag.Value for repeatable `--param`) duplicates the concept behind `internal/cli.FlagSet.StringArr`. | Reuse `cli.FlagSet` or move a shared `StringSlice` value type to `internal/cli` so `cmd/cards` and future consumers share it. |
| `cmd/cards/main.go` | `66–106` | low | `peelGlobals`, `hasPrefix`, `val`, and `splitEq` are a bespoke parser for the global flags.  They overlap with `internal/cli.FlagSet` parsing (e.g. `--flag=val` splitting). | Consider using `cli.FlagSet` for a single-pass parse, or at least keep the helper names aligned. |
| `cmd/cards/serve.go`, `cmd/cards/extensions.go`, `cmd/cards/import.go`, `cmd/cards/export.go`, `cmd/cards/directcli.go`, `cmd/cards/serve.go:mcpCmd` | many | medium | The same workspace-load + SQLite open + service-build sequence is repeated in `serveCmd`, `mcpCmd`, `runExtensionsCmd`, `importCmd`, `exportCmd`, and `newDirectBackend`. | Introduce an `openWorkspace(dir) (*sqlite.Store, *core.Service, *config.Result, error)` helper in `cmd/cards` to centralise setup and reduce drift. |

### 4. Logging / output-style inconsistencies

| File | Line | Severity | Finding | Recommendation |
|------|------|----------|---------|----------------|
| `cmd/cards/serve.go` | `44–46`, `56`, `89–101` | low | Operational messages use `log.Printf` (default logger writes to stderr).  This is fine for a server mode but mixes stderr logging with stdout UI. | Route operational messages through an explicit logger or keep the current convention but document it; ensure CLI exit paths never rely on stdout for human-readable status. |
| `cmd/cards/extensions.go` | `64`, `66`, `69` vs `146`, `168`, `175` | low | `runExtensionsCmd` uses `log.Printf`, while `extensionsCmd` uses `fmt.Printf`/`fmt.Println` for user-facing output.  Same command family, two output conventions. | Pick one convention per command category (operational → log; user-facing → stdout/stderr) and make it consistent. |
| `cmd/cards/import.go` | `84` | n/a | Sends import summary to `os.Stderr` so stdout stays valid JSONL.  This is the right choice; call it out as a positive pattern rather than a problem. | Keep. |
| `cmd/cards/export.go` | `73` | n/a | Same as import: summary to `os.Stderr`, data to stdout.  Keep. | Keep. |
| `cmd/cards/init.go` | `46–53` | n/a | Uses `fmt.Println` for post-init instructions on stdout.  Reasonable because stdout is the intended human-readable notice. | Keep, but consider adding a `--quiet` flag for init too. |

### 5. CLI behaviour / correctness nits

| File | Line | Severity | Finding | Recommendation |
|------|------|----------|---------|----------------|
| `internal/cli/client.go` | `169`, `224` | medium | `Client.Print` accepts an `idPath` for `--quiet` mode, but `idOf` only looks up a top-level map key.  `cmdTakeNext` passes `"card.id"` (commands.go:282), which will never match, so `--quiet take-next` falls back to printing the whole JSON blob instead of the card id. | Either implement dotted-path lookup in `idOf`, or have `take-next` extract the id manually and print it. |
| `internal/cli/commands.go` | `172` | medium | `cmdPatch` writes `body["tags"] = *tags` when `tags != nil`.  Because `fs.StringArr` always returns a non-nil pointer (even for an empty default), an unset `--tag` produces `"tags": null` in the request body.  This is inconsistent with `cmdCreate` (commands.go ~125) which checks `len(*tags) > 0`. | Mirror `cmdCreate` and only include `tags` when `len(*tags) > 0`. |
| `internal/cli/commands.go` | `514–532` | low | `boards show <board_id>` returns the entire workspace because there is no per-board API endpoint.  The comment is honest, but the UX is misleading. | Add `/v1/workspace?board_id=…` or a dedicated `/v1/boards/{id}` endpoint; until then, warn the user that only workspace-level introspection exists. |
| `cmd/cards/main.go` | `25–31` | low | `peelGlobals` only strips global flags from the *front* of the command line.  A user typing `cards list --json` will fail because `--json` is not a `list` flag.  The help text says global flags may appear before the subcommand, but many CLIs accept them anywhere. | Either document the position restriction more loudly or parse globals in a pre-pass that scans the whole slice (before positional args). |
| `cmd/cards/main.go` | `121–122` | low | The command lookup loop calls `cli.Commands()` on every iteration; it should be called once before the loop. | Cache the slice: `cmds := cli.Commands()`. |
| `cmd/cards/serve.go` | `92–99` | low | When `--run-extensions` is used, the hook supervisor goroutine is launched and then `ListenAndServe` blocks.  If `ListenAndServe` errors immediately, the supervisor keeps running until interrupted. | Tie the supervisor lifecycle to `httpSrv` shutdown (e.g. `httpSrv.RegisterOnShutdown`) or use a shared `context.Context`. |

### 6. HTTP API / UI observations (not defects)

| File | Line | Severity | Finding | Recommendation |
|------|------|----------|---------|----------------|
| `internal/httpapi/httpapi.go` | `751–810` | n/a | The idempotency recorder returns `200` for replays and preserves the original body.  This matches docs/SPEC.md §10. | Good; keep. |
| `internal/httpapi/httpapi.go` | `647–653` | n/a | SSE slow-consumer path writes `: dropped, reconnect` and returns.  This matches the spec intent. | Good; keep. |
| `internal/httpapi/httpapi.go` | `986–1000` | n/a | `uiCreateCard` coerces `field:number` values with `strconv.ParseFloat`.  Consistent with the JSON API. | Good. |

---

## Recommended cleanup priority

1. **Fix quiet-mode `take-next` id extraction** — user-visible bug (`internal/cli/client.go:224` / `internal/cli/commands.go:282`).
2. **Fix `cmdPatch` tags handling** — request-body inconsistency risk (`internal/cli/commands.go:172`).
3. **Centralise workspace load/store open wiring** — large duplication across `cmd/cards` and the biggest source of future drift.
4. **Stop silently dropping errors in UI/SSE/export** — at minimum log; for export, consider failing hard.
5. **Remove dead `actorKey` identifier** and redundant `containsBoard` wrapper.
6. **Standardise output channel conventions** between server log output and CLI user output (documentation or a tiny writer helper).

---

## Evidence

- `go test ./internal/httpapi/... ./internal/cli/... ./cmd/cards/...` → passed (cached).
- `go vet ./internal/httpapi/... ./internal/cli/... ./cmd/cards/...` → clean.
- Working tree is otherwise clean (see `inventory.md` §3); this review file is the only new artefact.
