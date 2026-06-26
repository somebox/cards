// Package seed inserts demo users and cards into an empty workspace DB so the
// UI is non-empty on first run. Idempotent: only runs when the cards table
// is empty.
package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/foz/work-cards/internal/core"
)

// IfEmpty seeds users + a few demo cards when no cards exist yet.
func IfEmpty(ctx context.Context, st core.Store, svc *core.Service, ws *core.Workspace) error {
	page, err := st.ListCards(ctx, core.CardQuery{Limit: 1})
	if err != nil {
		return err
	}
	if len(page.Items) > 0 {
		return nil // already has cards
	}
	// Users.
	users := []core.User{
		{ID: "local-dev", DisplayName: "Local Dev", Kind: "human", CreatedAt: time.Now().UTC()},
		{ID: "coder-agent", DisplayName: "Coder Agent", Kind: "agent", CreatedAt: time.Now().UTC()},
		{ID: "research-agent", DisplayName: "Research Agent", Kind: "agent", CreatedAt: time.Now().UTC()},
	}
	for _, u := range users {
		if err := st.InsertUser(ctx, u); err != nil {
			return fmt.Errorf("seed user %s: %w", u.ID, err)
		}
	}

	// Demo cards across types and columns.
	now := time.Now().UTC().Format(time.RFC3339)
	demos := []core.CreateCardRequest{
		{
			TypeID: "programming-task", Title: "Add OpenAPI spec for /v1/cards", Status: "todo",
			Fields: map[string]any{
				"description": "Generate an OpenAPI document from the existing handlers and serve it at /v1/openapi.json.",
				"branch":      "feat/openapi",
			},
			Tags: []string{"feature"},
		},
		{
			TypeID: "programming-task", Title: "Fix cursor pagination off-by-one", Status: "in_progress",
			Fields: map[string]any{
				"description": "ListCards returns one extra row when cursor is empty.",
				"branch":      "fix/list-cursor",
			},
			Tags: []string{"bug", "urgent"},
		},
		{
			TypeID: "programming-task", Title: "Harden transition_illegal error", Status: "review",
			Fields: map[string]any{
				"description": "Ensure valid_options are board column ids, not workspace ids.",
				"branch":      "fix/transition-error",
			},
		},
		{
			TypeID: "research-goal", Title: "Evaluate SQLite FTS5 vs LIKE for POC scale", Status: "in_progress",
			Fields: map[string]any{
				"hypothesis": "LIKE is sufficient for <10k cards and avoids the FTS5 build/CGO complexity.",
			},
		},
		{
			TypeID: "research-goal", Title: "Survey agent resume-from-history patterns", Status: "backlog",
			Fields: map[string]any{
				"hypothesis": "A structured event timeline lets an agent resume work after preemption without re-reading the whole card.",
			},
			Tags: []string{"feature"},
		},
	}
	_ = now
	for _, d := range demos {
		d.Actor = ws.Settings.DefaultUser
		if _, err := svc.CreateCard(ctx, d); err != nil {
			return fmt.Errorf("seed card %q: %w", d.Title, err)
		}
	}
	return nil
}
