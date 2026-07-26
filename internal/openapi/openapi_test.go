package openapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/openapi"
)

func TestBuild(t *testing.T) {
	ws := &core.Workspace{ID: "w", Name: "W"}
	types := map[string]*core.CardType{
		"task": {
			ID: "task", Name: "Task", SchemaVersion: 2,
			Fields: []core.FieldDef{
				{ID: "body", Type: core.FieldText, Required: true},
				{ID: "priority", Type: core.FieldEnum, Options: []string{"low", "high"}},
				{ID: "log", Type: core.FieldRepeating, ItemFields: []core.FieldDef{
					{ID: "note", Type: core.FieldText},
				}},
			},
		},
	}
	doc := openapi.Build(ws, types)

	// Round-trips as JSON (the handler encodes it).
	if _, err := json.Marshal(doc); err != nil {
		t.Fatalf("doc not JSON-encodable: %v", err)
	}

	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", doc["openapi"])
	}
	paths := doc["paths"].(map[string]any)
	for _, p := range []string{"/cards", "/cards/{id}", "/cards/{id}/upgrade-schema"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("missing path %s", p)
		}
	}

	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	if _, ok := schemas["Card"]; !ok {
		t.Error("missing Card schema")
	}
	tf, ok := schemas["task.fields"].(map[string]any)
	if !ok {
		t.Fatal("missing task.fields schema")
	}
	props := tf["properties"].(map[string]any)
	pr := props["priority"].(map[string]any)
	if pr["type"] != "string" || len(pr["enum"].([]any)) != 2 {
		t.Errorf("priority schema = %v, want string enum[2]", pr)
	}
	if props["log"].(map[string]any)["type"] != "array" {
		t.Errorf("repeating field should map to array, got %v", props["log"])
	}
	req, _ := tf["required"].([]any)
	if len(req) != 1 || req[0] != "body" {
		t.Errorf("required = %v, want [body]", req)
	}
}

func buildDoc(t *testing.T) map[string]any {
	t.Helper()
	return openapi.Build(&core.Workspace{ID: "w", Name: "W"}, map[string]*core.CardType{
		"task": {ID: "task", Name: "Task", SchemaVersion: 1},
	})
}

func operation(t *testing.T, doc map[string]any, method, path string) map[string]any {
	t.Helper()
	paths := doc["paths"].(map[string]any)
	item, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("no path %s", path)
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		t.Fatalf("no %s on %s", method, path)
	}
	return op
}

func paramNames(op map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	ps, _ := op["parameters"].([]any)
	for _, p := range ps {
		m := p.(map[string]any)
		out[m["name"].(string)] = m
	}
	return out
}

// TestTakeNextResponseShape pins the envelope the handler actually writes.
// apiTakeNext returns {"card": ...|null}, never a bare Card — the document
// previously claimed the latter.
func TestTakeNextResponseShape(t *testing.T) {
	op := operation(t, buildDoc(t), "post", "/cards/take-next")
	resp := op["responses"].(map[string]any)["200"].(map[string]any)
	schema := resp["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if got := schema["$ref"]; got != "#/components/schemas/TakeNextResult" {
		t.Errorf("take-next 200 schema = %v, want a TakeNextResult ref", got)
	}
}

// TestListParamsMatchHandler pins the GET /cards query surface against what
// apiListCards actually reads, including the owner caveat: 'me' is resolved by
// the board UI, not by this endpoint.
func TestListParamsMatchHandler(t *testing.T) {
	op := operation(t, buildDoc(t), "get", "/cards")
	got := paramNames(op)
	for _, want := range []string{
		"board_id", "type_id", "status", "owner", "q", "blocked",
		"has_link", "link_target", "include", "sort", "cursor", "limit",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("GET /cards is missing the %q query parameter", want)
		}
	}
	desc, _ := got["owner"]["description"].(string)
	if !strings.Contains(desc, "NOT resolved") {
		t.Errorf("owner description must state that 'me' is not resolved here, got %q", desc)
	}
}

// TestWritesDocumentTheirGuarantees — every mutating operation advertises the
// idempotency header and the two structured failures an agent must handle.
func TestWritesDocumentTheirGuarantees(t *testing.T) {
	doc := buildDoc(t)
	writes := []struct{ method, path string }{
		{"post", "/cards"},
		{"patch", "/cards/{id}"},
		{"delete", "/cards/{id}"},
		{"post", "/cards/{id}/claim"},
		{"post", "/cards/{id}/release"},
		{"post", "/cards/take-next"},
		{"post", "/cards/{id}/links"},
		{"post", "/cards/{id}/comments"},
		{"patch", "/cards/{id}/comments/{commentID}"},
		{"post", "/cards/{id}/fields/{field}/append"},
		{"patch", "/cards/{id}/fields/{field}/{entryID}"},
		{"delete", "/cards/{id}/fields/{field}/{entryID}"},
	}
	for _, w := range writes {
		op := operation(t, doc, w.method, w.path)
		if _, ok := paramNames(op)["Idempotency-Key"]; !ok {
			t.Errorf("%s %s does not document Idempotency-Key", w.method, w.path)
		}
		resp := op["responses"].(map[string]any)
		for _, code := range []string{"409", "422"} {
			if _, ok := resp[code]; !ok {
				t.Errorf("%s %s does not document a %s response", w.method, w.path, code)
			}
		}
	}
}

// TestEntryDeleteRequiresVersion — DELETE carries no body, so the CAS token
// travels as a REQUIRED ?version=; the card delete guard is optional. Getting
// these backwards is a silent lost-update.
func TestEntryDeleteRequiresVersion(t *testing.T) {
	doc := buildDoc(t)
	entry := paramNames(operation(t, doc, "delete", "/cards/{id}/fields/{field}/{entryID}"))
	if entry["version"]["required"] != true {
		t.Error("entry delete must document version as required")
	}
	card := paramNames(operation(t, doc, "delete", "/cards/{id}"))
	if card["version"]["required"] != false {
		t.Error("card delete must document version as optional")
	}
}

// TestEventEnumTracksCore keeps the published event vocabulary generated from
// core.EventTypes rather than restated, so a new event type cannot ship with a
// stale contract.
func TestEventEnumTracksCore(t *testing.T) {
	doc := buildDoc(t)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	ev := schemas["Event"].(map[string]any)["properties"].(map[string]any)["type"].(map[string]any)
	enum := ev["enum"].([]any)
	if len(enum) != len(core.EventTypes()) {
		t.Fatalf("event enum has %d entries, core declares %d", len(enum), len(core.EventTypes()))
	}
	got := map[string]bool{}
	for _, e := range enum {
		got[e.(string)] = true
	}
	for _, want := range core.EventTypes() {
		if !got[string(want)] {
			t.Errorf("event enum missing %q", want)
		}
	}
}
