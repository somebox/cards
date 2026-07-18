package core_test

// Events seam 3e: status_timeout/card_idle wired onto the seam 3d scheduler
// via monitorObserver (an EventObserver — zero new call sites in the
// mutation paths). These tests use persist_conditions to force the
// scheduler to arm regardless of live subscribers (a permanent consumer),
// isolating the temporal semantics from bus-interest mechanics (covered by
// monitor_test.go's synthetic-type tests).

import (
	"context"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/clocktest"
	"github.com/somebox/cards/internal/core/eventlogtest"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

// newTemporalTestService builds a service whose "eng" board monitors
// max_time_in_status/idle_after, with both temporal types escalated (so the
// scheduler is armed without needing a bus subscriber).
func newTemporalTestService(t *testing.T, maxTimeInStatus map[string]string, idleAfter string) (*core.Service, *clocktest.Fake) {
	t.Helper()
	ws, types, boards := testConfig()
	boards["eng"].Monitors = &core.BoardMonitors{
		MaxTimeInStatus: maxTimeInStatus,
		IdleAfter:       idleAfter,
	}
	ws.Settings.PersistConditions = []string{"status_timeout", "card_idle"}
	st := sqlitetest.Open(t, ws, 1)
	if err := st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	fake := clocktest.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	svc := core.NewService(ws, types, boards, st, core.WithClock(fake))
	t.Cleanup(svc.Close)
	return svc, fake
}

func waitForRec(t *testing.T, rec *eventlogtest.Recorder, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rec.Len() >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d events, got %d: %+v", n, rec.Len(), rec.Events())
}

func assertNoneSoon(t *testing.T, rec *eventlogtest.Recorder, want int) {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	if got := rec.Len(); got != want {
		t.Fatalf("recorded %d events, want %d: %+v", got, want, rec.Events())
	}
}

// status_timeout fires once a card has sat in a monitored status past its
// max, and does not re-fire on a further clock advance.
func TestTemporal_StatusTimeoutFires(t *testing.T) {
	svc, fake := newTemporalTestService(t, map[string]string{"review": "1h"}, "")
	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)
	ctx := core.WithActor(context.Background(), "u")

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "A", Status: "todo",
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	c = moveThroughToReview(t, svc, ctx, c)

	base := rec.Len()
	fake.Advance(59 * time.Minute)
	assertNoneSoon(t, rec, base)
	fake.Advance(time.Minute)
	waitForRec(t, rec, base+1)

	found := false
	for _, ev := range rec.Events() {
		if ev.Type == core.EventStatusTimeout && ev.CardID == c.ID {
			found = true
			diff, ok := ev.Diff.(core.StatusTimeoutDiff)
			if !ok || diff.Status != "review" || diff.Max != "1h" {
				t.Errorf("status_timeout diff = %+v", ev.Diff)
			}
		}
	}
	if !found {
		t.Fatalf("no status_timeout for %s: %+v", c.ID, rec.Events())
	}

	afterFire := rec.Len()
	fake.Advance(time.Hour)
	assertNoneSoon(t, rec, afterFire) // no duplicate
}

// moveThroughToReview walks a fresh todo card through eng's enforced
// transitions (todo->in_progress->review) to a legal "review" state.
func moveThroughToReview(t *testing.T, svc *core.Service, ctx context.Context, c *core.Card) *core.Card {
	t.Helper()
	toInProgress := "in_progress"
	c, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &toInProgress, Actor: "u"})
	if err != nil {
		t.Fatalf("move to in_progress: %v", err)
	}
	toReview := "review"
	c, err = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &toReview, Actor: "u"})
	if err != nil {
		t.Fatalf("move to review: %v", err)
	}
	return c
}

