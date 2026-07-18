package httpapi_test

// Sprint 2026-07-18 Phase 2: temporal catch-up on GET /v1/breaches across
// the HTTP + UI surfaces. Cold projection needs no scheduler, so a 50ms
// monitor threshold with a generous sleep is deterministic — no multi-day
// dogfood board, no clock plumbing at the HTTP layer.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/seed"
	"github.com/somebox/cards/internal/sqlite"
)

// newBreachesTemporalServer loads the demo workspace but overrides the engineering
// board monitors with a 50ms status_timeout threshold so tests can cross it
// with a short sleep.
func newBreachesTemporalServer(t *testing.T) *httptest.Server {
	t.Helper()
	r, err := config.New("../../examples/demo-workspace").Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r.Boards["engineering"].Monitors = &core.BoardMonitors{
		MaxTimeInStatus: map[string]string{"review": "50ms"},
		IdleAfter:       "50ms",
	}
	st, err := sqlite.Open(":memory:", r.Workspace)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := core.NewService(r.Workspace, r.CardTypes, r.Boards, st)
	if err := seed.IfEmpty(context.Background(), st, svc, r.Workspace); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv, err := httpapi.New(svc, r.Workspace, r.CardTypes, r.Boards, r.Themes, st)
	if err != nil {
		t.Fatalf("new http server: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(func() { ts.Close() })
	return ts
}

func TestAPIBreaches_TemporalProjection(t *testing.T) {
	ts := newBreachesTemporalServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "tester", "Content-Type": "application/json"}

	resp, body := do(t, ts, "POST", "/v1/cards", map[string]any{
		"type_id": "programming-task",
		"title":   "temporal smoke",
		"status":  "review",
		"fields":  map[string]any{"description": "smoke", "branch": "smoke/temporal"},
	}, H)
	if resp.StatusCode != 201 {
		t.Fatalf("create: status=%d body=%v", resp.StatusCode, body)
	}
	cardID, _ := body["id"].(string)

	// Not yet due.
	resp, body = do(t, ts, "GET", "/v1/breaches?type=status_timeout", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("breaches: status=%d body=%v", resp.StatusCode, body)
	}
	for _, raw := range body["items"].([]any) {
		it, _ := raw.(map[string]any)
		if it["card_id"] == cardID {
			t.Fatalf("card reported past-due before the 50ms threshold: %v", it)
		}
	}

	time.Sleep(300 * time.Millisecond)

	resp, body = do(t, ts, "GET", "/v1/breaches?type=status_timeout", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("breaches: status=%d body=%v", resp.StatusCode, body)
	}
	if limit, _ := body["limit"].(float64); limit != 500 {
		t.Errorf("limit echo = %v, want 500", body["limit"])
	}
	if trunc, _ := body["truncated"].(bool); trunc {
		t.Errorf("truncated = true on a tiny workspace, want false")
	}
	var found map[string]any
	for _, raw := range body["items"].([]any) {
		it, _ := raw.(map[string]any)
		if it["card_id"] == cardID {
			found = it
		}
		if it["type"] != "status_timeout" {
			t.Errorf("type=status_timeout leaked a %v row", it["type"])
		}
	}
	if found == nil {
		t.Fatalf("past-due card missing from status_timeout projection: %v", body["items"])
	}
	// Type→fields wire shape: flat scalars mirroring StatusTimeoutDiff.
	if found["status"] != "review" || found["max"] != "50ms" {
		t.Errorf("status/max = %v/%v, want review/50ms", found["status"], found["max"])
	}
	if found["since"] == nil || found["since"] == "" {
		t.Errorf("since missing on temporal row: %v", found)
	}

	// card_idle likewise projects past the 50ms idle threshold.
	resp, body = do(t, ts, "GET", "/v1/breaches?type=card_idle", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("breaches: status=%d body=%v", resp.StatusCode, body)
	}
	idleFound := false
	for _, raw := range body["items"].([]any) {
		it, _ := raw.(map[string]any)
		if it["card_id"] == cardID {
			idleFound = true
			if it["threshold"] != "50ms" {
				t.Errorf("threshold = %v, want 50ms", it["threshold"])
			}
		}
	}
	if !idleFound {
		t.Errorf("past-due card missing from card_idle projection: %v", body["items"])
	}

	// Moving the card out of review clears the status_timeout projection.
	_, cur := do(t, ts, "GET", "/v1/cards/"+cardID, nil, nil)
	version, _ := cur["version"].(float64)
	resp, body = do(t, ts, "PATCH", "/v1/cards/"+cardID, map[string]any{
		"version": int(version), "status": "done",
	}, H)
	if resp.StatusCode != 200 {
		t.Fatalf("patch: status=%d body=%v", resp.StatusCode, body)
	}
	_, body = do(t, ts, "GET", "/v1/breaches?type=status_timeout", nil, nil)
	for _, raw := range body["items"].([]any) {
		it, _ := raw.(map[string]any)
		if it["card_id"] == cardID {
			t.Errorf("card left review but still projects status_timeout: %v", it)
		}
	}
}

// The UI must render temporal rows with a friendly label and deadline
// context (status + max + since), not a raw event slug — and the empty
// state must acknowledge temporal monitors.
func TestUIBreachesTemporalRow(t *testing.T) {
	ts := newBreachesTemporalServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "tester", "Content-Type": "application/json"}

	// Empty state mentions temporal monitors.
	resp, body := doGet(t, ts, "/ui/breaches")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(body, "status/idle monitors") {
		t.Error("empty-state copy does not mention temporal monitors")
	}

	resp2, body2 := do(t, ts, "POST", "/v1/cards", map[string]any{
		"type_id": "programming-task",
		"title":   "UI temporal row",
		"status":  "review",
		"fields":  map[string]any{"description": "smoke", "branch": "smoke/ui-temporal"},
	}, H)
	if resp2.StatusCode != 201 {
		t.Fatalf("create: status=%d body=%v", resp2.StatusCode, body2)
	}
	time.Sleep(300 * time.Millisecond)

	resp, body = doGet(t, ts, "/ui/breaches")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(body, `data-condition="status_timeout"`) {
		t.Error("page missing status_timeout row")
	}
	if !strings.Contains(body, "Status timeout") {
		t.Error("page shows raw slug instead of the friendly label")
	}
	if !strings.Contains(body, "UI temporal row") {
		t.Error("page missing the card title")
	}
	if !strings.Contains(body, "in review beyond 50ms") {
		t.Error("row missing deadline context (status + max)")
	}
	if !strings.Contains(body, "data-ago=") {
		t.Error("row missing <time data-ago> for the since timestamp")
	}
}
