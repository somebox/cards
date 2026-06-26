// Package core — service.go
//
// The Service layer owns schema lookup, validation, transition evaluation,
// optimistic concurrency, idempotency (HTTP-layer), and event writing. All
// transports call into this package. See docs/ARCHITECTURE.md (Core Service
// Boundary) and docs/SPEC.md (§10, §11).
package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Service is the transport-independent core. Construct with NewService.
type Service struct {
	ws     *Workspace
	types  map[string]*CardType
	boards map[string]*Board
	store  Store
	bus    *Bus
	now    func() time.Time
}

// NewService binds loaded config + a Store implementation.
func NewService(ws *Workspace, types map[string]*CardType, boards map[string]*Board, st Store) *Service {
	return &Service{ws: ws, types: types, boards: boards, store: st, bus: NewBus(), now: func() time.Time {
		return time.Now().UTC()
	}}
}

// Bus returns the in-process event bus (for SSE/hooks subscribers).
func (s *Service) Bus() *Bus { return s.bus }

// publish fans out committed events on the bus. Called after every successful
// store mutation.
func (s *Service) publish(evs []*Event) {
	for _, e := range evs {
		if e == nil {
			continue
		}
		s.bus.Publish(e)
	}
}

// publishOne is a convenience for single-event commits.
func (s *Service) publishOne(e *Event) { s.publish([]*Event{e}) }

// Workspace returns the introspection snapshot (GET /v1/workspace).
func (s *Service) Workspace(ctx context.Context) (*WorkspaceSnapshot, error) {
	users, _ := s.store.ListUsers(ctx)
	if len(users) > 0 {
		s.ws.Users = users
	}
	curVersions := map[string]int{}
	for id, ct := range s.types {
		curVersions[id] = ct.SchemaVersion
	}
	return &WorkspaceSnapshot{
		Workspace:       s.ws,
		CardTypes:       s.types,
		Boards:          s.boards,
		CurrentVersions: curVersions,
	}, nil
}

// ResolveActor resolves an actor from header/env/default. SPEC §12.
func (s *Service) ResolveActor(header, envUser string) (string, *Error) {
	if header != "" {
		return header, nil
	}
	if envUser != "" {
		return envUser, nil
	}
	if s.ws.Settings.DefaultUser != "" {
		return s.ws.Settings.DefaultUser, nil
	}
	return "", ActorRequired()
}

// ListCards filters/paginates cards. SPEC §9. The service compiles the
// jq-like Filter DSL into a SQL fragment the store appends.
func (s *Service) ListCards(ctx context.Context, q CardQuery) (*Page[Card], error) {
	if q.BoardID != "" {
		b, ok := s.boards[q.BoardID]
		if !ok {
			return nil, NotFound("board " + q.BoardID)
		}
		s.applyBoardScope(&q, b)
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	if err := s.compileFilterInto(&q); err != nil {
		return nil, err
	}
	// Validate cursor before hitting the store — a bad cursor should be a
	// 400, not a silent fallthrough to the first page.
	if q.Cursor != "" {
		if _, _, err := DecodeCursor(q.Cursor); err != nil {
			return nil, NewValidationError("cursor", "invalid cursor: "+err.Error())
		}
	}
	return s.store.ListCards(ctx, q)
}

// applyBoardScope folds a board's type/column scope into the query without
// clobbering an explicit status/type_id the caller set.
func (s *Service) applyBoardScope(q *CardQuery, b *Board) {
	if len(q.TypeIDIn) == 0 && q.TypeID == "" && len(b.CardTypeIDs) > 0 {
		q.TypeIDIn = b.CardTypeIDs
	}
	// Board columns are an implicit status scope only when no status filter
	// is present at all.
	if q.Status == "" && len(q.StatusIn) == 0 && len(b.Columns) > 0 {
		q.StatusIn = b.Columns
	}
}

// GetCard returns a single card by id, with links + comments loaded.
func (s *Service) GetCard(ctx context.Context, id string) (*Card, error) {
	c, err := s.store.GetCard(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, NotFound("card " + id)
		}
		return nil, Internal("failed to get card: " + err.Error())
	}
	return c, nil
}

// getCard is the internal helper used by all mutating methods. It maps
// store errors correctly: ErrNotFound→404, other→500 (previously all store
// errors were masked as 404, hiding real failures).
func (s *Service) getCard(ctx context.Context, id string) (*Card, error) {
	c, err := s.store.GetCard(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, NotFound("card " + id)
		}
		return nil, Internal("failed to get card: " + err.Error())
	}
	return c, nil
}

