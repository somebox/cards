package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/agentguide"
	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/mcp"
	"github.com/somebox/cards/internal/seed"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

// The initialize handshake is the only place an MCP client is told how to work
// the board — the tool schemas describe the surface, not the loop. An empty
// instructions field means every agent starts by guessing.
func TestInitializeServesInstructions(t *testing.T) {
	srv := newMCPServer(t)
	res := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)

	result, ok := res["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize returned no result: %v", res)
	}
	got, _ := result["instructions"].(string)
	if strings.TrimSpace(got) == "" {
		t.Fatal("initialize returned no instructions")
	}
	if got != agentguide.MCPInstructions() {
		t.Error("initialize instructions differ from agentguide.MCPInstructions()")
	}
}

// serverInfo.version answered "poc" on every build including tagged releases,
// so a client could not tell what it was talking to.
func TestInitializeReportsRealVersion(t *testing.T) {
	r, err := config.New("../../examples/demo-workspace").Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	st := sqlitetest.Open(t, r.Workspace, 1)
	svc := core.NewService(r.Workspace, r.CardTypes, r.Boards, st)
	if err := seed.IfEmpty(context.Background(), st, svc, r.Workspace); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := mcp.New(svc, r.Workspace, r.CardTypes, r.Boards, "coder-agent", mcp.WithVersion("v9.9.9-test"))

	res := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	result := res["result"].(map[string]any)
	info, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("no serverInfo: %v", result)
	}
	if got := info["version"]; got != "v9.9.9-test" {
		t.Errorf("serverInfo.version = %v, want the injected build version", got)
	}
}

// Without the option the server must still not claim "poc".
func TestInitializeVersionDefaultIsNotPoc(t *testing.T) {
	srv := newMCPServer(t)
	res := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	info := res["result"].(map[string]any)["serverInfo"].(map[string]any)
	if got := info["version"]; got == "poc" {
		t.Error(`serverInfo.version is still the "poc" stub`)
	}
}
