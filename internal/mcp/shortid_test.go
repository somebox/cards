package mcp_test

// MCP-transport half of the sprint 2026-07-06 Phase 1 parity contract.
// MCP calls the Service directly (it does not share the HTTP router), so
// these tests pin that toolError renders the taxonomy's "ambiguous" error —
// structured, with candidates — instead of the pre-Phase-1 bare -32603.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/coretest"
	"github.com/somebox/cards/internal/mcp"
	"github.com/somebox/cards/internal/sqlite"
)

func newShortIDHarness(t *testing.T) (*mcp.Server, *core.Service, *sqlite.Store) {
	t.Helper()
	ws := &core.Workspace{
		ID: "t", Name: "T",
		Columns:  []core.Column{{ID: "todo", Name: "Todo"}},
		Settings: core.WorkspaceSettings{DefaultUser: "u"},
	}
	types := map[string]*core.CardType{
		"task": {ID: "task", Name: "Task", SchemaVersion: 1,
			Fields:         []core.FieldDef{{ID: "description", Type: core.FieldText}},
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
	_ = st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"})
	svc := core.NewService(ws, types, boards, st)
	return mcp.New(svc, ws, types, boards, "agent"), svc, st
}

func toolsCall(t *testing.T, srv *mcp.Server, name string, args map[string]any) map[string]any {
	t.Helper()
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	return call(t, srv, string(req))
}

func TestMCPAddComment_AmbiguousShortIDIsStructured(t *testing.T) {
	srv, _, st := newShortIDHarness(t)
	idA, idB := coretest.SeedCollidingCards(t, st, "t", "task", "MCPAMBI1")

	resp := toolsCall(t, srv, "add_comment", map[string]any{"card_id": "MCPAMBI1", "body": "hi"})
	// A structured tool error, NOT a JSON-RPC -32603.
	if resp["error"] != nil {
		t.Fatalf("expected structured tool result, got JSON-RPC error: %v", resp["error"])
	}
	raw, _ := json.Marshal(resp["result"])
	body := string(raw)
	if !strings.Contains(body, `\"isError\":true`) && !strings.Contains(body, `"isError":true`) {
		t.Fatalf("expected isError=true, got: %s", body)
	}
	for _, want := range []string{"ambiguous", idA, idB} {
		if !strings.Contains(body, want) {
			t.Errorf("tool error missing %q:\n%s", want, body)
		}
	}
}

func TestMCPAddComment_ShortIDResolvesAndNormalizes(t *testing.T) {
	srv, svc, st := newShortIDHarness(t)
	full := coretest.CardID("MCPHIT01", "z")
	coretest.SeedCard(t, st, "t", "task", full, nil)

	resp := toolsCall(t, srv, "add_comment", map[string]any{"card_id": "MCPHIT01", "body": "via mcp short id"})
	raw, _ := json.Marshal(resp)
	if strings.Contains(string(raw), `isError":true`) || resp["error"] != nil {
		t.Fatalf("add_comment by short id failed: %s", raw)
	}
	c, err := svc.ResolveCard(context.Background(), full)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Comments) != 1 || c.Comments[0].Body != "via mcp short id" {
		t.Fatalf("comment not on full-id card: %v", fmt.Sprint(c.Comments))
	}
}
