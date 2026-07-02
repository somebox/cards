package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// exportCmd dumps all card data (cards, events, comments, links, users) as
// JSONL to stdout (or a file). This is the portable backup format — commit
// it alongside definitions/ to make the full workspace state git-portable.
// Reads directly from SQLite; no server needed.
func exportCmd(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "workspace directory (contains definitions/)")
	out := fs.String("out", "", "output file (default: stdout)")
	format := fs.String("format", "jsonl", "output format: jsonl (default)")
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

	// Pre-flight: export reads an existing DB; opening would create an empty one.
	if _, err := os.Stat(dbPath(abs)); err != nil {
		return fmt.Errorf("no work-cards.db in workspace (run 'cards serve' first): %w", err)
	}
	st, _, result, err := openWorkspace(abs)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()

	// Write to stdout or file.
	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer f.Close()
		w = f
	}

	stats, err := exportJSONL(ctx, st, w, result.Workspace)
	if err != nil {
		return err
	}

	// Summary to stderr (so stdout stays clean JSONL).
	fmt.Fprintf(os.Stderr, "exported: %d cards, %d events, %d comments, %d links, %d users\n",
		stats.Cards, stats.Events, stats.Comments, stats.Links, stats.Users)
	return nil
}
