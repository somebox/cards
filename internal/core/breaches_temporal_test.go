package core_test

// Sprint 2026-07-18 Phase 2: cold temporal catch-up on GET /v1/breaches
// (seam 3e). Service.Breaches must project status_timeout/card_idle past-due
// cards with the same honesty as WIP/lane/blocked — same deadline math as
// rebuild/verify, read-only (never arms, never marks conditions fired), and
// working even when the scheduler was never armed (no subscribers, no
// persist_conditions) — that is the whole point of a *cold* catch-up query.

import (
	"context"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/clocktest"
	"github.com/somebox/cards/internal/sqlite"
)

// newColdMonitorService builds a service whose "eng" board monitors
// max_time_in_status/idle_after but with NO persist_conditions and no
// subscribers — the scheduler never arms, so anything Breaches reports is
// pure cold projection. Returns the store for fast direct inserts.
func newColdMonitorService(t *testing.T, maxTimeInStatus map[string]string, idleAfter string) (*core.Service, *clocktest.Fake, *sqlite.Store) {
	t.Helper()
	ws, types, boards := testConfig()
	boards["eng"].Monitors = &core.BoardMonitors{
		MaxTimeInStatus: maxTimeInStatus,
		IdleAfter:       idleAfter,
	}
	st, err := sqlite.Open(":memory:", ws)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	fake := clocktest.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	svc := core.NewService(ws, types, boards, st, core.WithClock(fake))
	t.Cleanup(svc.Close)
	return svc, fake, st
}

func createColdCard(t *testing.T, svc *core.Service, title, status string) *core.Card {
	t.Helper()
	c, err := svc.CreateCard(context.Background(), core.CreateCardRequest{
		TypeID: "task", Title: title, Status: status, Actor: "u",
		Fields: map[string]any{"description": "test card"},
	})
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	return c
}

func breachIDs(rep *core.BreachReport, t core.EventType) map[string]core.BreachItem {
	out := map[string]core.BreachItem{}
	for _, it := range rep.Items {
		if it.Type == t {
			out[it.CardID] = it
		}
	}
	return out
}

// A card sitting in a monitored status past max_time_in_status appears on the
// cold projection with the Type→fields wire shape; leaving the status clears
// it; type= filtering keeps temporal rows exclusive from WIP rows.
func TestBreaches_StatusTimeoutColdProjection(t *testing.T) {
	svc, fake, _ := newColdMonitorService(t, map[string]string{"review": "72h"}, "")
	ctx := context.Background()

	due := createColdCard(t, svc, "stuck in review", "review")
	fresh := createColdCard(t, svc, "fresh in todo", "todo")

	// Not yet due: 71h < 72h max.
	fake.Advance(71 * time.Hour)
	rep, err := svc.Breaches(ctx, "", []string{"status_timeout"})
	if err != nil {
		t.Fatalf("Breaches: %v", err)
	}
	if got := breachIDs(rep, core.EventStatusTimeout); len(got) != 0 {
		t.Fatalf("71h < 72h max: expected no timeouts, got %+v", got)
	}

	// Past due: 73h > 72h max.
	fake.Advance(2 * time.Hour)
	rep, err = svc.Breaches(ctx, "", []string{"status_timeout"})
	if err != nil {
		t.Fatalf("Breaches: %v", err)
	}
	got := breachIDs(rep, core.EventStatusTimeout)
	if len(got) != 1 {
		t.Fatalf("want exactly the stuck card, got %+v", got)
	}
	it, ok := got[due.ID]
	if !ok {
		t.Fatalf("stuck card %s missing; got %+v", due.ID, got)
	}
	if _, present := got[fresh.ID]; present {
		t.Errorf("todo is not a monitored status; fresh card must not appear")
	}
	// Wire shape: status/since/max populated, mirroring StatusTimeoutDiff.
	if it.Status != "review" || it.Max != "72h" {
		t.Errorf("status/max = %q/%q, want review/72h", it.Status, it.Max)
	}
	if it.Since == nil || !it.Since.Equal(due.StatusSince) {
		t.Errorf("since = %v, want card StatusSince %v", it.Since, due.StatusSince)
	}

	// Leaving the status clears the projection (verify semantics: the
	// identity key no longer matches).
	toDone := "done"
	if _, err := svc.PatchCard(ctx, due.ID, core.PatchCardRequest{Version: due.Version, Status: &toDone, Actor: "u"}); err != nil {
		t.Fatalf("patch status: %v", err)
	}
	rep, err = svc.Breaches(ctx, "", []string{"status_timeout"})
	if err != nil {
		t.Fatalf("Breaches: %v", err)
	}
	if got := breachIDs(rep, core.EventStatusTimeout); len(got) != 0 {
		t.Fatalf("card left review: expected clear, got %+v", got)
	}
}

