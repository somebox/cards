package httpapi_test

import (
	"net/url"
	"testing"
)

// GET /v1/breaches reports the engineering board's current in_progress WIP
// state without requiring a live subscriber to have seen the crossing.
func TestAPIBreaches_WIPExceeded(t *testing.T) {
	ts, _ := newServer(t)

	_, listBefore := do(t, ts, "GET", "/v1/cards?board_id=engineering&status=in_progress&limit=200", nil, nil)
	before, _ := listBefore["items"].([]any)

	// Push one more than the demo board's configured in_progress limit (3).
	const limit = 3
	toCreate := limit - len(before) + 1
	if toCreate < 1 {
		toCreate = 1
	}
	for i := 0; i < toCreate; i++ {
		resp, body := do(t, ts, "POST", "/v1/cards", map[string]any{
			"type_id": "programming-task",
			"title":   "breach smoke",
			"status":  "in_progress",
			"fields":  map[string]any{"description": "smoke", "branch": "smoke/breach"},
		}, map[string]string{"X-Work-Cards-Actor": "tester"})
		if resp.StatusCode != 201 {
			t.Fatalf("create: status=%d body=%v", resp.StatusCode, body)
		}
	}

	resp, body := do(t, ts, "GET", "/v1/breaches?board_id=engineering", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("breaches: status=%d body=%v", resp.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	found := false
	for _, raw := range items {
		it, _ := raw.(map[string]any)
		if it["type"] == "wip_exceeded" && it["board_id"] == "engineering" && it["column"] == "in_progress" {
			found = true
		}
	}
	if !found {
		t.Fatalf("breaches missing wip_exceeded for engineering/in_progress: %v", items)
	}

	// An unknown board 404s.
	resp, _ = do(t, ts, "GET", "/v1/breaches?board_id=nope", nil, nil)
	if resp.StatusCode != 404 {
		t.Errorf("unknown board_id: status=%d, want 404", resp.StatusCode)
	}

	// type= filters to just the requested type.
	resp, filtered := do(t, ts, "GET", "/v1/breaches?"+url.Values{"type": {"card_blocked"}}.Encode(), nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("filtered breaches: status=%d", resp.StatusCode)
	}
	for _, raw := range filtered["items"].([]any) {
		it, _ := raw.(map[string]any)
		if it["type"] != "card_blocked" {
			t.Errorf("type filter leaked a %v item", it["type"])
		}
	}
}
