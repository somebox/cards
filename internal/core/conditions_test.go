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
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
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

// card_blocked/card_unblocked share the CardQuery.Blocked SQL definition
// (store.Blockers) — a "related" link never affects blocked state, a second
// blocker while already blocked doesn't re-fire, and unblocking requires
// every blocker to reach "done" (moving one of two blockers to done leaves
// the card blocked by the other). Reopening a done blocker re-blocks.
func TestConditions_CardBlockedAndUnblocked(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(context.Background(), "u")

	sub := svc.Bus().Subscribe(core.EventFilter{Types: []string{"card_blocked", "card_unblocked"}}, 16)
	defer svc.Bus().Unsubscribe(sub.ID)

	a := mkTask(t, svc, ctx, "A", "todo")
	target := mkTask(t, svc, ctx, "Target", "todo")
	target2 := mkTask(t, svc, ctx, "Target2", "todo")

	addLink := func(from *core.Card, typeID string, to *core.Card) {
		t.Helper()
		if _, err := svc.AddLink(ctx, from.ID, core.LinkInput{TypeID: typeID, Target: to.ID, Actor: "u"}); err != nil {
			t.Fatalf("add %s link %s->%s: %v", typeID, from.ID, to.ID, err)
		}
	}
	moveForce := func(c *core.Card, to string) *core.Card {
		t.Helper()
		got, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &to, Actor: "u", Force: true})
		if err != nil {
			t.Fatalf("move %s->%s: %v", c.ID, to, err)
		}
		return got
	}

	// A "related" link is not a blocking type — must never fire.
	addLink(a, "related", target)
	if got := drain(sub.Ch); len(got) != 0 {
		t.Fatalf("related link must not affect blocked state, got %+v", got)
	}

	// blocked-by to a not-done target blocks A.
	addLink(a, "blocked-by", target)
	got := drain(sub.Ch)
	if len(got) != 1 || got[0].Type != core.EventCardBlocked || got[0].CardID != a.ID {
		t.Fatalf("expected card_blocked, got %+v", got)
	}
	if diff, ok := got[0].Diff.(core.BlockedDiff); !ok || len(diff.Blockers) != 1 || diff.Blockers[0] != target.ID {
		t.Fatalf("card_blocked diff = %+v, want blockers=[%s]", got[0].Diff, target.ID)
	}

	// A second blocker while already blocked must not re-fire.
	addLink(a, "depends-on", target2)
	if got := drain(sub.Ch); len(got) != 0 {
		t.Fatalf("no duplicate card_blocked expected for a second blocker, got %+v", got)
	}

	// Move only `target` through to done — `a` is still blocked by target2.
	target = moveForce(target, "in_progress")
	target = moveForce(target, "review")
	moveForce(target, "done")
	if got := drain(sub.Ch); len(got) != 0 {
		t.Fatalf("still blocked by target2 — no unblock expected yet, got %+v", got)
	}

	// Move target2 to done too — now every blocker is done: unblocked.
	target2 = moveForce(target2, "in_progress")
	target2 = moveForce(target2, "review")
	target2 = moveForce(target2, "done")
	got = drain(sub.Ch)
	if len(got) != 1 || got[0].Type != core.EventCardUnblocked || got[0].CardID != a.ID {
		t.Fatalf("expected card_unblocked, got %+v", got)
	}

	// Reopening a done blocker (its link was never removed) re-blocks A.
	moveForce(target2, "in_progress")
	got = drain(sub.Ch)
	if len(got) != 1 || got[0].Type != core.EventCardBlocked || got[0].CardID != a.ID {
		t.Fatalf("expected card_blocked again after reopening a blocker, got %+v", got)
	}

	// Removing the (now sole active) blocking link unblocks A.
	if _, err := svc.RemoveLink(ctx, a.ID, "depends-on", target2.ID); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	got = drain(sub.Ch)
	if len(got) != 1 || got[0].Type != core.EventCardUnblocked {
		t.Fatalf("expected card_unblocked after removing the link, got %+v", got)
	}
}

