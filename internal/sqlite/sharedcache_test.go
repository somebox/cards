package sqlite

// Test-only shared-cache raw-DB constructor for package sqlite's internal
// tests (sprint 2026-07-18 Phase 3 / 07-12 P2a). This mirrors
// internal/sqlite/sqlitetest.OpenRaw — which CANNOT be imported here
// (sqlitetest imports sqlite; importing it back would be a cycle). Keep the
// DSN format in lockstep with sqlitetest.dsn: same shared-cache memory
// semantics, same unique-name-per-open isolation, same keep-alive rule (a
// shared-cache memory DB is dropped when its last connection closes).

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

func openSharedCacheRaw(t *testing.T, maxConns int) *sql.DB {
	t.Helper()
	name := "test_" + uuid.NewString()
	dsn := "file:" + name + "?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("openSharedCacheRaw: %v", err)
	}
	if maxConns > 0 {
		db.SetMaxOpenConns(maxConns)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("openSharedCacheRaw ping: %v", err)
	}
	// Keep-alive: pin one conn from a second handle so the named DB outlives
	// pool churn in db.
	keep, err := sql.Open("sqlite", dsn)
	if err != nil {
		db.Close()
		t.Fatalf("openSharedCacheRaw keep-alive: %v", err)
	}
	keepConn, err := keep.Conn(context.Background())
	if err != nil {
		keep.Close()
		db.Close()
		t.Fatalf("openSharedCacheRaw pin: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		keepConn.Close()
		keep.Close()
	})
	return db
}
