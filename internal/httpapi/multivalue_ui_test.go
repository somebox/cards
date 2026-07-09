package httpapi_test

// UI save path for multi-value fields (frontend-rebuild Phase 3): a native
// <select multiple> posts one form entry per selection; uiSaveCard must
// collect them into a JSON array — and single-value fields stay scalar.
// Uses the real demo workspace (frontend-task.platforms is multiple:true).

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core"
)

func TestUISaveMultiValueField(t *testing.T) {
	ts, svc := newServer(t)
	ctx := context.Background()

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "frontend-task", Title: "mv", Status: "backlog", Actor: "local-dev",
		Fields: map[string]any{"description": "d", "surface": "board"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	form := url.Values{}
	form.Set("title", c.Title)
	form.Set("version", "1")
	form.Add("field:platforms", "desktop")
	form.Add("field:platforms", "tablet")
	req, _ := http.NewRequest("POST", ts.URL+"/ui/cards/"+c.ID+"/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Cards-Partial", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("save: %d", resp.StatusCode)
	}

	got, err := svc.GetCard(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := got.Fields.(map[string]any)["platforms"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "desktop" || arr[1] != "tablet" {
		t.Fatalf("platforms = %#v, want [desktop tablet]", got.Fields.(map[string]any)["platforms"])
	}
	// Single-value field untouched by the multi path: surface stays a scalar.
	if got.Fields.(map[string]any)["surface"] != "board" {
		t.Fatalf("surface = %#v, want scalar \"board\"", got.Fields.(map[string]any)["surface"])
	}
}

// TestUISaveMultiValueClearAll (rebuild P6): the edit form renders a hidden
// "" sentinel input alongside the native <select multiple>, so a fully
// deselected control still posts one (filtered-out) entry and the save
// UNSETS the field — with or without JS. This closes the P3 gap where
// deselect-all posted nothing and PATCH read it as "don't touch".
func TestUISaveMultiValueClearAll(t *testing.T) {
	ts, svc := newServer(t)
	ctx := context.Background()

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "frontend-task", Title: "clear", Status: "backlog", Actor: "local-dev",
		Fields: map[string]any{"description": "d", "surface": "board", "platforms": []any{"desktop", "mobile"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The modal markup carries the sentinel (no-JS honesty check).
	html := modalFor(t, ts.URL, c.ID)
	if !strings.Contains(html, `<input type="hidden" name="field:platforms" value="">`) {
		t.Fatal("edit form is missing the clear-all sentinel input")
	}

	// A no-JS clear-all posts ONLY the sentinel for the field.
	form := url.Values{}
	form.Set("title", c.Title)
	form.Set("version", "1")
	form.Add("field:platforms", "") // the sentinel — nothing selected
	req, _ := http.NewRequest("POST", ts.URL+"/ui/cards/"+c.ID+"/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Cards-Partial", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	got, err := svc.GetCard(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := got.Fields.(map[string]any)["platforms"]; present {
		t.Fatalf("platforms still present after sentinel-only save: %#v", got.Fields)
	}
}

// modalFor fetches the rendered modal fragment.
func modalFor(t *testing.T, tsURL, id string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", tsURL+"/ui/cards/"+id+"/modal", nil)
	req.Header.Set("X-Cards-Partial", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(strings.Builder)
	if _, err := io.Copy(b, resp.Body); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestUISaveReturnsRealStatusOnConflict (rebuild P8, closes bug
// card_096261c37): a stale-version modal save returns the REAL 409 with
// the error-embedded fragment — not HTTP 200 as before, which would toast
// "Saved" while dropping the write.
func TestUISaveReturnsRealStatusOnConflict(t *testing.T) {
	ts, svc := newServer(t)
	ctx := context.Background()

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "frontend-task", Title: "conflict-victim", Status: "backlog", Actor: "local-dev",
		Fields: map[string]any{"description": "d", "surface": "board"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Post with a stale version (server is at 1, we send 0).
	form := url.Values{}
	form.Set("title", "SHOULD NOT PERSIST")
	form.Set("version", "0")
	req, _ := http.NewRequest("POST", ts.URL+"/ui/cards/"+c.ID+"/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Cards-Partial", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Consume the body before checking status so the server's stream write
	// completes cleanly (avoids a "broken pipe" log line, not correctness).
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("save with stale version: %d, want 409 (honest status, per P8)", resp.StatusCode)
	}
	if !strings.Contains(string(body), "version_conflict") {
		t.Error("409 body should still carry the re-rendered modal with the alert")
	}
	got, err := svc.GetCard(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "conflict-victim" {
		t.Errorf("title mutated by stale save: %q", got.Title)
	}
}
