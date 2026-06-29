// Command cards — workspace resolution shared by serve/mcp/init. Mirrors how
// git locates a repo: an explicit path wins, otherwise the nearest .cards/
// walking up from the cwd, otherwise a global personal workspace.
package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
	"github.com/somebox/cards/internal/starter"
)

// resolveWorkspaceDir picks the workspace root. When explicit is non-empty it
// is used verbatim (absolute). Otherwise it discovers the nearest .cards/
// walking up from the cwd; failing that it returns the global personal
// workspace. autoInit is true only for the global fallback, signalling that the
// caller should scaffold + seed it if empty (an explicit or discovered path is
// expected to already be a workspace).
func resolveWorkspaceDir(explicit string) (dir string, autoInit bool, err error) {
	if explicit != "" {
		abs, aerr := filepath.Abs(explicit)
		return abs, false, aerr
	}
	if found, ok := findDotCards(); ok {
		return found, false, nil
	}
	home, herr := globalHome()
	return home, true, herr
}

// findDotCards walks up from the cwd looking for a .cards/ directory that holds
// a workspace (definitions/workspace.json), the way git resolves .git/.
func findDotCards() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		cand := filepath.Join(cwd, ".cards")
		if fi, err := os.Stat(filepath.Join(cand, "definitions", "workspace.json")); err == nil && !fi.IsDir() {
			return cand, true
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			return "", false
		}
		cwd = parent
	}
}

// globalHome is the personal workspace location: $CARDS_HOME or ~/.cards.
func globalHome() (string, error) {
	if h := os.Getenv("CARDS_HOME"); h != "" {
		return filepath.Abs(h)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cards"), nil
}

// initWorkspace scaffolds starter definitions into dir and seeds the welcome
// board when dir is not already a workspace. Returns true when it created a new
// workspace, false when one already existed (no-op). Idempotent.
func initWorkspace(dir string) (bool, error) {
	created, err := starter.Scaffold(dir)
	if err != nil {
		return false, err
	}
	if !created {
		return false, nil
	}
	result, err := config.New(dir).Load()
	if err != nil {
		return false, err
	}
	st, err := sqlite.Open(filepath.Join(dir, "work-cards.db"), result.Workspace)
	if err != nil {
		return false, err
	}
	defer st.Close()
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, st)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := starter.SeedWelcome(ctx, st, svc, result.Workspace); err != nil {
		return false, err
	}
	return true, nil
}
