package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
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

	// Load definitions (needed for the store to know the workspace schema).
	loader := config.New(abs)
	result, err := loader.Load()
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}

	dbPath := filepath.Join(abs, "work-cards.db")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("no work-cards.db in workspace (run 'cards serve' first): %w", err)
	}
	st, err := sqlite.Open(dbPath, result.Workspace)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
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

	enc := json.NewEncoder(w)
	enc.SetIndent("", "")
	enc.SetEscapeHTML(false)

	// Header — identifies the export format + workspace.
	header := map[string]any{
		"type":         "export",
		"version":      1,
		"workspace_id": result.Workspace.ID,
		"workspace":    result.Workspace.Name,
	}
	if err := enc.Encode(header); err != nil {
		return err
	}

	// Users
	users, err := st.ListUsers(ctx)
	if err != nil {
		return fmt.Errorf("export users: %w", err)
	}
	for _, u := range users {
		if err := enc.Encode(map[string]any{"type": "user", "data": u}); err != nil {
			return err
		}
	}

	// Cards (with links + comments loaded via GetCard)
	page, err := st.ListCards(ctx, core.CardQuery{Limit: 10000})
	if err != nil {
		return fmt.Errorf("export cards: %w", err)
	}
	commentCount := 0
	linkCount := 0
	for _, c := range page.Items {
		full, err := st.GetCard(ctx, c.ID)
		if err != nil {
			continue
		}
		commentCount += len(full.Comments)
		linkCount += len(full.Links)
		if err := enc.Encode(map[string]any{"type": "card", "data": full}); err != nil {
			return err
		}
	}

	// Events (all — full audit log)
	evs, err := st.ListEvents(ctx, core.EventQuery{Limit: 100000})
	if err != nil {
		return fmt.Errorf("export events: %w", err)
	}
	for _, e := range evs {
		if err := enc.Encode(map[string]any{"type": "event", "data": e}); err != nil {
			return err
		}
	}

	// Summary to stderr (so stdout stays clean JSONL).
	fmt.Fprintf(os.Stderr, "exported: %d cards, %d events, %d comments, %d links, %d users\n",
		len(page.Items), len(evs), commentCount, linkCount, len(users))
	return nil
}