// CreateCard validates and inserts a new card. SPEC §11.
func (s *Service) CreateCard(ctx context.Context, req CreateCardRequest) (*Card, error) {
	ct, ok := s.types[req.TypeID]
	if !ok {
		return nil, NotFound("card_type " + req.TypeID)
	}
	actor := req.Actor
	if actor == "" {
		return nil, ActorRequired()
	}
	// Schema version pin.
	if req.SchemaVersion != 0 && req.SchemaVersion != ct.SchemaVersion {
		return nil, newSchemaVersionMismatch(ct.SchemaVersion)
	}
	// Resolve default status.
	status := req.Status
	if status == "" {
		status = defaultStatus(ct, s.ws)
	}
	if status == "" {
		return nil, NewValidationError("status", "no status given and no allowed_columns/default to derive one")
	}
	if err := s.checkColumn(status, ct, nil); err != nil {
		return nil, err
	}
	// Validate fields (deep, incl. card_link target existence).
	fields, err := s.validateFields(ctx, ct, req.Fields, true)
	if err != nil {
		return nil, err
	}
	if err := s.validateTags(req.Tags); err != nil {
		return nil, err
	}
	if c := strings.TrimSpace(req.Title); c == "" {
		return nil, NewValidationError("title", "title is required")
	}

	now := s.now()
	c := &Card{
		ID:            newCardID(),
		WorkspaceID:   s.ws.ID,
		TypeID:        req.TypeID,
		SchemaVersion: ct.SchemaVersion,
		Title:         strings.TrimSpace(req.Title),
		Status:        status,
		Fields:        fields,
		Tags:          req.Tags,
		Version:       1,
		CreatedAt:     now,
		UpdatedAt:     now,
		CreatedBy:     actor,
	}
	if req.DryRun {
		return c, nil
	}
	ev := &Event{
		CardID: c.ID, Type: EventCardCreated, Actor: actor, At: now,
		Diff: map[string]any{"card": map[string]any{"id": c.ID, "type_id": c.TypeID, "title": c.Title, "status": c.Status}},
	}
	if err := s.store.InsertCard(ctx, c, ev); err != nil {
		return nil, err
	}
	s.publishOne(ev)
	return c, nil
}

// PatchCard applies a partial update with optimistic concurrency. SPEC §11.
func (s *Service) PatchCard(ctx context.Context, id string, req PatchCardRequest) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Version != current.Version {
		return nil, VersionConflict(current)
	}
	ct, ok := s.types[current.TypeID]
	if !ok {
		return nil, NotFound("card_type " + current.TypeID)
	}
	actor := req.Actor
	if actor == "" {
		return nil, ActorRequired()
	}

	var events []*Event
	now := s.now()
	next := *current // shallow copy

	// title
	if req.Title != nil {
		newTitle := strings.TrimSpace(*req.Title)
		if newTitle == "" {
			return nil, NewValidationError("title", "title cannot be empty")
		}
		if newTitle != current.Title {
			events = append(events, &Event{CardID: id, Type: EventFieldUpdated, Actor: actor, At: now,
				Diff: map[string]any{"field": "title", "before": current.Title, "after": newTitle}})
			next.Title = newTitle
		}
	}

	// status
	if req.Status != nil && *req.Status != current.Status {
		newStatus := *req.Status
		if err := s.checkColumn(newStatus, ct, nil); err != nil {
			return nil, err
		}
		if b := s.boardForCard(current); b != nil && b.Settings.EnforceTransitions && !req.Force {
			allowed, ok := b.Transitions[current.Status]
			if ok && !contains(allowed, newStatus) {
				return nil, newTransitionIllegal(current.Status, allowed)
			}
		}
		events = append(events, &Event{CardID: id, Type: EventStatusChanged, Actor: actor, At: now,
			Diff: map[string]any{"before": current.Status, "after": newStatus}})
		next.Status = newStatus
	}

	// owner
	if req.Owner != nil {
		newOwner := strings.TrimSpace(*req.Owner)
		if newOwner != "" {
			if err := s.checkUserExists(ctx, newOwner); err != nil {
				return nil, err
			}
		}
		if newOwner != current.Owner {
			events = append(events, &Event{CardID: id, Type: EventOwnerChanged, Actor: actor, At: now,
				Diff: map[string]any{"before": strOrEmpty(current.Owner), "after": newOwner}})
			next.Owner = newOwner
		}
	}

	// tags
	if req.Tags != nil {
		if err := s.validateTags(*req.Tags); err != nil {
			return nil, err
		}
		added, removed := diffTags(current.Tags, *req.Tags)
		if len(added) > 0 || len(removed) > 0 {
			events = append(events, &Event{CardID: id, Type: EventTagsChanged, Actor: actor, At: now,
				Diff: map[string]any{"added": added, "removed": removed}})
			next.Tags = *req.Tags
		}
	}

	// fields (scalar only; repeating uses AppendEntry/UpdateEntry/RemoveEntry)
	if len(req.Fields) > 0 {
		base, _ := current.Fields.(map[string]any)
		if base == nil {
			base = map[string]any{}
		}
		merged := map[string]any{}
		for k, v := range base {
			merged[k] = v
		}
		changed := []map[string]any{}
		for k, v := range req.Fields {
			before := base[k]
			if err := s.validateOneField(ctx, ct, k, v); err != nil {
				return nil, err
			}
			merged[k] = v
			changed = append(changed, map[string]any{"field": k, "before": before, "after": v})
		}
		if _, err := s.validateFields(ctx, ct, merged, false); err != nil {
			return nil, err
		}
		for _, cf := range changed {
			events = append(events, &Event{CardID: id, Type: EventFieldUpdated, Actor: actor, At: now, Diff: cf})
		}
		next.Fields = merged
	}

	if req.DryRun {
		next.Version = current.Version + 1
		next.UpdatedAt = now
		return &next, nil
	}

	if len(events) == 0 {
		return current, nil
	}

	next.Version = current.Version + 1
	next.UpdatedAt = now
	if err := s.store.UpdateCard(ctx, &next, events); err != nil {
		return nil, err
	}
	s.publish(events)
	return &next, nil
}

