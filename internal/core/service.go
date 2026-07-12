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
	"io"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/somebox/cards/internal/artifacts"
)

// Service is the transport-independent core. Construct with NewService.
type Service struct {
	ws      *Workspace
	types   map[string]*CardType
	boards  map[string]*Board
	store   Store
	bus     Bus
	emitter *Emitter
	clock   Clock

	// condState tracks the last-known crossing state per (board, column,
	// condition-kind) key so a condition fires only on a state crossing, not
	// on every mutation. Shared by every instant condition that derives from
	// evaluateColumn's census (wip_exceeded/cleared seam 3a, lane_drained/
	// refilled seam 3c) — one map, one lock, per EVENTS.md §12 Step 3's
	// "unify with 3a, don't build a second counting path" mandate.
	condMu    sync.Mutex
	condState map[string]bool

	// monitors is the seam 3d deadline scheduler, started only when a board
	// declares a temporal monitor (max_time_in_status/idle_after) or a
	// temporal type is persisted — most workspaces never construct one. (3e)
	monitors *MonitorScheduler

	// artifacts stores/serves bytes for artifact fields (local policy). Wired by
	// the composition root (SetArtifacts); nil in workspaces that never
	// configure a root, where AddArtifact/OpenArtifact return a clear error.
	artifacts *artifacts.Manager
}

// SetArtifacts wires the workspace artifact store (bytes for artifact fields).
// Called once by the composition root after construction.
func (s *Service) SetArtifacts(m *artifacts.Manager) { s.artifacts = m }

// now returns the service's current time (via its Clock) — a method, not a
// field, so every existing s.now() call site is unaffected by the seam 3d
// clock-injection seam below.
func (s *Service) now() time.Time { return s.clock.Now() }

// ServiceOption configures optional Service construction behavior. See
// WithClock.
type ServiceOption func(*Service)

// WithClock overrides the Service's time source (production default:
// wallClock). Tests use a clocktest.Fake to drive stamping and the seam 3d
// deadline scheduler deterministically, with no real sleeps.
func WithClock(c Clock) ServiceOption {
	return func(s *Service) { s.clock = c }
}

// WithBus shares an existing event bus with the new Service. The workspace
// reload seam rebuilds the Service around the SAME bus so live SSE
// subscribers and the hook supervisor stay attached across definition
// reloads instead of going silently stale.
func WithBus(b Bus) ServiceOption {
	return func(s *Service) { s.bus = b }
}

// NewService binds loaded config + a Store implementation.
func NewService(ws *Workspace, types map[string]*CardType, boards map[string]*Board, st Store, opts ...ServiceOption) *Service {
	bus := NewBus()
	svc := &Service{
		ws: ws, types: types, boards: boards, store: st,
		bus: bus, clock: wallClock{},
		condState: map[string]bool{},
	}
	for _, opt := range opts {
		opt(svc)
	}
	svc.emitter = newEmitter(st, svc.bus, svc.clock.Now)
	// Escalate any condition types the workspace opted into persisting (3b).
	if len(ws.Settings.PersistConditions) > 0 {
		esc := make([]EventType, 0, len(ws.Settings.PersistConditions))
		for _, t := range ws.Settings.PersistConditions {
			esc = append(esc, EventType(t))
		}
		svc.emitter.PersistConditions(esc...)
	}
	// The deadline scheduler only starts if something actually needs it — a
	// board declaring a temporal monitor, or a temporal type escalated via
	// persist_conditions above. (3d/3e)
	// The scheduler needs the concrete InProcBus (subscription-change +
	// interest probes are not part of the Bus interface). Both NewBus and any
	// bus shared via WithBus are InProcBus today; a foreign implementation
	// would simply run without temporal monitors.
	ipb, _ := svc.bus.(*InProcBus)
	if ipb != nil && (hasTemporalMonitors(boards) || svc.emitter.IsPersisted(EventStatusTimeout) || svc.emitter.IsPersisted(EventCardIdle)) {
		svc.monitors = NewMonitorScheduler(svc.clock, ipb, svc.emitter, st)
		svc.monitors.Register(EventStatusTimeout, svc.verifyStatusTimeout, svc.rebuildStatusTimeout)
		svc.monitors.Register(EventCardIdle, svc.verifyCardIdle, svc.rebuildCardIdle)
		svc.emitter.Observe(svc.monitorObserver)
		svc.monitors.Start()
	}
	return svc
}

