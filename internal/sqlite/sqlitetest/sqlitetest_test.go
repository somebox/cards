package sqlitetest

// Sprint 2026-07-18 Phase 3 step 3.1 — the stop-gate proof, BEFORE any mass
// migration of :memory: call sites. These tests characterize modernc.org/
// sqlite's shared-cache named-memory-DB semantics that the harness depends
// on. If any of these fail or flake, P2a parks with this file as evidence
// and no call sites move.

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

// Two connections to the SAME shared-cache name form one database: a commit
// on one is readable on the other. (Plain ":memory:" fails this — each conn
// gets its own DB, which is why the production store pins MaxOpenConns(1).)
func TestSharedCachePoolCoherence(t *testing.T) {
	db := OpenRaw(t, 4)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `CREATE TABLE probe (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO probe (v) VALUES ('committed')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// A second, distinct pool connection must see the committed row.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	var got string
	if err := conn.QueryRowContext(ctx, `SELECT v FROM probe WHERE id = 1`).Scan(&got); err != nil {
		t.Fatalf("read via second conn: %v", err)
	}
	if got != "committed" {
		t.Fatalf("second conn read %q, want committed", got)
	}
}

// The named trap (sprint plan Phase 3.1): shared-cache memory DBs serialize
// on TABLE locks via sqlite3_unlock_notify — a read on a table with an open
// write tx from another conn BLOCKS until commit/rollback. busy_timeout does
// NOT govern this path, and the wait ignores context — a goroutine that
// reads while IT holds the write tx self-deadlocks (proven during harness
// bring-up: reader + writer on one goroutine hung until the test timeout).
//
// This test characterizes the semantics the harness relies on: the blocked
// reader never observes uncommitted rows (it observes nothing until the
// writer finishes), and it unblocks with the full committed state.
func TestSharedCacheUncommittedIsolation(t *testing.T) {
	db := OpenRaw(t, 4)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `CREATE TABLE probe (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO probe (v) VALUES ('old')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil) // _txlock=immediate: grabs the write lock up front
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO probe (v) VALUES ('uncommitted')`); err != nil {
		t.Fatalf("tx insert: %v", err)
	}

	// Read on a SEPARATE goroutine (same-goroutine read here would
	// self-deadlock — see header). It must block on the table lock.
	type result struct {
		vals []string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		rows, err := db.QueryContext(ctx, `SELECT v FROM probe ORDER BY id`)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer rows.Close()
		var vals []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				done <- result{err: err}
				return
			}
			vals = append(vals, v)
		}
		done <- result{vals: vals}
	}()

	select {
	case r := <-done:
		t.Fatalf("reader returned while writer tx open (%+v) — table lock did not block it", r)
	case <-time.After(200 * time.Millisecond):
		// still blocked: correct — unlock_notify holds the reader
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("reader errored after commit: %v", r.err)
		}
		if len(r.vals) != 2 || r.vals[0] != "old" || r.vals[1] != "uncommitted" {
			t.Fatalf("reader saw %v, want [old uncommitted] — it must see exactly the committed state", r.vals)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not unblock after commit")
	}
}

// Two harness opens get DIFFERENT databases: nothing leaks between tests
// sharing the process (parallel go test).
func TestSharedCacheUniqueNamesIsolate(t *testing.T) {
	a := OpenRaw(t, 2)
	b := OpenRaw(t, 2)
	ctx := context.Background()

	if _, err := a.ExecContext(ctx, `CREATE TABLE only_a (id INTEGER)`); err != nil {
		t.Fatalf("create on a: %v", err)
	}
	var name string
	err := b.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE name = 'only_a'`).Scan(&name)
	if err != sql.ErrNoRows {
		t.Fatalf("b sees a's schema (err=%v) — shared-cache names are not isolating", err)
	}
}

// The keep-alive rules: while one conn stays pinned the named DB survives
// full pool closure; once the last conn closes the DB is dropped. This is
// the lifetime invariant the harness exists to enforce.
func TestSharedCacheKeepAliveLifetime(t *testing.T) {
	ctx := context.Background()
	name := uniqueName()

	k := pin(t, name) // harness keep-alive (released at test cleanup)

	db1, err := sql.Open("sqlite", dsn(name))
	if err != nil {
		t.Fatalf("open db1: %v", err)
	}
	if _, err := db1.ExecContext(ctx, `CREATE TABLE keeps (id INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db1.ExecContext(ctx, `INSERT INTO keeps (id) VALUES (1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db1.Close(); err != nil { // entire pool closed — DB must survive on the keep-alive
		t.Fatalf("close db1: %v", err)
	}

	db2, err := sql.Open("sqlite", dsn(name))
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	var n int
	if err := db2.QueryRowContext(ctx, `SELECT count(*) FROM keeps`).Scan(&n); err != nil {
		t.Fatalf("DB did not survive pool closure despite keep-alive: %v", err)
	}
	if n != 1 {
		t.Fatalf("count after reopen = %d, want 1", n)
	}
	db2.Close()

	// Release the keep-alive: the DB is dropped; a fresh handle starts empty.
	k.conn.Close()
	k.db.Close()
	db3, err := sql.Open("sqlite", dsn(name))
	if err != nil {
		t.Fatalf("open db3: %v", err)
	}
	defer db3.Close()
	var tname string
	err = db3.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE name = 'keeps'`).Scan(&tname)
	if err != sql.ErrNoRows {
		t.Fatalf("DB survived last-connection close (err=%v) — drop rule violated", err)
	}
}

// The Store variant opens a migrated store whose pool shares one cache —
// concurrent writers serialize cleanly (the >1-conn topology P2b will
// measure). Run under -race with confidence.
func TestStoreVariantConcurrentWriters(t *testing.T) {
	st := Open(t, &core.Workspace{ID: "t", Name: "t"}, 4)
	ctx := context.Background()

	const writers = 8
	const per = 25
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				id := strings.Repeat("u", 1) + "_" + strings.Repeat("x", w%3+1) + "_" + string(rune('a'+w)) + "_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
				if err := st.InsertUser(ctx, core.User{ID: id, Kind: "agent"}); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent insert: %v", err)
	}
}

// The store variant is coherent across its own pool: a write is readable
// immediately through a different checkout.
func TestStoreVariantReadYourWrites(t *testing.T) {
	st := Open(t, &core.Workspace{ID: "t", Name: "t"}, 4)
	ctx := context.Background()

	if err := st.InsertUser(ctx, core.User{ID: "u1", Kind: "human"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	users, err := st.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, u := range users {
		if u.ID == "u1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("read back missing u1 in %+v", users)
	}
}
