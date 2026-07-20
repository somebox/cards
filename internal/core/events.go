// Package core — events.go
//
// The event core: the one emission seam. Every event — mutation, board, or
// condition — is built with a constructor (never a raw literal) and flows
// through the Emitter, which stamps identity/time, persists (for durable
// facts), publishes to the Bus, and notifies observers, in that order.
//
// Design: docs/events/core.md. Invariants enforced here:
//   - persist before publish (dispatch is package-private; durable card
//     writes go through Service.commitCard);
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
	persist   map[EventType]bool // condition types promoted to the durable path (3b)
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
	e.dispatch(evs)
	return nil
}

// Signal is the ephemeral path: stamp -> dispatch, with no persistence. Used for
// condition signals that are derived and not replayed. A dropped signal is, by
// definition, for nobody.
func (e *Emitter) Signal(ctx context.Context, evs ...*Event) {
	e.stamp(ctx, evs)
	e.dispatch(evs)
}

// EmitPersistedFire records and publishes a fired persist:true condition with
// exactly-once semantics and no mark-then-append crash window. It stamps ev,
// then calls commit — which must atomically record the fire AND append ev in a
// single transaction (see Store.MarkConditionFiredAndAppend), returning whether
// this call was the first to record it. On a first fire ev is dispatched
// (post-commit publish, the same persist-before-publish order as Emit); a
// duplicate fire or a commit error publishes nothing. This keeps the Emitter the
// sole stamper/publisher while the store owns the atomic transaction — the
// scheduler (fireOne) never touches the bus or the clock-stamp directly.
func (e *Emitter) EmitPersistedFire(ctx context.Context, ev *Event, commit func(ev *Event) (first bool, err error)) (bool, error) {
	e.stamp(ctx, []*Event{ev})
	first, err := commit(ev)
	if err != nil {
		return false, err
	}
	if first {
		e.dispatch([]*Event{ev})
	}
	return first, nil
}

// PersistConditions promotes the named condition event types to the durable
// fact path: Condition then routes those types through Emit (append + dispatch)
// instead of Signal (dispatch only). Everything else stays ephemeral. Configure
// once at startup, before emission. (3b — persist:true escalation.)
func (e *Emitter) PersistConditions(types ...EventType) {
	e.mu.Lock()
	if e.persist == nil {
		e.persist = map[EventType]bool{}
	}
	for _, t := range types {
		e.persist[t] = true
	}
	e.mu.Unlock()
}

// shouldPersist reports whether a condition type has been escalated to durable.
func (e *Emitter) shouldPersist(t EventType) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.persist[t]
}

// IsPersisted reports whether t is escalated to the durable path via
// PersistConditions. Used by the seam 3d scheduler: a persist:true temporal
// type is treated as a permanent consumer (armed regardless of live
// subscribers), since its whole purpose is a durable record that outlives
// any particular SSE/hook connection.
func (e *Emitter) IsPersisted(t EventType) bool { return e.shouldPersist(t) }

