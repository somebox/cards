# Documentation / Examples / Root Config Review Findings

**Scope:** `docs/` (14 markdown files), `examples/demo-workspace/`, and root config (`go.mod`, `go.sum`, `.gitignore`, `.golangci.yml`, `README.md`, `.github/workflows/ci.yml`).

**Verification commands run:**
- `go build ./...` — passed
- `go vet ./...` — passed
- `go test -count=1 ./...` — all packages passed
- `go version` — `go1.26.4 linux/amd64`

No `TODO`/`FIXME` markers were found in docs. The overall doc set is in good shape, but several docs have drifted from the code or contain stale forward-looking statements that are now implemented.

---

## 1. CI / toolchain misalignment (config hygiene)

| Severity | File | Line | Finding |
|----------|------|------|---------|
| **warning** | `.github/workflows/ci.yml` | 19 | Test matrix includes Go `1.25`, but `go.mod` declares `go 1.26.4`. Since Go 1.21+ enforces the `go` directive as a minimum version, CI jobs on Go `1.25` will fail to build the module. Recommended fix: drop `1.25` and test on `1.26.4` and/or `1.27`. |
| **warning** | `.golangci.yml` | 21 | `errcheck.exclude-functions` references `(*github.com/foz/work-cards/internal/sqlite.Store).Close` — the old module path. The current module path is `github.com/somebox/cards`. Because of the path mismatch this exclusion is inert and `errcheck` may still complain about an ignored `Store.Close` if it is not handled elsewhere. |
| **note** | `.golangci.yml` | 33 | `install-mode: goinstall` is used in the workflow, which is acceptable for `golangci-lint-action@v6`, but relying on `version: latest` in CI can introduce unrelated lint failures on unrelated PRs. Pinning to a specific version is more hygienic. |

## 2. Stale / inaccurate statements about test seams and implementation status

| Severity | File | Line | Finding |
|----------|------|------|---------|
| **warning** | `docs/EVENTS.md` | §10 / line ~273 | States that `internal/core/events_test.go` does not exist and that the test fakes/fixtures from §10 are not built. In fact the file exists and contains `TestEventContracts_GoldenFixtures` and `TestNoRawEventLiterals`, and `internal/core/eventlogtest/` provides `Mem`, `Recorder`, and a `Conformance` suite. The doc should be updated or the caveats removed. |
| **warning** | `docs/EVENTS.md` | §12 Step 1 | Lists "Add test fakes + seam acceptance tests" as not yet done. The seam acceptance test exists via `internal/core/eventlog_conformance_test.go`. Update the status to reflect this. |
| **note** | `docs/SLICE3-REFLECTION.md` | Resolution summary | Says `INTEGRATION.md`'s fuller `card_ready`/`card_unblocked` design supersedes F1. `INTEGRATION.md` actually talks about `card_unblocked` (`card_ready` is not in the code or event catalog), so the reference is slightly imprecise. |

## 3. YAML support for workspace / card-type / board definitions is overstated

Multiple documents state that JSON *and* YAML are supported for workspace definitions, but the loader in `internal/config/config.go` only reads JSON for `workspace.json`, `card-types/*.json`, and `boards/*.json`. Only `definitions/extensions.{yaml,json}` is actually parsed (with a minimal inline YAML parser).

| Severity | File | Finding |
|----------|------|---------|
| **warning** | `docs/SPEC.md` §3 | "Definitions are JSON or YAML." This is only true for extensions; core definition files must be JSON today. |
| **warning** | `docs/ARCHITECTURE.md` | "Definitions are JSON or YAML. The loader normalizes both." The loader does not normalize YAML for workspace/card-types/boards. |
| **warning** | `docs/DEVELOPER-REFERENCE.md` §2 | "JSON vs YAML authoring: Both supported." Not accurate for `workspace.json`, `card-types/`, or `boards/`. |
| **warning** | `internal/config/config.go` package comment | "JSON/YAML files in definitions/" and "Reload-on-change is handled in serve mode" are both inaccurate: only JSON files are loaded for core definitions, and reload-on-change is not implemented. The package comment should match the comment on `Loader` ("JSON only, single context, no file watching, no merge"). |

## 4. Hook / extension contract drift

| Severity | File | Line | Finding |
|----------|------|------|---------|
| **warning** | `docs/EXTENSIONS.md` | §"Event contract for hooks" | The example event JSON on stdin shows `"board_ids": ["engineering"]`. The actual payload built in `internal/hooks/hooks.go:133-141` includes `id`, `type`, `card_id`, `actor`, `at`, `diff`, and `workspace_id`; there is no `board_ids` field. Either the code should emit the board(s) a card belongs to, or the doc should be corrected. |
| **warning** | `docs/EXTENSIONS.md` | §"Declaration" / §"CLI surface" | Implies `service` extensions with `autostart: true` are supervised by `cards run-extensions`. The current `runExtensionsCmd` (`cmd/cards/extensions.go`) only wires up `kind: hook`; `service` extensions are parsed but never started or supervised. This is accurately called out in `docs/INTEGRATOR-REFERENCE.md` §7, so the two docs contradict each other. |
| **note** | `examples/demo-workspace/.cards/ext/notify.sh` | line 5 | Comment reads `# ... SPEC EXTENSIONS.md.` — likely meant to be `See EXTENSIONS.md`. |

