package core_test

import (
	"context"
	"testing"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/eventlogtest"
)

func newCard(t *testing.T, svc *core.Service, ctx context.Context, title string) *core.Card {
	t.Helper()
	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: title, Status: "todo",
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1},
		Actor:  "u",
	})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	return c
}

// The Recorder (Events seam 1f) captures a mutation's emitted events for
// assertion without bus channels/draining.
func TestObserver_RecorderCapturesEvents(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(context.Background(), "u") // AddComment reads actor from ctx
	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)

	c := newCard(t, svc, ctx, "recorded")
	if _, err := svc.AddComment(ctx, c.ID, "hello"); err != nil {
		t.Fatalf("add comment: %v", err)
	}

	got := rec.Types()
	want := []core.EventType{core.EventCardCreated, core.EventCommentAdded}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("recorded types = %v, want %v", got, want)
	}
	if evs := rec.Events(); evs[0].CardID != c.ID {
		t.Errorf("recorded card_id = %q, want %q", evs[0].CardID, c.ID)
	}
}

// A panicking observer must never crash the write path or starve later
// observers (docs/EVENTS.md §8; notifyObserver recovers per observer).
func TestObserver_PanicIsolation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	rec := &eventlogtest.Recorder{}
	// Registered BEFORE the recorder, so if a panic escaped it would skip the
	// recorder and/or fail the mutation.
	svc.Emitter().Observe(func(*core.Event) { panic("observer boom") })
	svc.Emitter().Observe(rec.Record)

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "survives", Status: "todo",
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1},
		Actor:  "u",
	})
	if err != nil {
		t.Fatalf("mutation must succeed despite a panicking observer: %v", err)
	}
	if c == nil {
		t.Fatal("card should be created")
	}
	if got := rec.Types(); len(got) != 1 || got[0] != core.EventCardCreated {
		t.Errorf("later observer should still receive events; recorded %v", got)
	}
}