// A card that leaves the monitored status before its deadline never fires —
// the fire-time re-verify checks (status, status_since) identity.
func TestTemporal_StatusTimeoutSkippedIfCardLeavesStatusFirst(t *testing.T) {
	svc, fake := newTemporalTestService(t, map[string]string{"review": "1h"}, "")
	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)
	ctx := core.WithActor(context.Background(), "u")

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "A", Status: "todo",
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	c = moveThroughToReview(t, svc, ctx, c)
	fake.Advance(30 * time.Minute)
	toDone := "done"
	if _, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &toDone, Actor: "u"}); err != nil {
		t.Fatalf("move to done: %v", err)
	}

	base := rec.Len()
	fake.Advance(time.Hour) // past the original review deadline
	assertNoneSoon(t, rec, base)
}

// card_idle fires after no mutation for idle_after; any mutation resets it,
// but a condition event (dispatched to the same observer) must not.
func TestTemporal_CardIdleResetSemantics(t *testing.T) {
	svc, fake := newTemporalTestService(t, nil, "1h")
	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)
	ctx := core.WithActor(context.Background(), "u")

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "A", Status: "todo",
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A real mutation at the 30-minute mark resets the idle clock.
	fake.Advance(30 * time.Minute)
	title := "A renamed"
	c, err = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Title: &title, Actor: "u"})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	base := rec.Len()

	// 45 more minutes: only 45m since the reset — must not fire yet, proving
	// the original (30m-old) deadline was correctly stale-discarded, not
	// mistakenly honored.
	fake.Advance(45 * time.Minute)
	assertNoneSoon(t, rec, base)

	// 15 more minutes completes the full hour since the reset.
	fake.Advance(15 * time.Minute)
	waitForRec(t, rec, base+1)
	last := rec.Events()[len(rec.Events())-1]
	if last.Type != core.EventCardIdle || last.CardID != c.ID {
		t.Fatalf("fired = %+v, want card_idle for %s", last, c.ID)
	}
}

// A condition event (e.g. wip_exceeded, dispatched through the very same
// observer as card mutations) must never reset the idle deadline — only
// durable card-mutation events do.
func TestTemporal_ConditionEventsDoNotResetIdle(t *testing.T) {
	ws, types, boards := testConfig() // eng: WIPLimits{in_progress: 1}
	boards["eng"].Monitors = &core.BoardMonitors{IdleAfter: "1h"}
	ws.Settings.PersistConditions = []string{"card_idle"} // wip_exceeded stays ephemeral
	st := sqlitetest.Open(t, ws, 1)
	if err := st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	fake := clocktest.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	svc := core.NewService(ws, types, boards, st, core.WithClock(fake))
	t.Cleanup(svc.Close)

	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)
	ctx := core.WithActor(context.Background(), "u")

	a, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "A", Status: "in_progress",
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}

	fake.Advance(30 * time.Minute)
	// Crossing the WIP limit fires wip_exceeded — a condition event, dispatched
	// through the same observer as every mutation.
	if _, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "B", Status: "in_progress",
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1}, Actor: "u",
	}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	sawWIP := false
	for _, ev := range rec.Events() {
		if ev.Type == core.EventWIPExceeded {
			sawWIP = true
		}
	}
	if !sawWIP {
		t.Fatal("test setup: expected wip_exceeded to have fired")
	}

	// If wip_exceeded had reset A's idle deadline, A would not go idle until
	// 30m (already elapsed) + 1h from here. It must instead fire at the
	// ORIGINAL 1h mark from creation — 30 more minutes from now.
	fake.Advance(30 * time.Minute)
	waitForCardIdle(t, rec, a.ID)
}

// waitForCardIdle polls (bounded, real wall-clock) until a card_idle event
// for cardID has been recorded.
func waitForCardIdle(t *testing.T, rec *eventlogtest.Recorder, cardID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range rec.Events() {
			if ev.Type == core.EventCardIdle && ev.CardID == cardID {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("card %s did not go idle at its original deadline (condition event wrongly reset it?): %+v", cardID, rec.Events())
}