// AppendEntry appends to a repeating field; returns the updated card. SPEC §11.
// Each entry gets a stable server-generated entry_id; entries are addressed
// by that id thereafter. SPEC §6 D6.
func (s *Service) AppendEntry(ctx context.Context, id, field string, entry map[string]any, version int) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	if version != current.Version {
		return nil, VersionConflict(current)
	}
	ct := s.types[current.TypeID]
	fd, err := s.findField(ct, field)
	if err != nil {
		return nil, err
	}
	if fd.Type != FieldRepeating {
		return nil, NewValidationError(field, fmt.Sprintf("field %q is not a repeating field", field))
	}
	actor := ctxActor(ctx)
	if entry == nil {
		entry = map[string]any{}
	}
	if err := validateEntry(fd, entry); err != nil {
		return nil, err
	}
	// Inject entry_id into the stored entry object.
	stored := map[string]any{}
	for k, v := range entry {
		stored[k] = v
	}
	stored["entry_id"] = newEntryID()

	arr := appendEntry(current.Fields, field, stored)
	next := *current
	next.Fields = setField(current.Fields, field, arr)
	next.Version = current.Version + 1
	next.UpdatedAt = s.now()
	ev := &Event{CardID: id, Type: EventItemAppended, Actor: actor, At: next.UpdatedAt,
		Diff: map[string]any{"field": field, "entry_id": stored["entry_id"], "entry": entry, "index": len(arr) - 1}}
	if err := s.store.UpdateCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
	s.publishOne(ev)
	return &next, nil
}

// UpdateEntry replaces an entry's data (keeping its entry_id). SPEC §11.
func (s *Service) UpdateEntry(ctx context.Context, id, field, entryID string, entry map[string]any, version int) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	if version != current.Version {
		return nil, VersionConflict(current)
	}
	ct := s.types[current.TypeID]
	fd, err := s.findField(ct, field)
	if err != nil {
		return nil, err
	}
	if fd.Type != FieldRepeating {
		return nil, NewValidationError(field, "not a repeating field")
	}
	if err := validateEntry(fd, entry); err != nil {
		return nil, err
	}
	arr, _ := current.Fields.(map[string]any)[field].([]any)
	before, idx, found := findEntry(arr, entryID)
	if !found {
		return nil, NotFound("entry " + entryID)
	}
	newEntry := map[string]any{}
	for k, v := range entry {
		newEntry[k] = v
	}
	newEntry["entry_id"] = entryID
	arr[idx] = newEntry
	next := *current
	next.Fields = setField(current.Fields, field, arr)
	next.Version = current.Version + 1
	next.UpdatedAt = s.now()
	ev := &Event{CardID: id, Type: EventItemUpdated, Actor: ctxActor(ctx), At: next.UpdatedAt,
		Diff: map[string]any{"field": field, "entry_id": entryID, "before": before, "after": newEntry}}
	if err := s.store.UpdateCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
	s.publishOne(ev)
	return &next, nil
}

// RemoveEntry deletes an entry by entry_id. SPEC §11. The version must be
// supplied (the HTTP handler enforces this via ?version=N) for lost-update
// protection; a mismatch yields version_conflict.
func (s *Service) RemoveEntry(ctx context.Context, id, field, entryID string, version int) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	if version == 0 {
		return nil, NewValidationError("version", "version is required for entry deletion")
	}
	if version != current.Version {
		return nil, VersionConflict(current)
	}
	arr, _ := getMapField(current.Fields, field).([]any)
	entry, idx, found := findEntry(arr, entryID)
	if !found {
		return nil, NotFound("entry " + entryID)
	}
	arr = append(arr[:idx], arr[idx+1:]...)
	next := *current
	next.Fields = setField(current.Fields, field, arr)
	next.Version = current.Version + 1
	next.UpdatedAt = s.now()
	ev := &Event{CardID: id, Type: EventItemRemoved, Actor: ctxActor(ctx), At: next.UpdatedAt,
		Diff: map[string]any{"field": field, "entry_id": entryID, "entry": entry}}
	if err := s.store.UpdateCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
	s.publishOne(ev)
	return &next, nil
}

// AddLink validates and adds a link; returns the updated card. SPEC §11/§4.
// Links are append-only graph state; the request does not carry a version
// (the store CAS-guards against lost updates), but the card version bumps.
func (s *Service) AddLink(ctx context.Context, id string, in LinkInput) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	actor := in.Actor
	if actor == "" {
		actor = ctxActor(ctx)
	}
	if actor == "" {
		return nil, ActorRequired()
	}
	lt := s.lookupLinkType(in.TypeID)
	if lt == nil {
		return nil, newUnknownEnum("type_id", in.TypeID, linkTypeIDs(s.ws))
	}
	// Target must exist.
	target, err := s.getCard(ctx, in.Target)
	if err != nil {
		if ce := AsError(err); ce != nil && ce.Code == "not_found" {
			return nil, newTargetCardMissing(in.Target, lt.TargetTypes...)
		}
		return nil, err
	}
	// source/target type constraints.
	if err := s.checkLinkTypeConstraints(lt, current.TypeID, target.TypeID); err != nil {
		return nil, err
	}
	// No duplicate.
	for _, l := range current.Links {
		if l.TypeID == in.TypeID && l.Target == in.Target {
			return current, nil // idempotent: already linked
		}
	}
	l := Link{TypeID: in.TypeID, Target: in.Target, Note: in.Note, CreatedBy: actor, CreatedAt: s.now()}
	next := *current
	next.Version = current.Version + 1
	next.UpdatedAt = l.CreatedAt
	next.Links = append(append([]Link{}, current.Links...), l)
	ev := &Event{CardID: id, Type: EventLinkAdded, Actor: actor, At: l.CreatedAt,
		Diff: map[string]any{"type_id": in.TypeID, "target": in.Target, "note": in.Note}}
	if err := s.store.UpdateCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
	s.publishOne(ev)
	// Persist the link row for graph queries.
	_ = s.store.InsertLink(ctx, id, l)
	return &next, nil
}