// The store's Blockers (used by evaluateBlocked) and CardQuery.Blocked (used
// by list/filter) must agree — they are required to share one SQL fragment
// (blockedLinkTypesIN), not maintain two notions of "blocked".
func TestConditions_BlockersAgreesWithCardQueryBlocked(t *testing.T) {
	svc, st := newTestService(t)
	ctx := core.WithActor(context.Background(), "u")

	a := mkTask(t, svc, ctx, "A", "todo")
	b := mkTask(t, svc, ctx, "B", "todo") // never blocked
	target := mkTask(t, svc, ctx, "Target", "todo")
	if _, err := svc.AddLink(ctx, a.ID, core.LinkInput{TypeID: "blocked-by", Target: target.ID, Actor: "u"}); err != nil {
		t.Fatalf("add link: %v", err)
	}

	blockers, err := st.Blockers(ctx, a.ID)
	if err != nil {
		t.Fatalf("Blockers: %v", err)
	}
	if len(blockers) != 1 || blockers[0] != target.ID {
		t.Fatalf("Blockers(a) = %v, want [%s]", blockers, target.ID)
	}
	if bb, err := st.Blockers(ctx, b.ID); err != nil || len(bb) != 0 {
		t.Fatalf("Blockers(b) = %v, %v, want empty", bb, err)
	}

	page, err := svc.ListCards(ctx, core.CardQuery{Blocked: true, Limit: 10})
	if err != nil {
		t.Fatalf("list blocked: %v", err)
	}
	found := map[string]bool{}
	for _, c := range page.Items {
		found[c.ID] = true
	}
	if !found[a.ID] {
		t.Errorf("CardQuery{Blocked:true} missing %s, which Blockers() reports blocked", a.ID)
	}
	if found[b.ID] {
		t.Errorf("CardQuery{Blocked:true} wrongly includes %s, which Blockers() reports unblocked", b.ID)
	}
}

// newRejectionWatchedService is newTestService with the "eng" board opted
// into transition_rejected (WIPLimits/Transitions/EnforceTransitions are
// unchanged from testConfig).
func newRejectionWatchedService(t *testing.T) (*core.Service, *sqlite.Store) {
	t.Helper()
	ws, types, boards := testConfig()
	boards["eng"].Monitors = &core.BoardMonitors{EmitRejections: true}
	return newTestServiceWith(t, ws, types, boards)
}

// transition_rejected is opt-in: only a PatchCard move genuinely refused by
// EnforceTransitions fires it (not Force, not TakeNext — which pre-filters
// candidates to legal from-statuses, so it never attempts an illegal move).
func TestConditions_TransitionRejected(t *testing.T) {
	svc, _ := newRejectionWatchedService(t) // eng: todo->in_progress legal, todo->done is not
	ctx := core.WithActor(context.Background(), "u")

	sub := svc.Bus().Subscribe(core.EventFilter{Types: []string{"transition_rejected"}}, 8)
	defer svc.Bus().Unsubscribe(sub.ID)

	c := mkTask(t, svc, ctx, "A", "todo")
	illegal := "done"
	if _, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &illegal, Actor: "u"}); err == nil {
		t.Fatal("expected the illegal transition to be rejected")
	}
	got := drain(sub.Ch)
	if len(got) != 1 || got[0].Type != core.EventTransitionRejected || got[0].CardID != c.ID {
		t.Fatalf("expected transition_rejected, got %+v", got)
	}
	diff, ok := got[0].Diff.(core.TransitionRejectedDiff)
	if !ok || diff.From != "todo" || diff.To != "done" || diff.BoardID != "eng" {
		t.Fatalf("transition_rejected diff = %+v, want from=todo to=done board=eng", got[0].Diff)
	}

	// Force bypasses the check entirely — no rejection to report.
	if _, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &illegal, Actor: "u", Force: true}); err != nil {
		t.Fatalf("forced move should succeed: %v", err)
	}
	if got := drain(sub.Ch); len(got) != 0 {
		t.Fatalf("no transition_rejected expected for a forced move, got %+v", got)
	}
}

func TestConditions_TransitionRejectedOptOut(t *testing.T) {
	svc, _ := newTestService(t) // default monitors: EmitRejections unset
	ctx := core.WithActor(context.Background(), "u")

	sub := svc.Bus().Subscribe(core.EventFilter{Types: []string{"transition_rejected"}}, 8)
	defer svc.Bus().Unsubscribe(sub.ID)

	c := mkTask(t, svc, ctx, "A", "todo")
	illegal := "done"
	if _, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Status: &illegal, Actor: "u"}); err == nil {
		t.Fatal("expected the illegal transition to be rejected")
	}
	if got := drain(sub.Ch); len(got) != 0 {
		t.Fatalf("no transition_rejected expected without emit_rejections, got %+v", got)
	}
}

// TakeNext restricts candidates to statuses that can legally reach the
// requested one — it never attempts (and so never gets refused for) an
// illegal move, so transition_rejected must not fire from it.
func TestConditions_TransitionRejectedNeverFromTakeNext(t *testing.T) {
	svc, _ := newRejectionWatchedService(t)
	ctx := core.WithActor(context.Background(), "u")

	sub := svc.Bus().Subscribe(core.EventFilter{Types: []string{"transition_rejected"}}, 8)
	defer svc.Bus().Unsubscribe(sub.ID)

	mkTask(t, svc, ctx, "A", "todo")
	c, err := svc.TakeNext(ctx, core.TakeNextRequest{BoardID: "eng", Status: "in_progress", Actor: "u"})
	if err != nil || c == nil {
		t.Fatalf("take-next: card=%v err=%v", c, err)
	}
	if got := drain(sub.Ch); len(got) != 0 {
		t.Fatalf("TakeNext must never fire transition_rejected, got %+v", got)
	}
}

