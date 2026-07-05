package mcp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/mcp"
	"github.com/somebox/cards/internal/sqlite"
)

// attach_artifact stores base64 content on an artifact field, and get_artifact
// returns byte-identical content as base64 — the deliberate stdio asymmetry
// with HTTP's raw body.
func TestMCPAttachAndGetArtifact(t *testing.T) {
	ws := &core.Workspace{
		ID: "t", Name: "T",
		Columns:  []core.Column{{ID: "todo", Name: "Todo"}},
		Settings: core.WorkspaceSettings{DefaultUser: "u"},
	}
	types := map[string]*core.CardType{
		"task": {ID: "task", Name: "Task", SchemaVersion: 1,
			Fields: []core.FieldDef{
				{ID: "description", Type: core.FieldText, Required: true},
				{ID: "screenshot", Type: core.FieldArtifact},
			},
			AllowedColumns: []string{"todo"}},
	}
	boards := map[string]*core.Board{
		"eng": {ID: "eng", Name: "Eng", Columns: []string{"todo"}, CardTypeIDs: []string{"task"}},
	}
	st, err := sqlite.Open(":memory:", ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	_ = st.InsertUser(ctx, core.User{ID: "u", Kind: "human"})
	svc := core.NewService(ws, types, boards, st)
	am, _ := artifacts.New(t.TempDir())
	svc.SetArtifacts(am)
	c, err := svc.CreateCard(core.WithActor(ctx, "u"), core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := mcp.New(svc, ws, types, boards, "agent")

	content := []byte("mcp artifact payload")
	b64 := base64.StdEncoding.EncodeToString(content)

	attach, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "attach_artifact", "arguments": map[string]any{
			"card_id": c.ID, "field": "screenshot", "content_base64": b64}}})
	if resp := call(t, srv, string(attach)); resp["error"] != nil {
		t.Fatalf("attach_artifact error: %v", resp["error"])
	}

	updated, err := svc.GetCard(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	uri := updated.Fields.(map[string]any)["screenshot"].(map[string]any)["uri"].(string)

	get, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "get_artifact", "arguments": map[string]any{"uri": uri}}})
	gresp := call(t, srv, string(get))
	if gresp["error"] != nil {
		t.Fatalf("get_artifact error: %v", gresp["error"])
	}
	raw, _ := json.Marshal(gresp)
	if !strings.Contains(string(raw), b64) {
		t.Errorf("get_artifact did not return the base64 payload:\n%s", raw)
	}
}
