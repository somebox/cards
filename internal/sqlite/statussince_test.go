package sqlite

import (
	"context"
	"testing"
	"time"
)

// Migrating a pre-seam-3d cards table (no status_since) adds the column and
// backfills it: from the latest status_changed event's timestamp for a card
// that has moved, else from created_at for one that hasn't. Idempotent.
func TestMigrateStatusSince_FromPreSeam3dDB(t *testing.T) {
	db := openSharedCacheRaw(t, 1)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `CREATE TABLE cards (
		id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, type_id TEXT NOT NULL,
		schema_version INTEGER NOT NULL, title TEXT NOT NULL, status TEXT NOT NULL,
		owner TEXT, tags TEXT, fields TEXT NOT NULL, version INTEGER NOT NULL,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL, created_by TEXT NOT NULL)`); err != nil {
		t.Fatalf("old cards schema: %v", err)
	}
	// Post-migration events shape (so migrateEventsScope no-ops and this test
	// isolates the status_since migration).
	if _, err := db.ExecContext(ctx, `CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT, card_id TEXT, board_id TEXT,
		scope TEXT NOT NULL DEFAULT 'card', type TEXT NOT NULL, actor TEXT NOT NULL,
		at TEXT NOT NULL, diff TEXT)`); err != nil {
		t.Fatalf("events schema: %v", err)
	}

	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	moved := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)

	// c1 has moved: a status_changed event exists — backfill from its `at`.
	if _, err := db.ExecContext(ctx, `INSERT INTO cards(id,workspace_id,type_id,schema_version,title,status,fields,version,created_at,updated_at,created_by)
		VALUES('c1','w','task',1,'T','in_progress','{}',2,?,?,'u')`, created, moved); err != nil {
		t.Fatalf("seed c1: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO events(card_id,scope,type,actor,at,diff) VALUES('c1','card','status_changed','u',?,'{}')`, moved); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	// c2 has never moved: no status_changed event — backfill from created_at.
	if _, err := db.ExecContext(ctx, `INSERT INTO cards(id,workspace_id,type_id,schema_version,title,status,fields,version,created_at,updated_at,created_by)
		VALUES('c2','w','task',1,'T','todo','{}',1,?,?,'u')`, created, created); err != nil {
		t.Fatalf("seed c2: %v", err)
	}

	// Drive the full Init path, as a real deployment would.
	st := &Store{db: db}
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init/migrate: %v", err)
	}

	var c1Since, c2Since string
	if err := db.QueryRowContext(ctx, `SELECT status_since FROM cards WHERE id='c1'`).Scan(&c1Since); err != nil {
		t.Fatalf("c1 status_since (column should exist): %v", err)
	}
	if c1Since != moved {
		t.Errorf("c1 status_since = %q, want %q (backfilled from status_changed)", c1Since, moved)
	}
	if err := db.QueryRowContext(ctx, `SELECT status_since FROM cards WHERE id='c2'`).Scan(&c2Since); err != nil {
		t.Fatal(err)
	}
	if c2Since != created {
		t.Errorf("c2 status_since = %q, want %q (backfilled from created_at)", c2Since, created)
	}

	// Idempotent: re-running the migration directly is a no-op, values unchanged.
	if err := st.migrateStatusSince(ctx); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
	var c1Again string
	if err := db.QueryRowContext(ctx, `SELECT status_since FROM cards WHERE id='c1'`).Scan(&c1Again); err != nil {
		t.Fatal(err)
	}
	if c1Again != c1Since {
		t.Errorf("re-run changed status_since: %q -> %q", c1Since, c1Again)
	}
}

// A fresh DB (Init on an empty file) needs no migration and StatusSince is
// set going forward by InsertCard/UpdateCard/ClaimAtomic — not this migration.
func TestMigrateStatusSince_NoOpOnFreshDB(t *testing.T) {
	db := openSharedCacheRaw(t, 1)
	st := &Store{db: db}
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.migrateStatusSince(context.Background()); err != nil {
		t.Errorf("migrateStatusSince on a fresh DB should be a no-op, got: %v", err)
	}
}