// hasTemporalMonitors reports whether any board declares max_time_in_status
// or idle_after.
func hasTemporalMonitors(boards map[string]*Board) bool {
	for _, b := range boards {
		if b.Monitors != nil && (len(b.Monitors.MaxTimeInStatus) > 0 || b.Monitors.IdleAfter != "") {
			return true
		}
	}
	return false
}

// Close releases resources the Service started internally — currently just
// the seam 3d deadline scheduler, if one was started. A workspace with no
// temporal monitors and nothing temporal persisted never starts one, so
// Close is a no-op for the common case. The underlying Store is closed by
// its owner (cmd/cards), not by Service — Service has never owned Store's
// lifecycle, and this doesn't change that.
func (s *Service) Close() {
	if s.monitors != nil {
		s.monitors.Stop()
	}
}

// evaluateColumn is the single column census (Events seam 3a/3c): one
// ListCards query per (board, column) per mutation feeds both the WIP-limit
// crossing (wip_exceeded/wip_cleared) and, for columns declared in
// board.Monitors.AlertWhenEmpty, the drained-lane crossing (lane_drained/
// lane_refilled) — the same count, one counting path, per EVENTS.md §12 Step
// 3c. Called from every mutation path that can change a column's membership:
// PatchCard (status move), CreateCard (card lands directly in a column), and
// TakeNext (claim + optional status move).
//
// Best-effort: a failed count silently returns (never affects the triggering
// mutation); a failed escalated append is logged by evaluateCrossing, never
// returned here.
//
// Caveat (documented, not fixed — EVENTS.md §12 Step 3c): membership is
// TypeIDIn only (board.CardTypeIDs); a board scoped by DefaultFilter instead
// is not counted correctly, and the census caps at 500 cards. Revisit only if
// a filter-defined board needs WIP/lane limits.
func (s *Service) evaluateColumn(ctx context.Context, b *Board, column string) {
	if b == nil {
		return
	}
	limit, hasLimit := b.WIPLimits[column]
	watchEmpty := b.Monitors != nil && Contains(b.Monitors.AlertWhenEmpty, column)
	if (!hasLimit || limit <= 0) && !watchEmpty {
		return
	}
	count, err := s.countColumn(ctx, b, column)
	if err != nil {
		return
	}
	if hasLimit && limit > 0 {
		s.evaluateCrossing(ctx, b.ID+"\x00"+column+"\x00wip", count > limit,
			func() *Event { return WIPExceeded(b.ID, column, count, limit) },
			func() *Event { return WIPCleared(b.ID, column, count, limit) })
	}
	if watchEmpty {
		s.evaluateCrossing(ctx, b.ID+"\x00"+column+"\x00lane", count == 0,
			func() *Event { return LaneDrained(b.ID, column, count) },
			func() *Event { return LaneRefilled(b.ID, column, count) })
	}
}

// countColumn is the single column-census query: how many of board b's card
// types currently sit in column. Shared by evaluateColumn (crossing
// detection on a mutation) and Breaches (on-demand catch-up) — one counting
// path, not two (EVENTS.md §12 Step 3c).
func (s *Service) countColumn(ctx context.Context, b *Board, column string) (int, error) {
	page, err := s.store.ListCards(ctx, CardQuery{Status: column, TypeIDIn: b.CardTypeIDs, Limit: 500})
	if err != nil {
		return 0, err
	}
	return len(page.Items), nil
}

// evaluateCrossing is the shared instant-condition crossing tracker: it fires
// onCrossed only the first time key transitions to crossed=true, and
// onRecovered only the first time it transitions back to crossed=false —
// staying quiet on every mutation in between (seam 3a's idempotence,
// generalized in 3c to any boolean condition sharing this state map).
// Escalated (persist:true) events route through Condition; a failed durable
// append is logged, never returned — a best-effort signal must never fail the
// mutation that triggered it (EVENTS.md §8 point 7, §12 Step 3c hardening).
func (s *Service) evaluateCrossing(ctx context.Context, key string, crossed bool, onCrossed, onRecovered func() *Event) {
	s.condMu.Lock()
	if s.condState[key] == crossed {
		s.condMu.Unlock()
		return // no crossing — stay quiet
	}
	s.condState[key] = crossed
	s.condMu.Unlock()
	var ev *Event
	if crossed {
		ev = onCrossed()
	} else {
		ev = onRecovered()
	}
	if err := s.emitter.Condition(ctx, ev); err != nil {
		log.Printf("ERROR: escalated condition append failed (type=%s key=%s): %v", ev.Type, key, err)
	}
}

