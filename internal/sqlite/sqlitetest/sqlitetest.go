// Package sqlitetest is the single constructor for in-memory SQLite test
// stores (sprint 2026-07-18 Phase 3 / 07-12 P2a). Every test that needs a
// store — or a raw *sql.DB — opens through here.
//
// Each Open mints a UNIQUE shared-cache name
// (file:test_<uuid>?mode=memory&cache=shared), which gives two things plain
// ":memory:" cannot:
//
//   - per-test isolation under parallel go test (distinct names are
//     distinct databases), and
//   - a >1-connection topology — connections in the pool share one cache,
//     so concurrent readers/writers exercise the same locking behavior a
//     pooled production store would (the ground P2b measurement stands on).
//
// A shared-cache memory DB is dropped when its LAST connection closes, so
// the harness pins a keep-alive connection for the store's lifetime and
// releases it on test cleanup (after the store closes).
//
// The DSN deliberately matches production locking (_txlock=immediate +
// busy_timeout) but NOT journal_mode(WAL) — in-memory databases have no
// journal to put in WAL mode, and shared-cache memory uses table locks;
// the isolation/lifetime semantics are pinned by this package's tests.
package sqlitetest

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
	_ "modernc.org/sqlite" // pure-Go driver (raw-db variant)
)

// dsn builds the shared-cache memory DSN for name. This is the one code
// path — both the Store and raw-db variants use it.
func dsn(name string) string {
	return "file:" + name + "?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
}

// keepAlive holds one connection open so the named memory DB outlives pool
// churn in the store under test. Released by the registered t.Cleanup.
type keepAlive struct {
	db   *sql.DB
	conn *sql.Conn
}

// pin opens a second handle to name and checks out one connection for the
// lifetime of the test.
func pin(t *testing.T, name string) *keepAlive {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(name))
	if err != nil {
		t.Fatalf("sqlitetest: open keep-alive: %v", err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		t.Fatalf("sqlitetest: pin keep-alive conn: %v", err)
	}
	k := &keepAlive{db: db, conn: conn}
	t.Cleanup(func() {
		conn.Close()
		db.Close()
	})
	return k
}

func uniqueName() string {
	return "test_" + uuid.NewString()
}

// Open returns a migrated *sqlite.Store on a fresh, uniquely-named
// shared-cache memory DB, with maxConns pool connections (0 = unlimited;
// tests that want the production single-conn shape pass 1). The store is
// closed on test cleanup, then the keep-alive is released.
func Open(t *testing.T, ws *core.Workspace, maxConns int) *sqlite.Store {
	t.Helper()
	name := uniqueName()
	st, err := sqlite.OpenDSN(dsn(name), maxConns, ws)
	if err != nil {
		t.Fatalf("sqlitetest: open store: %v", err)
	}
	pin(t, name)
	t.Cleanup(func() { st.Close() })
	return st
}

// OpenRaw returns a raw *sql.DB on a fresh, uniquely-named shared-cache
// memory DB (for tests that drive SQL directly, e.g. migration tests). The
// caller runs its own DDL/migrations. maxConns semantics as Open.
func OpenRaw(t *testing.T, maxConns int) *sql.DB {
	t.Helper()
	name := uniqueName()
	db, err := sql.Open("sqlite", dsn(name))
	if err != nil {
		t.Fatalf("sqlitetest: open raw db: %v", err)
	}
	if maxConns > 0 {
		db.SetMaxOpenConns(maxConns)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("sqlitetest: ping raw db: %v", err)
	}
	pin(t, name)
	t.Cleanup(func() { db.Close() })
	return db
}

// Name documents the naming scheme for assertion in tests.
func Name() string { return fmt.Sprintf("test_<uuid> (mode=memory&cache=shared)") }
