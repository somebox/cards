package config

import (
	"path/filepath"
	"testing"
)

// TestLoadDemoWorkspace loads examples/demo-workspace and asserts the shape
// documented in the spec.
func TestLoadDemoWorkspace(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "demo-workspace")
	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if r.Workspace.ID != "demo" {
		t.Errorf("workspace id = %q, want demo", r.Workspace.ID)
	}
	if got := len(r.Workspace.Columns); got != 5 {
		t.Errorf("columns = %d, want 5", got)
	}
	if got := len(r.Workspace.TagSet); got != 3 {
		t.Errorf("tag_set = %v, want 3", got)
	}
	if got := len(r.Workspace.LinkTypes); got != 4 {
		t.Errorf("link_types = %d, want 4", got)
	}
	if got := len(r.CardTypes); got != 3 {
		t.Fatalf("card types = %d, want 3", got)
	}
	if _, ok := r.CardTypes["programming-task"]; !ok {
		t.Error("missing programming-task type")
	}
	if _, ok := r.CardTypes["research-goal"]; !ok {
		t.Error("missing research-goal type")
	}
	if _, ok := r.CardTypes["task"]; !ok {
		t.Error("missing task type")
	}
	if got := len(r.Boards); got != 2 {
		t.Fatalf("boards = %d, want 2", got)
	}
	if _, ok := r.Boards["welcome"]; !ok {
		t.Error("missing welcome board")
	}
	b := r.Boards["engineering"]
	if b == nil || !b.Settings.EnforceTransitions {
		t.Error("engineering board should enforce transitions")
	}
	if got := b.Transitions["todo"]; len(got) != 1 || got[0] != "in_progress" {
		t.Errorf("todo transitions = %v, want [in_progress]", got)
	}
	// default_user seed
	if r.Workspace.Settings.DefaultUser != "local-dev" {
		t.Errorf("default_user = %q, want local-dev", r.Workspace.Settings.DefaultUser)
	}
}

// TestRejectBoardUnknownColumn ensures a board referencing an unknown column
// fails at load.
func TestRejectBoardUnknownColumn(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "definitions", "workspace.json"), `{
		"id":"t","name":"T",
		"columns":[{"id":"a","name":"A"}],
		"settings":{"default_user":"u"}
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a","nope"]
	}`)
	if _, err := New(dir).Load(); err == nil {
		t.Fatal("expected error for unknown column, got nil")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := writeFile(path, content); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
