package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestDirectBackend exercises the serverless CLI backend: it scaffolds a
// workspace, then reads and writes through the in-process router with no server.
func TestDirectBackend(t *testing.T) {
	dir := t.TempDir()
	if _, err := initWorkspace(dir); err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	t.Setenv("CARDS_WORKSPACE", dir)

	b, err := newDirectBackend()
	if err != nil {
		t.Fatalf("newDirectBackend: %v", err)
	}
	defer b.Close()

	// Read: the seeded welcome board.
	status, body, err := b.Do("GET", "/cards?board_id=welcome", nil, nil)
	if err != nil || status != 200 {
		t.Fatalf("list welcome: status=%d err=%v", status, err)
	}
	var env struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(env.Items) == 0 {
		t.Fatal("expected seeded welcome cards, got none")
	}

	// Write: create a card through the in-process router.
	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	hdr.Set("X-Work-Cards-Actor", "me")
	status, body, err = b.Do("POST", "/cards", []byte(`{"type_id":"task","title":"t","fields":{"notes":"n"}}`), hdr)
	if err != nil || status != 201 {
		t.Fatalf("create: status=%d err=%v body=%s", status, err, body)
	}

	// An invalid card type surfaces the structured error, just as over HTTP.
	status, _, _ = b.Do("POST", "/cards", []byte(`{"type_id":"nope","title":"x"}`), hdr)
	if status != 404 {
		t.Errorf("create with unknown type: status=%d, want 404", status)
	}
}
