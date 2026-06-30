package openapi_test

import (
	"encoding/json"
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
