package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/somebox/cards/internal/core"
)

// Supports Last-Event-ID (and since=) for resumable replay. SPEC §3/§11 D11.
func (s *Server) apiEventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	cardID := r.URL.Query().Get("card_id")
	types := splitCSV(r.URL.Query().Get("types"))
	boardID := r.URL.Query().Get("board_id")
	actor := r.URL.Query().Get("actor")
	owner := r.URL.Query().Get("owner")

	// Validate Last-Event-ID / since before committing the SSE response —
	// an invalid value should be a 400, not silently treated as 0.
	var afterID int64
	var leidRaw string
	if leid := r.Header.Get("Last-Event-ID"); leid != "" {
		leidRaw = leid
	} else if since := r.URL.Query().Get("since"); since != "" {
		leidRaw = since
	}
	if leidRaw != "" {
		n, ok := parseEventID(leidRaw)
		if !ok {
			writeAPIError(w, core.NewValidationError("last_event_id", "invalid Last-Event-ID (or since=): must be a positive integer"))
			return
		}
		afterID = n
	}

	// SSE headers.
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // disable proxy buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Replay: events after Last-Event-ID (or since=) matching the filter.
	if afterID > 0 {
		evs, err := s.svc.ListEvents(r.Context(), core.EventQuery{CardID: cardID, Types: types, Actor: actor, Owner: owner, AfterID: afterID, Limit: 500})
		if err != nil {
			// Headers are already written (200); surface the gap as an SSE
			// comment so the client knows replay is incomplete and can
			// reconnect with Last-Event-ID rather than silently missing events.
			log.Printf("ERROR: SSE replay after id %d failed: %v", afterID, err)
			fmt.Fprint(w, ": replay failed, reconnect to retry\n\n")
		}
		for _, e := range filterBoardEvents(s, evs, boardID) {
			writeSSEEvent(w, &e)
		}
		flusher.Flush()
	}

	// Live: subscribe to the bus.
	filter := core.EventFilter{CardID: cardID, Types: types, Actor: actor}
	sub := s.svc.Bus().Subscribe(filter, 64)
	defer s.svc.Bus().Unsubscribe(sub.ID)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.Ch:
			if !ok {
				// Dropped (slow consumer). Send a comment and resubscribe so the
				// client can decide to reconnect with Last-Event-ID.
				w.Write([]byte(": dropped, reconnect\n\n"))
				flusher.Flush()
				return
			}
			// A board's feed = board-scoped facts for that board (board_id match)
			// OR card events whose card is in the board (type membership). (2c)
			if boardID != "" && e.BoardID != boardID && !s.cardInBoard(e.CardID, boardID) {
				continue
			}
			if owner != "" && !s.cardOwnedBy(e.CardID, owner) {
				continue
			}
			writeSSEEvent(w, e)
			flusher.Flush()
		}
	}
}

// writeSSEEvent writes one event in SSE wire format.
func writeSSEEvent(w io.Writer, e *core.Event) {
	m := map[string]any{
		"id": e.ID, "type": e.Type, "card_id": e.CardID, "actor": e.Actor, "at": e.At, "diff": e.Diff,
	}
	if e.BoardID != "" { // board-scoped facts (seam 2c); omitted for card events
		m["board_id"] = e.BoardID
		m["scope"] = "board"
	}
	payload, err := json.Marshal(m)
	if err != nil {
		// The value set is closed (ids, strings, times, JSON-decoded diffs), so
		// this is unreachable in practice; if it ever fires, drop the event
		// loudly rather than emit a malformed SSE frame.
		log.Printf("ERROR: SSE marshal event %d (%s): %v", e.ID, e.Type, err)
		return
	}
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, payload)
}

// filterBoardEvents keeps only events whose card belongs to the board (POC:
// board membership = card's type is in the board's card_type_ids).
func filterBoardEvents(s *Server, evs []core.Event, boardID string) []core.Event {
	if boardID == "" {
		return evs
	}
	out := make([]core.Event, 0, len(evs))
	for _, e := range evs {
		if e.BoardID == boardID || s.cardInBoard(e.CardID, boardID) {
			out = append(out, e)
		}
	}
	return out
}

// cardInBoard reports whether the card's type is in the board's card_type_ids.
func (s *Server) cardInBoard(cardID, boardID string) bool {
	b := s.boards[boardID]
	if b == nil {
		return false
	}
	c, err := s.svc.GetCard(context.Background(), cardID)
	if err != nil {
		return false
	}
	for _, t := range b.CardTypeIDs {
		if t == c.TypeID {
			return true
		}
	}
	return false
}

// cardOwnedBy reports whether the card is currently owned by owner. Used to
// filter the live SSE stream by owner (the feed does this in SQL).
func (s *Server) cardOwnedBy(cardID, owner string) bool {
	c, err := s.svc.GetCard(context.Background(), cardID)
	if err != nil {
		return false
	}
	return c.Owner == owner
}
