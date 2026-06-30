// Package core — store.go
//
// The Store interface is defined here (consumer-side) to avoid an import
// cycle between core and storage implementations. internal/sqlite implements
// this interface. See docs/ARCHITECTURE.md (Storage) and docs/SPEC.md (§3, §8).
package core

import (
	"context"
	"time"
)

// Store is the persistence interface. Implementations must be safe for
// concurrent use and transactional on mutations that write both an event and
// the materialized card row. See SPEC.md §11.
type Store interface {
	// Init creates tables/indexes if missing.
	Init(ctx context.Context) error

	// Cards
	ListCards(ctx context.Context, q CardQuery) (*Page[Card], error)
	GetCard(ctx context.Context, id string) (*Card, error) // loads links + comments
	InsertCard(ctx context.Context, c *Card, ev *Event) error
	UpdateCard(ctx context.Context, c *Card, evs []*Event) error
	// ClaimAtomic picks the oldest unowned card matching q (updated_at ASC,
	// id ASC) and atomically sets its owner (+status). Returns the claimed
	// card, or nil if nothing matched. SPEC §11 take-next.
	ClaimAtomic(ctx context.Context, q CardQuery, owner, status, actor string, now time.Time) (*Card, []*Event, error)

	// Events
	ListEvents(ctx context.Context, q EventQuery) ([]Event, error)
	// ListEventsPage is the cursor-paged catch-up feed (GET /v1/events).
	// Events are ordered by id ASC; NextCursor is the last event id returned
	// (use as cursor=/since= to continue). Supports actor/owner/type/card-type
	// filters; AfterID is the floor (events with id > AfterID).
	ListEventsPage(ctx context.Context, q EventQuery) (*Page[Event], error)

	// Links
	ListLinks(ctx context.Context, cardID string) ([]Link, error)
	InsertLink(ctx context.Context, cardID string, l Link) error
	DeleteLink(ctx context.Context, cardID, typeID, target string) (Link, error)

	// Comments
	ListComments(ctx context.Context, cardID string) ([]Comment, error)
	InsertComment(ctx context.Context, cardID string, c Comment) error
	UpdateComment(ctx context.Context, cardID, commentID, body string, editedAt time.Time) error

	// Idempotency
	GetIdempotency(ctx context.Context, key, actor string) (*IdempotencyRecord, error)
	PutIdempotency(ctx context.Context, rec IdempotencyRecord) error

	// Users
	ListUsers(ctx context.Context) ([]User, error)
	InsertUser(ctx context.Context, u User) error

	// Close releases any underlying resources.
	Close() error
}
