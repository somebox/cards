package httpapi_test

import (
	"testing"
)

// TestListValueStatusFilter pins the P3a·B wiring: a comma-separated status=
// (and type_id=) list on GET /v1/cards matches ANY of the values (IN), while a
// bare single value keeps the scalar path. Asserted relationally (multi == the
// disjoint union of the singles) so it survives any change to the seeded board.
func TestListValueStatusFilter(t *testing.T) {
	ts, _ := newServer(t)

	statusesOf := func(path string) map[string]bool {
		_, out := do(t, ts, "GET", path, nil, nil)
		items, _ := out["items"].([]any)
		ids := map[string]bool{}
		for _, it := range items {
			m, _ := it.(map[string]any)
			id, _ := m["id"].(string)
			ids[id] = true
		}
		return ids
	}

	todo := statusesOf("/v1/cards?board_id=engineering&status=todo&limit=500")
	prog := statusesOf("/v1/cards?board_id=engineering&status=in_progress&limit=500")
	multi := statusesOf("/v1/cards?board_id=engineering&status=todo,in_progress&limit=500")

	// A card has exactly one status, so todo and in_progress are disjoint and
	// the comma-list must return precisely their union.
	if len(multi) != len(todo)+len(prog) {
		t.Fatalf("status=todo,in_progress returned %d cards; want union of todo (%d) + in_progress (%d) = %d",
			len(multi), len(todo), len(prog), len(todo)+len(prog))
	}
	for id := range todo {
		if !multi[id] {
			t.Errorf("card %s (todo) missing from the comma-list result", id)
		}
	}
	for id := range prog {
		if !multi[id] {
			t.Errorf("card %s (in_progress) missing from the comma-list result", id)
		}
	}

	// A single value still behaves as scalar equality (backward compatible).
	single := statusesOf("/v1/cards?board_id=engineering&status=todo&limit=500")
	if len(single) != len(todo) {
		t.Errorf("single status=todo changed shape: %d vs %d", len(single), len(todo))
	}

	// Whitespace after the comma is tolerated (splitCSV trims).
	spaced := statusesOf("/v1/cards?board_id=engineering&status=todo,%20in_progress&limit=500")
	if len(spaced) != len(multi) {
		t.Errorf("status=todo, in_progress (spaced) returned %d; want %d", len(spaced), len(multi))
	}
}

// TestListValueTypeIDFilter pins the same wiring for type_id= across two card
// types present on the engineering board.
func TestListValueTypeIDFilter(t *testing.T) {
	ts, _ := newServer(t)

	count := func(path string) int {
		_, out := do(t, ts, "GET", path, nil, nil)
		items, _ := out["items"].([]any)
		return len(items)
	}

	fe := count("/v1/cards?board_id=engineering&type_id=frontend-task&limit=500")
	pg := count("/v1/cards?board_id=engineering&type_id=programming-task&limit=500")
	both := count("/v1/cards?board_id=engineering&type_id=frontend-task,programming-task&limit=500")

	if both != fe+pg {
		t.Fatalf("type_id=frontend-task,programming-task returned %d; want %d + %d = %d",
			both, fe, pg, fe+pg)
	}
}
