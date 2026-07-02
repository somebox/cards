package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

// Migrating a pre-seam-2a events table (card_id NOT NULL, no board_id/scope)
// adds the columns, backfills scope='card', keeps existing rows/queries intact,
// and relaxes card_id to nullable. Idempotent on re-run.
func TestMigrateEventsScope_FromPreScopeDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT, card_id TEXT NOT NULL,
		type TEXT NOT NULL, actor TEXT NOT NULL, at TEXT NOT NULL, diff TEXT)`); err != nil {
		t.Fatalf("old schema: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, cid := range []string{"c1", "c2", "c1"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO events(card_id,type,actor,at,diff) VALUES(?,?,?,?,?)`,
			cid, "status_changed", "u", now, `{"before":"a","after":"b"}`); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Drive the FULL Init path (CREATE IF NOT EXISTS is a no-op on the old table,
	// then migrate, then index scope) — what a real deployment hits, and it
	// catches ordering bugs the direct migrate call would miss.
	st := &Store{db: db}
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init/migrate: %v", err)
	}
	var idxCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_events_scope'`).Scan(&idxCount)
	if idxCount != 1 {
		t.Errorf("idx_events_scope should exist after Init, got %d", idxCount)
	}

	var scoped int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE scope='card'`).Scan(&scoped); err != nil {
		t.Fatalf("scope query (column should exist): %v", err)
	}
	if scoped != 3 {
		t.Errorf("rows backfilled to scope='card' = %d, want 3", scoped)
	}
	var c1 int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE card_id='c1'`).Scan(&c1)
	if c1 != 2 {
		t.Errorf("existing card_id='c1' query = %d, want 2 (unaffected)", c1)
	}
	// card_id is now nullable — a board event inserts.
	if _, err := db.ExecContext(ctx, `INSERT INTO events(board_id,scope,type,actor,at,diff) VALUES('b1','board','x','u',?,'{}')`, now); err != nil {
		t.Errorf("board event insert (card_id should be nullable now): %v", err)
	}
	// Idempotent.
	if err := st.migrateEventsScope(ctx); err != nil {
		t.Errorf("re-migrate should be a no-op: %v", err)
	}
}

// A board-scoped event (card_id empty, scope=board) round-trips through the
// store; a card event keeps scope off the wire.
func TestEventScope_BoardEventRoundTrip(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	card := &core.Event{CardID: "c1", Version: 1, Type: core.EventStatusChanged, Actor: "u", At: now, Diff: map[string]any{"before": "a", "after": "b"}}
	board := &core.Event{BoardID: "b1", Scope: "board", Version: 1, Type: core.EventStatusChanged, Actor: "u", At: now, Diff: map[string]any{}}
	if err := st.Append(ctx, card, board); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := st.List(ctx, core.EventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d events, want 2", len(got))
	}
	var b, c *core.Event
	for i := range got {
		switch got[i].Scope {
		case "board":
			b = &got[i]
		default:
			c = &got[i]
		}
	}
	if b == nil {
		t.Fatal("board event not found / scope not 'board'")
	}
	if b.CardID != "" || b.BoardID != "b1" {
		t.Errorf("board event card_id=%q board_id=%q, want '' and 'b1'", b.CardID, b.BoardID)
	}
	if c == nil || c.CardID != "c1" || c.Scope != "" {
		t.Errorf("card event should keep scope empty (off the wire): %+v", c)
	}
}

// The feed can filter by board_id and scope, distinct from card_id / card-type
// membership; existing card filters are unaffected. (Events seam 2c)
func TestEventFeed_FilterByBoardAndScope(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.Append(ctx,
		&core.Event{CardID: "c1", Version: 1, Type: core.EventStatusChanged, Actor: "u", At: now, Diff: map[string]any{}},
		&core.Event{BoardID: "b1", Scope: "board", Version: 1, Type: "wip_exceeded", Actor: "u", At: now, Diff: map[string]any{}},
		&core.Event{BoardID: "b2", Scope: "board", Version: 1, Type: "wip_exceeded", Actor: "u", At: now, Diff: map[string]any{}},
	); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got, _ := st.List(ctx, core.EventQuery{BoardID: "b1", Limit: 10}); len(got) != 1 || got[0].BoardID != "b1" {
		t.Errorf("board_id=b1 feed = %+v, want 1 event for b1", got)
	}
	boards, _ := st.List(ctx, core.EventQuery{Scope: "board", Limit: 10})
	if len(boards) != 2 || boards[0].ID > boards[1].ID {
		t.Errorf("scope=board feed = %+v, want 2 events ordered by id", boards)
	}
	if got, _ := st.List(ctx, core.EventQuery{Scope: "card", Limit: 10}); len(got) != 1 || got[0].CardID != "c1" {
		t.Errorf("scope=card feed = %+v, want the single card event", got)
	}
	if got, _ := st.List(ctx, core.EventQuery{CardID: "c1", Limit: 10}); len(got) != 1 {
		t.Errorf("existing card_id filter = %d, want 1 (unaffected)", len(got))
	}
	if got, _ := st.List(ctx, core.EventQuery{Limit: 10}); len(got) != 3 {
		t.Errorf("unfiltered feed = %d events, want 3 (card + both board)", len(got))
	}
}
