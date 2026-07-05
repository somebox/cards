# Small-package review findings

**Scope:** `internal/artifacts/`, `internal/mcp/`, `internal/config/`, `internal/hooks/`, `internal/openapi/`, `internal/seed/`, `internal/starter/`  
**Generated:** 2026-07-02

## Summary

No blockers. The assigned small packages are mostly clean. The only hard debt marker is the single `TODO` already catalogued in the inventory; the remaining items are low-severity cleanup: two dead-import placeholders kept alive with `var _ = …`, one unused computed variable, one test-only helper duplication, and a conceptual overlap between JSON-schema builders in `mcp` and `openapi`.

No undocumented `fmt.Println`, `log.Print`, `console.log`, `panic("...")`, or editor/temp file artefacts were found inside the assigned packages.

## Findings

| ID | Severity | Location | Issue | Recommendation |
|----|----------|----------|-------|----------------|
| S1 | low / debt | `internal/artifacts/artifacts.go:11-12` | `Manager` is an empty struct with only a `TODO` comment flagging content addressing, SHA-256, MIME sniff, and path-confinement validation. The local artifact policy is therefore not enforced. | Convert this `TODO` into a tracked work item and implement the policy before artifact handling is exposed to untrusted paths. |
| S2 | cleanup | `internal/mcp/mcp.go:466-467` | Dead `"log"` import. The only reference to `log` is `var _ = log.Print` with a comment that says it exists only to silence unused-import warnings. | Remove the `"log"` import and the `var _ = log.Print` line. Re-import if/when real logging is needed. |
| S3 | cleanup | `internal/hooks/hooks.go:215-216` | Dead `"io"` import. The only reference to `io` is `var _ = io.EOF`. The comment on the preceding line says "strings import lives here", but the blank identifier is for `io.EOF`, so the comment is stale as well. | Remove the `"io"` import and the `var _ = io.EOF` line; update or delete the stale comment. |
| S4 | cleanup | `internal/seed/seed.go:36,75` | Unused variable: `now := time.Now().UTC().Format(time.RFC3339)` is computed, immediately discarded with `_ = now`, and the formatted timestamp is never referenced in the demo cards. | Remove the line (and the blank assignment) or wire it into the seeded card data if it was intended for a `created_at`/notes field. |
| S5 | cleanup | `internal/hooks/hooks_test.go:191-198` | Redundant test helpers `contains` and `indexOf` duplicate the standard library (`strings.Contains` and `strings.Index`). | Import `strings` in the test file and delete the custom helpers. |
| S6 | observation | `internal/mcp/mcp.go` + `internal/openapi/openapi.go` | Both packages define a `fieldSchema` helper that maps `core.FieldDef` to JSON Schema. The signatures and behaviour differ (`mcp` takes a `required bool` and handles `x-required`, `openapi` does not), so this is conceptual duplication rather than a copy-paste bug. | Defer to a product/architecture decision. If a shared schema-building package is introduced later, consolidate these two helpers to reduce drift. |

## Verified clean

- `internal/config/` – no `TODO`s, no stray print statements, no dead imports in the files reviewed.
- `internal/openapi/` – no `TODO`s or debug statements; all helpers are exercised by tests.
- `internal/starter/` – no `TODO`s or debug statements; starter assets are embedded cleanly.
- No ignored/staged temp files relate to these packages.

## Commands run

```bash
go test ./internal/artifacts ./internal/mcp ./internal/config \
  ./internal/hooks ./internal/openapi ./internal/seed ./internal/starter
```

Result: all packages passed (cached for most; hooks took ~1.5s).

---

```acceptance-report
{
  "criteriaSatisfied": [
    {
      "id": "criterion-1",
      "status": "satisfied",
      "evidence": "Created only the requested issues-small.md file with findings scoped to the assigned packages; no source-code changes were made."
    }
  ],
  "changedFiles": [
    "issues-small.md"
  ],
  "testsAddedOrUpdated": [],
  "commandsRun": [
    {
      "command": "go test ./internal/artifacts ./internal/mcp ./internal/config ./internal/hooks ./internal/openapi ./internal/seed ./internal/starter",
      "result": "passed",
      "summary": "All assigned packages pass; hooks package took ~1.5s, others cached."
    }
  ],
  "validationOutput": [
    "No blockers found in assigned packages.",
    "One TODO (artifacts), two dead-import placeholders (mcp, hooks), one unused variable (seed), one duplicated test helper (hooks), one conceptual fieldSchema overlap (mcp/openapi)."
  ],
  "residualRisks": [
    "Findings are low-severity cleanup; the artifacts TODO describes unimplemented policy that should be addressed before artifact upload is exposed to untrusted input."
  ],
  "noStagedFiles": true,
  "diffSummary": "Added issues-small.md documenting small-package review findings; no source files modified.",
  "reviewFindings": [
    "internal/artifacts/artifacts.go:11-12 - TODO: content-addressed subdirs / sha256 / mime sniff / path confinement not implemented",
    "internal/mcp/mcp.go:466-467 - dead log import kept alive by var _ = log.Print",
    "internal/hooks/hooks.go:215-216 - dead io import kept alive by var _ = io.EOF; stale comment",
    "internal/seed/seed.go:36,75 - unused now variable computed and discarded",
    "internal/hooks/hooks_test.go:191-198 - contains/indexOf duplicate strings.Contains/strings.Index",
    "internal/mcp/mcp.go + internal/openapi/openapi.go - conceptual duplication of fieldSchema helpers (defer to architecture)"
  ],
  "manualNotes": "Working tree was clean before this change except for the new issues-small.md file. No source edits were performed per the narrow scope of the task."
}
```
