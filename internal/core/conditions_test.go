package core_test

// Events seam 3c: the unified column census (evaluateColumn) feeds both the
// WIP-limit crossing (3a) and the drained-lane crossing (3c) from one
// ListCards query, and fires from every mutation path that can change a
// column's membership — PatchCard, CreateCard, and TakeNext (the latter two
// were gaps before this seam; see docs/events/EVENTS.md §12 Step 3).

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
)

// newLaneWatchedService is newTestService with the "eng" board additionally
// watching "todo" for lane_drained/lane_refilled (WIPLimits{in_progress: 1}
// is unchanged from testConfig).
func newLaneWatchedService(t *testing.T) (*core.Service, *sqlite.Store) {
	t.Helper()
	ws, types, boards := testConfig()
	boards["eng"].Monitors = &core.BoardMonitors{AlertWhenEmpty: []string{"todo"}}
	return newTestServiceWith(t, ws, types, boards)
}

func mkTask(t *testing.T, svc *core.Service, ctx context.Context, title, status string) *core.Card {
	t.Helper()
	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: title, Status: status,
		Fields: map[string]any{"description": "d", "priority": "high", "estimate": 1}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create %s: %v", title, err)
	}
	return c
}

// DEBT/gap: CreateCard never called evaluateWIP before this seam — a card
// landing directly in an over-limit column crossed silently.
func TestConditions_CreateCardEvaluatesColumn(t *testing.T) {
	svc, _ := newTestService(t) // eng: WIPLimits{in_progress: 1}
	ctx := core.WithActor(context.Background(), "u")

	sub := svc.Bus().Subscribe(core.EventFilter{Types: []string{"wip_exceeded"}}, 8)
	defer svc.Bus().Unsubscribe(sub.ID)

	mkTask(t, svc, ctx, "A", "in_progress") // at the limit — quiet
	if got := drain(sub.Ch); len(got) != 0 {
		t.Fatalf("no signal expected at the limit (create), got %d", len(got))
	}
	mkTask(t, svc, ctx, "B", "in_progress") // crosses — CreateCard alone must fire it
	got := drain(sub.Ch)
	if len(got) != 1 || got[0].Type != core.EventWIPExceeded {
		t.Fatalf("CreateCard did not trigger column evaluation: got %+v", got)
	}
}

// Gap: TakeNext dispatched its claim events directly, bypassing evaluateWIP —
// a claim that moved a card's status never re-checked either column.
func TestConditions_TakeNextEvaluatesColumn(t *testing.T) {
	svc, _ := newTestService(t) // eng: WIPLimits{in_progress: 1}, todo->in_progress legal
	ctx := core.WithActor(context.Background(), "u")

	sub := svc.Bus().Subscribe(core.EventFilter{Types: []string{"wip_exceeded"}}, 8)
	defer svc.Bus().Unsubscribe(sub.ID)

	mkTask(t, svc, ctx, "A", "todo")
	mkTask(t, svc, ctx, "B", "todo")

	take := func() *core.Card {
		c, err := svc.TakeNext(ctx, core.TakeNextRequest{BoardID: "eng", Status: "in_progress", Actor: "u"})
		if err != nil {
			t.Fatalf("take-next: %v", err)
		}
		if c == nil {
			t.Fatal("take-next: no card claimed")
		}
		return c
	}

	take() // moves one card todo->in_progress: at the limit — quiet
	if got := drain(sub.Ch); len(got) != 0 {
		t.Fatalf("no signal expected at the limit (take-next), got %d", len(got))
	}
	take() // second card crosses the limit
	got := drain(sub.Ch)
	if len(got) != 1 || got[0].Type != core.EventWIPExceeded {
		t.Fatalf("TakeNext did not trigger column evaluation: got %+v", got)
	}
}

// lane_drained/lane_refilled share evaluateColumn's census with wip_exceeded/
// cleared — driven here via PatchCard, the same path 3a already covers.
func TestConditions_LaneDrainedAndRefilled(t *testing.T) {
	svc, _ := newLaneWatchedService(t)
	ctx := core.WithActor(context.Background(), "u")

	sub := svc.Bus().Subscribe(core.EventFilter{Types: []string{"lane_drained", "lane_refilled"}}, 8)
	defer svc.Bus().Unsubscribe(sub.ID)

	c := mkTask(t, svc, ctx, "A", "todo")
	if got := drain(sub.Ch); len(got) != 0 {
		t.Fatalf("no lane signal expected while todo still holds a card, got %d", len(got))
	}

	to := "in_progress"
	c, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &to, Actor: "u"})
	if err != nil {
		t.Fatalf("move to in_progress: %v", err)
	}
	got := drain(sub.Ch)
	if len(got) != 1 || got[0].Type != core.EventLaneDrained || got[0].BoardID != "eng" || got[0].Scope != "board" {
		t.Fatalf("expected lane_drained after emptying todo, got %+v", got)
	}

	// Same column state (0), different mutation — must not re-fire.
	d := mkTask(t, svc, ctx, "B", "in_progress") // WIP limit is 1; this crosses it too, but that's a different event type
	_ = d
	if got := drainByType(sub.Ch, "lane_drained", "lane_refilled"); len(got) != 0 {
		t.Fatalf("no duplicate lane_drained expected, got %+v", got)
	}

	// Force the move back (in_progress -> todo has no transition edge on eng).
	back := "todo"
	if _, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &back, Actor: "u", Force: true}); err != nil {
		t.Fatalf("move back to todo: %v", err)
	}
	got = drain(sub.Ch)
	if len(got) != 1 || got[0].Type != core.EventLaneRefilled {
		t.Fatalf("expected lane_refilled after refilling todo, got %+v", got)
	}
}

