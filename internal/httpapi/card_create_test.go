package httpapi_test

// UI sprint P2 (sprint-2026-07-07, card adb1ebcf): in-board card creation.
// The modal fragment is schema-driven (one renderer for every type), the old
// full-page form is replaced by a redirect into the modal flow, and creation
// itself is the UI acting as a thin client of POST /v1/cards — so these tests
// pin the fragment's affordances, the redirect, per-field error shape, the
// allowed_columns discipline, and double-submit idempotency.

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// getNoRedirect GETs without following redirects (to assert 303s).
func getNoRedirect(t *testing.T, url string) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d", url, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestCreateModal_PickerAndSchemaForm(t *testing.T) {
	ts, _, _ := newServerStore(t)

	// Mode 1: no type → a picker of the board's types, badges with type ids.
	picker := getBody(t, ts.URL+"/ui/cards/new/modal?board=engineering")
	for _, want := range []string{"data-create-modal", `data-create-type="programming-task"`,
		`data-create-type="api-task"`, "card__type-badge"} {
		if !strings.Contains(picker, want) {
			t.Errorf("picker missing %q", want)
		}
	}
	if strings.Contains(picker, "data-create-save") {
		t.Error("picker should not render the save button")
	}

	// Mode 2: with a type → the schema-driven form. api-task pins the
	// interesting cases: required string, enum with a default, card_link
	// excluded, repeating/artifact excluded.
	form := getBody(t, ts.URL+"/ui/cards/new/modal?board=engineering&type=api-task&status=todo")
	for _, want := range []string{
		`data-create-form`, `data-type-id="api-task"`, "data-create-save",
		`data-create-input="title"`, `data-create-input="status"`,
		`data-create-input="field:endpoint"`, `data-create-input="field:description"`,
		`data-create-input="field:api_change"`, // enum
		`data-error-for="field:endpoint"`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("form missing %q", want)
		}
	}
	// Enum default pre-selected (api_change defaults to "additive").
	if !strings.Contains(form, `<option value="additive" selected>`) {
		t.Error("enum default not pre-selected")
	}
	// The preset lane is selected in the status options.
	if !strings.Contains(form, `<option value="todo" selected`) {
		t.Error("preset status not selected")
	}
	// Post-creation surfaces stay out of the create form.
	for _, absent := range []string{`data-create-input="field:work_log"`, `data-create-input="field:evidence"`, `data-create-input="field:spec"`} {
		if strings.Contains(form, absent) {
			t.Errorf("create form should not render %q", absent)
		}
	}
}

func TestCreateModal_DisallowedStatusDisabled(t *testing.T) {
	ts, _, _ := newServerStore(t)
	// The starter "task" type allows only todo/in_progress/done on the demo
	// workspace — on the engineering board its status options must mark other
	// columns disabled rather than offering an invalid create.
	form := getBody(t, ts.URL+"/ui/cards/new/modal?board=engineering&type=task")
	if !strings.Contains(form, "disabled") {
		t.Error("expected at least one disallowed status option to be disabled")
	}
}

func TestNewCardPageRedirectsIntoModalFlow(t *testing.T) {
	ts, _, _ := newServerStore(t)
	resp := getNoRedirect(t, ts.URL+"/ui/cards/new?type=api-task&status=todo&board=engineering")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /ui/cards/new: %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	for _, want := range []string{"/ui/boards/engineering", "new=1", "type=api-task", "status=todo"} {
		if !strings.Contains(loc, want) {
			t.Errorf("redirect %q missing %q", loc, want)
		}
	}
	// The old form POST route is gone.
	post, _ := http.Post(ts.URL+"/ui/cards", "application/x-www-form-urlencoded", strings.NewReader("type_id=task"))
	if post.StatusCode != 404 && post.StatusCode != 405 {
		t.Errorf("POST /ui/cards still exists: %d", post.StatusCode)
	}
}

func TestCreateViaAPI_PerFieldErrorAndIdempotency(t *testing.T) {
	ts, _, _ := newServerStore(t)

	// Required field missing → the structured error names the FIELD, which is
	// exactly what the modal maps onto its [data-error-for] elements.
	resp, out := do(t, ts, "POST", "/v1/cards",
		map[string]any{"type_id": "api-task", "title": "no endpoint", "status": "todo",
			"fields": map[string]any{"description": "d", "api_change": "additive"}},
		map[string]string{"X-Work-Cards-Actor": "local-dev"})
	if resp.StatusCode != 422 {
		t.Fatalf("missing required: %d %v (want 422)", resp.StatusCode, out)
	}
	if out["field"] != "endpoint" {
		t.Errorf("error field = %v, want endpoint", out["field"])
	}

	// Double-submit with the same Idempotency-Key creates ONE card.
	body := map[string]any{"type_id": "api-task", "title": "idempotent create", "status": "todo",
		"fields": map[string]any{"description": "d", "endpoint": "GET /v1/x", "api_change": "additive"}}
	hdr := map[string]string{"X-Work-Cards-Actor": "local-dev", "Idempotency-Key": "ui-create-test-1"}
	r1, out1 := do(t, ts, "POST", "/v1/cards", body, hdr)
	r2, out2 := do(t, ts, "POST", "/v1/cards", body, hdr)
	if r1.StatusCode >= 300 || r2.StatusCode >= 300 {
		t.Fatalf("idempotent creates: %d / %d", r1.StatusCode, r2.StatusCode)
	}
	if out1["id"] != out2["id"] {
		t.Errorf("double-submit created two cards: %v vs %v", out1["id"], out2["id"])
	}
}
