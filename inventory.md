# Inventory — `/home/user/src/cards`

Generated: 2026-07-02
Source-language scan: primarily Go (`*.go`); no `*.ts`/`*.tsx`/`*.jsx`/`*.py`/`*.js`
files are present in the tree. Markdown, JSON, HTML/CSS, YAML, SQL, and shell
files are also listed where relevant.

Working tree: clean (no staged, unstaged, or untracked changes per `git status -uall`).
Branch: `main`, up to date with `origin/main`.

---

## 1. TODO / FIXME / debt markers in source

| File | Line | Marker | Snippet |
|------|------|--------|---------|
| `internal/artifacts/artifacts.go` | 11 | `TODO` | `// TODO: content-addressed or per-card subdirs, sha256, mime sniff,` |
| `internal/artifacts/artifacts.go` | 12 | (continuation) | `// path-confinement validation for local policy.` |

No other `TODO`, `FIXME`, `XXX`, `HACK`, or `DEPRECATED` markers were found in
`*.go`, `*.md`, `*.json`, `*.yml`, `*.yaml`, `*.sql`, `*.html`, `*.css`, or
`*.sh` files inside the repo (excluding `.git/`, `.claude/`, and
`.code-quality-pipeline/`).

The single debt marker is a 2-line `TODO` block in `internal/artifacts` that
flags three follow-up items (content addressing, mime sniffing, path
confinement) for the local-artifact policy.

---

## 2. Debug / "smell" statements worth review

These are not hard debt markers, but they are candidates for a code-quality
sweep.

| File | Line | Pattern | Note |
|------|------|---------|------|
| `internal/mcp/mcp.go` | 466-467 | `var _ = log.Print` | "// silence unused import warnings if log is not otherwise used." — the `log` import is currently dead. Either remove the import or use it. |
| `internal/core/observer_test.go` | 55 | `panic("observer boom")` | Test-only panic used to assert observer panic-isolation. Intentional; not a leak. |
| `cmd/cards/extensions.go` | 146, 175 | `fmt.Println` | Legitimate CLI output. |
| `cmd/cards/init.go` | 46-53 | `fmt.Printf` / `fmt.Println` | Legitimate CLI output (post-init instructions). |
| `cmd/cards/serve.go` | 44, 46, 56, 89-101 | `log.Printf` | Operational logging on serve path. |
| `cmd/cards/extensions.go` | 64, 66, 69 | `log.Printf` | Operational logging on extension supervise path. |
| `internal/cli/client.go` | 178, 186, 190, 196, 199, 208, 216, 219 | `fmt.Println` | CLI response rendering. |

No `panic("not implemented")`, no `//nolint` directives, no `pprof` debug
imports, and no stray `t.Logf` debug noise in tests were observed.

---

## 3. Untracked / ignored runtime artefacts (not in the tree)

`git status` reports the working tree as clean. The following files are
present on disk but excluded by `.gitignore` (and are NOT staged or unstaged
changes). They are listed here so a parallel reviewer does not pick them up
as "modified source".

| Path | Size | Reason (per `.gitignore`) |
|------|------|---------------------------|
| `/home/user/src/cards/cards` | 20 MB | Build output (`/cards`). |
| `/home/user/src/cards/work-cards.db` | 0 B | `*.db` — empty stub created locally. |
| `/home/user/src/cards/examples/demo-workspace/work-cards.db` | 872 KB | Explicitly ignored demo db. |
| `/home/user/src/cards/examples/demo-workspace/work-cards.db-shm` | 32 KB | SQLite WAL/SHM sidecars. |
| `/home/user/src/cards/examples/demo-workspace/work-cards.db-wal` | 1.3 MB | SQLite WAL/SHM sidecars. |
| `/home/user/src/cards/.claude/` | dir | Tool config, ignored. |
| `/home/user/src/cards/.code-quality-pipeline/` | dir | Empty pipeline scratch dir. |

No stray editor backups (`*.bak`, `*.orig`, `*.swp`, `*.swo`, `*~`,
`*.rej`, `*.patch`, `*.diff`, `*.tmp`, `*.scratch`, `*.draft`) were found.

---

## 4. Repo layout and proposed parallel review areas

The repository is a single Go module (`go.mod`) implementing a card/board
workspace service (CLI + HTTP + MCP + SQLite + OpenAPI + embedded assets +
docs). The natural fault lines for parallel review are the top-level Go
package boundaries and the supporting documentation / examples.

### Area A — `cmd/cards/` (CLI entry points)
- `cmd/cards/main.go`, `serve.go`, `init.go`, `workspace.go`, `extensions.go`,
  `import.go`, `export.go`, `portable.go`, `directcli.go`
- `cmd/cards/portable_test.go`, `cmd/cards/directcli_test.go`
- Touches: workspace bootstrap, hook supervisor, serve loop, subcommands.
- Debt noted: none in source; many `log.Printf` / `fmt.Println` call-sites
  to confirm output style is consistent.

### Area B — `internal/artifacts/`
- `internal/artifacts/artifacts.go`
- Touches: artifact Manager skeleton; the lone repo-wide `TODO` block lives
  here (content addressing, mime sniff, path confinement).
- Debt noted: explicit `TODO` lines 11-12.

