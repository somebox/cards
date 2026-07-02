package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

func insertUnowned(t *testing.T, st *Store, id string) {
	t.Helper()
	c := &core.Card{
		ID: id, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
		Title: "T " + id, Status: "todo", Fields: map[string]any{},
		Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), CreatedBy: "u",
	}
	if err := st.InsertCard(context.Background(), c, nil); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

// The claim's events must come from the shared constructors: Version 1 and the
// canonical before/after diff shape, matching every other mutation path.
func TestClaimAtomicEventsUseConstructors(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	insertUnowned(t, st, "c1")

	claimed, evs, err := st.ClaimAtomic(ctx, core.CardQuery{Unowned: true, Limit: 1}, "alice", "done", "alice", time.Now().UTC())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed == nil || claimed.Owner != "alice" {
		t.Fatalf("claimed = %+v", claimed)
	}
	if len(evs) != 2 {
		t.Fatalf("got %d events, want owner_changed + status_changed", len(evs))
	}
	for _, ev := range evs {
		if ev.Version != 1 {
			t.Errorf("%s: Version = %d, want 1", ev.Type, ev.Version)
		}
		if ev.Actor != "alice" {
			t.Errorf("%s: Actor = %q, want alice", ev.Type, ev.Actor)
		}
		if ev.ID == 0 {
			t.Errorf("%s: event not persisted (ID 0)", ev.Type)
		}
	}
	if evs[0].Type != core.EventOwnerChanged || evs[1].Type != core.EventStatusChanged {
		t.Errorf("event types = %s, %s", evs[0].Type, evs[1].Type)
	}
}

// A failed FTS upsert must fail the claim (roll back), not commit a mutation
// with silently missing side effects (DEBT-01).
func TestClaimAtomicFTSFailureRollsBack(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	insertUnowned(t, st, "c1")

	if _, err := st.db.Exec(`DROP TABLE fts_cards`); err != nil {
		t.Fatalf("drop fts: %v", err)
	}
	claimed, _, err := st.ClaimAtomic(ctx, core.CardQuery{Unowned: true, Limit: 1}, "alice", "", "alice", time.Now().UTC())
	if err == nil {
		t.Fatalf("claim succeeded despite FTS failure: %+v", claimed)
	}
	// The claim must have rolled back: card still unowned.
	got, gerr := st.GetCard(ctx, "c1")
	if gerr != nil {
		t.Fatalf("get: %v", gerr)
	}
	if got.Owner != "" {
		t.Errorf("owner = %q after rolled-back claim, want unowned", got.Owner)
	}
	if got.Version != 1 {
		t.Errorf("version = %d after rolled-back claim, want 1", got.Version)
	}
}

// A real DB failure during the candidate scan must surface as an error, not be
// masked as "no cards available" (DEBT-02).
func TestClaimAtomicScanErrorPropagates(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	insertUnowned(t, st, "c1")

	if _, err := st.db.Exec(`ALTER TABLE cards RENAME TO cards_broken`); err != nil {
		t.Fatalf("rename: %v", err)
	}
	_, _, err := st.ClaimAtomic(ctx, core.CardQuery{Unowned: true, Limit: 1}, "alice", "", "alice", time.Now().UTC())
	if err == nil {
		t.Fatal("scan failure returned (nil, nil, nil); want error")
	}
}

// No matching card is not an error: (nil, nil, nil).
func TestClaimAtomicNoMatchIsNotAnError(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()

	claimed, evs, err := st.ClaimAtomic(ctx, core.CardQuery{Unowned: true, Limit: 1}, "alice", "", "alice", time.Now().UTC())
	if err != nil || claimed != nil || evs != nil {
		t.Errorf("empty pool: got (%+v, %v, %v), want (nil, nil, nil)", claimed, evs, err)
	}
}
