package httpapi_test

import (
	"testing"
)

// A malformed filter DSL in take-next must surface as a client error (422
// validation_failed), proving the store-compiled filter error flows through
// the service to the HTTP error mapping intact.
func TestTakeNextMalformedFilterIs422(t *testing.T) {
	ts, _ := newServer(t)
	resp, body := do(t, ts, "POST", "/v1/cards/take-next", map[string]any{
		"filter": map[string]any{"status": "not-an-operator-object"},
	}, map[string]string{"X-Cards-Actor": "demo"})
	if resp.StatusCode != 422 {
		t.Fatalf("status = %d (body %v), want 422", resp.StatusCode, body)
	}
	if body["error"] != "validation_failed" {
		t.Errorf("error = %v, want validation_failed", body["error"])
	}
}
