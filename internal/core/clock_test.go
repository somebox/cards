package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/clocktest"
	"github.com/somebox/cards/internal/sqlite"
)

// WithClock lets a test pin the Service's notion of "now" — event timestamps
// (Emitter.stamp) come from the injected clock, not the wall clock, and
// stepping the fake clock changes what the Service sees next.
func TestWithClock_StampsFromInjectedClock(t *testing.T) {
	ws, types, boards := testConfig()
	st, err := sqlite.Open(":memory:", ws)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
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
	if !c.CreatedAt.Equal(start) {
		t.Errorf("CreatedAt = %v, want the injected clock's start time %v", c.CreatedAt, start)
	}

	fake.Advance(time.Hour)
	to := "in_progress"
	c, err = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &to, Actor: "u"})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	want := start.Add(time.Hour)
	if !c.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt after Advance(1h) = %v, want %v", c.UpdatedAt, want)
	}
}
