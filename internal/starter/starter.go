// Package starter bootstraps a fresh personal workspace: it writes a minimal
// set of definitions (a "task" type and a "welcome" board) and seeds an
// onboarding board whose cards are the tutorial. Used by `cards init` and by
// the zero-config serve path when falling back to the global workspace.
package starter

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/somebox/cards/internal/core"
)

// Scaffold writes the starter definitions into root/definitions. It is a no-op
// (returns false) when root already contains a definitions/workspace.json, so
// it never clobbers an existing workspace. Returns true when it created files.
func Scaffold(root string) (bool, error) {
	if _, err := os.Stat(filepath.Join(root, "definitions", "workspace.json")); err == nil {
		return false, nil
	}
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		return false, err
	}
	walkErr := fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(root, path)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(sub, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if walkErr != nil {
		return false, walkErr
	}
	return true, nil
}

// SeedWelcome creates the onboarding cards when the workspace has none. Like
// Scaffold, it is idempotent: an existing workspace with any card is left alone.
func SeedWelcome(ctx context.Context, st core.Store, svc *core.Service, ws *core.Workspace) error {
	page, err := st.ListCards(ctx, core.CardQuery{Limit: 1})
	if err != nil {
		return fmt.Errorf("check existing cards: %w", err)
	}
	if len(page.Items) > 0 {
		return nil
	}
	for _, c := range welcomeCards(ws.Settings.DefaultUser) {
		if _, err := svc.CreateCard(ctx, c); err != nil {
			return fmt.Errorf("seed welcome card %q: %w", c.Title, err)
		}
	}
	return nil
}

// welcomeCards is the onboarding tutorial, authored as cards so a fresh
// workspace is self-documenting. They reference docs/CONCEPTS.md for depth.
func welcomeCards(actor string) []core.CreateCardRequest {
	card := func(title, status, notes string) core.CreateCardRequest {
		return core.CreateCardRequest{
			TypeID: "task", Title: title, Status: status,
			Fields: map[string]any{"notes": notes}, Actor: actor,
		}
	}
	return []core.CreateCardRequest{
		card("👋 Welcome to Cards", "todo",
			"This board is your workspace. A card has a title, a status (the columns above), and typed fields. Drag this card to Doing, then Done, to see how status works. Everything here is editable — these cards are just a tutorial you can delete."),
		card("Make it yours: edit boards and card types", "todo",
			"Your definitions live next to this database under definitions/: workspace.json (columns + settings), card-types/ (field shapes), and boards/ (these views). Edit the JSON and restart the server to change the model. Card types are shared by every board; boards select and filter."),
		card("Add a board per project", "todo",
			"One workspace can hold many boards — one per project or area. Add a file under definitions/boards/ that picks the card types and columns to show. To isolate a board to a slice of cards, give it a default_filter. See docs/CONCEPTS.md."),
		card("Drive it from the CLI and agents (MCP)", "todo",
			"Set CARDS_URL and CARDS_USER, then `cards list`, `cards create`, `cards take-next`. Agents can use the same model over MCP: `cards mcp`. The HTTP API, CLI, MCP tools, and this UI are all the same contract, generated from your definitions."),
		card("Back up and move your workspace", "doing",
			"`cards export` writes a JSONL snapshot of everything; `cards import` restores it into a fresh workspace. Commit that snapshot alongside definitions/ to version or share the whole workspace — no database server required."),
	}
}
