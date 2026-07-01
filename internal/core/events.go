// Package core — events.go
//
// The event core: the one emission seam. Every event — mutation, board, or
// condition — is built with a constructor (never a raw literal) and flows
// through the Emitter, which stamps identity/time, persists (for durable
// facts), publishes to the Bus, and notifies observers, in that order.
//
// Design: docs/EVENTS.md. Invariants enforced here:
//   - persist before publish (dispatchCommitted is package-private; durable
//     card writes go through Service.commitCard);
//   - call sites never assign ID/Actor/At (the store assigns ID; the seam
//     stamps Actor/At);
//   - event payloads are named contracts (the Diff structs below), kept
//     wire-compatible and pinned by golden fixtures in events_test.go.
package core

import (
	"context"
	"sync"
	"time"
)

// EventLog is the append-only journal of events: durable, ordered by id,
// replayable. Card-mutation events are appended transactionally with the card
// write (Store.UpdateCard/InsertCard); standalone facts use Append directly.
type EventLog interface {
	// Append persists standalone events, assigning each a monotonic ID and
	// preserving order.
	Append(ctx context.Context, evs ...*Event) error
	// List returns events matching q (card view, filters).
	List(ctx context.Context, q EventQuery) ([]Event, error)
	// Page is the cursor-paged catch-up feed.
	Page(ctx context.Context, q EventQuery) (*Page[Event], error)
	// Replay streams events with id > fromID in ascending order into fn,
	// stopping on the first error fn returns.
	Replay(ctx context.Context, fromID int64, fn func(*Event) error) error
}

// EventObserver is an in-process instrumentation hook, notified synchronously
// for every dispatched event. Observers must be fast and non-blocking; offload
// any I/O to a goroutine. A panicking observer is isolated (recovered) and does
// not affect the mutation or other observers.
type EventObserver func(e *Event)

// Emitter is the single emission seam. It owns the durable log, the live bus,
// the clock, and the observer chain. It is the only writer of Actor/At and the
// only publisher.
type Emitter struct {
	log EventLog
	bus Bus
	now func() time.Time

	mu        sync.RWMutex
	observers []EventObserver
}

func newEmitter(log EventLog, bus Bus, now func() time.Time) *Emitter {
	return &Emitter{log: log, bus: bus, now: now}
}

// Observe registers an instrumentation hook. Safe to call at any time.
func (e *Emitter) Observe(o EventObserver) {
	e.mu.Lock()
	e.observers = append(e.observers, o)
	e.mu.Unlock()
}

// Emit is the durable-fact path for standalone events (board / persisted
// condition events): stamp -> append to the log -> dispatch. Nothing is
// published if the append fails.
func (e *Emitter) Emit(ctx context.Context, evs ...*Event) error {
	e.stamp(ctx, evs)
	if err := e.log.Append(ctx, evs...); err != nil {
		return err
	}
	e.dispatchCommitted(evs)
	return nil
}

// Signal is the ephemeral path: stamp -> dispatch, with no persistence. Used for
// condition signals that are derived and not replayed. A dropped signal is, by
// definition, for nobody.
func (e *Emitter) Signal(ctx context.Context, evs ...*Event) {
	e.stamp(ctx, evs)
	e.dispatchCommitted(evs)
}

// stamp fills Actor (from ctx) and At (from the clock) on any event that has not
// already set them. Idempotent. One clock read per batch so co-emitted events
// share a timestamp.
func (e *Emitter) stamp(ctx context.Context, evs []*Event) {
	actor := ActorFromCtx(ctx)
	now := e.now()
	for _, ev := range evs {
		if ev == nil {
			continue
		}
		if ev.Actor == "" {
			ev.Actor = actor
		}
		if ev.At.IsZero() {
			ev.At = now
		}
	}
}

// dispatchCommitted publishes durable-committed events to the bus and notifies
// observers. Package-private on purpose: the only ways to reach it are Emit,
// Signal, or Service.commitCard — so "publish only after commit" is enforced by
// API shape, not caller discipline.
func (e *Emitter) dispatchCommitted(evs []*Event) {
	e.mu.RLock()
	obs := e.observers
	e.mu.RUnlock()
	for _, ev := range evs {
		if ev == nil {
			continue
		}
		e.bus.Publish(ev)
		for _, o := range obs {
			notifyObserver(o, ev)
		}
	}
}

// notifyObserver isolates a single observer call so a panic cannot escape into
// the request path or skip later observers.
func notifyObserver(o EventObserver, ev *Event) {
	defer func() { _ = recover() }()
	o(ev)
}

// --- Event constructors + payload contracts -------------------------------
//
// Build events only via these constructors. The Diff types are the wire
// contract for each event's payload; changing a field's name or meaning is a
// breaking change (add a new event version instead). Golden fixtures in
// events_test.go pin the JSON shape.

// CardEvent is the base constructor: a card-scoped event with a typed diff.
// Actor and At are left unset for the seam to stamp.
func CardEvent(cardID string, t EventType, diff any) *Event {
	return &Event{CardID: cardID, Type: t, Diff: diff}
}

