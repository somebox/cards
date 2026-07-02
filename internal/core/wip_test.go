package core_test

import (
	"context"
	"testing"

	"github.com/somebox/cards/internal/core"
)

// WIP condition (Events seam 3a): crossing a column's WIP limit fires an
// ephemeral wip_exceeded/wip_cleared signal on the bus (SSE-visible) that is
// NOT persisted to the durable feed, and only fires on a state crossing.
func TestWIP_SignalNotFactAndIdempotent(t *testing.T) {
	svc, _ := newTestService(t) // eng: WIPLimits{in_progress: 1}
	ctx := core.WithActor(context.Background(), "u")

	sub := svc.Bus().Subscribe(core.EventFilter{Types: []string{"wip_exceeded", "wip_cleared"}}, 16)
	defer svc.Bus().Unsubscribe(sub.ID)

	mk := func(title string) *core.Card {
		c, err := svc.CreateCard(ctx, core.CreateCardRequest{
			TypeID: "task", Title: title, Status: "todo",
			Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1}, Actor: "u",
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return c
	}
	move := func(c *core.Card, to string) *core.Card {
		got, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &to, Actor: "u"})
		if err != nil {
			t.Fatalf("move %s->%s: %v", c.ID, to, err)
		}
		return got
	}

	a, b, cc := mk("A"), mk("B"), mk("C")

	move(a, "in_progress") // count 1, at limit — no signal
	if got := drain(sub.Ch); len(got) != 0 {
		t.Fatalf("no signal expected at the limit, got %d", len(got))
	}

	b = move(b, "in_progress") // count 2 > 1 — wip_exceeded fires (crossing)
	ex := drain(sub.Ch)
	if len(ex) != 1 || ex[0].Type != core.EventWIPExceeded || ex[0].BoardID != "eng" || ex[0].Scope != "board" {
		t.Fatalf("wip_exceeded: got %+v", ex)
	}

	cc = move(cc, "in_progress") // count 3, still exceeded — no duplicate (idempotent)
	if got := drain(sub.Ch); len(got) != 0 {
		t.Errorf("no duplicate wip_exceeded expected, got %d", len(got))
	}

	// The signal is NOT durable — it must not appear in the feed/replay.
	feed, err := svc.ListEvents(ctx, core.EventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range feed {
		if e.Type == core.EventWIPExceeded || e.Type == core.EventWIPCleared {
			t.Errorf("wip signal leaked into the durable feed: %+v", e)
		}
	}

	// Drop back under the limit — wip_cleared fires (crossing back).
	move(b, "review")
	move(cc, "review") // count in_progress = 1 (only A) — cleared
	cl := drain(sub.Ch)
	if len(cl) != 1 || cl[0].Type != core.EventWIPCleared {
		t.Errorf("wip_cleared: got %+v", cl)
	}
}