// evaluateBlocked fires card_blocked/card_unblocked when cardID's blocked
// state crosses, sharing evaluateCrossing's state map (keyed per-card) so a
// repeat blocking link or an unrelated re-evaluation does not re-fire while
// the crossing state is unchanged. "Blocked" is store.Blockers' definition —
// the same one CardQuery.Blocked applies — so this shares its single source
// of truth with list/filter queries (Events seam 3c).
func (s *Service) evaluateBlocked(ctx context.Context, cardID string) {
	blockers, err := s.store.Blockers(ctx, cardID)
	if err != nil {
		return
	}
	s.evaluateCrossing(ctx, "card\x00"+cardID+"\x00blocked", len(blockers) > 0,
		func() *Event { return CardBlocked(cardID, blockers) },
		func() *Event { return CardUnblocked(cardID) })
}

// reevaluateDependents re-checks the blocked state of every card holding a
// blocked-by/depends-on link to cardID — called after cardID's status
// changes, since that status is the only thing that can flip a dependent's
// blocked state (a target reaching "done" unblocks it; leaving "done" again
// re-blocks it). Best-effort: a failed lookup silently returns. (3c)
func (s *Service) reevaluateDependents(ctx context.Context, cardID string) {
	deps, err := s.store.BlockingDependents(ctx, cardID)
	if err != nil {
		return
	}
	for _, dep := range deps {
		s.evaluateBlocked(ctx, dep)
	}
}

// monitorObserver arms/re-arms the seam 3d temporal deadlines from every
// dispatched event — zero new call sites in the mutation paths themselves.
// status_changed/card_created (entering a status) arm status_timeout;
// every durable card-mutation event re-arms card_idle — except a condition
// event, which must never reset the idle deadline (§12 Step 3e: a fired
// card_idle would otherwise immediately re-arm itself). Best-effort: a
// failed card lookup just skips re-arming until the next event. (3e)
func (s *Service) monitorObserver(ev *Event) {
	if s.monitors == nil || ev.CardID == "" || isConditionType(ev.Type) {
		return
	}
	ctx := context.Background()
	c, err := s.store.GetCard(ctx, ev.CardID)
	if err != nil {
		return
	}
	b := s.boardForCard(c)
	if b == nil || b.Monitors == nil {
		return
	}
	if ev.Type == EventStatusChanged || ev.Type == EventCardCreated {
		if max, ok := b.Monitors.MaxTimeInStatus[c.Status]; ok {
			key := c.Status + "\x00" + c.StatusSince.Format(time.RFC3339Nano)
			s.monitors.Arm(EventStatusTimeout, c.ID, c.StatusSince.Add(mustParseMonitorDuration(max)), key)
		}
	}
	if b.Monitors.IdleAfter != "" {
		key := c.UpdatedAt.Format(time.RFC3339Nano)
		s.monitors.Arm(EventCardIdle, c.ID, c.UpdatedAt.Add(mustParseMonitorDuration(b.Monitors.IdleAfter)), key)
	}
}