// Condition is the single emission seam for condition events. Each event routes
// by policy: types escalated via PersistConditions go through Emit (durable
// fact, replayable from the feed); the rest go through Signal (ephemeral). Bus
// and observer delivery are identical either way — persistence is the only
// difference, so consumers cannot tell an escalated event from a signalled one
// on the live path. Returns the durable append error, if any (the signalled
// portion is always best-effort). (3b)
func (e *Emitter) Condition(ctx context.Context, evs ...*Event) error {
	var durable, ephemeral []*Event
	for _, ev := range evs {
		if ev == nil {
			continue
		}
		if e.shouldPersist(ev.Type) {
			durable = append(durable, ev)
		} else {
			ephemeral = append(ephemeral, ev)
		}
	}
	if len(ephemeral) > 0 {
		e.Signal(ctx, ephemeral...)
	}
	if len(durable) > 0 {
		return e.Emit(ctx, durable...)
	}
	return nil
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

// dispatch publishes events to the bus and notifies observers. It serves both
// the durable path (Emit, after a successful append) and the ephemeral path
// (Signal) — the name deliberately does not say "committed" since not every
// caller commits. Package-private on purpose: the only ways to reach it are
// Emit, Signal, or Service.commitCard — so "publish only after any required
// persistence" is enforced by API shape, not caller discipline.
func (e *Emitter) dispatch(evs []*Event) {
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
	return &Event{CardID: cardID, Version: 1, Type: t, Diff: diff}
}

// BoardEvent is the base constructor for a board-scoped fact: no card_id, a
// board_id, scope=board. Emit it via Emitter.Emit (durable) — board conditions
// (Step 3) build on this path. Actor/At are left for the seam to stamp. (2b)
func BoardEvent(boardID string, t EventType, diff any) *Event {
	return &Event{BoardID: boardID, Scope: "board", Version: 1, Type: t, Diff: diff}
}

// WIPDiff is the payload of wip_exceeded / wip_cleared (a board column crossing
// its WIP limit). These are ephemeral signals — emit via Emitter.Signal. (3a)
type WIPDiff struct {
	Column string `json:"column"`
	Count  int    `json:"count"`
	Limit  int    `json:"limit"`
}

func WIPExceeded(boardID, column string, count, limit int) *Event {
	return BoardEvent(boardID, EventWIPExceeded, WIPDiff{Column: column, Count: count, Limit: limit})
}

func WIPCleared(boardID, column string, count, limit int) *Event {
	return BoardEvent(boardID, EventWIPCleared, WIPDiff{Column: column, Count: count, Limit: limit})
}

// LaneDiff is the payload of lane_drained / lane_refilled (a monitored column
// crossing to/from zero matching cards). Ephemeral signals — emit via
// Emitter.Signal (or Condition, which may escalate per persist_conditions). (3c)
type LaneDiff struct {
	Column string `json:"column"`
	Count  int    `json:"count"`
}

func LaneDrained(boardID, column string, count int) *Event {
	return BoardEvent(boardID, EventLaneDrained, LaneDiff{Column: column, Count: count})
}

func LaneRefilled(boardID, column string, count int) *Event {
	return BoardEvent(boardID, EventLaneRefilled, LaneDiff{Column: column, Count: count})
}

// BlockedDiff is the payload of card_blocked / card_unblocked: the cards
// currently holding a blocked-by/depends-on link to this card (empty on
// card_unblocked). Card-scoped, ephemeral by default (escalatable via
// persist_conditions). (3c)
type BlockedDiff struct {
	Blockers []string `json:"blockers"`
}

func CardBlocked(cardID string, blockers []string) *Event {
	return CardEvent(cardID, EventCardBlocked, BlockedDiff{Blockers: blockers})
}

func CardUnblocked(cardID string) *Event {
	return CardEvent(cardID, EventCardUnblocked, BlockedDiff{Blockers: []string{}})
}

// TransitionRejectedDiff is the payload of transition_rejected: an
// EnforceTransitions board refused a status move. Opt-in via
// board.monitors.emit_rejections. Card-scoped, ephemeral by default.
type TransitionRejectedDiff struct {
	From    string `json:"from"`
	To      string `json:"to"`
	BoardID string `json:"board_id"`
}

func TransitionRejected(cardID, from, to, boardID string) *Event {
	return CardEvent(cardID, EventTransitionRejected, TransitionRejectedDiff{From: from, To: to, BoardID: boardID})
}

// StatusTimeoutDiff is the payload of status_timeout: a card has sat in
// Status since Since longer than Max (board.monitors.max_time_in_status).
// Card-scoped, ephemeral by default. (3e)
type StatusTimeoutDiff struct {
	Status string    `json:"status"`
	Since  time.Time `json:"since"`
	Max    string    `json:"max"`
}

func StatusTimeout(cardID, status string, since time.Time, max string) *Event {
	return CardEvent(cardID, EventStatusTimeout, StatusTimeoutDiff{Status: status, Since: since, Max: max})
}

// CardIdleDiff is the payload of card_idle: no mutation event on the card
// since Since, past Threshold (board.monitors.idle_after). Card-scoped,
// ephemeral by default. (3e)
type CardIdleDiff struct {
	Since     time.Time `json:"since"`
	Threshold string    `json:"threshold"`
}

func CardIdle(cardID string, since time.Time, threshold string) *Event {
	return CardEvent(cardID, EventCardIdle, CardIdleDiff{Since: since, Threshold: threshold})
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

// CardDeletedDiff is the payload of card_deleted — a tombstone recording what
// was removed so the append-only log stays self-describing after the live row
// is gone.
type CardDeletedDiff struct {
	Card CardRef `json:"card"`
}

func CardDeleted(c *Card) *Event {
	return CardEvent(c.ID, EventCardDeleted, CardDeletedDiff{
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

// ArtifactAddedDiff is the payload of artifact_added: bytes were stored for an
// artifact field. It mirrors the field metadata a card holds ({uri, mime, size,
// sha256}) plus the field id, so a durable consumer can react to an upload
// without re-reading the card.
type ArtifactAddedDiff struct {
	Field  string `json:"field"`
	URI    string `json:"uri"`
	MIME   string `json:"mime,omitempty"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

func ArtifactAdded(cardID, field, uri, mime string, size int64, sha256 string) *Event {
	return CardEvent(cardID, EventArtifactAdded, ArtifactAddedDiff{Field: field, URI: uri, MIME: mime, Size: size, SHA256: sha256})
}