## 5. Event catalog / scope drift

| Severity | File | Line | Finding |
|----------|------|------|---------|
| **warning** | `docs/INTEGRATOR-REFERENCE.md` | §4 | Says "Today every event is card-scoped" and board-scoped events are only [proposed]. The code in `internal/core/types.go:223-230` has `Scope` and `BoardID` on `Event`, and `internal/core/wip_test.go` emits `wip_exceeded` / `wip_cleared` with `Scope: "board"` and `BoardID: "eng"`. Those are ephemeral signals, but they are board-scoped. |
| **warning** | `docs/INTEGRATOR-REFERENCE.md` | §4 | "Canonical enumeration (15 declared, 13 emitted today)" is stale. `internal/core/types.go:209-230` declares 17 constants (13 durable facts + `artifact_added` + `definition_reloaded` + `wip_exceeded` + `wip_cleared`). Update the count or remove it. |

## 6. Spec examples using unimplemented query parameters

| Severity | File | Line | Finding |
|----------|------|------|---------|
| **warning** | `docs/LIFECYCLE-EXAMPLES.md` | B6 | Uses `GET /v1/cards?board_id=fabrication&status=queued&updated_before=...` and `cards list --board fabrication --status queued --updated-before ...`. The list handler in `internal/httpapi/httpapi.go` and the CLI `list` command do not read any `updated_before`/`updated_after`/`created_before`/`created_after` parameter. `SPEC.md` §9 already correctly notes these are not implemented. This example contradicts that. |

## 7. Out-of-date design-decision notes

| Severity | File | Line | Finding |
|----------|------|------|---------|
| **note** | `docs/NOTES.md` | D5 status note | Claims `SPEC.md` and `DEVELOPER-REFERENCE.md` still mention an `If-Match` header alias and should be updated. Both docs already explicitly state that `If-Match` is not implemented and only body/query `version` is used. The D5 follow-up note can be removed or updated. |

## 8. Minor doc / code-comment inconsistencies

| Severity | File | Finding |
|----------|------|---------|
| **note** | `docs/ARCHITECTURE.md` | Recommends `gopkg.in/yaml.v3` as a dependency, but it is not in `go.mod` and is not used by the loader. Drop the recommendation or add the dependency if full YAML support is intended. |
| **note** | `docs/CONCEPTS.md` | The zero-config launch path (`cards` with no args serves `~/.cards`) is described as "Planned" but is implemented in `cmd/cards/main.go`. The doc should reflect current behavior. |
| **note** | `docs/DEVELOPER-REFERENCE.md` §9 `take-next` | Mentions `--filter-file ./filters/todo.json` in an example. This works, but there is no file checked in at `examples/demo-workspace/filters/todo.json`; the example is illustrative. Not a bug, just a note for users trying to copy-paste. |

## 9. Root `.gitignore` review

- Coverage is good: build output `/cards`, runtime DBs, SQLite WAL/SHM sidecars, editor files, `.claude/`, `.code-quality-pipeline/`, and `examples/demo-workspace/work-cards.db*` are all ignored.
- The tracked `examples/demo-workspace/.cards/ext/notify.sh` is intentionally committed, which is correct.

## 10. Summary / residual risks

What is correct and up to date:

- HTTP/CLI endpoint tables in `docs/INTEGRATOR-REFERENCE.md` match the router in `internal/httpapi/httpapi.go`.
- The actor-resolution and no-double-claim descriptions match the code.
- `release`, `force`, and `claim` semantics are documented consistently.
- Field-type catalog, universal envelope, and event `diff` descriptions match `internal/core/types.go`.
- README quick-start commands match current CLI behavior.

Residual risks / follow-up work not fixed here (this review only records findings):

1. CI may fail on the Go `1.25` matrix entry due to the `go 1.26.4` module requirement.
2. `.golangci.yml` contains an inert old-module-path exclusion.
3. Multiple docs claim YAML support for core definitions, which the loader does not implement.
4. `cards run-extensions` does not supervise `service` extensions as some docs claim.
5. Hook event payload doc omits/misstates the actual JSON schema.
6. `LIFECYCLE-EXAMPLES.md` example B6 cites an unimplemented time-range query.
7. `EVENTS.md` still claims acceptance test seams are missing even though they are built.

---

*Review completed on 2026-07-02.*
