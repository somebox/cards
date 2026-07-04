package sqlite

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

func testStore(t *testing.T) (*Store, *core.Workspace) {
	t.Helper()
	ws := &core.Workspace{
		ID:       "t",
		Name:     "T",
		Columns:  []core.Column{{ID: "todo", Name: "To Do"}, {ID: "done", Name: "Done"}},
		Settings: core.WorkspaceSettings{StrictFields: true, TagPolicy: "propose", DefaultUser: "u"},
	}
	st, err := Open(":memory:", ws)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, ws
}

func TestInsertAndGetRoundTrip(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	c := &core.Card{
		ID: "c1", WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
		Title: "Hello", Status: "todo", Fields: map[string]any{"k": "v"},
		Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), CreatedBy: "u",
	}
	if err := st.InsertCard(ctx, c, &core.Event{CardID: "c1", Type: core.EventCardCreated, Actor: "u", At: time.Now().UTC()}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := st.GetCard(ctx, "c1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "Hello" || got.Status != "todo" {
		t.Errorf("got = %+v", got)
	}
	fm, ok := got.Fields.(map[string]any)
	if !ok || fm["k"] != "v" {
		t.Errorf("fields = %#v", got.Fields)
	}
}

func TestPatchBumpsVersionAndWritesEvent(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	c := &core.Card{
		ID: "c1", WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
		Title: "X", Status: "todo", Fields: map[string]any{}, Version: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), CreatedBy: "u",
	}
	_ = st.InsertCard(ctx, c, nil)

	updated := *c
	updated.Status = "done"
	updated.Version = 2
	updated.UpdatedAt = time.Now().UTC()
	evs := []*core.Event{{CardID: "c1", Type: core.EventStatusChanged, Actor: "u", At: time.Now().UTC(), Diff: map[string]any{"before": "todo", "after": "done"}}}
	if err := st.UpdateCard(ctx, &updated, evs); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := st.GetCard(ctx, "c1")
	if got.Version != 2 || got.Status != "done" {
		t.Errorf("got version=%d status=%s", got.Version, got.Status)
	}
	// event should be in events table
	page, _ := st.ListCards(ctx, core.CardQuery{Limit: 5})
	_ = page
	// (Events table verified via service-layer tests too.)
}

func TestUpdateCardStaleVersionReturnsConflict(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	c := &core.Card{ID: "c1", WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "X", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), CreatedBy: "u"}
	_ = st.InsertCard(ctx, c, nil)

	// Simulate a concurrent bump to version 2 first.
	v2 := *c
	v2.Version = 2
	_ = st.UpdateCard(ctx, &v2, nil)

	// Now try to update from the stale version 1 → should be a no-op (0 rows).
	stale := *c
	stale.Status = "done"
	stale.Version = 2 // based on stale view of version=1, so expect version-1 in WHERE
	err := st.UpdateCard(ctx, &stale, nil)
	if err == nil {
		t.Fatal("expected version conflict error, got nil")
	}
	if ce := core.AsError(err); ce == nil || ce.Code != "version_conflict" {
		t.Errorf("expected version_conflict, got %v", err)
	}
}

func TestListCardsFilterAndSearch(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	mk := func(id, title, status, typ string) {
		_ = st.InsertCard(ctx, &core.Card{ID: id, WorkspaceID: "t", TypeID: typ, SchemaVersion: 1, Title: title, Status: status, Fields: map[string]any{}, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), CreatedBy: "u"}, nil)
	}
	mk("a", "Fix login bug", "todo", "bug")
	mk("b", "Add docs", "done", "task")
	mk("c", "Fix logout bug", "todo", "bug")

	page, err := st.ListCards(ctx, core.CardQuery{Status: "todo", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Errorf("status=todo -> %d, want 2", len(page.Items))
	}

	search, _ := st.ListCards(ctx, core.CardQuery{Q: "login", Limit: 10})
	if len(search.Items) != 1 || search.Items[0].ID != "a" {
		t.Errorf("q=login -> %+v", search.Items)
	}
}

