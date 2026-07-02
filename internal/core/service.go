// Package core — service.go
//
// The Service layer owns schema lookup, validation, transition evaluation,
// optimistic concurrency, idempotency (HTTP-layer), and event writing. All
// transports call into this package. See docs/ARCHITECTURE.md (Core Service
// Boundary) and docs/SPEC.md (§10, §11).
package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Service is the transport-independent core. Construct with NewService.
type Service struct {
	ws      *Workspace
	types   map[string]*CardType
	boards  map[string]*Board
	store   Store
	bus     Bus
	emitter *Emitter
	now     func() time.Time

	// wipExceeded tracks the last-known WIP-exceeded state per board+column so
	// wip signals fire only on a crossing, not on every mutation. (seam 3a)
	wipMu       sync.Mutex
	wipExceeded map[string]bool
}

// NewService binds loaded config + a Store implementation.
func NewService(ws *Workspace, types map[string]*CardType, boards map[string]*Board, st Store) *Service {
	now := func() time.Time { return time.Now().UTC() }
	bus := NewBus()
	return &Service{
		ws: ws, types: types, boards: boards, store: st,
		bus: bus, emitter: newEmitter(st, bus, now), now: now,
		wipExceeded: map[string]bool{},
	}
}

// evaluateWIP fires wip_exceeded/wip_cleared as ephemeral board signals when a
// board column crosses its configured WIP limit. Best-effort — it's a signal,
// not a fact: a failed count never affects the mutation. Idempotent: fires only
// on a state crossing (seam 3a).
func (s *Service) evaluateWIP(ctx context.Context, b *Board, column string) {
	if b == nil {
		return
	}
	limit, ok := b.WIPLimits[column]
	if !ok || limit <= 0 {
		return
	}
	page, err := s.store.ListCards(ctx, CardQuery{Status: column, TypeIDIn: b.CardTypeIDs, Limit: 500})
	if err != nil {
		return
	}
	exceeded := len(page.Items) > limit
	key := b.ID + "\x00" + column
	s.wipMu.Lock()
	if s.wipExceeded[key] == exceeded {
		s.wipMu.Unlock()
		return // no crossing — stay quiet
	}
	s.wipExceeded[key] = exceeded
	s.wipMu.Unlock()
	if exceeded {
		s.emitter.Signal(ctx, WIPExceeded(b.ID, column, len(page.Items), limit))
	} else {
		s.emitter.Signal(ctx, WIPCleared(b.ID, column, len(page.Items), limit))
	}
}

// Bus returns the in-process event bus (for SSE/hooks subscribers).
func (s *Service) Bus() Bus { return s.bus }

// Emitter returns the event emission seam (for registering observers).
func (s *Service) Emitter() *Emitter { return s.emitter }

// commitCard persists a card mutation and its events atomically, then dispatches
// the events to the bus + observers. This is the only path for durable
// card-mutation events: stamp (before) -> store.UpdateCard (atomic persist +
// id backfill) -> dispatchCommitted (after commit). Publish never precedes
// durable commit.
func (s *Service) commitCard(ctx context.Context, next *Card, evs []*Event) error {
	s.emitter.stamp(ctx, evs)
	if err := s.store.UpdateCard(ctx, next, evs); err != nil {
		return err
	}
	s.emitter.dispatchCommitted(evs)
	return nil
}

