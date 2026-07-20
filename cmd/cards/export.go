package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/somebox/cards/internal/artifacts"
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
	stateOnly := fs.Bool("state-only", false, "omit the event journal — canonical card-state export (events are SQLite-owned)")
	withArtifacts := fs.Bool("with-artifacts", false, "also copy referenced artifact bytes into an artifacts/ dir beside --out (sha256-verified bundle)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("--workspace is required")
	}
	if *format != "jsonl" {
		return fmt.Errorf("unsupported format %q (only jsonl)", *format)
	}
	if *withArtifacts && *out == "" {
		return fmt.Errorf("--with-artifacts requires --out (artifact bytes are written beside the output file)")
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

	stats, err := exportJSONL(ctx, st, w, result.Workspace, *stateOnly)
	if err != nil {
		return err
	}

	blobs := 0
	if *withArtifacts {
		am, err := artifacts.New(artifactsRoot(abs))
		if err != nil {
			return err
		}
		absOut, err := filepath.Abs(*out)
		if err != nil {
			return err
		}
		blobs, err = exportArtifacts(ctx, st, result.CardTypes, am, filepath.Join(filepath.Dir(absOut), "artifacts"))
		if err != nil {
			return err
		}
	}

	// Summary to stderr (so stdout stays clean JSONL).
	fmt.Fprintf(os.Stderr, "exported: %d cards, %d events, %d comments, %d links, %d users",
		stats.Cards, stats.Events, stats.Comments, stats.Links, stats.Users)
	if *withArtifacts {
		fmt.Fprintf(os.Stderr, ", %d artifact blobs", blobs)
	}
	fmt.Fprintln(os.Stderr)
	return nil
}