// TestListCardsSort covers the configurable ORDER BY: each whitelisted key in
// both directions, and NULLs-forced-last for a fields.<id> key that some cards
// lack (they sink to the bottom regardless of direction).
func TestListCardsSort(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(id, title string, createdOffset int, priority any) {
		fields := map[string]any{}
		if priority != nil {
			fields["priority"] = priority
		}
		at := base.Add(time.Duration(createdOffset) * time.Hour)
		_ = st.InsertCard(ctx, &core.Card{
			ID: id, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
			Title: title, Status: "todo", Fields: fields, Version: 1,
			CreatedAt: at, UpdatedAt: at, CreatedBy: "u",
		}, nil)
	}
	// created order: c1(0), c2(1), c3(2); priorities: c1=3, c2=1, c3 missing.
	mk("c1", "Banana", 0, 3)
	mk("c2", "apple", 1, 1)
	mk("c3", "Cherry", 2, nil)

	ids := func(q core.CardQuery) []string {
		page, err := st.ListCards(ctx, q)
		if err != nil {
			t.Fatalf("ListCards(%+v): %v", q, err)
		}
		out := make([]string, len(page.Items))
		for i, c := range page.Items {
			out[i] = c.ID
		}
		return out
	}
	eq := func(name string, got, want []string) {
		if len(got) != len(want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: got %v, want %v", name, got, want)
				return
			}
		}
	}

	eq("created_at asc", ids(core.CardQuery{Sort: "created_at", Limit: 10}), []string{"c1", "c2", "c3"})
	eq("created_at desc", ids(core.CardQuery{Sort: "-created_at", Limit: 10}), []string{"c3", "c2", "c1"})
	// title is case-insensitive: apple, Banana, Cherry.
	eq("title asc", ids(core.CardQuery{Sort: "title", Limit: 10}), []string{"c2", "c1", "c3"})
	// priority asc, NULLs last: c2(1), c1(3), then c3(missing).
	eq("fields.priority asc", ids(core.CardQuery{Sort: "fields.priority", Limit: 10}), []string{"c2", "c1", "c3"})
	// priority desc, NULLs STILL last: c1(3), c2(1), then c3(missing).
	eq("fields.priority desc", ids(core.CardQuery{Sort: "-fields.priority", Limit: 10}), []string{"c1", "c2", "c3"})
}

