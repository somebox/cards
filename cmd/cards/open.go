// Command cards — workspace composition root. Every subcommand that needs a
// live workspace goes through openWorkspace, so the load → open → service
// sequence exists exactly once (and cmd/cards stays the only package that
// knows the concrete sqlite store).
package main

import (
	"fmt"
	"path/filepath"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
)

// dbPath is the SQLite file location inside a workspace dir.
func dbPath(dir string) string { return filepath.Join(dir, "work-cards.db") }

// openWorkspace loads the definitions in dir and opens its store + service.
// Callers own resolving/initializing dir beforehand and closing the returned
// store afterwards.
func openWorkspace(dir string) (*sqlite.Store, *core.Service, *config.Result, error) {
	result, err := config.New(dir).Load()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load workspace: %w", err)
	}
	st, err := sqlite.Open(dbPath(dir), result.Workspace)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open store: %w", err)
	}
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, st)
	return st, svc, result, nil
}
