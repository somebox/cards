// Package sqlite is the default storage implementation: cards, events, links,
// comments, users, idempotency keys, and an FTS5 virtual table for search.
//
// One workspace = one SQLite file. See docs/ARCHITECTURE.md (Storage).
package sqlite

// Store implements store.Store using modernc.org/sqlite (pure Go) or a CGO
// SQLite driver, depending on build tags.
type Store struct {
	// TODO: open DB, create tables, migrate, transactional card+event writes,
	// FTS5 index maintenance, cursor pagination.
}
