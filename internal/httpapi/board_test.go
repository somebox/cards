package httpapi_test

import "testing"

func TestGetBoardReturnsOneBoard(t *testing.T) {
	ts, _ := newServer(t)
	resp, body := do(t, ts, "GET", "/v1/boards/engineering", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d (body %v)", resp.StatusCode, body)
	}
	if body["id"] != "engineering" {
		t.Errorf("id = %v, want engineering", body["id"])
	}
	// The response is one board, not the whole workspace.
	if _, isWorkspace := body["card_types"]; isWorkspace {
		t.Errorf("response looks like a workspace snapshot: %v", body)
	}

	resp, body = do(t, ts, "GET", "/v1/boards/nope", nil, nil)
	if resp.StatusCode != 404 || body["error"] != "not_found" {
		t.Errorf("unknown board: status=%d error=%v, want 404 not_found", resp.StatusCode, body["error"])
	}
}
