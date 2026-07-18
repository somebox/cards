package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/clocktest"
	"github.com/somebox/cards/internal/sqlite"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

// openStatusSinceTestStore opens an in-memory store with the "u" actor
// seeded, matching the pattern used by clock_test.go.
func openStatusSinceTestStore(t *testing.T, ws *core.Workspace) *sqlite.Store {
	t.Helper()
	st := sqlitetest.Open(t, ws, 1)
	if err := st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return st
}

// StatusSince is server-maintained: set at creation, flipped only by a real
// status change (PatchCard, Claim/Release via PatchCard, TakeNext's claim),
// and preserved by every other mutation.
func TestStatusSince_Maintenance(t *testing.T) {
	ws, types, boards := testConfig()
	st := openStatusSinceTestStore(t, ws)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fake := clocktest.New(start)
	svc := core.NewService(ws, types, boards, st, core.WithClock(fake))
	ctx := core.WithActor(context.Background(), "u")

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "A", Status: "todo",
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !c.StatusSince.Equal(start) {
		t.Fatalf("StatusSince at creation = %v, want %v", c.StatusSince, start)
	}

	fake.Advance(time.Hour)
	title := "A renamed"
	c, err = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Title: &title, Actor: "u"})
	if err != nil {
		t.Fatalf("title-only patch: %v", err)
	}
	if !c.StatusSince.Equal(start) {
		t.Errorf("title-only patch flipped StatusSince: got %v, want unchanged %v", c.StatusSince, start)
	}

	fake.Advance(time.Hour)
	toInProgress := "in_progress"
	c, err = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &toInProgress, Actor: "u"})
	if err != nil {
		t.Fatalf("status patch: %v", err)
	}
	wantFlip := start.Add(2 * time.Hour)
	if !c.StatusSince.Equal(wantFlip) {
		t.Errorf("status patch StatusSince = %v, want %v (now)", c.StatusSince, wantFlip)
	}

	// Persisted, not just in-memory: a fresh read agrees.
	got, err := svc.GetCard(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StatusSince.Equal(wantFlip) {
		t.Errorf("persisted StatusSince = %v, want %v", got.StatusSince, wantFlip)
	}
}

// TakeNext's claim (which may move status via ClaimAtomic, bypassing
// PatchCard) must flip StatusSince exactly like PatchCard does.
func TestStatusSince_TakeNextFlipsOnClaim(t *testing.T) {
	ws, types, boards := testConfig()
	st := openStatusSinceTestStore(t, ws)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fake := clocktest.New(start)
	svc := core.NewService(ws, types, boards, st, core.WithClock(fake))
	ctx := core.WithActor(context.Background(), "u")

	_, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "A", Status: "todo",
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	fake.Advance(3 * time.Hour)
	claimed, err := svc.TakeNext(ctx, core.TakeNextRequest{BoardID: "eng", Status: "in_progress", Actor: "u"})
	if err != nil {
		t.Fatalf("take-next: %v", err)
	}
	if claimed == nil {
		t.Fatal("take-next: no card claimed")
	}
	want := start.Add(3 * time.Hour)
	if !claimed.StatusSince.Equal(want) {
		t.Errorf("StatusSince after take-next claim = %v, want %v", claimed.StatusSince, want)
	}
}

// A claim that only changes owner (no req.Status) must not touch StatusSince.
func TestStatusSince_TakeNextPreservesOnOwnerOnlyClaim(t *testing.T) {
	ws, types, boards := testConfig()
	st := openStatusSinceTestStore(t, ws)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fake := clocktest.New(start)
	svc := core.NewService(ws, types, boards, st, core.WithClock(fake))
	ctx := core.WithActor(context.Background(), "u")

	created, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "A", Status: "todo",
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	fake.Advance(3 * time.Hour)
	claimed, err := svc.TakeNext(ctx, core.TakeNextRequest{BoardID: "eng", Actor: "u"}) // no Status: owner-only claim
	if err != nil {
		t.Fatalf("take-next: %v", err)
	}
	if claimed == nil {
		t.Fatal("take-next: no card claimed")
	}
	if !claimed.StatusSince.Equal(created.StatusSince) {
		t.Errorf("owner-only claim changed StatusSince: got %v, want unchanged %v", claimed.StatusSince, created.StatusSince)
	}
}