func drainByType(ch <-chan *core.Event, types ...string) []*core.Event {
	set := map[string]bool{}
	for _, t := range types {
		set[t] = true
	}
	var out []*core.Event
	for _, e := range drain(ch) {
		if set[string(e.Type)] {
			out = append(out, e)
		}
	}
	return out
}

// The census + crossing tracker are read-only against card state: neither
// evaluateColumn nor evaluateCrossing may mutate a card beyond the single
// version bump the triggering mutation itself already applied.
func TestConditions_EvaluationDoesNotMutateCard(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(context.Background(), "u")

	a := mkTask(t, svc, ctx, "A", "todo")
	b := mkTask(t, svc, ctx, "B", "todo")

	to := "in_progress"
	a2, err := svc.PatchCard(ctx, a.ID, core.PatchCardRequest{Version: a.Version, Status: &to, Actor: "u"})
	if err != nil {
		t.Fatalf("move a: %v", err)
	}
	b2, err := svc.PatchCard(ctx, b.ID, core.PatchCardRequest{Version: b.Version, Status: &to, Actor: "u"}) // crosses the limit
	if err != nil {
		t.Fatalf("move b: %v", err)
	}

	gotA, err := svc.GetCard(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := svc.GetCard(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotA.Version != a2.Version || gotA.Status != a2.Status || gotA.Owner != a2.Owner {
		t.Errorf("card A mutated by condition evaluation: got %+v, want it to match %+v", gotA, a2)
	}
	if gotB.Version != b2.Version || gotB.Status != b2.Status || gotB.Owner != b2.Owner {
		t.Errorf("card B mutated by condition evaluation: got %+v, want it to match %+v", gotB, b2)
	}
}

// failingAppendStore wraps a real Store and fails Append for one event type,
// simulating a durable-append failure on the escalated path without touching
// production code.
type failingAppendStore struct {
	core.Store
	failType core.EventType
}

func (f *failingAppendStore) Append(ctx context.Context, evs ...*core.Event) error {
	for _, e := range evs {
		if e.Type == f.failType {
			return errFailingAppend
		}
	}
	return f.Store.Append(ctx, evs...)
}

var errFailingAppend = &testAppendError{"simulated append failure"}

type testAppendError struct{ msg string }

func (e *testAppendError) Error() string { return e.msg }

// §8 point 7: a failed escalated-condition append must be logged, never fail
// the mutation that triggered evaluation.
func TestConditions_FailedEscalatedAppendIsLoggedNotFailing(t *testing.T) {
	ws, types, boards := testConfig()
	ws.Settings.PersistConditions = []string{"wip_exceeded"}
	st, err := sqlite.Open(":memory:", ws)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	fst := &failingAppendStore{Store: st, failType: core.EventWIPExceeded}
	svc := core.NewService(ws, types, boards, fst)
	ctx := core.WithActor(context.Background(), "u")

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	a := mkTask(t, svc, ctx, "A", "todo")
	b := mkTask(t, svc, ctx, "B", "todo")
	to := "in_progress"
	if _, err := svc.PatchCard(ctx, a.ID, core.PatchCardRequest{Version: a.Version, Status: &to, Actor: "u"}); err != nil {
		t.Fatalf("move a: %v", err)
	}
	// This crossing triggers the escalated (persisted) wip_exceeded append,
	// which the wrapped store fails — the mutation itself must still succeed.
	if _, err := svc.PatchCard(ctx, b.ID, core.PatchCardRequest{Version: b.Version, Status: &to, Actor: "u"}); err != nil {
		t.Fatalf("mutation failed despite a best-effort escalated append failure: %v", err)
	}
	if !strings.Contains(logBuf.String(), "escalated condition append failed") {
		t.Errorf("expected the append failure to be logged, got: %s", logBuf.String())
	}
}
