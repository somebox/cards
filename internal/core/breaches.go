// Package core — breaches.go
//
// GET /v1/breaches (INTEGRATION.md): the catch-up counterpart to the ephemeral
// instant-condition signals (wip_exceeded, lane_drained, card_blocked) — a
// consumer that wasn't listening when one last crossed can ask "what's
// breaching right now?" instead of reconstructing state from the feed.
// Computed on demand: no persistence, no cache, no pagination.
package core

import (
	"context"
	"sort"
	"time"
)

// BreachItem is one currently-true condition, its fields mirroring the
// corresponding event's Diff so a client parses catch-up state and live
// events with the same code. Which optional fields are populated depends on
// Type (docs/spec/api-surface.md has the Type→fields table):
// wip_exceeded/lane_limit → column/count/limit; card_blocked → blockers (a
// nested array); status_timeout → status/since/max; card_idle →
// since/threshold (flat scalars).
type BreachItem struct {
	Type      EventType  `json:"type"`
	Scope     string     `json:"scope"` // "board" | "card"
	BoardID   string     `json:"board_id,omitempty"`
	CardID    string     `json:"card_id,omitempty"`
	Column    string     `json:"column,omitempty"`
	Count     int        `json:"count,omitempty"`
	Limit     int        `json:"limit,omitempty"`
	Blockers  []string   `json:"blockers,omitempty"`
	Status    string     `json:"status,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	Max       string     `json:"max,omitempty"`
	Threshold string     `json:"threshold,omitempty"`
}

// BreachReport is the GET /v1/breaches response. Limit echoes the card-scan
// ceiling applied to item scans (blocked + temporal); Truncated marks that a
// scan hit it — the report is then a partial view, never a "complete"
// catch-up. WIP/lane counts are unaffected (uncapped CountCards).
type BreachReport struct {
	AsOf      time.Time    `json:"as_of"`
	Items     []BreachItem `json:"items"`
	Limit     int          `json:"limit,omitempty"`
	Truncated bool         `json:"truncated,omitempty"`
}

// breachScanLimit caps each card item scan (blocked + temporal), matching
// the ListCards ceiling. The unclamped deep-wash path is a separate
// candidate (census B), not this seam.
const breachScanLimit = 500

// Breaches computes the currently-true instant conditions across boards (or
// one board, if boardID is set), optionally filtered to the given event
// types. It shares countColumn (WIP/lane) and store.Blockers (blocked) with
// the live crossing evaluators in service.go — one counting path, not a
// second one built for catch-up.
//
// Temporal conditions (status_timeout/card_idle, 3e) are projected cold:
// the same deadline math as the rebuild/verify pair
// (statusTimeoutDeadline/cardIdleDeadline), filtered to At <= now, against
// live monitor config — never arming, never marking conditions fired; the
// scheduler alone owns firing.
//
// Caveat (same as evaluateColumn, EVENTS.md §12 Step 3c): membership is
// TypeIDIn only; a board scoped by DefaultFilter instead is not counted
// correctly.
func (s *Service) Breaches(ctx context.Context, boardID string, types []string) (*BreachReport, error) {
	var boardList []*Board
	if boardID != "" {
		b, ok := s.boards[boardID]
		if !ok {
			return nil, NotFound("board " + boardID)
		}
		boardList = []*Board{b}
	} else {
		ids := make([]string, 0, len(s.boards))
		for id := range s.boards {
			ids = append(ids, id)
		}
		sort.Strings(ids) // deterministic order; s.boards iteration isn't
		for _, id := range ids {
			boardList = append(boardList, s.boards[id])
		}
	}

	wantAll := len(types) == 0
	want := map[string]bool{}
	for _, t := range types {
		want[t] = true
	}
	include := func(t EventType) bool { return wantAll || want[string(t)] }

	now := s.now()
	// scanRan/truncated track only the card item scans (blocked + temporal,
	// ListCards-capped); WIP/lane counts are uncapped CountCards and never
	// truncate.
	var scanRan, truncated bool

	items := []BreachItem{}
	for _, b := range boardList {
		for _, column := range b.Columns {
			limit, hasLimit := b.WIPLimits[column]
			watchEmpty := b.Monitors != nil && Contains(b.Monitors.AlertWhenEmpty, column)
			if !hasLimit && !watchEmpty {
				continue
			}
			count, err := s.countColumn(ctx, b, column)
			if err != nil {
				continue
			}
			if hasLimit && limit > 0 && count > limit && include(EventWIPExceeded) {
				items = append(items, BreachItem{Type: EventWIPExceeded, Scope: "board", BoardID: b.ID, Column: column, Count: count, Limit: limit})
			}
			if watchEmpty && count == 0 && include(EventLaneDrained) {
				items = append(items, BreachItem{Type: EventLaneDrained, Scope: "board", BoardID: b.ID, Column: column, Count: count})
			}
		}
	}

	// card_blocked/card_unblocked are card-scoped, not board-scoped — the live
	// event carries no board_id (CardEvent, not BoardEvent). Computed once
	// per call, not once per board, so a card whose type is shared across
	// boards (boardForTypeID's whole reason for existing) isn't double-
	// reported. A requested board_id narrows by that board's card types
	// without attributing the item to it.
	if include(EventCardBlocked) {
		scanRan = true
		q := CardQuery{Blocked: true, Limit: breachScanLimit}
		if boardID != "" {
			q.TypeIDIn = boardList[0].CardTypeIDs
		}
		page, err := s.store.ListCards(ctx, q)
		if err == nil {
			truncated = truncated || page.NextCursor != ""
			for _, c := range page.Items {
				blockers, err := s.store.Blockers(ctx, c.ID)
				if err != nil {
					continue
				}
				items = append(items, BreachItem{Type: EventCardBlocked, Scope: "card", CardID: c.ID, Blockers: blockers})
			}
		}
	}

	// status_timeout (3e): a card is past due when it still sits in a
	// monitored status past StatusSince+max. The scan is its own verify —
	// cards come fresh from the store and the monitor config is read live,
	// so the identity key holds by construction; golden rule: the projected
	// set equals what verify would emit if every deadline came due now.
	if include(EventStatusTimeout) {
		scanRan = true
		seen := map[string]bool{}
		for _, b := range boardList {
			if b.Monitors == nil || len(b.Monitors.MaxTimeInStatus) == 0 {
				continue
			}
			for status, maxStr := range b.Monitors.MaxTimeInStatus {
				max, err := ParseMonitorDuration(maxStr)
				if err != nil {
					continue // validated at config load; defensive
				}
				page, err := s.store.ListCards(ctx, CardQuery{Status: status, TypeIDIn: b.CardTypeIDs, Limit: breachScanLimit})
				if err != nil {
					continue
				}
				truncated = truncated || page.NextCursor != ""
				for _, c := range page.Items {
					if seen[c.ID] {
						continue // type shared across boards monitoring the same status
					}
					if c.StatusSince.IsZero() || statusTimeoutDeadline(c, max).After(now) {
						continue // unbackfilled row, or not yet due
					}
					seen[c.ID] = true
					since := c.StatusSince
					items = append(items, BreachItem{Type: EventStatusTimeout, Scope: "card", CardID: c.ID, Status: c.Status, Since: &since, Max: maxStr})
				}
			}
		}
	}

	// card_idle (3e): a card is past due when no mutation has touched it for
	// idle_after. Idle isn't board-scoped; a card whose type is monitored by
	// more than one board is reported once (same seen-dedupe as
	// rebuildCardIdle).
	if include(EventCardIdle) {
		scanRan = true
		seen := map[string]bool{}
		for _, b := range boardList {
			if b.Monitors == nil || b.Monitors.IdleAfter == "" {
				continue
			}
			idleAfter, err := ParseMonitorDuration(b.Monitors.IdleAfter)
			if err != nil {
				continue
			}
			page, err := s.store.ListCards(ctx, CardQuery{TypeIDIn: b.CardTypeIDs, Limit: breachScanLimit})
			if err != nil {
				continue
			}
			truncated = truncated || page.NextCursor != ""
			for _, c := range page.Items {
				if seen[c.ID] {
					continue
				}
				if cardIdleDeadline(c, idleAfter).After(now) {
					continue // not yet due
				}
				seen[c.ID] = true
				since := c.UpdatedAt
				items = append(items, BreachItem{Type: EventCardIdle, Scope: "card", CardID: c.ID, Since: &since, Threshold: b.Monitors.IdleAfter})
			}
		}
	}

	rep := &BreachReport{AsOf: now, Items: items}
	if scanRan {
		rep.Limit = breachScanLimit
		rep.Truncated = truncated
	}
	return rep, nil
}