// A card with no mutation past idle_after appears; any mutation re-arms it.
func TestBreaches_CardIdleColdProjection(t *testing.T) {
	svc, fake, _ := newColdMonitorService(t, nil, "24h")
	ctx := context.Background()

	idle := createColdCard(t, svc, "gone quiet", "todo")

	fake.Advance(23 * time.Hour)
	rep, err := svc.Breaches(ctx, "", []string{"card_idle"})
	if err != nil {
		t.Fatalf("Breaches: %v", err)
	}
	if got := breachIDs(rep, core.EventCardIdle); len(got) != 0 {
		t.Fatalf("23h < 24h idle_after: expected none, got %+v", got)
	}

	fake.Advance(2 * time.Hour)
	rep, err = svc.Breaches(ctx, "", []string{"card_idle"})
	if err != nil {
		t.Fatalf("Breaches: %v", err)
	}
	got := breachIDs(rep, core.EventCardIdle)
	it, ok := got[idle.ID]
	if !ok || len(got) != 1 {
		t.Fatalf("want exactly the idle card, got %+v", got)
	}
	if it.Threshold != "24h" || it.Since == nil {
		t.Errorf("threshold/since = %q/%v, want 24h/<set>", it.Threshold, it.Since)
	}

	// A mutation is activity: the card is no longer idle.
	cur, err := svc.GetCard(ctx, idle.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	touched := "touched"
	if _, err := svc.PatchCard(ctx, cur.ID, core.PatchCardRequest{Version: cur.Version, Title: &touched, Actor: "u"}); err != nil {
		t.Fatalf("patch title: %v", err)
	}
	rep, err = svc.Breaches(ctx, "", []string{"card_idle"})
	if err != nil {
		t.Fatalf("Breaches: %v", err)
	}
	if got := breachIDs(rep, core.EventCardIdle); len(got) != 0 {
		t.Fatalf("card mutated: expected clear, got %+v", got)
	}
}

// Golden rule: the projected set equals what verify would emit if every
// armed deadline came due right now — and never what rebuild alone would arm
// (rebuild arms not-yet-due cards too). Mixed scenario: due, not-due,
// left-status, and unmonitored cards.
func TestBreaches_TemporalGoldenMatchesVerifyIfDueNow(t *testing.T) {
	svc, fake, _ := newColdMonitorService(t, map[string]string{"review": "72h"}, "24h")
	ctx := context.Background()

	dueTimeout := createColdCard(t, svc, "due timeout", "review")
	notDue := createColdCard(t, svc, "not due", "review")
	willLeave := createColdCard(t, svc, "left status", "review")

	fake.Advance(48 * time.Hour) // dueTimeout now 48h in review; notDue/willLeave also 48h — all < 72h
	late := createColdCard(t, svc, "late arrival", "review")
	_ = late

	fake.Advance(36 * time.Hour) // dueTimeout 84h (due), notDue/willLeave 84h (due), late 36h (not due)
	cur, err := svc.GetCard(ctx, willLeave.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	toDone := "done"
	if _, err := svc.PatchCard(ctx, cur.ID, core.PatchCardRequest{Version: cur.Version, Status: &toDone, Actor: "u"}); err != nil {
		t.Fatalf("patch: %v", err)
	}

	rep, err := svc.Breaches(ctx, "", nil)
	if err != nil {
		t.Fatalf("Breaches: %v", err)
	}
	got := breachIDs(rep, core.EventStatusTimeout)
	want := map[string]bool{dueTimeout.ID: true, notDue.ID: true}
	if len(got) != len(want) {
		t.Fatalf("golden set mismatch: want ids %v, got %+v", want, got)
	}
	for id := range want {
		if _, ok := got[id]; !ok {
			t.Errorf("golden: %s should project (verify would emit if due now)", id)
		}
	}

	// card_idle under a mixed clock: everything created before the last
	// advance is >24h idle EXCEPT willLeave (mutated at the end) and late
	// (36h idle — also past). Both due-timeout and not-due are idle too —
	// idleness doesn't care about status.
	idleGot := breachIDs(rep, core.EventCardIdle)
	for _, id := range []string{dueTimeout.ID, notDue.ID, late.ID} {
		if _, ok := idleGot[id]; !ok {
			t.Errorf("idle: %s should project (>24h since mutation)", id)
		}
	}
	if _, ok := idleGot[willLeave.ID]; ok {
		t.Errorf("idle: %s just mutated — must not project", willLeave.ID)
	}
}

// The projection is read-only: no condition events appended, no conditions
// marked fired — the scheduler alone owns firing.
func TestBreaches_TemporalProjectionIsReadOnly(t *testing.T) {
	svc, fake, _ := newColdMonitorService(t, map[string]string{"review": "72h"}, "24h")
	ctx := context.Background()

	createColdCard(t, svc, "stuck", "review")
	fake.Advance(100 * time.Hour)

	if _, err := svc.Breaches(ctx, "", nil); err != nil {
		t.Fatalf("Breaches: %v", err)
	}
	if _, err := svc.Breaches(ctx, "", nil); err != nil {
		t.Fatalf("Breaches: %v", err)
	}

	page, err := svc.ListEventsPage(ctx, core.EventQuery{Limit: 100})
	if err != nil {
		t.Fatalf("ListEventsPage: %v", err)
	}
	for _, ev := range page.Items {
		if core.ConditionTypes() != nil {
			for _, ct := range core.ConditionTypes() {
				if ev.Type == ct {
					t.Fatalf("Breaches appended condition event %v — projection must be read-only", ev.Type)
				}
			}
		}
	}
}

// Item scans echo the applied ceiling and flag truncation so a client can
// tell a partial catch-up from a complete one.
func TestBreaches_TemporalScanLimitEchoAndTruncation(t *testing.T) {
	svc, _, st := newColdMonitorService(t, map[string]string{"review": "1h"}, "")
	ctx := context.Background()

	// Small case: scans ran, nothing clamped.
	createColdCard(t, svc, "one", "review")
	rep, err := svc.Breaches(ctx, "", []string{"status_timeout"})
	if err != nil {
		t.Fatalf("Breaches: %v", err)
	}
	if rep.Limit != 500 {
		t.Errorf("Limit = %d, want 500 echoed when item scans run", rep.Limit)
	}
	if rep.Truncated {
		t.Errorf("Truncated = true with one card, want false")
	}

	// Past the ListCards ceiling: insert 501 cards directly (fast path, no
	// events), all in the monitored status, all long past due.
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) // far before fake now
	for i := 0; i < 501; i++ {
		id := "card_bulk_" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)))
		if err := st.InsertCard(ctx, &core.Card{
			ID: id, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
			Title: id, Status: "review", Fields: map[string]any{}, Version: 1,
			CreatedAt: base, UpdatedAt: base, CreatedBy: "u", StatusSince: base,
		}, nil); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	rep, err = svc.Breaches(ctx, "", []string{"status_timeout"})
	if err != nil {
		t.Fatalf("Breaches: %v", err)
	}
	if !rep.Truncated {
		t.Errorf("501 past-due cards: Truncated = false, want true (partial catch-up must be detectable)")
	}
	if rep.Limit != 500 {
		t.Errorf("Limit = %d, want 500", rep.Limit)
	}
	if got := len(breachIDs(rep, core.EventStatusTimeout)); got > 500 {
		t.Errorf("projected %d items past the scan ceiling", got)
	}
}