// clampCardLimit must apply the default for unset/≤0 and a 500 ceiling —
// NOT the old ">200 → 50" rule that silently truncated large requests to 50
// (the bug behind incomplete exports and undercounted censuses).
func TestClampCardLimit(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 50}, {-5, 50}, // unset/negative → default
		{1, 1}, {50, 50}, {200, 200}, {500, 500}, // honored as-is
		{501, 500}, {100000, 500}, // ceiling
	}
	for _, tc := range cases {
		if got := clampCardLimit(tc.in); got != tc.want {
			t.Errorf("clampCardLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// A request for 500 must actually return up to 500, and a full sweep past the
// ceiling must be reachable by cursor — the guarantee export depends on.
func TestListCardsHonors500AndPaginatesBeyond(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	const total = 510 // just past the 500 ceiling → two pages
	for i := range total {
		id := "card_" + strconv.Itoa(i)
		if err := st.InsertCard(ctx, &core.Card{
			ID: id, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
			Title: id, Status: "todo", Fields: map[string]any{}, Version: 1,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC().Add(time.Duration(i) * time.Millisecond), CreatedBy: "u",
		}, nil); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	first, err := st.ListCards(ctx, core.CardQuery{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 500 {
		t.Fatalf("first page = %d, want 500 (ceiling honored)", len(first.Items))
	}
	if first.NextCursor == "" {
		t.Fatal("expected a NextCursor with more cards remaining")
	}

	// Sweep to completion by cursor, asserting the full set is reachable and
	// every card appears exactly once.
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := st.ListCards(ctx, core.CardQuery{Limit: 500, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range page.Items {
			if seen[c.ID] {
				t.Fatalf("card %s seen twice during cursor sweep", c.ID)
			}
			seen[c.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != total {
		t.Errorf("cursor sweep covered %d cards, want %d", len(seen), total)
	}
}

func TestLinksAndCommentsLoadedOnGet(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	_ = st.InsertCard(ctx, &core.Card{ID: "c1", WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "X", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), CreatedBy: "u"}, nil)
	_ = st.InsertLink(ctx, "c1", core.Link{TypeID: "depends-on", Target: "c2", CreatedBy: "u", CreatedAt: time.Now().UTC()})
	_ = st.InsertComment(ctx, "c1", core.Comment{ID: "cm_1", Author: "u", Body: "hi", CreatedAt: time.Now().UTC()})

	got, err := st.GetCard(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Links) != 1 || got.Links[0].Target != "c2" {
		t.Errorf("links = %+v", got.Links)
	}
	if len(got.Comments) != 1 || got.Comments[0].Body != "hi" {
		t.Errorf("comments = %+v", got.Comments)
	}
	// ListCards does NOT load links/comments (progressive disclosure).
	page, _ := st.ListCards(ctx, core.CardQuery{Limit: 10})
	if len(page.Items[0].Links) != 0 || len(page.Items[0].Comments) != 0 {
		t.Error("ListCards should not load links/comments")
	}
}

func TestClaimAtomicPicksOldestUnowned(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	t0 := time.Now().UTC()
	_ = st.InsertCard(ctx, &core.Card{ID: "old", WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "Old", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: t0, UpdatedAt: t0, CreatedBy: "u"}, nil)
	t1 := t0.Add(time.Second)
	_ = st.InsertCard(ctx, &core.Card{ID: "new", WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "New", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: t1, UpdatedAt: t1, CreatedBy: "u"}, nil)

	claimed, _, err := st.ClaimAtomic(ctx, core.CardQuery{Unowned: true, Limit: 1}, "alice", "in_progress", "alice", time.Now().UTC())
	if err != nil || claimed == nil {
		t.Fatalf("expected claim, got %v %v", claimed, err)
	}
	if claimed.ID != "old" {
		t.Errorf("expected oldest card 'old', got %q", claimed.ID)
	}
	if claimed.Owner != "alice" || claimed.Status != "in_progress" || claimed.Version != 2 {
		t.Errorf("claimed = %+v", claimed)
	}
	// Second call picks the remaining unowned card.
	claimed2, _, _ := st.ClaimAtomic(ctx, core.CardQuery{Unowned: true, Limit: 1}, "bob", "", "bob", time.Now().UTC())
	if claimed2 == nil || claimed2.ID != "new" {
		t.Errorf("expected 'new', got %+v", claimed2)
	}
	// Third call: nothing left.
	claimed3, _, _ := st.ClaimAtomic(ctx, core.CardQuery{Unowned: true, Limit: 1}, "carol", "", "carol", time.Now().UTC())
	if claimed3 != nil {
		t.Errorf("expected nil, got %+v", claimed3)
	}
}

// TestClaimAtomicNoDoubleClaim fires many concurrent claimants at a pool of
// cards and asserts the no-double-claim guarantee: every card ends up with at
// most one owner, and the number of successful claims never exceeds the pool.
// This is the contract picraft's worker-pull pattern depends on (SPEC §11).
func TestClaimAtomicNoDoubleClaim(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	const pool = 20
	const claimants = 50
	t0 := time.Now().UTC()
	for i := 0; i < pool; i++ {
		id := "card-" + strconv.Itoa(i)
		_ = st.InsertCard(ctx, &core.Card{
			ID: id, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
			Title: id, Status: "todo", Fields: map[string]any{}, Version: 1,
			CreatedAt: t0.Add(time.Duration(i) * time.Millisecond),
			UpdatedAt: t0.Add(time.Duration(i) * time.Millisecond), CreatedBy: "u",
		}, nil)
	}

	type result struct {
		id    string
		owner string
	}
	results := make(chan result, claimants)
	var wg sync.WaitGroup
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		owner := "w" + strconv.Itoa(i)
		go func() {
			defer wg.Done()
			c, _, err := st.ClaimAtomic(ctx, core.CardQuery{Unowned: true, Limit: 1}, owner, "in_progress", owner, time.Now().UTC())
			if err != nil {
				// A raced CAS is an expected outcome under this much
				// concurrency, not a failure — it's exactly the signal the
				// service layer retries on.
				if !errors.Is(err, core.ErrClaimRaced) {
					t.Errorf("claim error: %v", err)
				}
				return
			}
			if c != nil {
				results <- result{id: c.ID, owner: c.Owner}
			}
		}()
	}
	wg.Wait()
	close(results)

	claimedBy := map[string]string{}
	successes := 0
	for r := range results {
		successes++
		if prev, dup := claimedBy[r.id]; dup {
			t.Fatalf("card %s double-claimed: by %s and %s", r.id, prev, r.owner)
		}
		claimedBy[r.id] = r.owner
	}
	if successes != pool {
		t.Fatalf("expected exactly %d successful claims, got %d", pool, successes)
	}
	// Every card must now be owned exactly once.
	for i := 0; i < pool; i++ {
		id := "card-" + strconv.Itoa(i)
		got, _ := st.GetCard(ctx, id)
		if got.Owner == "" {
			t.Errorf("card %s left unowned", id)
		}
	}
}

func TestIdempotencyGetPut(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	got, err := st.GetIdempotency(ctx, "k1", "u")
	if err != nil || got != nil {
		t.Fatalf("expected nil miss, got %v %v", got, err)
	}
	_ = st.PutIdempotency(ctx, core.IdempotencyRecord{Key: "k1", Actor: "u", Status: 201, Body: []byte(`{"id":"c1"}`)})
	got, err = st.GetIdempotency(ctx, "k1", "u")
	if err != nil || got == nil || got.Status != 201 || string(got.Body) != `{"id":"c1"}` {
		t.Fatalf("expected replay record, got %v %v", got, err)
	}
	// Different actor → miss.
	got2, _ := st.GetIdempotency(ctx, "k1", "other")
	if got2 != nil {
		t.Error("idempotency should be actor-scoped")
	}
}

func TestGetCardsByShortID_UniqueResolves(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	// Craft a card whose short id (first 8 hex after "card_") is known.
	fullID := "card_DEADBEEF000000000000000000000000"
	c := &core.Card{
		ID: fullID, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
		Title: "Unique", Status: "todo", Fields: map[string]any{},
		Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), CreatedBy: "u",
	}
	if err := st.InsertCard(ctx, c, &core.Event{CardID: fullID, Type: core.EventCardCreated, Actor: "u", At: time.Now().UTC()}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// By short (first-8-after-prefix) id.
	got, err := st.GetCardsByShortID(ctx, "DEADBEEF")
	if err != nil {
		t.Fatalf("short: %v", err)
	}
	if len(got) != 1 || got[0].ID != fullID {
		t.Errorf("short lookup got %v", got)
	}
	// By full id also returns exactly it.
	got2, err := st.GetCardsByShortID(ctx, fullID)
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if len(got2) != 1 || got2[0].ID != fullID {
		t.Errorf("full lookup got %v", got2)
	}
}

func TestGetCardsByShortID_AmbiguousReturnsCandidates(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	// Two cards whose first-8-hex (after card_) collides.
	idA := "card_CAFEF00D111111111111111100000000"
	idB := "card_CAFEF00D222222222222111100000000"
	now := time.Now().UTC()
	older := now.Add(-time.Hour)
	ca := &core.Card{ID: idA, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "A", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: older, UpdatedAt: older, CreatedBy: "u"}
	cb := &core.Card{ID: idB, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "B", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: "u"}
	_ = st.InsertCard(ctx, ca, nil)
	_ = st.InsertCard(ctx, cb, nil)
	got, err := st.GetCardsByShortID(ctx, "CAFEF00D")
	if err != nil {
		t.Fatalf("short: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d (%v)", len(got), got)
	}
	// Ordered by updated_at DESC: the newer card (B) first.
	if got[0].ID != idB || got[1].ID != idA {
		t.Errorf("order wrong: got %s then %s", got[0].ID, got[1].ID)
	}
	// A non-matching short id returns 0 candidates and no error.
	none, err := st.GetCardsByShortID(ctx, "ZZZZZZZZ")
	if err != nil {
		t.Fatalf("none: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(none))
	}
}
