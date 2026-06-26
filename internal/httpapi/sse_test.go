package httpapi_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/foz/work-cards/internal/core"
)

// readSSEEvents reads text/event-stream lines until n data lines arrive or timeout.
func readSSEEvents(t *testing.T, url string, headers map[string]string, n int) []string {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	var out []string
	deadline := time.Now().Add(3 * time.Second)
	for scanner.Scan() && len(out) < n && time.Now().Before(deadline) {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			out = append(out, strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}

// TestSSELiveStream verifies that an SSE subscriber receives a live event
// published after subscription.
func TestSSELiveStream(t *testing.T) {
	ts, svc := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}

	// Subscribe in a goroutine; collect events.
	got := make(chan []string, 1)
	go func() {
		evs := readSSEEvents(t, ts.URL+"/v1/events/stream?types=status_changed", nil, 1)
		got <- evs
	}()
	time.Sleep(200 * time.Millisecond) // let the subscriber connect

	// Make a mutation that publishes a status_changed event.
	_, created := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "SSE test", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, H)
	id := created["id"].(string)
	do(t, ts, "PATCH", "/v1/cards/"+id, map[string]any{"version": 1, "status": "in_progress"}, H)

	select {
	case evs := <-got:
		if len(evs) == 0 {
			t.Fatal("no events received")
		}
		if !strings.Contains(evs[0], "status_changed") {
			t.Errorf("event = %s, want status_changed", evs[0])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SSE event")
	}
	_ = svc
}

// TestSSEReplayWithLastEventID verifies that a reconnect with Last-Event-ID
// replays events after that id.
func TestSSEReplayWithLastEventID(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}

	// Create + patch to generate events.
	_, created := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "Replay test", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, H)
	id := created["id"].(string)
	// status_changed to in_progress (event E1)
	_, _ = do(t, ts, "PATCH", "/v1/cards/"+id, map[string]any{"version": 1, "status": "in_progress"}, H)
	// Find E1's id from the events endpoint.
	_, evResp := do(t, ts, "GET", "/v1/cards/"+id+"/events?types=status_changed", nil, nil)
	events := evResp["items"].([]any)
	if len(events) == 0 {
		t.Fatal("no status_changed events")
	}
	e1ID := int64(events[0].(map[string]any)["id"].(float64))

	// Now make a second status change.
	_, _ = do(t, ts, "PATCH", "/v1/cards/"+id, map[string]any{"version": 2, "status": "review"}, H)

	// Replay from E1: should get the review event (id > e1ID).
	evs := readSSEEvents(t, ts.URL+"/v1/events/stream?types=status_changed",
		map[string]string{"Last-Event-ID": itoa(e1ID)}, 1)
	if len(evs) == 0 {
		t.Fatal("replay returned no events")
	}
	if !strings.Contains(evs[0], `"after":"review"`) {
		t.Errorf("replay event = %s, want after=review", evs[0])
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// keep context imported for future use
var _ = context.Background
