package core_test

// Regression: the column census (WIP/lane monitors + Breaches) must report the
// TRUE number of cards in a column, not a value silently capped at the
// ListCards row ceiling (clampCardLimit, 500). countColumn used to do
// ListCards(Limit:500)+len, so a column with >500 cards under-reported as 500 —
// making wip_exceeded counts and /breaches wrong on real boards. It now issues
// a scalar COUNT(*) via Store.CountCards. Sprint P2.

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

func TestBreachesCensusCountsBeyond500(t *testing.T) {
	svc, st := newTestService(t) // eng board: WIPLimits{in_progress: 1}, type "task"
	ctx := context.Background()

	const total = 600 // deliberately past the 500 ListCards ceiling
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range total {
		id := "card_" + strconv.Itoa(i)
		if err := st.InsertCard(ctx, &core.Card{
			ID: id, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
			Title: id, Status: "in_progress", Fields: map[string]any{}, Version: 1,
			CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Millisecond), CreatedBy: "u",
		}, nil); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	rep, err := svc.Breaches(ctx, "eng", nil)
	if err != nil {
		t.Fatalf("Breaches: %v", err)
	}

	var found *core.BreachItem
	for i := range rep.Items {
		if it := &rep.Items[i]; it.Type == core.EventWIPExceeded && it.Column == "in_progress" {
			found = it
			break
		}
	}
	if found == nil {
		t.Fatalf("no wip_exceeded breach for in_progress; items=%+v", rep.Items)
	}
	if found.Count != total {
		t.Errorf("census count = %d, want %d (a 500 here is the undercount bug)", found.Count, total)
	}
}

// CountCards is the uncapped counterpart to ListCards: it counts every matching
// row with no LIMIT clamp, while ListCards tops out at the 500 ceiling.
func TestCountCardsIsUncapped(t *testing.T) {
	_, st := newTestService(t)
	ctx := context.Background()

	const total = 620
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range total {
		id := "card_" + strconv.Itoa(i)
		if err := st.InsertCard(ctx, &core.Card{
			ID: id, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
			Title: id, Status: "todo", Fields: map[string]any{}, Version: 1,
			CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Millisecond), CreatedBy: "u",
		}, nil); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	n, err := st.CountCards(ctx, core.CardQuery{Status: "todo", TypeIDIn: []string{"task"}})
	if err != nil {
		t.Fatalf("CountCards: %v", err)
	}
	if n != total {
		t.Errorf("CountCards = %d, want %d", n, total)
	}

	// ListCards still caps at the ceiling — the contrast the census fix relies on.
	page, err := st.ListCards(ctx, core.CardQuery{Status: "todo", TypeIDIn: []string{"task"}, Limit: 500})
	if err != nil {
		t.Fatalf("ListCards: %v", err)
	}
	if len(page.Items) != 500 {
		t.Fatalf("ListCards items = %d, want 500 (ceiling)", len(page.Items))
	}
}
