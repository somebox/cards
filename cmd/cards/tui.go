// Command cards — bare-`cards` TUI entry point. With no subcommand and an
// interactive terminal, `cards` opens the TUI against the resolved workspace
// (same resolution + in-process service as the serverless CLI backend).
// Non-interactive callers (piped stdout/stdin, scripts, agents) still get
// the usage text, so the bare invocation stays script-safe.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/somebox/cards/internal/cli"
	"github.com/somebox/cards/internal/tui"
)

// isTerminal reports whether f is a character device (an interactive TTY).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// interactive reports whether `cards` should open the TUI: both streams are
// terminals and the caller didn't ask for machine output (--json/--jsonl).
func interactive(cfg cli.Config) bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stdout) && !cfg.JSON && !cfg.JSONL
}

// tuiCmd resolves the workspace (same precedence as the direct backend),
// opens it, and runs the TUI.
func tuiCmd(cfg cli.Config) error {
	dir := cfg.Workspace
	if dir == "" {
		dir = os.Getenv("CARDS_WORKSPACE")
	}
	d, autoInit, err := resolveWorkspaceDir(dir)
	if err != nil {
		return err
	}
	if autoInit {
		if _, err := initWorkspace(d); err != nil {
			return fmt.Errorf("initialize workspace: %w", err)
		}
	}
	st, svc, result, err := openWorkspace(d)
	if err != nil {
		return fmt.Errorf("workspace %s: %w", d, err)
	}
	defer st.Close()

	actor := cfg.As
	if actor == "" {
		actor = os.Getenv("CARDS_USER")
	}
	if actor == "" {
		actor = os.Getenv("USER")
	}
	if actor == "" && result.Workspace != nil {
		actor = result.Workspace.Settings.DefaultUser
	}

	return tui.Run(context.Background(), svc, result, actor)
}