// Workspace returns the introspection snapshot (GET /v1/workspace).
// The snapshot carries a copy of the workspace so concurrent requests never
// share (or race on) the Service's live *Workspace; config-loaded fields are
// immutable after startup, only Users is refreshed per call.
func (s *Service) Workspace(ctx context.Context) (*WorkspaceSnapshot, error) {
	ws := *s.ws
	if users, err := s.store.ListUsers(ctx); err == nil && len(users) > 0 {
		ws.Users = users
	}
	curVersions := map[string]int{}
	for id, ct := range s.types {
		curVersions[id] = ct.SchemaVersion
	}
	return &WorkspaceSnapshot{
		Workspace:       &ws,
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

// ListCards filters/paginates cards. SPEC §9. The jq-like Filter DSL is
// passed through raw; the store compiles it.
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
	// The board's default_filter is a hard isolation boundary: AND it with any
	// caller-supplied filter so the board's scope can be narrowed but never
	// widened. SPEC §9: a board_id query applies the board's default_filter.
	if len(b.DefaultFilter) > 0 {
		if len(q.Filter) == 0 {
			q.Filter = b.DefaultFilter
		} else {
			q.Filter = map[string]any{"$and": []any{b.DefaultFilter, q.Filter}}
		}
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

// ResolveCard resolves an id that may be a full id or a last-8-hex short id.
// A full id wins even if it suffixes another card. 0 → ErrNotFound (mapped to
// NotFound/404), 1 → that card (with links + comments loaded), >1 →
// *AmbiguousIDError with candidates (never auto-resolves). (1e)
func (s *Service) ResolveCard(ctx context.Context, id string) (*Card, error) {
	// Full-id path first: a full id that also suffixes another card still
	// resolves to itself.
	if c, err := s.store.GetCard(ctx, id); err == nil {
		return c, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, Internal("failed to get card: " + err.Error())
	}
	// Fall back to short-id (last-8-hex) matching.
	cands, err := s.store.GetCardsByShortID(ctx, id)
	if err != nil {
		return nil, Internal("failed to resolve short id: " + err.Error())
	}
	switch len(cands) {
	case 0:
		return nil, NotFound("card " + id)
	case 1:
		c := cands[0]
		c.Links, _ = s.store.ListLinks(ctx, c.ID)
		c.Comments, _ = s.store.ListComments(ctx, c.ID)
		return &c, nil
	default:
		cs := make([]CardCandidate, len(cands))
		for i, c := range cands {
			cs[i] = CardCandidate{ID: c.ID, Title: c.Title}
		}
		return nil, &AmbiguousIDError{Short: id, Candidates: cs}
	}
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
	if err := s.checkColumn(status, ct); err != nil {
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
	ctx = WithActor(ctx, actor)
	ev := CardCreated(c)
	s.emitter.stamp(ctx, []*Event{ev})
	if err := s.store.InsertCard(ctx, c, ev); err != nil {
		return nil, err
	}
	s.emitter.dispatchCommitted([]*Event{ev})
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
	ctx = WithActor(ctx, actor)

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
			events = append(events, FieldChanged(id, "title", current.Title, newTitle))
			next.Title = newTitle
		}
	}

	// status
	if req.Status != nil && *req.Status != current.Status {
		newStatus := *req.Status
		if err := s.checkColumn(newStatus, ct); err != nil {
			return nil, err
		}
		if b := s.boardForCard(current); b != nil && b.Settings.EnforceTransitions && !req.Force {
			allowed, ok := b.Transitions[current.Status]
			if ok && !contains(allowed, newStatus) {
				return nil, newTransitionIllegal(current.Status, allowed)
			}
		}
		events = append(events, StatusChanged(id, current.Status, newStatus))
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
			events = append(events, OwnerChanged(id, current.Owner, newOwner))
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
			events = append(events, TagsChanged(id, added, removed))
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
		type fieldChange struct {
			field         string
			before, after any
		}
		changed := []fieldChange{}
		for k, v := range req.Fields {
			before := base[k]
			if err := s.validateOneField(ctx, ct, k, v); err != nil {
				return nil, err
			}
			merged[k] = v
			changed = append(changed, fieldChange{field: k, before: before, after: v})
		}
		if _, err := s.validateFields(ctx, ct, merged, false); err != nil {
			return nil, err
		}
		for _, cf := range changed {
			events = append(events, FieldChanged(id, cf.field, cf.before, cf.after))
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
	if err := s.commitCard(ctx, &next, events); err != nil {
		return nil, err
	}
	// After the status change commits, re-check WIP on both columns: the
	// destination may now exceed its limit, the source may have cleared. (3a)
	if next.Status != current.Status {
		b := s.boardForCard(&next)
		s.evaluateWIP(ctx, b, next.Status)
		s.evaluateWIP(ctx, b, current.Status)
	}
	return &next, nil
}

// UpgradeSchemaRequest re-pins a card to a newer schema version of its type.
type UpgradeSchemaRequest struct {
	TargetVersion int  // 0 means the type's current schema_version
	DryRun        bool // preview the upgraded card without persisting
	Actor         string
}

// UpgradeSchema re-pins a card to a newer schema version of its type. It drops
// fields no longer in the target schema, applies the migrations' field_defaults
// for fields introduced between the card's version and the target, then
// validates the result against the target schema. Emits schema_upgraded. SPEC §6.
//
// MVP scope: the target must equal the type's current schema_version (the only
// schema the server has loaded) and upgrades go forward only.
func (s *Service) UpgradeSchema(ctx context.Context, id string, req UpgradeSchemaRequest) (*Card, error) {
	if req.Actor == "" {
		return nil, ActorRequired()
	}
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
	ct, ok := s.types[current.TypeID]
	if !ok {
		return nil, NotFound("card_type " + current.TypeID)
	}
	target := req.TargetVersion
	if target == 0 {
		target = ct.SchemaVersion
	}
	if target == current.SchemaVersion {
		return current, nil // already at target; no-op
	}
	if target < current.SchemaVersion {
		return nil, NewValidationError("target_version",
			fmt.Sprintf("cannot downgrade card from version %d to %d", current.SchemaVersion, target))
	}
	if target != ct.SchemaVersion {
		return nil, NewValidationError("target_version",
			fmt.Sprintf("only upgrading to the current type version %d is supported", ct.SchemaVersion))
	}

	// Start from the card's fields, dropping any no longer defined in the
	// target schema (a field removed in a newer version).
	known := map[string]bool{}
	for _, f := range ct.Fields {
		known[f.ID] = true
	}
	base, _ := current.Fields.(map[string]any)
	merged := map[string]any{}
	dropped := []string{}
	for k, v := range base {
		if known[k] {
			merged[k] = v
		} else {
			dropped = append(dropped, k)
		}
	}
	// Apply field_defaults for each migration step (current+1 .. target),
	// filling only fields not already present.
	applied := map[string]any{}
	for v := current.SchemaVersion + 1; v <= target; v++ {
		m, ok := ct.Migrations[fmt.Sprintf("%d", v)]
		if !ok {
			continue
		}
		for fid, def := range m.FieldDefaults {
			if _, present := merged[fid]; !present {
				merged[fid] = def
				applied[fid] = def
			}
		}
	}
	// The upgraded card must be fully valid at the target schema.
	validated, err := s.validateFields(ctx, ct, merged, true)
	if err != nil {
		return nil, err
	}

	now := s.now()
	next := *current
	next.SchemaVersion = target
	next.Fields = validated
	next.Version = current.Version + 1
	next.UpdatedAt = now
	if req.DryRun {
		return &next, nil
	}

	if req.Actor != "" {
		ctx = WithActor(ctx, req.Actor)
	}
	ev := SchemaUpgraded(id, current.SchemaVersion, target, applied, dropped)
	if err := s.commitCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
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
	entryID, _ := stored["entry_id"].(string)
	ev := ItemAppended(id, field, entryID, entry, len(arr)-1)
	if err := s.commitCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
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
	ev := ItemUpdated(id, field, entryID, before, newEntry)
	if err := s.commitCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
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
	ev := ItemRemoved(id, field, entryID, entry)
	if err := s.commitCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
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
	ctx = WithActor(ctx, actor)
	l := Link{TypeID: in.TypeID, Target: in.Target, Note: in.Note, CreatedBy: actor, CreatedAt: s.now()}
	next := *current
	next.Version = current.Version + 1
	next.UpdatedAt = l.CreatedAt
	next.Links = append(append([]Link{}, current.Links...), l)
	ev := LinkAdded(id, in.TypeID, in.Target, in.Note)
	if err := s.commitCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
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
	ev := LinkRemoved(id, typeID, target)
	if err := s.commitCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
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
	ev := CommentAdded(id, c.ID)
	if err := s.commitCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
	_ = s.store.InsertComment(ctx, id, c)
	return &next, nil
}

// EditComment updates a comment's body. SPEC §11.
func (s *Service) EditComment(ctx context.Context, id, commentID, body string) (*Card, error) {
	current, err := s.getCard(ctx, id)
	if err != nil {
		return nil, err
	}
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
	ev := CommentEdited(id, commentID, before, body)
	if err := s.commitCard(ctx, &next, []*Event{ev}); err != nil {
		return nil, err
	}
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
	c, evs, err := s.store.ClaimAtomic(ctx, q, assignTo, req.Status, actor, s.now())
	if err != nil {
		return nil, err
	}
	s.emitter.dispatchCommitted(evs)
	return c, nil // nil card → caller renders {card:null}
}

// ListEvents returns recent events for a card. SPEC §11.
func (s *Service) ListEvents(ctx context.Context, q EventQuery) ([]Event, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 50
	}
	return s.store.List(ctx, q)
}

// ListEventsPage is the cursor-paged catch-up feed (GET /v1/events): the
// durable path for an integrator to replay what it missed while disconnected,
// then resume the live SSE stream. Ordered by id ASC; NextCursor is the last
// event id. See docs/INTEGRATION.md.
func (s *Service) ListEventsPage(ctx context.Context, q EventQuery) (*Page[Event], error) {
	return s.store.Page(ctx, q)
}

// History renders a resumption-ready timeline for a card. SPEC §8.
func (s *Service) History(ctx context.Context, id string) ([]HistoryEntry, error) {
	if _, err := s.getCard(ctx, id); err != nil {
		return nil, err
	}
	evs, err := s.store.List(ctx, EventQuery{CardID: id, Limit: 500})
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(evs))
	for _, e := range evs {
		out = append(out, HistoryEntry{At: e.At, Actor: e.Actor, Type: e.Type, Summary: summarizeEvent(e)})
	}
	return out, nil
}
