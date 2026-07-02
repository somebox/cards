package core_test

import (
	"context"
	"testing"

	"github.com/somebox/cards/internal/core"
)

// Escalation (Events seam 3b): a condition type opted into `persist_conditions`
// is routed through Emit (durable fact) instead of Signal (ephemeral). The
// escalated event survives a process restart and replays from the feed; an
// un-escalated one does not; live bus delivery is identical either way.
func TestEscalation_PersistedConditionSurvivesRestart(t *testing.T) {
	ctx := core.WithActor(context.Background(), "u")

	// Drive three cards through in_progress (WIPLimits{in_progress: 1}) to cross
	// the limit and fire wip_exceeded. Returns the emitted event seen on the bus.
	cross := func(svc *core.Service) *core.Event {
		t.Helper()
		sub := svc.Bus().Subscribe(core.EventFilter{Types: []string{"wip_exceeded"}}, 16)
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
		move := func(c *core.Card, to string) {
			if _, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &to, Actor: "u"}); err != nil {
				t.Fatalf("move %s->%s: %v", c.ID, to, err)
			}
		}
		a, b := mk("A"), mk("B")
		move(a, "in_progress") // at limit — quiet
		move(b, "in_progress") // over limit — wip_exceeded fires
		got := drain(sub.Ch)
		if len(got) != 1 || got[0].Type != core.EventWIPExceeded {
			t.Fatalf("expected one wip_exceeded on the bus, got %+v", got)
		}
		return got[0]
	}

	feedHasWIP := func(svc *core.Service) bool {
		feed, err := svc.ListEvents(ctx, core.EventQuery{Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range feed {
			if e.Type == core.EventWIPExceeded {
				return true
			}
		}
		return false
	}

	t.Run("escalated persists and replays across restart", func(t *testing.T) {
		svc, st := newTestServiceWithSettings(t, core.WorkspaceSettings{
			StrictFields: true, TagPolicy: "propose", DefaultUser: "u",
			PersistConditions: []string{"wip_exceeded"},
		})
		ev := cross(svc)
		// Live delivery still happened (bus path identical).
		if ev.BoardID != "eng" || ev.Scope != "board" {
			t.Fatalf("bus event lost board scope: %+v", ev)
		}
		// A brand-new Service over the SAME store models a process restart: the
		// in-memory Emitter state is gone, but the durable fact is in the log.
		ws2, types2, boards2 := testConfig()
		restart := core.NewService(ws2, types2, boards2, st)
		if !feedHasWIP(restart) {
			t.Error("escalated wip_exceeded did not survive restart / replay from feed")
		}
	})

	t.Run("un-escalated stays ephemeral", func(t *testing.T) {
		svc, _ := newTestService(t) // default settings: no persist_conditions
		ev := cross(svc)
		if ev.Type != core.EventWIPExceeded {
			t.Fatalf("bus delivery differs when un-escalated: %+v", ev)
		}
		if feedHasWIP(svc) {
			t.Error("un-escalated wip_exceeded leaked into the durable feed")
		}
	})
}
