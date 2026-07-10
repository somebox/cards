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
// events with the same code.
type BreachItem struct {
	Type     EventType `json:"type"`
	Scope    string    `json:"scope"` // "board" | "card"
	BoardID  string    `json:"board_id,omitempty"`
	CardID   string    `json:"card_id,omitempty"`
	Column   string    `json:"column,omitempty"`
	Count    int       `json:"count,omitempty"`
	Limit    int       `json:"limit,omitempty"`
	Blockers []string  `json:"blockers,omitempty"`
}

// BreachReport is the GET /v1/breaches response.
type BreachReport struct {
	AsOf  time.Time    `json:"as_of"`
	Items []BreachItem `json:"items"`
}

// Breaches computes the currently-true instant conditions across boards (or
// one board, if boardID is set), optionally filtered to the given event
// types. It shares countColumn (WIP/lane) and store.Blockers (blocked) with
// the live crossing evaluators in service.go — one counting path, not a
// second one built for catch-up.
//
// Caveat (same as evaluateColumn, EVENTS.md §12 Step 3c): membership is
// TypeIDIn only; a board scoped by DefaultFilter instead is not counted
// correctly. Temporal conditions (status_timeout/card_idle) are not included
// yet — see seam 3e.
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
		q := CardQuery{Blocked: true, Limit: 500}
		if boardID != "" {
			q.TypeIDIn = boardList[0].CardTypeIDs
		}
		page, err := s.store.ListCards(ctx, q)
		if err == nil {
			for _, c := range page.Items {
				blockers, err := s.store.Blockers(ctx, c.ID)
				if err != nil {
					continue
				}
				items = append(items, BreachItem{Type: EventCardBlocked, Scope: "card", CardID: c.ID, Blockers: blockers})
			}
		}
	}
	return &BreachReport{AsOf: s.now(), Items: items}, nil
}