// RemoveLink deletes a link by (type_id, target). SPEC §11.
func (s *Service) RemoveLink(ctx context.Context, id, typeID, target string) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	actor := ctxActor(ctx)
	found := false
	links := []Link{}
	for _, l := range current.Links {
		if l.TypeID == typeID && l.Target == target {
			found = true
			continue
		}
		links = append(links, l)
	}
	if !found {
		return nil, NotFound("link " + typeID + "/" + target)
	}
	next := *current
	next.Version = current.Version + 1
	next.UpdatedAt = s.now()
	next.Links = links
	ev := &Event{CardID: id, Type: EventLinkRemoved, Actor: actor, At: next.UpdatedAt,
		Diff: map[string]any{"type_id": typeID, "target": target}}
	if err := s.store.UpdateCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
	s.publishOne(ev)
	if _, err := s.store.DeleteLink(ctx, id, typeID, target); err != nil {
		// non-fatal: the card row is already updated; graph table may be stale.
		_ = err
	}
	return &next, nil
}

// AddComment adds a comment; returns the updated card. SPEC §11.
func (s *Service) AddComment(ctx context.Context, id string, body string) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	actor := ctxActor(ctx)
	if actor == "" {
		return nil, ActorRequired()
	}
	if strings.TrimSpace(body) == "" {
		return nil, NewValidationError("body", "comment body is required")
	}
	c := Comment{ID: newCommentID(), Author: actor, Body: body, CreatedAt: s.now()}
	next := *current
	next.Version = current.Version + 1
	next.UpdatedAt = c.CreatedAt
	next.Comments = append(append([]Comment{}, current.Comments...), c)
	ev := &Event{CardID: id, Type: EventCommentAdded, Actor: actor, At: c.CreatedAt,
		Diff: map[string]any{"comment_id": c.ID}}
	if err := s.store.UpdateCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
	s.publishOne(ev)
	_ = s.store.InsertComment(ctx, id, c)
	return &next, nil
}

// EditComment updates a comment's body. SPEC §11.
func (s *Service) EditComment(ctx context.Context, id, commentID, body string) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	actor := ctxActor(ctx)
	if strings.TrimSpace(body) == "" {
		return nil, NewValidationError("body", "comment body is required")
	}
	var before string
	found := false
	comments := []Comment{}
	for _, c := range current.Comments {
		if c.ID == commentID {
			before = c.Body
			c.Body = body
			c.EditedAt = s.now()
			found = true
		}
		comments = append(comments, c)
	}
	if !found {
		return nil, NotFound("comment " + commentID)
	}
	next := *current
	next.Version = current.Version + 1
	next.UpdatedAt = s.now()
	next.Comments = comments
	ev := &Event{CardID: id, Type: EventCommentEdited, Actor: actor, At: next.UpdatedAt,
		Diff: map[string]any{"comment_id": commentID, "before": before, "after": body}}
	if err := s.store.UpdateCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
	s.publishOne(ev)
	_ = s.store.UpdateComment(ctx, id, commentID, body, next.UpdatedAt)
	return &next, nil
}

// Claim atomically sets owner (+optional status) via compare-and-set. SPEC §11.
// Returns version_conflict (409) if the card is already owned by another actor.
func (s *Service) Claim(ctx context.Context, id string, req ClaimRequest) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Version != current.Version {
		return nil, VersionConflict(current)
	}
	actor := req.Actor
	if actor == "" {
		actor = ctxActor(ctx)
	}
	if actor == "" {
		return nil, ActorRequired()
	}
	if current.Owner != "" && current.Owner != actor {
		return nil, VersionConflict(current) // owned by another
	}
	patch := PatchCardRequest{Version: req.Version, Owner: &actor, Actor: actor}
	if req.Status != "" {
		st := req.Status
		patch.Status = &st
	}
	return s.PatchCard(ctx, id, patch)
}

// Release clears the card's owner (the inverse of claim). SPEC §11.
// If req.Status is set, the card is also moved to that status; combined with
// req.Force this is the recovery path for mis-claimed or mis-triaged cards —
// e.g. moving a deferred card from todo to backlog when the enforced
// transition graph has no todo→backlog edge.
func (s *Service) Release(ctx context.Context, id string, req ReleaseRequest) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Version != current.Version {
		return nil, VersionConflict(current)
	}
	actor := req.Actor
	if actor == "" {
		actor = ctxActor(ctx)
	}
	if actor == "" {
		return nil, ActorRequired()
	}
	empty := ""
	patch := PatchCardRequest{Version: req.Version, Owner: &empty, Actor: actor, Force: req.Force}
	if req.Status != "" {
		st := req.Status
		patch.Status = &st
	}
	return s.PatchCard(ctx, id, patch)
}

