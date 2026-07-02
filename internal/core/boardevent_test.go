package core_test

import (
	"context"
	"testing"

	"github.com/somebox/cards/internal/core"
)

// A board-scoped event round-trips end-to-end (Events seam 2b): Emit -> store ->
// bus -> feed, with board_id set and card_id omitted, and card-scoped
// subscribers/consumers are unaffected.
func TestBoardEvent_EndToEnd(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(context.Background(), "u")
	const boardFact core.EventType = "board_fact"

	boardSub := svc.Bus().Subscribe(core.EventFilter{BoardID: "b1"}, 8)
	cardSub := svc.Bus().Subscribe(core.EventFilter{CardID: "c1"}, 8)
	defer svc.Bus().Unsubscribe(boardSub.ID)
	defer svc.Bus().Unsubscribe(cardSub.ID)

	if err := svc.Emitter().Emit(ctx, core.BoardEvent("b1", boardFact, map[string]any{"limit": 3})); err != nil {
		t.Fatalf("emit board event: %v", err)
	}

	// Emit -> bus: the board subscriber receives it; a card subscriber does not.
	bgot := drain(boardSub.Ch)
	if len(bgot) != 1 {
		t.Fatalf("board subscriber got %d events, want 1", len(bgot))
	}
	if bgot[0].BoardID != "b1" || bgot[0].CardID != "" || bgot[0].Scope != "board" {
		t.Errorf("bus event = %+v, want board_id=b1 card_id='' scope=board", bgot[0])
	}
	if cgot := drain(cardSub.Ch); len(cgot) != 0 {
		t.Errorf("card-filtered subscriber wrongly received %d board events", len(cgot))
	}

	// Emit -> store -> feed: it persisted (card_id NULL) and reads back.
	feed, err := svc.ListEvents(ctx, core.EventQuery{Limit: 10})
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	var found *core.Event
	for i := range feed {
		if feed[i].Scope == "board" {
			found = &feed[i]
		}
	}
	if found == nil {
		t.Fatal("board event not found in the feed")
	}
	if found.BoardID != "b1" || found.CardID != "" || found.Type != boardFact {
		t.Errorf("persisted board event = %+v, want board_id=b1 card_id='' type=board_fact", found)
	}
}
