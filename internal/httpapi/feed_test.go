package httpapi_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

func TestEventFeed(t *testing.T) {
	ts, _ := newServer(t)

	// The HTTP layer resolves the actor from the X-Work-Cards-Actor header
	// (the body Actor field is ignored), so set it per create.
	mk := func(actor, title string) string {
		_, out := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
			TypeID: "programming-task", Title: title, Status: "todo",
			Fields: map[string]any{"description": "d", "branch": "b"},
		}, map[string]string{"X-Work-Cards-Actor": actor})
		return out["id"].(string)
	}

	// Generate a known set of card_created events from two actors.
	mk("alice", "a1")
	mk("bob", "b1")
	aliceCard := mk("alice", "a2")

	get := func(query string) (*http.Response, map[string]any) {
		return do(t, ts, "GET", "/v1/events"+query, nil, nil)
	}
	itemsOf := func(out map[string]any) []map[string]any {
		raw, _ := out["items"].([]any)
		items := make([]map[string]any, 0, len(raw))
		for _, r := range raw {
			items = append(items, r.(map[string]any))
		}
		return items
	}

	// Full feed: at least our three creates, ordered by id ASC.
	resp, out := get("")
	if resp.StatusCode != 200 {
		t.Fatalf("feed status %d", resp.StatusCode)
	}
	all := itemsOf(out)
	if len(all) < 3 {
		t.Fatalf("expected >=3 events, got %d", len(all))
	}
	var lastID float64
	for _, e := range all {
		id := e["id"].(float64)
		if id <= lastID {
			t.Fatalf("events not ascending by id: %v then %v", lastID, id)
		}
		lastID = id
	}

	// actor= filter: only alice's events (she created two cards).
	_, out = get("?actor=alice&type=card_created")
	aliceEvents := itemsOf(out)
	if len(aliceEvents) != 2 {
		t.Fatalf("alice card_created = %d, want 2", len(aliceEvents))
	}
	for _, e := range aliceEvents {
		if e["actor"].(string) != "alice" {
			t.Errorf("actor filter leaked: %v", e["actor"])
		}
	}

	// owner= filter: claim alice's second card as worker-1 (the claimer becomes
	// the owner), then its events should appear under owner=worker-1. The owner
	// must be a registered user, so register it first.
	do(t, ts, "POST", "/v1/users", map[string]any{"id": "worker-1", "kind": "agent"}, nil)
	_, claimed := do(t, ts, "POST", "/v1/cards/"+aliceCard+"/claim",
		map[string]any{"version": 1}, map[string]string{"X-Work-Cards-Actor": "worker-1"})
	if claimed["owner"] != "worker-1" {
		t.Fatalf("claim failed: %v", claimed)
	}
	_, out = get("?owner=worker-1")
	ownerEvents := itemsOf(out)
	if len(ownerEvents) == 0 {
		t.Fatalf("owner=worker-1 returned no events")
	}
	for _, e := range ownerEvents {
		if e["card_id"].(string) != aliceCard {
			t.Errorf("owner filter leaked card %v", e["card_id"])
		}
	}
}

func TestEventFeedPaginationAndCursor(t *testing.T) {
	ts, _ := newServer(t)
	for i := 0; i < 5; i++ {
		do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
			TypeID: "programming-task", Title: "p" + strconv.Itoa(i), Status: "todo",
			Fields: map[string]any{"description": "d", "branch": "b"}, Actor: "alice",
		}, nil)
	}

	// limit=2 over card_created events: expect a next_cursor and exactly 2 items.
	resp, out := do(t, ts, "GET", "/v1/events?type=card_created&limit=2", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	page1, _ := out["items"].([]any)
	if len(page1) != 2 {
		t.Fatalf("page1 = %d items, want 2", len(page1))
	}
	cursor, _ := out["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("expected next_cursor on a truncated page")
	}

	// Page 2 via cursor=: ids strictly greater than the page-1 tail.
	page1LastID := page1[1].(map[string]any)["id"].(float64)
	_, out2 := do(t, ts, "GET", "/v1/events?type=card_created&limit=2&cursor="+cursor, nil, nil)
	page2, _ := out2["items"].([]any)
	if len(page2) == 0 {
		t.Fatalf("page2 empty")
	}
	if got := page2[0].(map[string]any)["id"].(float64); got <= page1LastID {
		t.Fatalf("cursor did not advance: page2 first id %v <= page1 last %v", got, page1LastID)
	}
}

// TestSSEActorFilter verifies the live stream honors ?actor= — a subscriber
// watching alice's events does not receive bob's.
func TestSSEActorFilter(t *testing.T) {
	ts, _ := newServer(t)

	got := make(chan []string, 1)
	go func() {
		got <- readSSEEvents(t, ts.URL+"/v1/events/stream?actor=alice&types=card_created", nil, 1)
	}()
	time.Sleep(200 * time.Millisecond)

	// bob's event must be filtered out; alice's must arrive.
	do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "by bob", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, map[string]string{"X-Work-Cards-Actor": "bob"})
	do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "by alice", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, map[string]string{"X-Work-Cards-Actor": "alice"})

	select {
	case evs := <-got:
		if len(evs) == 0 {
			t.Fatal("no events received")
		}
		if !strings.Contains(evs[0], `"actor":"alice"`) {
			t.Errorf("expected alice's event, got %s", evs[0])
		}
		if strings.Contains(evs[0], `"actor":"bob"`) {
			t.Errorf("bob's event leaked through actor filter: %s", evs[0])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for filtered SSE event")
	}
}

func TestEventFeedInvalidCursor(t *testing.T) {
	ts, _ := newServer(t)
	resp, out := do(t, ts, "GET", "/v1/events?cursor=abc", nil, nil)
	if resp.StatusCode != 400 && resp.StatusCode != 422 {
		t.Fatalf("invalid cursor status %d body %v", resp.StatusCode, out)
	}
}

func TestEventFeedUnknownBoard(t *testing.T) {
	ts, _ := newServer(t)
	resp, _ := do(t, ts, "GET", "/v1/events?board_id=does-not-exist", nil, nil)
	if resp.StatusCode != 400 && resp.StatusCode != 422 {
		t.Fatalf("unknown board status %d", resp.StatusCode)
	}
}
