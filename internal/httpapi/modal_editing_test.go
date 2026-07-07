package httpapi_test

// UI sprint P1 (sprint-2026-07-07, cards a09f0455 + b9e17b9e): the card modal
// is a work surface — comments and repeating entries are editable, as a thin
// client of the existing /v1 endpoints. These tests pin (a) the modal renders
// the composer + the schema-driven entry editor with everything the JS needs
// (ids, raw prefill values, required marks), and (b) the endpoint round-trips
// the UI performs, including the stale-version 409 the modal renders as
// "card changed — reload".

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core"
)

// modalHTML fetches the rendered modal for a card.
func modalHTML(t *testing.T, tsURL, cardID string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", tsURL+"/ui/cards/"+cardID+"/modal", nil)
	req.Header.Set("X-Modal", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("modal: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("modal: %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func mkWorkLogCard(t *testing.T, svc *core.Service) *core.Card {
	t.Helper()
	c, err := svc.CreateCard(core.WithActor(context.Background(), "local-dev"), core.CreateCardRequest{
		TypeID: "programming-task", Title: "P1 modal editing", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"}, Actor: "local-dev",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return c
}

func TestModalRendersComposerAndEntryEditor(t *testing.T) {
	ts, svc, _ := newServerStore(t)
	c := mkWorkLogCard(t, svc)

	// Seed one comment and one work_log entry so both feeds render.
	if _, err := svc.AddComment(core.WithActor(context.Background(), "local-dev"), c.ID, "first comment"); err != nil {
		t.Fatal(err)
	}
	cur, _ := svc.ResolveCard(context.Background(), c.ID)
	entry := map[string]any{"commit_hash": "abc1234", "notes": "did things",
		"author": "local-dev", "timestamp": "2026-07-07"}
	if _, err := svc.AppendEntry(core.WithActor(context.Background(), "local-dev"), c.ID, "work_log", entry, cur.Version); err != nil {
		t.Fatal(err)
	}

	html := modalHTML(t, ts.URL, c.ID)

	// Comments: composer always present, existing comment editable.
	for _, want := range []string{"data-comment-composer", "data-comment-input",
		"data-comment-submit", "data-comment-edit", "data-comment-body", "first comment"} {
		if !strings.Contains(html, want) {
			t.Errorf("modal missing %q", want)
		}
	}

	// Entry editor: add button + one schema-driven sub-form input per
	// item_field, with required marks, plus the entry's id and raw prefill.
	for _, want := range []string{
		`data-entry-field="work_log"`, "data-entry-add", "data-entry-form",
		`data-entry-input="commit_hash"`, `data-entry-input="notes"`,
		`data-entry-input="author"`, `data-entry-input="timestamp"`,
		"data-entry-edit", "data-entry-remove",
		`data-raw-key="commit_hash" data-raw-val="abc1234"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("modal missing %q", want)
		}
	}
	// The entry id must be on the entry element for update/remove.
	fresh, _ := svc.ResolveCard(context.Background(), c.ID)
	fm := fresh.Fields.(map[string]any)
	arr := fm["work_log"].([]any)
	entryID, _ := arr[0].(map[string]any)["entry_id"].(string)
	if entryID == "" || !strings.Contains(html, `data-entry-id="`+entryID+`"`) {
		t.Errorf("modal missing entry id %q", entryID)
	}
	// Layout roles: head/grid split with author chip + right-aligned time.
	for _, want := range []string{"entry__head", "entry__grid", "entry__author", "entry__time"} {
		if !strings.Contains(html, want) {
			t.Errorf("modal missing layout role %q", want)
		}
	}
	// Structure guard: comments and entries each live in ONE field__val
	// wrapper (label | value), so themes that grid .field two-column can
	// never wrap the composer or the add-toolbar into the label column.
	// Micro-actions share the icon-btn system.
	for _, want := range []string{"comments-box", "entries-box", "entries-toolbar", "icon-btn icon-btn--primary"} {
		if !strings.Contains(html, want) {
			t.Errorf("modal missing structural role %q", want)
		}
	}
}

func TestModalCommentAndEntryLifecycle(t *testing.T) {
	ts, svc, _ := newServerStore(t)
	c := mkWorkLogCard(t, svc)

	// 1. Add a comment exactly as the composer does.
	resp, out := do(t, ts, "POST", "/v1/cards/"+c.ID+"/comments",
		map[string]any{"body": "from the composer"}, map[string]string{"X-Work-Cards-Actor": "local-dev"})
	if resp.StatusCode != 201 {
		t.Fatalf("add comment: %d %v", resp.StatusCode, out)
	}
	html := modalHTML(t, ts.URL, c.ID)
	if !strings.Contains(html, "from the composer") {
		t.Error("modal does not show the new comment")
	}

	// 2. Edit it as the in-place editor does.
	fresh, _ := svc.ResolveCard(context.Background(), c.ID)
	commentID := fresh.Comments[0].ID
	resp, out = do(t, ts, "PATCH", "/v1/cards/"+c.ID+"/comments/"+commentID,
		map[string]any{"body": "edited body"}, map[string]string{"X-Work-Cards-Actor": "local-dev"})
	if resp.StatusCode != 200 {
		t.Fatalf("edit comment: %d %v", resp.StatusCode, out)
	}
	if html = modalHTML(t, ts.URL, c.ID); !strings.Contains(html, "edited body") {
		t.Error("modal does not show the edited comment")
	}

	// 3. Append an entry as the sub-form does ({entry, version}).
	fresh, _ = svc.ResolveCard(context.Background(), c.ID)
	resp, out = do(t, ts, "POST", "/v1/cards/"+c.ID+"/fields/work_log/append",
		map[string]any{"entry": map[string]any{"commit_hash": "beef001", "author": "local-dev", "timestamp": "2026-07-07"},
			"version": fresh.Version},
		map[string]string{"X-Work-Cards-Actor": "local-dev"})
	if resp.StatusCode != 200 {
		t.Fatalf("append entry: %d %v", resp.StatusCode, out)
	}

	// 4. A STALE append (same version again) → the 409 shape the UI renders
	// as "card changed — reload".
	resp, out = do(t, ts, "POST", "/v1/cards/"+c.ID+"/fields/work_log/append",
		map[string]any{"entry": map[string]any{"commit_hash": "beef002", "author": "local-dev", "timestamp": "2026-07-07"},
			"version": fresh.Version},
		map[string]string{"X-Work-Cards-Actor": "local-dev"})
	if resp.StatusCode != 409 || out["error"] != "version_conflict" {
		t.Fatalf("stale append: %d %v (want 409 version_conflict)", resp.StatusCode, out)
	}

	// 5. Update then remove the entry, versions read fresh each time.
	fresh, _ = svc.ResolveCard(context.Background(), c.ID)
	arr := fresh.Fields.(map[string]any)["work_log"].([]any)
	entryID, _ := arr[0].(map[string]any)["entry_id"].(string)
	resp, out = do(t, ts, "PATCH", "/v1/cards/"+c.ID+"/fields/work_log/"+entryID,
		map[string]any{"entry": map[string]any{"commit_hash": "beef003", "author": "local-dev", "timestamp": "2026-07-07"},
			"version": fresh.Version},
		map[string]string{"X-Work-Cards-Actor": "local-dev"})
	if resp.StatusCode != 200 {
		t.Fatalf("update entry: %d %v", resp.StatusCode, out)
	}
	if html = modalHTML(t, ts.URL, c.ID); !strings.Contains(html, "beef003") {
		t.Error("modal does not show the updated entry")
	}
	fresh, _ = svc.ResolveCard(context.Background(), c.ID)
	req, _ := http.NewRequest("DELETE",
		fmt.Sprintf("%s/v1/cards/%s/fields/work_log/%s?version=%d", ts.URL, c.ID, entryID, fresh.Version), nil)
	req.Header.Set("X-Work-Cards-Actor", "local-dev")
	dresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(dresp.Body)
	dresp.Body.Close()
	if dresp.StatusCode != 200 {
		t.Fatalf("remove entry: %d %s", dresp.StatusCode, body)
	}
	var after core.Card
	_ = json.Unmarshal(body, &after)
	if html = modalHTML(t, ts.URL, c.ID); strings.Contains(html, "beef003") {
		t.Error("removed entry still renders in the modal")
	}
}
