package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/core"
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
	withArtifacts := fs.Bool("with-artifacts", false, "restore artifact bytes from the artifacts/ dir beside --in (sha256-verified)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("--workspace is required")
	}
	if *format != "jsonl" {
		return fmt.Errorf("unsupported format %q (only jsonl)", *format)
	}
	if *withArtifacts && *in == "" {
		return fmt.Errorf("--with-artifacts requires --in (artifact bytes are read from beside the input file)")
	}
	abs, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}

	st, _, result, err := openWorkspace(abs)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()

	// Pre-flight: import restores into a fresh DB. Refuse a non-empty workspace
	// rather than risk a partial overwrite of existing cards.
	if existing, err := st.ListCards(ctx, core.CardQuery{Limit: 1}); err != nil {
		return fmt.Errorf("check workspace: %w", err)
	} else if len(existing.Items) > 0 {
		return fmt.Errorf("workspace already contains cards; import restores into a fresh DB. Remove %s (and -wal/-shm) to re-import", dbPath(abs))
	}

	// Restore artifact bytes first, so a bad bundle fails loudly with the
	// DB untouched (content-addressed blobs from a partial restore are
	// harmless; partial card state is not).
	blobs := 0
	if *withArtifacts {
		absIn, err := filepath.Abs(*in)
		if err != nil {
			return err
		}
		// No pre-check that artifacts/ exists beside the input: a snapshot
		// with zero artifact pointers needs no directory, and when pointers
		// ARE present a missing blob fails loudly per-ref in restoreArtifacts.
		srcRoot := filepath.Join(filepath.Dir(absIn), "artifacts")
		f, err := os.Open(*in)
		if err != nil {
			return fmt.Errorf("open input file: %w", err)
		}
		am, err := artifacts.New(artifactsRoot(abs))
		if err != nil {
			f.Close()
			return err
		}
		blobs, err = restoreArtifacts(f, result.CardTypes, srcRoot, am)
		f.Close()
		if err != nil {
			return err
		}
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

	fmt.Fprintf(os.Stderr, "imported: %d cards, %d events, %d comments, %d links, %d users",
		stats.Cards, stats.Events, stats.Comments, stats.Links, stats.Users)
	if *withArtifacts {
		fmt.Fprintf(os.Stderr, ", %d artifact blobs", blobs)
	}
	fmt.Fprintln(os.Stderr)
	return nil
}
