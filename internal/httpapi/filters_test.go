package httpapi

import "testing"

func TestResolveMeFilter(t *testing.T) {
	in := map[string]any{
		"owner":      map[string]any{"$eq": "me"},
		"created_by": map[string]any{"$in": []any{"me", "alice"}},
		"status":     map[string]any{"$nin": []any{"done"}},
		"note":       "me", // not an identity key → left as-is
	}
	out := resolveMeFilter(in, "local-dev")

	if got := out["owner"].(map[string]any)["$eq"]; got != "local-dev" {
		t.Errorf("owner.$eq = %v, want local-dev", got)
	}
	cb := out["created_by"].(map[string]any)["$in"].([]any)
	if cb[0] != "local-dev" || cb[1] != "alice" {
		t.Errorf("created_by.$in = %v, want [local-dev alice]", cb)
	}
	if got := out["note"]; got != "me" {
		t.Errorf("note should be untouched, got %v", got)
	}
	// Input must not be mutated (deep copy).
	if in["owner"].(map[string]any)["$eq"] != "me" {
		t.Error("resolveMeFilter mutated its input")
	}
	// Empty actor leaves "me" untouched.
	out2 := resolveMeFilter(in, "")
	if out2["owner"].(map[string]any)["$eq"] != "me" {
		t.Error("empty actor should leave 'me' untouched")
	}
}
