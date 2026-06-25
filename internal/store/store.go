// Package store defines the storage interface that the core service depends
// on. The default implementation lives in internal/sqlite (SQLite + FTS5).
//
// See docs/ARCHITECTURE.md (Storage) and docs/SPEC.md (§3, §8).
package store

import "context"

// Store is the persistence interface. Implementations must be safe for
// concurrent use and transactional on mutations that write both an event and
// the materialized card row.
type Store interface {
	// TODO: Card CRUD, event append, link/comment storage, idempotency-key
	// dedupe, FTS search, cursor pagination. See SPEC.md §11.

	// Close releases any underlying resources.
	Close() error
}

// ErrNotFound is returned when a card or other resource does not exist.
type ErrNotFound struct{ Resource string }

func (e *ErrNotFound) Error() string { return "not_found: " + e.Resource }

// ErrVersionConflict is returned on a stale optimistic-concurrency write.
type ErrVersionConflict struct{ CurrentVersion int }

func (e *ErrVersionConflict) Error() string {
	return "version_conflict"
}

// Use context.Context on all methods that touch storage. Silence unused
// import warning until methods are added.
var _ = context.Background