// Breaches (the on-demand catch-up query) must agree with the live crossing
// evaluators: both derive from the same countColumn/Blockers helpers, so a
// column currently over its WIP limit, a watched-empty lane, and a blocked
// card must all appear as breach items — before any live event ever fired.
func TestConditions_BreachesAgreesWithLiveEvaluators(t *testing.T) {
	svc, _ := newLaneWatchedService(t) // eng: WIPLimits{in_progress:1}, alert_when_empty:[todo]
	ctx := core.WithActor(context.Background(), "u")

	// Cross the WIP limit and drain "todo" without ever subscribing to the
	// bus — Breaches must still see both from a cold read.
	mkTask(t, svc, ctx, "A", "in_progress")
	mkTask(t, svc, ctx, "B", "in_progress") // crosses in_progress's limit of 1

	target := mkTask(t, svc, ctx, "Target", "todo")
	blocked := mkTask(t, svc, ctx, "Blocked", "todo")
	blocked, err := svc.AddLink(ctx, blocked.ID, core.LinkInput{TypeID: "blocked-by", Target: target.ID, Actor: "u"})
	if err != nil {
		t.Fatalf("add link: %v", err)
	}
	// Empty "todo" by moving both cards out of it.
	toInProgress := "in_progress"
	if _, err := svc.PatchCard(ctx, target.ID, core.PatchCardRequest{Version: target.Version, Status: &toInProgress, Actor: "u"}); err != nil {
		t.Fatalf("move target: %v", err)
	}
	if _, err := svc.PatchCard(ctx, blocked.ID, core.PatchCardRequest{Version: blocked.Version, Status: &toInProgress, Actor: "u"}); err != nil {
		t.Fatalf("move blocked: %v", err)
	}

	report, err := svc.Breaches(ctx, "", nil)
	if err != nil {
		t.Fatalf("breaches: %v", err)
	}
	var sawWIP, sawLane, sawBlocked bool
	for _, it := range report.Items {
		switch it.Type {
		case core.EventWIPExceeded:
			if it.BoardID == "eng" && it.Column == "in_progress" {
				sawWIP = true
			}
		case core.EventLaneDrained:
			if it.BoardID == "eng" && it.Column == "todo" {
				sawLane = true
			}
		case core.EventCardBlocked:
			if it.CardID == blocked.ID && it.BoardID == "" && len(it.Blockers) == 1 && it.Blockers[0] == target.ID {
				sawBlocked = true
			}
		}
	}
	if !sawWIP {
		t.Errorf("breaches missing wip_exceeded for eng/in_progress: %+v", report.Items)
	}
	if !sawLane {
		t.Errorf("breaches missing lane_drained for eng/todo: %+v", report.Items)
	}
	if !sawBlocked {
		t.Errorf("breaches missing card_blocked for %s: %+v", blocked.ID, report.Items)
	}

	// testConfig() declares two boards (eng, hipri) sharing the "task" type;
	// board_id=eng must not double-count the blocked card even though "hipri"
	// also has type "task" — card_blocked is computed once, not per board.
	scoped, err := svc.Breaches(ctx, "eng", nil)
	if err != nil {
		t.Fatalf("board-scoped breaches: %v", err)
	}
	if len(scoped.Items) != len(report.Items) {
		t.Errorf("board-scoped breaches = %d items, all-boards = %d, want equal (hipri contributes none): %+v vs %+v",
			len(scoped.Items), len(report.Items), scoped.Items, report.Items)
	}
	if _, err := svc.Breaches(ctx, "nope", nil); core.AsError(err) == nil || core.AsError(err).Code != "not_found" {
		t.Errorf("unknown board_id: err = %v, want not_found", err)
	}

	// type filter narrows to just the requested type.
	onlyWIP, err := svc.Breaches(ctx, "", []string{"wip_exceeded"})
	if err != nil {
		t.Fatalf("breaches (filtered): %v", err)
	}
	for _, it := range onlyWIP.Items {
		if it.Type != core.EventWIPExceeded {
			t.Errorf("type filter leaked a %s item: %+v", it.Type, it)
		}
	}
	if len(onlyWIP.Items) == 0 {
		t.Error("type filter for wip_exceeded returned nothing")
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
	st := sqlitetest.Open(t, ws, 1)
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
