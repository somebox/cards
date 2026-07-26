// Package core — store.go
//
// The Store interface is defined here (consumer-side) to avoid an import
// cycle between core and storage implementations. internal/sqlite implements
// this interface. See docs/architecture/index.md (Storage) and
// docs/spec/data-model.md (§3 Workspace/storage, §4 Core data model).
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
	// CountCards returns how many cards match q — the same filter/status/
	// type/blocked semantics as ListCards, but a scalar COUNT(*) with NO limit
	// clamp (q.Limit/Sort/Cursor are ignored). Use it for census/aggregate
	// counts where materializing rows would be wasteful and the 500-row list
	// ceiling would silently undercount.
	CountCards(ctx context.Context, q CardQuery) (int, error)
	GetCard(ctx context.Context, id string) (*Card, error) // loads links + comments
	// GetCardsByShortID returns cards whose id equals short or whose leading 8 hex chars
	// suffix equals short. Used by ResolveCard (1e); returns 0, 1, or many.
	GetCardsByShortID(ctx context.Context, short string) ([]Card, error)
	InsertCard(ctx context.Context, c *Card, ev *Event) error
	UpdateCard(ctx context.Context, c *Card, evs []*Event) error
	// DeleteCard removes the card row and its dependent rows (links in both
	// directions, comments, search index, condition marks) and appends the
	// tombstone event in the same transaction. The append-only event history
	// (including ev) is retained. Returns ErrNotFound if the row is already
	// gone.
	DeleteCard(ctx context.Context, id string, ev *Event) error
	// ClaimAtomic picks the oldest unowned card matching q (updated_at ASC,
	// id ASC) and atomically sets its owner (+status). Returns the claimed
	// card, or nil if nothing matched. SPEC §11 take-next.
	ClaimAtomic(ctx context.Context, q CardQuery, owner, status, actor string, now time.Time) (*Card, []*Event, error)

	// Events — the append-only journal. See EventLog (events.go).
	EventLog

	// Links
	ListLinks(ctx context.Context, cardID string) ([]Link, error)
	InsertLink(ctx context.Context, cardID string, l Link) error
	DeleteLink(ctx context.Context, cardID, typeID, target string) (Link, error)
	// AllLinks returns every link edge (source→target) in the workspace, for
	// building in/outbound relationship views without N+1 queries.
	AllLinks(ctx context.Context) ([]LinkEdge, error)
	// Blockers returns the ids of not-yet-done cards currently blocking
	// cardID (targets of its blocked-by/depends-on links); cardID is blocked
	// iff non-empty — the same definition CardQuery.Blocked applies. (3c)
	Blockers(ctx context.Context, cardID string) ([]string, error)
	// BlockingDependents returns the ids of cards whose blocked-by/depends-on
	// link targets targetID — the cards to re-evaluate when targetID's status
	// changes (Events seam 3c).
	BlockingDependents(ctx context.Context, targetID string) ([]string, error)

	// Comments
	ListComments(ctx context.Context, cardID string) ([]Comment, error)
	InsertComment(ctx context.Context, cardID string, c Comment) error
	UpdateComment(ctx context.Context, cardID, commentID, body string, editedAt time.Time) error
	// CommentCounts returns card_id→comment count for all cards (one query).
	CommentCounts(ctx context.Context) (map[string]int, error)

	// Idempotency
	GetIdempotency(ctx context.Context, key, actor string) (*IdempotencyRecord, error)
	PutIdempotency(ctx context.Context, rec IdempotencyRecord) error

	// Condition marks (seam 3d fired-marker for temporal conditions).
	// ConditionFired reports whether (cardID, t, key) has already fired.
	ConditionFired(ctx context.Context, cardID string, t EventType, key string) (bool, error)
	// MarkConditionFired atomically records a first fire, returning true iff
	// this call was the one to record it (an atomic check-and-set).
	MarkConditionFired(ctx context.Context, cardID string, t EventType, key string, firedAt time.Time) (bool, error)
	// MarkConditionFiredAndAppend records a first fire AND appends the escalated
	// event ev in a single transaction: either both the fired-marker and the
	// durable event row commit, or neither does. This closes the mark-then-append
	// crash window for persist:true conditions, where a crash between the two
	// separate writes would lose the event forever and suppress re-fire. Returns
	// true iff this call recorded the fire; when false, nothing is appended
	// (already fired). ev must be stamped (Actor/At) before the call; on a first
	// fire ev.ID is assigned.
	MarkConditionFiredAndAppend(ctx context.Context, cardID string, t EventType, key string, firedAt time.Time, ev *Event) (bool, error)

	// Users
	ListUsers(ctx context.Context) ([]User, error)
	InsertUser(ctx context.Context, u User) error

	// Close releases any underlying resources.
	Close() error
}

// SearchableFieldsSetter is an OPTIONAL Store capability: a store that keeps a
// full-text index can be told which fields each card type declares searchable
// (CardType.SearchableFields), keyed by type id. A type absent from the map —
// or present with an empty list — places no restriction and indexes every
// field value, which is both the backward-compatible reading and the only one
// that doesn't silently empty the index for types that never declared it.
//
// It is deliberately not part of Store: search is an accelerator, and a store
// without an index (the in-memory event-log fake) should not have to stub it.
type SearchableFieldsSetter interface {
	SetSearchableFields(byType map[string][]string) error
}

// searchableFieldsByType projects the declaration out of the loaded schemas.
// Types declaring nothing are omitted rather than mapped to an empty slice —
// the two mean the same thing to the store, and omitting keeps the digest
// (and so the rebuild decision) stable when a type is added without one.
func searchableFieldsByType(types map[string]*CardType) map[string][]string {
	out := make(map[string][]string, len(types))
	for id, ct := range types {
		if len(ct.SearchableFields) > 0 {
			out[id] = append([]string(nil), ct.SearchableFields...)
		}
	}
	return out
}
