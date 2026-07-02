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

	st := &Store{db: db}
	if err := st.migrateEventsScope(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
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
