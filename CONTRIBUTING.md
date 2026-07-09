# Contributing

Cards keeps engineering rules close to the contracts they protect. This file is
the short contributor-facing checklist for code quality and style.

## Required Standards

- Keep the core small. Put domain behavior in the shared service/core layer;
  HTTP, CLI, MCP, and the UI are adapters over the same model.
- Preserve explicit contracts: schema-defined fields, structured `core.Error`
  responses, card `version` checks, and idempotent writes.
- Follow existing package boundaries, helpers, and tests before adding new
  abstractions. Avoid new dependencies unless they clearly reduce maintenance
  and fit the boring-tech philosophy.
- Format Go with `gofmt`; use `goimports` when imports change. Keep ordinary Go
  style: useful names, lower-case error strings, wrapped errors with context,
  and standard `testing` patterns.
- Add or update focused tests for behavior changes. Treat CI and the contract
  tests in `internal/docaudit`, `internal/themecss`, and `tests/js` as repo
  standards, not optional checks.
- For UI work, follow [`docs/architecture/DESIGN.md`](docs/architecture/DESIGN.md):
  token-driven CSS, container-owned spacing, role-based typography, WYSIWYG edit
  controls, and CSS-only themes with no template branching.
- Keep JavaScript dependency-free unless that policy is intentionally changed.
  Assets are syntax-checked with Node and helper behavior is tested with
  `node:test`.
- Update docs with the code when API behavior, schemas, events, CLI behavior,
  UI contracts, or implemented/proposed status changes. Mark deliberate drift in
  [`docs/reference/INTEGRATOR-REFERENCE.md`](docs/reference/INTEGRATOR-REFERENCE.md).
- Keep changes focused. Do not mix unrelated refactors, tooling changes, and
  behavior changes in one review.

## Source of Truth

- Principles: [`docs/concepts/PHILOSOPHY.md`](docs/concepts/PHILOSOPHY.md)
- API and data contracts: [`docs/spec/SPEC.md`](docs/spec/SPEC.md)
- Architecture: [`docs/architecture/ARCHITECTURE.md`](docs/architecture/ARCHITECTURE.md)
- UI design system: [`docs/architecture/DESIGN.md`](docs/architecture/DESIGN.md)
- Built/proposed/drift audit: [`docs/reference/INTEGRATOR-REFERENCE.md`](docs/reference/INTEGRATOR-REFERENCE.md)
- Agent map: [`CLAUDE.md`](CLAUDE.md)

## Rituals

Build the binary once:

```bash
go build -o cards ./cmd/cards
```

Run the demo workspace with live reload:

```bash
scripts/dev-server.sh
open http://127.0.0.1:8787/ui/boards/engineering
```

Dogfood the server from the CLI in a second terminal:

```bash
export CARDS_URL=http://127.0.0.1:8787
export CARDS_USER=me
./cards create --type task --title "Test the feature" --status todo
./cards list
./cards patch <id> --status doing --version 1
./cards comment add <id> --body "works on my machine"
```

Validate before committing:

```bash
go vet ./...
go build ./...
go test ./...
go test -race -count=1 ./...
golangci-lint run
node --check internal/httpapi/templates/assets/*.js
node --test "tests/js/*.test.cjs"
```

For UI/template/CSS work, at minimum run:

```bash
go test ./internal/httpapi ./internal/docaudit ./internal/themecss
node --test "tests/js/*.test.cjs"
```

Track and share the demo board state in git:

The project's own dev tasks live in the demo workspace at
`examples/demo-workspace`. The live SQLite database (`work-cards.db`) is
machine-local and gitignored; the committed, portable state is `backlog.jsonl`.
Export before committing so cards, statuses, and comments are tracked in git
and shared with other contributors:

```bash
scripts/board.sh export   # writes examples/demo-workspace/backlog.jsonl
```

Load that state back into a fresh workspace:

```bash
scripts/board.sh import   # loads backlog.jsonl into a new work-cards.db
```

Install a pre-commit hook to export the board automatically on each commit:

```bash
scripts/board.sh install-hook
```

Cut a release (maintainers): move the `Unreleased` section in
[`CHANGELOG.md`](CHANGELOG.md) under a new `## [X.Y.Z]` heading, commit, then:

```bash
git tag vX.Y.Z && git push origin vX.Y.Z
```

The release workflow builds cross-platform archives and publishes a GitHub
Release. See [`README.md`](README.md) for more examples.

Templates and CSS are embedded in the Go binary. Use `scripts/dev-server.sh` for
UI review loops and hard-refresh the browser when reviewing style changes.
