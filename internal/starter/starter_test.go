package starter_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
	"github.com/somebox/cards/internal/starter"
)

// TestScaffoldAndSeed scaffolds into a temp dir, confirms the workspace loads
// and the welcome board seeds, and that both operations are idempotent.
func TestScaffoldAndSeed(t *testing.T) {
	dir := t.TempDir()

	created, err := starter.Scaffold(dir)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if !created {
		t.Fatal("scaffold reported no work on an empty dir")
	}
	// Idempotent: second scaffold is a no-op.
	if again, err := starter.Scaffold(dir); err != nil || again {
		t.Fatalf("re-scaffold = (%v, %v), want (false, nil)", again, err)
	}

	result, err := config.New(dir).Load()
	if err != nil {
		t.Fatalf("load scaffolded workspace: %v", err)
	}
	if _, ok := result.CardTypes["task"]; !ok {
		t.Errorf("missing 'task' card type; have %v", keys(result.CardTypes))
	}
	if _, ok := result.Boards["welcome"]; !ok {
		t.Errorf("missing 'welcome' board; have %v", boardKeys(result.Boards))
	}

	st, err := sqlite.Open(filepath.Join(dir, "work-cards.db"), result.Workspace)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, st)
	ctx := context.Background()

	if err := starter.SeedWelcome(ctx, st, svc, result.Workspace); err != nil {
		t.Fatalf("seed: %v", err)
	}
	page, err := st.ListCards(ctx, core.CardQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	first := len(page.Items)
	if first == 0 {
		t.Fatal("seed created no welcome cards")
	}

	// Idempotent: a workspace that already has cards is left alone.
	if err := starter.SeedWelcome(ctx, st, svc, result.Workspace); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	page, _ = st.ListCards(ctx, core.CardQuery{Limit: 100})
	if len(page.Items) != first {
		t.Errorf("re-seed changed card count: %d -> %d", first, len(page.Items))
	}
}

func keys(m map[string]*core.CardType) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func boardKeys(m map[string]*core.Board) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