### Area C — `internal/cli/`
- `internal/cli/client.go`, `internal/cli/commands.go`
- Touches: client-side CLI transport + command definitions.

### Area D — `internal/config/`
- `internal/config/config.go`, `config_test.go`, `extensions.go`, `io.go`,
  `time.go`
- Touches: workspace config loading, extensions, time helpers.

### Area E — `internal/core/` (domain + events)
- `internal/core/types.go`, `service.go`, `store.go`, `bus.go`, `events.go`,
  `errors.go`
- Tests: `service_test.go`, `bus_test.go`, `events_test.go`,
  `boardevent_test.go`, `wip_test.go`, `observer_test.go`,
  `eventlog_conformance_test.go`
- Test infrastructure: `internal/core/eventlogtest/eventlogtest.go`,
  `internal/core/eventlogtest/recorder.go`
- Fixtures: `internal/core/testdata/events/*.json` (13 golden files)
- Touches: domain types, service, store, event bus, event log conformance
  test harness, observer panic-isolation test.

### Area F — `internal/hooks/`
- `internal/hooks/hooks.go`, `hooks_test.go`
- Touches: extension hook supervision.

### Area G — `internal/httpapi/`
- `internal/httpapi/httpapi.go`, `feed_test.go`, `sse_test.go`,
  `httpapi_test.go`
- Templates: `internal/httpapi/templates/*.html` (10 files) + `style.css`
- Touches: HTTP API server, embed.FS template loading, SSE feed, board/card
  UI rendering.

### Area H — `internal/mcp/`
- `internal/mcp/mcp.go`, `mcp_test.go`
- Touches: MCP JSON-RPC server.
- Debt noted: dead `log` import kept alive by `var _ = log.Print`
  (mcp.go:466-467).

### Area I — `internal/openapi/`
- `internal/openapi/openapi.go`, `openapi_test.go`
- Touches: generated/served OpenAPI spec.

### Area J — `internal/seed/`
- `internal/seed/seed.go`

### Area K — `internal/sqlite/`
- `internal/sqlite/sqlite.go`, `sqlite_test.go`,
  `eventlog_conformance_test.go`, `eventscope_test.go`
- Touches: SQLite-backed event log; conformance test runs against the real
  store; recent events-seam work (migrations, scope column, idx_events_scope)
  per `git log` (commits 8c8a5cc, 35a193a, 1cf17e5, etc.).

### Area L — `internal/starter/`
- `internal/starter/starter.go`, `starter_test.go`, `embed.go`
- Assets: `internal/starter/assets/definitions/{boards,card-types,workspace}.json`

### Area M — `docs/` (documentation set)
- 14 markdown files: `SPEC.md`, `ARCHITECTURE.md`, `DESIGN.md`,
  `EVENTS.md`, `DEVELOPER-REFERENCE.md`, `INTEGRATOR-REFERENCE.md`,
  `INTEGRATION.md`, `EXTENSIONS.md`, `MCP.md`, `LIFECYCLE-EXAMPLES.md`,
  `CONCEPTS.md`, `PHILOSOPHY.md`, `NOTES.md`, `SLICE3-REFLECTION.md`.
- No `TODO`/`FIXME` markers found inside any of them.

### Area N — `examples/demo-workspace/`
- `definitions/boards/{engineering,welcome}.json`,
  `definitions/card-types/{programming-task,research-goal,task}.json`,
  `definitions/extensions.json`, `definitions/workspace.json`,
  `.cards/ext/notify.sh`
- Plus ignored runtime db artefacts listed in §3.

### Area O — Root / config / CI
- `go.mod`, `go.sum`, `.gitattributes`, `.gitignore`, `.golangci.yml`,
  `README.md`, `.github/workflows/ci.yml`, `.claude/settings.local.json`.

### Area P — `media/`
- `media/board.png`, `media/ui/*.png` — UI screenshots; no source
  significance for code review.

---

## 5. Suggested parallel-review slicing

The cleanest split for a fan-out reviewer pool is along the package
boundaries above. For this repo specifically, the highest-signal areas
(based on the actual debt and the in-flight "events" work in `git log`)
are:

1. **Area E (`internal/core/`)** + **Area K (`internal/sqlite/`)** —
   the events work spans both; treat as one logical review.
2. **Area B (`internal/artifacts/`)** — only place with a real `TODO`.
3. **Area H (`internal/mcp/`)** — small surface, but the only place
   with a flagged code smell (`var _ = log.Print`).
4. **Area G (`internal/httpapi/`)** — large surface, templates + SSE.
5. **Area A (`cmd/cards/`)** — CLI surface; many `log.Printf` /
   `fmt.Println` sites worth a single consistency pass.
6. **Area M (`docs/`)** — review in one pass (no debt markers; just
   freshness / accuracy).
7. **Areas C, D, F, I, J, L, N, O, P** — smaller, independent; each
   can be reviewed in isolation.

## Issues encountered

- None blocking. The spec asked for a debt scan + a temp-file scan +
  area proposal. The repo is unusually clean: one `TODO` block, one
  `var _ = log.Print` smell, no editor backup files, and the working
  tree is clean (no staged or unstaged changes). All ignored runtime
  artefacts (binary, dbs) are already covered by `.gitignore`, so they
  are not "untracked" in the `git status` sense — they are listed in
  §3 strictly so a reviewer does not mistake them for pending changes.
