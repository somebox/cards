package httpapi_test

// UI save path for multi-value fields (frontend-rebuild Phase 3): a native
// <select multiple> posts one form entry per selection; uiSaveCard must
// collect them into a JSON array — and single-value fields stay scalar.
// Uses the real demo workspace (frontend-task.platforms is multiple:true).

import (
	"context"
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