// CardCreatedDiff is the payload of card_created.
type CardCreatedDiff struct {
	Card CardRef `json:"card"`
}

// CardRef is a minimal card reference embedded in some diffs.
type CardRef struct {
	ID     string `json:"id"`
	TypeID string `json:"type_id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

func CardCreated(c *Card) *Event {
	return CardEvent(c.ID, EventCardCreated, CardCreatedDiff{
		Card: CardRef{ID: c.ID, TypeID: c.TypeID, Title: c.Title, Status: c.Status},
	})
}

// FieldUpdatedDiff is the payload of field_updated (scalar fields, incl. title).
type FieldUpdatedDiff struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

func FieldChanged(cardID, field string, before, after any) *Event {
	return CardEvent(cardID, EventFieldUpdated, FieldUpdatedDiff{Field: field, Before: before, After: after})
}

// BeforeAfterDiff is the payload of status_changed and owner_changed.
type BeforeAfterDiff struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

func StatusChanged(cardID, before, after string) *Event {
	return CardEvent(cardID, EventStatusChanged, BeforeAfterDiff{Before: before, After: after})
}

func OwnerChanged(cardID, before, after string) *Event {
	return CardEvent(cardID, EventOwnerChanged, BeforeAfterDiff{Before: before, After: after})
}

// TagsChangedDiff is the payload of tags_changed.
type TagsChangedDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

func TagsChanged(cardID string, added, removed []string) *Event {
	return CardEvent(cardID, EventTagsChanged, TagsChangedDiff{Added: added, Removed: removed})
}

// ItemAppendedDiff / ItemUpdatedDiff / ItemRemovedDiff are repeating-field payloads.
type ItemAppendedDiff struct {
	Field   string `json:"field"`
	EntryID string `json:"entry_id"`
	Entry   any    `json:"entry"`
	Index   int    `json:"index"`
}

func ItemAppended(cardID, field, entryID string, entry any, index int) *Event {
	return CardEvent(cardID, EventItemAppended, ItemAppendedDiff{Field: field, EntryID: entryID, Entry: entry, Index: index})
}

type ItemUpdatedDiff struct {
	Field   string `json:"field"`
	EntryID string `json:"entry_id"`
	Before  any    `json:"before"`
	After   any    `json:"after"`
}

func ItemUpdated(cardID, field, entryID string, before, after any) *Event {
	return CardEvent(cardID, EventItemUpdated, ItemUpdatedDiff{Field: field, EntryID: entryID, Before: before, After: after})
}

type ItemRemovedDiff struct {
	Field   string `json:"field"`
	EntryID string `json:"entry_id"`
	Entry   any    `json:"entry"`
}

func ItemRemoved(cardID, field, entryID string, entry any) *Event {
	return CardEvent(cardID, EventItemRemoved, ItemRemovedDiff{Field: field, EntryID: entryID, Entry: entry})
}

// LinkAddedDiff is the payload of link_added (note always present).
type LinkAddedDiff struct {
	TypeID string `json:"type_id"`
	Target string `json:"target"`
	Note   string `json:"note"`
}

func LinkAdded(cardID, typeID, target, note string) *Event {
	return CardEvent(cardID, EventLinkAdded, LinkAddedDiff{TypeID: typeID, Target: target, Note: note})
}

// LinkRemovedDiff is the payload of link_removed.
type LinkRemovedDiff struct {
	TypeID string `json:"type_id"`
	Target string `json:"target"`
}

func LinkRemoved(cardID, typeID, target string) *Event {
	return CardEvent(cardID, EventLinkRemoved, LinkRemovedDiff{TypeID: typeID, Target: target})
}

// CommentAddedDiff / CommentEditedDiff are comment payloads.
type CommentAddedDiff struct {
	CommentID string `json:"comment_id"`
}

func CommentAdded(cardID, commentID string) *Event {
	return CardEvent(cardID, EventCommentAdded, CommentAddedDiff{CommentID: commentID})
}

type CommentEditedDiff struct {
	CommentID string `json:"comment_id"`
	Before    string `json:"before"`
	After     string `json:"after"`
}

func CommentEdited(cardID, commentID, before, after string) *Event {
	return CardEvent(cardID, EventCommentEdited, CommentEditedDiff{CommentID: commentID, Before: before, After: after})
}

// SchemaUpgradedDiff is the payload of schema_upgraded.
type SchemaUpgradedDiff struct {
	From            int            `json:"from"`
	To              int            `json:"to"`
	DefaultsApplied map[string]any `json:"defaults_applied"`
	FieldsDropped   []string       `json:"fields_dropped"`
}

func SchemaUpgraded(cardID string, from, to int, defaultsApplied map[string]any, fieldsDropped []string) *Event {
	return CardEvent(cardID, EventSchemaUpgraded, SchemaUpgradedDiff{From: from, To: to, DefaultsApplied: defaultsApplied, FieldsDropped: fieldsDropped})
}