// TakeNext picks the oldest unowned matching card and atomically claims it.
// SPEC §11/§9 (D7). Returns (nil, nil) when nothing matches.
func (s *Service) TakeNext(ctx context.Context, req TakeNextRequest) (*Card, error) {
	actor := req.Actor
	if actor == "" {
		actor = ctxActor(ctx)
	}
	if actor == "" {
		return nil, ActorRequired()
	}
	assignTo := req.AssignTo
	if assignTo == "" {
		assignTo = actor
	}
	q := CardQuery{
		TypeID:  req.TypeID,
		BoardID: req.BoardID,
		Filter:  req.Filter,
		Unowned: true,
		Limit:   1,
	}
	if q.BoardID != "" {
		b, ok := s.boards[q.BoardID]
		if !ok {
			return nil, NotFound("board " + q.BoardID)
		}
		s.applyBoardScope(&q, b)
	}
	// If a status move is requested under an enforced board, restrict
	// candidates to statuses that may legally transition to req.Status.
	if req.Status != "" {
		if b := s.boardForTypeID(req.TypeID, req.BoardID); b != nil && b.Settings.EnforceTransitions {
			q.StatusIn = allowedFromStatuses(b, req.Status)
		}
	}
	if err := s.compileFilterInto(&q); err != nil {
		return nil, err
	}
	c, evs, err := s.store.ClaimAtomic(ctx, q, assignTo, req.Status, actor, s.now())
	if err != nil {
		return nil, err
	}
	s.publish(evs)
	return c, nil // nil card → caller renders {card:null}
}

// ListEvents returns recent events for a card. SPEC §11.
func (s *Service) ListEvents(ctx context.Context, q EventQuery) ([]Event, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 50
	}
	return s.store.ListEvents(ctx, q)
}

// History renders a resumption-ready timeline for a card. SPEC §8.
func (s *Service) History(ctx context.Context, id string) ([]HistoryEntry, error) {
	if _, err := s.getCard(ctx, id); err != nil {
		return nil, err
	}
	evs, err := s.store.ListEvents(ctx, EventQuery{CardID: id, Limit: 500})
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(evs))
	for _, e := range evs {
		out = append(out, HistoryEntry{At: e.At, Actor: e.Actor, Type: e.Type, Summary: summarizeEvent(e)})
	}
	return out, nil
}

// --- validation helpers ---

func (s *Service) findField(ct *CardType, id string) (*FieldDef, error) {
	for i := range ct.Fields {
		if ct.Fields[i].ID == id {
			return &ct.Fields[i], nil
		}
	}
	return nil, newUnknownField(id, fieldIDs(ct))
}

// validateFields validates a full fields map against the type's schema.
func (s *Service) validateFields(ctx context.Context, ct *CardType, in map[string]any, checkRequired bool) (map[string]any, error) {
	strict := s.ws.Settings.StrictFields
	defs := map[string]FieldDef{}
	for _, f := range ct.Fields {
		defs[f.ID] = f
	}
	if strict {
		for k := range in {
			if _, ok := defs[k]; !ok {
				return nil, newUnknownField(k, fieldIDs(ct))
			}
		}
	}
	out := map[string]any{}
	for _, f := range ct.Fields {
		v, present := in[f.ID]
		if !present || v == nil {
			if f.Default != nil {
				out[f.ID] = f.Default
				continue
			}
			if checkRequired && f.Required {
				return nil, NewValidationError(f.ID, fmt.Sprintf("field %q is required", f.ID))
			}
			continue
		}
		if err := s.validateFieldValue(ctx, &f, v); err != nil {
			return nil, err
		}
		out[f.ID] = v
	}
	if !strict {
		for k, v := range in {
			if _, ok := defs[k]; !ok {
				out[k] = v
			}
		}
	}
	return out, nil
}

func (s *Service) validateOneField(ctx context.Context, ct *CardType, id string, v any) error {
	for i := range ct.Fields {
		f := ct.Fields[i]
		if f.ID == id {
			if f.Type == FieldRepeating {
				return NewValidationError(id, "repeating fields use the append/update/remove API")
			}
			return s.validateFieldValue(ctx, &f, v)
		}
	}
	if s.ws.Settings.StrictFields {
		return newUnknownField(id, fieldIDs(ct))
	}
	return nil
}

// validateFieldValue checks one value; card_link targets are resolved against
// the store (existence + optional target_type).
func (s *Service) validateFieldValue(ctx context.Context, f *FieldDef, v any) error {
	switch f.Type {
	case FieldCardLink:
		id, ok := v.(string)
		if !ok {
			return NewValidationError(f.ID, "card_link expects a card id string")
		}
		if id == "" {
			return nil
		}
		target, err := s.getCard(ctx, id)
		if err != nil {
			if ce := AsError(err); ce != nil && ce.Code == "not_found" {
				return newTargetCardMissing(id, f.TargetType)
			}
			return err
		}
		if f.TargetType != "" && target.TypeID != f.TargetType {
			return newTargetCardTypeMismatch(id, []string{f.TargetType})
		}
		return nil
	case FieldRepeating:
		arr, ok := v.([]any)
		if !ok {
			return NewValidationError(f.ID, "repeating field expects an array")
		}
		for i, e := range arr {
			em, ok := e.(map[string]any)
			if !ok {
				return NewValidationError(f.ID, fmt.Sprintf("repeating entry %d is not an object", i))
			}
			if err := validateEntry(f, em); err != nil {
				return err
			}
		}
		return nil
	default:
		return validateValue(f, v)
	}
}

