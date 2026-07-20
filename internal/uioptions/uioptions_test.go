package uioptions

import (
	"testing"

	"github.com/somebox/cards/internal/core"
)

func TestResolveMeFilter(t *testing.T) {
	in := map[string]any{
		"owner":      map[string]any{"$eq": "me"},
		"created_by": map[string]any{"$in": []any{"me", "alice"}},
		"status":     map[string]any{"$nin": []any{"done"}},
		"note":       "me", // not an identity key → left as-is
	}
	out := ResolveMeFilter(in, "local-dev")

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
		t.Error("ResolveMeFilter mutated its input")
	}
	// Empty actor leaves "me" untouched.
	out2 := ResolveMeFilter(in, "")
	if out2["owner"].(map[string]any)["$eq"] != "me" {
		t.Error("empty actor should leave 'me' untouched")
	}
}

func TestSortOptions(t *testing.T) {
	t.Run("presets, no board presentation", func(t *testing.T) {
		opts := SortOptions("-created_at", &core.Board{})
		want := []struct{ value, label string }{
			{"-updated_at", "Recently updated"},
			{"-created_at", "Newest"},
			{"created_at", "Oldest"},
			{"title", "Title (A–Z)"},
		}
		if len(opts) != len(want) {
			t.Fatalf("got %d options, want %d", len(opts), len(want))
		}
		for i, w := range want {
			if opts[i].Value != w.value || opts[i].Label != w.label {
				t.Errorf("opts[%d] = %q %q, want %q %q", i, opts[i].Value, opts[i].Label, w.value, w.label)
			}
			if sel := opts[i].Selected; sel != (w.value == "-created_at") {
				t.Errorf("opts[%d].Selected = %v", i, sel)
			}
		}
	})

	t.Run("nil board is safe", func(t *testing.T) {
		if opts := SortOptions("", nil); len(opts) != 4 {
			t.Errorf("nil board: got %d options, want 4 presets", len(opts))
		}
	})

	t.Run("custom lane_sort surfaces a Board default entry", func(t *testing.T) {
		b := &core.Board{Presentation: &core.BoardPresentation{LaneSort: "-fields.priority"}}
		opts := SortOptions("-fields.priority", b)
		if len(opts) != 5 {
			t.Fatalf("got %d options, want 5 (board default + presets)", len(opts))
		}
		if opts[0].Value != "-fields.priority" || opts[0].Label != "Board default" || !opts[0].Selected {
			t.Errorf("opts[0] = %+v, want selected Board default -fields.priority", opts[0])
		}
	})

	t.Run("lane_sort matching a preset does not duplicate", func(t *testing.T) {
		b := &core.Board{Presentation: &core.BoardPresentation{LaneSort: "title"}}
		opts := SortOptions("title", b)
		if len(opts) != 4 {
			t.Fatalf("got %d options, want 4 (preset doubles as board default)", len(opts))
		}
		if !opts[3].Selected {
			t.Error("title preset should be selected")
		}
	})
}
