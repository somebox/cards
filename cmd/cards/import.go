package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
)

// importCmd is the inverse of exportCmd: it reads a JSONL export (from stdin or
// a file) and loads users, cards (with embedded comments + links), and the
// event log into the workspace's SQLite DB. Reads directly from SQLite; no
// server needed.
//
// This is a full-snapshot restore for backup/migration/disaster-recovery — the
// counterpart to `cards export`. It targets a fresh/empty workspace and refuses
// to run against one that already holds cards, so it never silently merges into
// or overwrites live state (SPEC §3). The version-gated, per-file PATCH import
// (`cards import --mirror`, NOTES.md D13) is a separate future mode.
func importCmd(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "workspace directory (contains definitions/)")
	in := fs.String("in", "", "input file (default: stdin)")
	format := fs.String("format", "jsonl", "input format: jsonl (default)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("--workspace is required")
	}
	if *format != "jsonl" {
		return fmt.Errorf("unsupported format %q (only jsonl)", *format)
	}
	abs, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}

	// Load definitions so the store knows the workspace schema.
	loader := config.New(abs)
	result, err := loader.Load()
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}

	dbPath := filepath.Join(abs, "work-cards.db")
	st, err := sqlite.Open(dbPath, result.Workspace)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer st.Close()

	ctx := context.Background()

	// Pre-flight: import restores into a fresh DB. Refuse a non-empty workspace
	// rather than risk a partial overwrite of existing cards.
	if existing, err := st.ListCards(ctx, core.CardQuery{Limit: 1}); err != nil {
		return fmt.Errorf("check workspace: %w", err)
	} else if len(existing.Items) > 0 {
		return fmt.Errorf("workspace already contains cards; import restores into a fresh DB. Remove %s (and -wal/-shm) to re-import", dbPath)
	}

	// Read from stdin or file.
	r := os.Stdin
	if *in != "" {
		f, err := os.Open(*in)
		if err != nil {
			return fmt.Errorf("open input file: %w", err)
		}
		defer f.Close()
		r = f
	}

	stats, err := importJSONL(ctx, st, r)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "imported: %d cards, %d events, %d comments, %d links, %d users\n",
		stats.Cards, stats.Events, stats.Comments, stats.Links, stats.Users)
	return nil
}