// verifyStatusTimeout re-checks a due status_timeout deadline: fires only if
// the card is still in the exact (status, status_since) it was armed for —
// a card that left the status (or re-entered it, getting a fresh
// status_since) makes the deadline stale, discarded silently. (3e)
func (s *Service) verifyStatusTimeout(ctx context.Context, cardID, key string) (*Event, error) {
	c, err := s.store.GetCard(ctx, cardID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if c.Status+"\x00"+c.StatusSince.Format(time.RFC3339Nano) != key {
		return nil, nil // stale — card moved on
	}
	b := s.boardForCard(c)
	if b == nil || b.Monitors == nil {
		return nil, nil
	}
	max, ok := b.Monitors.MaxTimeInStatus[c.Status]
	if !ok {
		return nil, nil // monitor config changed since arming
	}
	return StatusTimeout(c.ID, c.Status, c.StatusSince, max), nil
}

// rebuildStatusTimeout scans every board's monitored statuses for currently-
// due status_timeout deadlines — called when interest in the type is
// (re)gained (e.g. the first matching SSE subscriber connects). (3e)
func (s *Service) rebuildStatusTimeout(ctx context.Context) ([]MonitorDeadline, error) {
	var out []MonitorDeadline
	for _, b := range s.boards {
		if b.Monitors == nil || len(b.Monitors.MaxTimeInStatus) == 0 {
			continue
		}
		for status, maxStr := range b.Monitors.MaxTimeInStatus {
			max, err := ParseMonitorDuration(maxStr)
			if err != nil {
				continue // validated at config load; defensive
			}
			page, err := s.store.ListCards(ctx, CardQuery{Status: status, TypeIDIn: b.CardTypeIDs, Limit: 500})
			if err != nil {
				continue
			}
			for _, c := range page.Items {
				key := c.Status + "\x00" + c.StatusSince.Format(time.RFC3339Nano)
				out = append(out, MonitorDeadline{At: c.StatusSince.Add(max), CardID: c.ID, Key: key})
			}
		}
	}
	return out, nil
}

// verifyCardIdle re-checks a due card_idle deadline: fires only if the card
// has had no further mutation since the exact UpdatedAt it was armed for —
// any later mutation makes the deadline stale, discarded silently. (3e)
func (s *Service) verifyCardIdle(ctx context.Context, cardID, key string) (*Event, error) {
	c, err := s.store.GetCard(ctx, cardID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if c.UpdatedAt.Format(time.RFC3339Nano) != key {
		return nil, nil // stale — card mutated again since this deadline armed
	}
	b := s.boardForCard(c)
	if b == nil || b.Monitors == nil || b.Monitors.IdleAfter == "" {
		return nil, nil
	}
	return CardIdle(c.ID, c.UpdatedAt, b.Monitors.IdleAfter), nil
}

// rebuildCardIdle scans every idle-monitoring board's cards for currently-
// due card_idle deadlines. A card whose type is monitored by more than one
// board is armed once (idleness isn't board-scoped). (3e)
func (s *Service) rebuildCardIdle(ctx context.Context) ([]MonitorDeadline, error) {
	var out []MonitorDeadline
	seen := map[string]bool{}
	for _, b := range s.boards {
		if b.Monitors == nil || b.Monitors.IdleAfter == "" {
			continue
		}
		idleAfter, err := ParseMonitorDuration(b.Monitors.IdleAfter)
		if err != nil {
			continue
		}
		page, err := s.store.ListCards(ctx, CardQuery{TypeIDIn: b.CardTypeIDs, Limit: 500})
		if err != nil {
			continue
		}
		for _, c := range page.Items {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			key := c.UpdatedAt.Format(time.RFC3339Nano)
			out = append(out, MonitorDeadline{At: c.UpdatedAt.Add(idleAfter), CardID: c.ID, Key: key})
		}
	}
	return out, nil
}

// isConditionType reports whether t is one of the condition types (as
// opposed to a durable card-mutation fact) — used by monitorObserver to
// never let a condition event reset the card_idle deadline.
func isConditionType(t EventType) bool {
	return slices.Contains(ConditionTypes(), t)
}

// mustParseMonitorDuration parses a duration already validated at config
// load; a parse failure here would mean that validation was bypassed
// (test-constructed workspace) — fail open with 0 (fires immediately, loud
// and safe) rather than panic.
func mustParseMonitorDuration(s string) time.Duration {
	d, err := ParseMonitorDuration(s)
	if err != nil {
		log.Printf("ERROR: invalid monitor duration %q (should have been rejected at config load): %v", s, err)
		return 0
	}
	return d
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
	s.emitter.dispatch(evs)
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
	// Limit is clamped once, in the store (clampCardLimit: default 50, ceiling
	// 500) — this layer no longer applies its own cap, which used to conflate
	// the default and ceiling into ">200 → 50" and silently truncated any
	// larger API request down to 50.
	//
	// Validate the sort directive up front so a junk key 422s instead of
	// silently ordering by nothing.
	if _, err := ParseSort(q.Sort); err != nil {
		return nil, err
	}
	// Keyset pagination is welded to the default order (updated_at, id), so a
	// custom sort and a cursor can't coexist — reject rather than return a
	// silently-wrong page. The only cursor consumers (export, API paging) use
	// the default sort; the UI board is limit-200/no-cursor, so every sort
	// works there.
	if q.Sort != "" && q.Cursor != "" {
		return nil, NewValidationError("sort", "sort is not supported together with cursor pagination")
	}
	// Validate cursor before hitting the store — a bad cursor should be a
	// 400, not a silent fallthrough to the first page.
	if q.Cursor != "" {
		if _, _, err := DecodeCursor(q.Cursor); err != nil {
			return nil, NewValidationError("cursor", "invalid cursor: "+err.Error())
		}
	}
	wantLinks, wantComments, err := parseInclude(q.Include)
	if err != nil {
		return nil, err
	}
	page, err := s.store.ListCards(ctx, q)
	if err != nil {
		return nil, err
	}
	// Eager-load the requested related collections. This is an N+1 over the
	// page (bounded by the clamped limit), traded for the client reading the
	// whole graph in one call — the store's GetCard loads the same two.
	for i := range page.Items {
		if wantLinks {
			links, err := s.store.ListLinks(ctx, page.Items[i].ID)
			if err != nil {
				return nil, err
			}
			page.Items[i].Links = links
		}
		if wantComments {
			comments, err := s.store.ListComments(ctx, page.Items[i].ID)
			if err != nil {
				return nil, err
			}
			page.Items[i].Comments = comments
		}
	}
	return page, nil
}

// parseInclude validates the ?include= set and reports which related
// collections to eager-load. Unknown values are rejected so a typo surfaces
// rather than silently omitting data.
func parseInclude(include []string) (links, comments bool, err error) {
	for _, inc := range include {
		switch inc {
		case "links":
			links = true
		case "comments":
			comments = true
		case "":
			// tolerate empty segments from a trailing comma
		default:
			return false, false, NewValidationError("include", "unknown include "+inc+" (valid: links, comments)")
		}
	}
	return links, comments, nil
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

// ResolveCard resolves an id that may be a full id or an 8-char short id (the leading 8 chars of the hex part).
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
	// Fall back to short-id (leading-8) matching.
	cands, err := s.store.GetCardsByShortID(ctx, id)
	if err != nil {
		return nil, Internal("failed to resolve short id: " + err.Error())
	}
	switch len(cands) {
	case 0:
		return nil, NotFound("card " + id)
	case 1:
		c := cands[0]
		if c.Links, err = s.store.ListLinks(ctx, c.ID); err != nil {
			return nil, Internal("failed to load links: " + err.Error())
		}
		if c.Comments, err = s.store.ListComments(ctx, c.ID); err != nil {
			return nil, Internal("failed to load comments: " + err.Error())
		}
		return &c, nil
	default:
		cs := make([]CardCandidate, len(cands))
		for i, c := range cands {
			cs[i] = CardCandidate{ID: c.ID, Title: c.Title}
		}
		return nil, &AmbiguousIDError{Short: id, Candidates: cs}
	}
}

// resolveForWrite is the lookup used by every mutating method: a card
// reference may be a full id or an 8-char short id, exactly like reads.
// It normalizes *ref to the card's full id in place, so a short id can never
// leak into events, link rows, or store writes — taking the reference by
// pointer makes the normalization impossible to forget at a call site.
// Error discipline is preserved from the old getCard helper: ErrNotFound→404,
// ambiguous→*AmbiguousIDError (code "ambiguous", 409), and every OTHER store
// error stays 500-class (previously all store errors were masked as 404,
// hiding real failures — do not regress this).
func (s *Service) resolveForWrite(ctx context.Context, ref *string) (*Card, error) {
	c, err := s.ResolveCard(ctx, *ref)
	if err != nil {
		return nil, err
	}
	*ref = c.ID
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
		StatusSince:   now, // entering its initial status now (seam 3d)
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
	s.emitter.dispatch([]*Event{ev})
	// A card can land directly in a capped or watched column (e.g. created
	// straight into an over-limit status) — evaluate it the same as any other
	// column-changing mutation (3c closes this CreateCard gap).
	s.evaluateColumn(ctx, s.boardForCard(c), c.Status)
	return c, nil
}

// PatchCard applies a partial update with optimistic concurrency. SPEC §11.
func (s *Service) PatchCard(ctx context.Context, id string, req PatchCardRequest) (*Card, error) {
	current, err := s.resolveForWrite(ctx, &id)
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
			if ok && !Contains(allowed, newStatus) {
				// Opt-in: TakeNext never reaches this branch (it pre-filters
				// candidates to legal from-statuses), so only a genuinely
				// attempted-and-refused PatchCard move fires it. (3c)
				if b.Monitors != nil && b.Monitors.EmitRejections {
					ev := TransitionRejected(id, current.Status, newStatus, b.ID)
					if err := s.emitter.Condition(ctx, ev); err != nil {
						log.Printf("ERROR: escalated condition append failed (type=%s card=%s): %v", ev.Type, id, err)
					}
				}
				return nil, newTransitionIllegal(current.Status, allowed)
			}
		}
		events = append(events, StatusChanged(id, current.Status, newStatus))
		next.Status = newStatus
		next.StatusSince = now // entering the new status now (seam 3d)
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
			// Multi-value unset contract: patching a multiple field to null or
			// [] removes the key (absent on the wire, never null/[]), recorded
			// as a change to nil. Scalar fields are untouched by this.
			if def := fieldDef(ct, k); def != nil {
				if _, unset := normalizeMultiple(def, v); unset {
					if _, had := merged[k]; had {
						delete(merged, k)
						changed = append(changed, fieldChange{field: k, before: before, after: nil})
					}
					continue
				}
			}
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
	// After the status change commits, re-check both columns: the destination
	// may now exceed its limit or fill a watched empty lane, the source may
	// have cleared its limit or emptied one. (3a/3c) A status change is also
	// the only thing that can flip a dependent's blocked state (e.g. reaching
	// or leaving "done") — re-check every card that depends on this one. (3c)
	if next.Status != current.Status {
		b := s.boardForCard(&next)
		s.evaluateColumn(ctx, b, next.Status)
		s.evaluateColumn(ctx, b, current.Status)
		s.reevaluateDependents(ctx, next.ID)
	}
	return &next, nil
}

// DeleteCard removes a card, appending a card_deleted tombstone to the
// append-only log. The id may be a full or short id (resolved like GET/PATCH).
// A non-zero req.Version is an optimistic-concurrency guard. Returns the
// deleted card (its last state) so callers can confirm what was removed.
func (s *Service) DeleteCard(ctx context.Context, id string, req DeleteCardRequest) (*Card, error) {
	if req.Actor == "" {
		return nil, ActorRequired()
	}
	current, err := s.resolveForWrite(ctx, &id)
	if err != nil {
		return nil, err
	}
	if req.Version != 0 && req.Version != current.Version {
		return nil, VersionConflict(current)
	}
	ctx = WithActor(ctx, req.Actor)
	// Capture the cards this one blocks BEFORE deleting — DeleteCard removes the
	// inbound links, so BlockingDependents would return nothing afterward.
	deps, _ := s.store.BlockingDependents(ctx, current.ID)
	ev := CardDeleted(current)
	ev.Actor = req.Actor
	ev.At = s.now()
	if err := s.store.DeleteCard(ctx, current.ID, ev); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, NotFound("card " + id)
		}
		return nil, Internal("failed to delete card: " + err.Error())
	}
	// A deleted card can unblock its dependents (its blocking links are gone),
	// so re-evaluate each for card_unblocked — same as moving it to done would.
	for _, dep := range deps {
		s.evaluateBlocked(ctx, dep)
	}
	return current, nil
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
	current, err := s.resolveForWrite(ctx, &id)
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
	current, err := s.resolveForWrite(ctx, &id)
	if err != nil {
		return nil, err
	}
	if version <= 0 {
		// Same shape as RemoveEntry: an omitted version is a caller bug and
		// says so, not a version_conflict masquerading as a race.
		return nil, NewValidationError("version", "version is required for entry append")
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
	current, err := s.resolveForWrite(ctx, &id)
	if err != nil {
		return nil, err
	}
	if version <= 0 {
		return nil, NewValidationError("version", "version is required for entry update")
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
	current, err := s.resolveForWrite(ctx, &id)
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
	current, err := s.resolveForWrite(ctx, &id)
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
	// Target must exist. Resolved like the subject: full id or short id, with
	// in.Target normalized to the full id before it reaches the link row/event.
	target, err := s.resolveForWrite(ctx, &in.Target)
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
	// Persist the link row for graph queries. The mutation is already durable
	// (card JSON + event committed above), so a failure here means graph-table
	// drift, not a failed request — log it loudly instead of failing a commit
	// the caller would then wrongly retry.
	if err := s.store.InsertLink(ctx, id, l); err != nil {
		log.Printf("ERROR: links table drift: insert %s -> %s (%s) on card %s: %v", id, in.Target, in.TypeID, id, err)
	}
	// A new link may have just blocked this card (store.Blockers reads the
	// links table, so this must run after InsertLink). (3c)
	s.evaluateBlocked(ctx, id)
	return &next, nil
}

// RemoveLink deletes a link by (type_id, target). SPEC §11.
func (s *Service) RemoveLink(ctx context.Context, id, typeID, target string) (*Card, error) {
	current, err := s.resolveForWrite(ctx, &id)
	if err != nil {
		return nil, err
	}
	if ctxActor(ctx) == "" {
		return nil, ActorRequired()
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
	// See AddLink: post-commit graph-table write — log drift, don't fail.
	if _, err := s.store.DeleteLink(ctx, id, typeID, target); err != nil {
		log.Printf("ERROR: links table drift: delete %s/%s on card %s: %v", typeID, target, id, err)
	}
	// Removing a blocking link may have just unblocked this card. (3c)
	s.evaluateBlocked(ctx, id)
	return &next, nil
}

// AddComment adds a comment; returns the updated card. SPEC §11.
func (s *Service) AddComment(ctx context.Context, id string, body string) (*Card, error) {
	current, err := s.resolveForWrite(ctx, &id)
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
	// See AddLink: post-commit denormalized write — log drift, don't fail.
	if err := s.store.InsertComment(ctx, id, c); err != nil {
		log.Printf("ERROR: comments table drift: insert %s on card %s: %v", c.ID, id, err)
	}
	return &next, nil
}

// AddArtifact stores bytes for an artifact field (artifact_policy: local) and
// records the resulting metadata on the card, emitting artifact_added. It
// mirrors AddLink/AddComment: resolve + actor + validate, then commit the card
// mutation and the event together via commitCard. The Manager enforces
// content-addressing and path confinement; a "uri"-policy field takes an
// external URI via patch, not an upload, and is rejected here.
//
// version is an OPTIONAL optimistic-concurrency guard, mirroring DeleteCard:
// version==0 proceeds against the current card (so a simple `cards attach <id>
// <file>` still works); a non-zero version that does not match the card is
// rejected with version_conflict before any bytes are published.
//
// Ordering matters for durability: the bytes are STAGED (written to a temp
// file, metadata computed) but only published to the store after commitCard
// succeeds. A stale version, a lost CAS race, or any store error therefore
// discards the staged bytes instead of leaking an orphan blob.
func (s *Service) AddArtifact(ctx context.Context, id, field string, r io.Reader, version int) (*Card, error) {
	if s.artifacts == nil {
		return nil, NewValidationError("artifact", "artifact storage is not configured for this workspace")
	}
	current, err := s.resolveForWrite(ctx, &id)
	if err != nil {
		return nil, err
	}
	if version != 0 && version != current.Version {
		return nil, VersionConflict(current)
	}
	actor := ctxActor(ctx)
	if actor == "" {
		return nil, ActorRequired()
	}
	fd, err := s.findField(s.types[current.TypeID], field)
	if err != nil {
		return nil, err
	}
	if fd.Type != FieldArtifact {
		return nil, NewValidationError(field, fmt.Sprintf("field %q is not an artifact field", field))
	}
	if fd.ArtifactPolicy == "uri" {
		return nil, NewValidationError(field, fmt.Sprintf("field %q uses artifact_policy: uri; set the uri via patch, not upload", field))
	}
	// Stage the bytes (temp file + metadata) but do not publish them yet.
	staged, err := s.artifacts.Stage(r)
	if err != nil {
		return nil, fmt.Errorf("store artifact: %w", err)
	}
	meta := staged.Meta()
	next := *current
	next.Fields = setField(current.Fields, field, map[string]any{
		"uri": meta.URI, "mime": meta.MIME, "size": meta.Size, "sha256": meta.SHA256,
	})
	next.Version = current.Version + 1
	next.UpdatedAt = s.now()
	ev := ArtifactAdded(id, field, meta.URI, meta.MIME, meta.Size, meta.SHA256)
	if err := s.commitCard(ctx, &next, []*Event{ev}); err != nil {
		// The card write lost (stale version / CAS race / store error): drop
		// the staged bytes so a failed attach never orphans a blob.
		_ = staged.Discard()
		return nil, err
	}
	// Card committed — publish the bytes, then confinement-sanity-check the URI
	// (the same check the serve path applies).
	if err := staged.Commit(); err != nil {
		return nil, fmt.Errorf("store artifact: %w", err)
	}
	if _, err := s.artifacts.Resolve(meta.URI); err != nil {
		return nil, fmt.Errorf("store artifact: %w", err)
	}
	return &next, nil
}

// OpenArtifact opens stored artifact bytes by URI, enforcing the local-policy
// path confinement (artifacts.Manager.Resolve): a relative URI that stays inside
// the artifacts root, symlinks included.
func (s *Service) OpenArtifact(uri string) (io.ReadCloser, error) {
	if s.artifacts == nil {
		return nil, NewValidationError("artifact", "artifact storage is not configured for this workspace")
	}
	return s.artifacts.Open(uri)
}

// EditComment updates a comment's body. SPEC §11.
func (s *Service) EditComment(ctx context.Context, id, commentID, body string) (*Card, error) {
	current, err := s.resolveForWrite(ctx, &id)
	if err != nil {
		return nil, err
	}
	if ctxActor(ctx) == "" {
		return nil, ActorRequired()
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
	// See AddLink: post-commit denormalized write — log drift, don't fail.
	if err := s.store.UpdateComment(ctx, id, commentID, body, next.UpdatedAt); err != nil {
		log.Printf("ERROR: comments table drift: update %s on card %s: %v", commentID, id, err)
	}
	return &next, nil
}

// Claim atomically sets owner (+optional status) via compare-and-set. SPEC §11.
// Returns version_conflict (409) if the card is already owned by another actor.
func (s *Service) Claim(ctx context.Context, id string, req ClaimRequest) (*Card, error) {
	current, err := s.resolveForWrite(ctx, &id)
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
	current, err := s.resolveForWrite(ctx, &id)
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
	c, evs, err := claimWithRetry(3, func() (*Card, []*Event, error) {
		return s.store.ClaimAtomic(ctx, q, assignTo, req.Status, actor, s.now())
	})
	if err != nil {
		return nil, err
	}
	s.emitter.dispatch(evs)
	// A claim can move the card's status (ClaimAtomic emits status_changed
	// alongside owner_changed when it does) — evaluate both the entered and
	// left columns the same as PatchCard/CreateCard (3c closes this TakeNext
	// gap). A claim that only changes owner leaves every column's membership
	// unchanged, so no evaluation is needed.
	if c != nil {
		for _, ev := range evs {
			diff, ok := ev.Diff.(BeforeAfterDiff)
			if ev.Type != EventStatusChanged || !ok {
				continue
			}
			b := s.boardForCard(c)
			s.evaluateColumn(ctx, b, diff.After)
			s.evaluateColumn(ctx, b, diff.Before)
			s.reevaluateDependents(ctx, c.ID)
			break
		}
	}
	return c, nil // nil card → caller renders {card:null}
}

// claimWithRetry re-invokes claim on a raced CAS (ErrClaimRaced), up to
// attempts times. A fresh transaction sees the racer's already-committed
// claim and naturally selects the next candidate — no need to track or
// exclude any ids across attempts. Exhausting every attempt while still
// racing returns (nil, nil, nil): the same "nothing claimable right now"
// result as an empty pool, not an error — TakeNext renders {card:null}
// either way rather than surfacing a raced-out attempt to the caller.
func claimWithRetry(attempts int, claim func() (*Card, []*Event, error)) (*Card, []*Event, error) {
	for range attempts {
		c, evs, err := claim()
		if errors.Is(err, ErrClaimRaced) {
			continue
		}
		return c, evs, err
	}
	return nil, nil, nil
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
// The reference may be a short id; it is normalized to the full id before the
// event query (events are keyed by full card id).
func (s *Service) History(ctx context.Context, id string) ([]HistoryEntry, error) {
	if _, err := s.resolveForWrite(ctx, &id); err != nil {
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