// validateValue checks a scalar value (no store needed). See SPEC §6.
func validateValue(f *FieldDef, v any) error {
	switch f.Type {
	case FieldString, FieldText:
		if _, ok := v.(string); !ok {
			return NewValidationError(f.ID, fmt.Sprintf("field %q expects a string", f.ID))
		}
	case FieldNumber:
		n, ok := toFloat(v)
		if !ok {
			return NewValidationError(f.ID, fmt.Sprintf("field %q expects a number", f.ID))
		}
		if f.Min != nil && n < *f.Min {
			return NewValidationError(f.ID, fmt.Sprintf("field %q below min %v", f.ID, *f.Min))
		}
		if f.Max != nil && n > *f.Max {
			return NewValidationError(f.ID, fmt.Sprintf("field %q above max %v", f.ID, *f.Max))
		}
	case FieldDate:
		str, ok := v.(string)
		if !ok {
			return NewValidationError(f.ID, fmt.Sprintf("field %q expects an ISO date string", f.ID))
		}
		if _, err := time.Parse(time.RFC3339, str); err != nil {
			if _, err := time.Parse("2006-01-02", str); err != nil {
				return NewValidationError(f.ID, fmt.Sprintf("field %q is not a parseable date", f.ID))
			}
		}
	case FieldEnum:
		str, ok := v.(string)
		if !ok {
			return NewValidationError(f.ID, fmt.Sprintf("field %q expects a string", f.ID))
		}
		if !contains(f.Options, str) {
			return newUnknownEnum(f.ID, str, f.Options)
		}
	case FieldUser:
		str, ok := v.(string)
		if !ok {
			return NewValidationError(f.ID, fmt.Sprintf("field %q expects a user id string", f.ID))
		}
		_ = str // existence checked at card level
	case FieldTags, FieldArtifact:
		// tags validated at card level against tag_set; artifact stored as-is.
	}
	return nil
}

// validateEntry validates a repeating entry against item_fields. The reserved
// entry_id key is ignored. SPEC §6 (no nested repeating).
func validateEntry(f *FieldDef, entry map[string]any) error {
	defs := map[string]FieldDef{}
	for _, sf := range f.ItemFields {
		defs[sf.ID] = sf
	}
	// Unknown sub-fields rejected.
	for k := range entry {
		if k == "entry_id" {
			continue
		}
		if _, ok := defs[k]; !ok {
			return NewValidationError(f.ID+"."+k, "unknown item_field")
		}
	}
	for _, sf := range f.ItemFields {
		v, present := entry[sf.ID]
		if !present || v == nil {
			if sf.Required {
				return NewValidationError(f.ID+"."+sf.ID, fmt.Sprintf("item field %q is required", sf.ID))
			}
			continue
		}
		// Reuse the scalar validator; sub-fields can't be repeating in v1.
		if err := validateValue(&sf, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateTags(tags []string) error {
	set := map[string]bool{}
	for _, t := range s.ws.TagSet {
		set[t] = true
	}
	for _, t := range tags {
		if !set[t] {
			return newUnknownTag(t, s.ws.TagSet)
		}
	}
	return nil
}

func (s *Service) checkColumn(status string, ct *CardType, _ *Board) *Error {
	colSet := map[string]bool{}
	for _, c := range s.ws.Columns {
		colSet[c.ID] = true
	}
	if !colSet[status] {
		return newUnknownEnum("status", status, columnIDs(s.ws))
	}
	if len(ct.AllowedColumns) > 0 && !contains(ct.AllowedColumns, status) {
		return newUnknownEnum("status", status, ct.AllowedColumns)
	}
	return nil
}

func (s *Service) checkUserExists(ctx context.Context, userID string) error {
	users, _ := s.store.ListUsers(ctx)
	for _, u := range users {
		if u.ID == userID {
			return nil
		}
	}
	return newUnknownUser(userID)
}

func (s *Service) lookupLinkType(id string) *LinkType {
	for i := range s.ws.LinkTypes {
		if s.ws.LinkTypes[i].ID == id {
			return &s.ws.LinkTypes[i]
		}
	}
	return nil
}

func (s *Service) checkLinkTypeConstraints(lt *LinkType, sourceType, targetType string) *Error {
	if len(lt.SourceTypes) > 0 && !contains(lt.SourceTypes, sourceType) {
		return newTargetCardTypeMismatch(sourceType, lt.SourceTypes)
	}
	if len(lt.TargetTypes) > 0 && !contains(lt.TargetTypes, targetType) {
		return newTargetCardTypeMismatch(targetType, lt.TargetTypes)
	}
	return nil
}

func (s *Service) boardForCard(c *Card) *Board {
	return s.boardForTypeID(c.TypeID, "")
}

func (s *Service) boardForTypeID(typeID, boardID string) *Board {
	if boardID != "" {
		if b, ok := s.boards[boardID]; ok {
			return b
		}
	}
	for _, b := range s.boards {
		if contains(b.CardTypeIDs, typeID) {
			return b
		}
	}
	return nil
}

// allowedFromStatuses returns statuses that may transition to `to` under b's graph.
func allowedFromStatuses(b *Board, to string) []string {
	out := []string{}
	for from, nexts := range b.Transitions {
		if contains(nexts, to) {
			out = append(out, from)
		}
	}
	if len(out) == 0 {
		// No explicit edge; allow same-status (no-op) so a card already at `to`
		// can still be claimed.
		out = append(out, to)
	}
	return out
}

// --- filter DSL (§9 subset) → SQL ---

// compileFilterInto compiles q.Filter into q.FilterSQL/q.FilterArgs.
func (s *Service) compileFilterInto(q *CardQuery) error {
	if len(q.Filter) == 0 {
		return nil
	}
	sqlFrag, args, err := compileFilterNode(q.Filter)
	if err != nil {
		return NewValidationError("filter", err.Error())
	}
	q.FilterSQL = sqlFrag
	q.FilterArgs = args
	return nil
}

func compileFilterNode(node map[string]any) (string, []any, error) {
	var parts []string
	var args []any
	keys := sortedKeys(node)
	for _, k := range keys {
		v := node[k]
		switch k {
		case "$and", "$or":
			arr, ok := v.([]any)
			if !ok {
				return "", nil, fmt.Errorf("%s expects an array", k)
			}
			sub := []string{}
			for _, e := range arr {
				em, ok := e.(map[string]any)
				if !ok {
					return "", nil, fmt.Errorf("%s element must be an object", k)
				}
				p, a, err := compileFilterNode(em)
				if err != nil {
					return "", nil, err
				}
				if p != "" {
					sub = append(sub, "("+p+")")
					args = append(args, a...)
				}
			}
			if len(sub) > 0 {
				joiner := " AND "
				if k == "$or" {
					joiner = " OR "
				}
				parts = append(parts, "("+strings.Join(sub, joiner)+")")
			}
		default:
			opMap, ok := v.(map[string]any)
			if !ok {
				return "", nil, fmt.Errorf("field %q must be an operator object", k)
			}
			p, a, err := compileFieldOp(k, opMap)
			if err != nil {
				return "", nil, err
			}
			parts = append(parts, p)
			args = append(args, a...)
		}
	}
	return strings.Join(parts, " AND "), args, nil
}

func compileFieldOp(key string, opMap map[string]any) (string, []any, error) {
	expr, tagMode, err := columnExpr(key)
	if err != nil {
		return "", nil, err
	}
	var parts []string
	var args []any
	for op, val := range opMap {
		p, a, err := compileOp(expr, tagMode, op, val)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, p)
		args = append(args, a...)
	}
	return strings.Join(parts, " AND "), args, nil
}

// columnExpr returns the SQL expression for a filter key and whether it's the
// tags array (which needs json_each).
func columnExpr(key string) (expr string, tagMode bool, err error) {
	switch key {
	case "status", "owner", "type_id", "created_by":
		return key, false, nil
	case "updated_at", "created_at":
		return key, false, nil
	case "tag", "tags":
		return "tags", true, nil
	default:
		if strings.HasPrefix(key, "fields.") {
			return "json_extract(fields, '$." + strings.TrimPrefix(key, "fields.") + "')", false, nil
		}
		// Unknown top-level key → treat as a typed field path.
		return "json_extract(fields, '$." + key + "')", false, nil
	}
}

func compileOp(expr string, tagMode bool, op string, val any) (string, []any, error) {
	if tagMode {
		return compileTagOp(op, val)
	}
	switch op {
	case "$eq":
		if val == nil {
			return expr + " IS NULL", nil, nil
		}
		return expr + " = ?", []any{val}, nil
	case "$ne":
		if val == nil {
			return expr + " IS NOT NULL", nil, nil
		}
		return expr + " != ?", []any{val}, nil
	case "$in":
		arr := toAnySlice(val)
		if len(arr) == 0 {
			return "0", nil, nil // empty IN matches nothing
		}
		return expr + " IN (" + placeholders(len(arr)) + ")", arr, nil
	case "$nin":
		arr := toAnySlice(val)
		if len(arr) == 0 {
			return "1", nil, nil
		}
		return expr + " NOT IN (" + placeholders(len(arr)) + ")", arr, nil
	case "$gt":
		return expr + " > ?", []any{val}, nil
	case "$gte":
		return expr + " >= ?", []any{val}, nil
	case "$lt":
		return expr + " < ?", []any{val}, nil
	case "$lte":
		return expr + " <= ?", []any{val}, nil
	case "$contains":
		return expr + " LIKE ?", []any{"%" + fmt.Sprint(val) + "%"}, nil
	default:
		return "", nil, fmt.Errorf("unsupported operator %q", op)
	}
}

func compileTagOp(op string, val any) (string, []any, error) {
	switch op {
	case "$eq", "$contains":
		return "EXISTS (SELECT 1 FROM json_each(tags) WHERE value = ?)", []any{val}, nil
	case "$in":
		arr := toAnySlice(val)
		if len(arr) == 0 {
			return "0", nil, nil
		}
		return "EXISTS (SELECT 1 FROM json_each(tags) WHERE value IN (" + placeholders(len(arr)) + "))", arr, nil
	case "$nin":
		arr := toAnySlice(val)
		if len(arr) == 0 {
			return "1", nil, nil
		}
		return "NOT EXISTS (SELECT 1 FROM json_each(tags) WHERE value IN (" + placeholders(len(arr)) + "))", arr, nil
	default:
		return "", nil, fmt.Errorf("unsupported tag operator %q", op)
	}
}

// --- id + cursor helpers ---

func newCardID() string    { return "card_" + strings.ReplaceAll(uuid.NewString(), "-", "") }
func newEntryID() string   { return "ent_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] }
func newCommentID() string { return "cm_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] }

// EncodeCursor / DecodeCursor: base64("updated_at|id").
func EncodeCursor(updatedAt time.Time, id string) string {
	s := updatedAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func DecodeCursor(c string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("bad cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return t, parts[1], nil
}

// --- small utilities ---


func ctxActor(ctx context.Context) string {
	if v, ok := ctx.Value(actorCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// ActorFromCtx is the exported accessor for transports.
func ActorFromCtx(ctx context.Context) string { return ctxActor(ctx) }

// WithActor returns a context carrying an actor (used by transports).
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorCtxKey{}, actor)
}

type actorCtxKey struct{}

func defaultStatus(ct *CardType, ws *Workspace) string {
	if len(ct.AllowedColumns) > 0 {
		return ct.AllowedColumns[0]
	}
	if len(ws.Columns) > 0 {
		return ws.Columns[0].ID
	}
	return ""
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func fieldIDs(ct *CardType) []string {
	out := make([]string, 0, len(ct.Fields))
	for _, f := range ct.Fields {
		out = append(out, f.ID)
	}
	sort.Strings(out)
	return out
}

func linkTypeIDs(ws *Workspace) []string {
	out := make([]string, 0, len(ws.LinkTypes))
	for _, l := range ws.LinkTypes {
		out = append(out, l.ID)
	}
	return out
}

func columnIDs(ws *Workspace) []string {
	out := make([]string, 0, len(ws.Columns))
	for _, c := range ws.Columns {
		out = append(out, c.ID)
	}
	return out
}

func strOrEmpty(s string) string { return s }

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func diffTags(old, new []string) (added, removed []string) {
	oset := map[string]bool{}
	for _, t := range old {
		oset[t] = true
	}
	nset := map[string]bool{}
	for _, t := range new {
		nset[t] = true
	}
	for _, t := range new {
		if !oset[t] {
			added = append(added, t)
		}
	}
	for _, t := range old {
		if !nset[t] {
			removed = append(removed, t)
		}
	}
	return
}

// getMapField fetches a key from a fields map (any-shaped).
func getMapField(fields any, key string) any {
	m, _ := fields.(map[string]any)
	if m == nil {
		return nil
	}
	return m[key]
}

func setField(fields any, key string, val any) map[string]any {
	m, _ := fields.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	out[key] = val
	return out
}

func appendEntry(fields any, field string, entry any) []any {
	m, _ := fields.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	arr, _ := m[field].([]any)
	out := make([]any, 0, len(arr)+1)
	out = append(out, arr...)
	out = append(out, entry)
	return out
}

func findEntry(arr []any, entryID string) (map[string]any, int, bool) {
	for i, e := range arr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := em["entry_id"].(string); id == entryID {
			return em, i, true
		}
	}
	return nil, -1, false
}

func toAnySlice(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		if s, ok := v.([]string); ok {
			arr = make([]any, len(s))
			for i, x := range s {
				arr[i] = x
			}
			return arr
		}
		return nil
	}
	return arr
}

func placeholders(n int) string {
	return strings.Repeat("?,", n-1) + "?"
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// summarizeEvent renders a human/agent-readable one-liner for the history
// timeline. SPEC §8.
func summarizeEvent(e Event) string {
	d, _ := e.Diff.(map[string]any)
	switch e.Type {
	case EventCardCreated:
		if c, ok := d["card"].(map[string]any); ok {
			return fmt.Sprintf("card created: %q (type=%v, status=%v)", c["title"], c["type_id"], c["status"])
		}
		return "card created"
	case EventFieldUpdated:
		return fmt.Sprintf("field %v: %v → %v", d["field"], d["before"], d["after"])
	case EventStatusChanged:
		return fmt.Sprintf("status: %v → %v", d["before"], d["after"])
	case EventOwnerChanged:
		return fmt.Sprintf("owner: %v → %v", d["before"], d["after"])
	case EventTagsChanged:
		return fmt.Sprintf("tags + %v / - %v", d["added"], d["removed"])
	case EventItemAppended:
		return fmt.Sprintf("appended entry to %v (entry_id=%v)", d["field"], d["entry_id"])
	case EventItemUpdated:
		return fmt.Sprintf("updated entry in %v (entry_id=%v)", d["field"], d["entry_id"])
	case EventItemRemoved:
		return fmt.Sprintf("removed entry from %v (entry_id=%v)", d["field"], d["entry_id"])
	case EventLinkAdded:
		return fmt.Sprintf("link %v → %v", d["type_id"], d["target"])
	case EventLinkRemoved:
		return fmt.Sprintf("removed link %v → %v", d["type_id"], d["target"])
	case EventCommentAdded:
		return fmt.Sprintf("comment added (%v)", d["comment_id"])
	case EventCommentEdited:
		return fmt.Sprintf("comment edited (%v)", d["comment_id"])
	case EventSchemaUpgraded:
		return fmt.Sprintf("schema upgraded: %v → %v", d["from"], d["to"])
	default:
		return string(e.Type)
	}
}


