package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/mcp"
	"github.com/somebox/cards/internal/seed"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

func newMCPServer(t *testing.T) *mcp.Server {
	t.Helper()
	r, err := config.New("../../examples/demo-workspace").Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	st := sqlitetest.Open(t, r.Workspace, 1)
	svc := core.NewService(r.Workspace, r.CardTypes, r.Boards, st)
	if err := seed.IfEmpty(context.Background(), st, svc, r.Workspace); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return mcp.New(svc, r.Workspace, r.CardTypes, r.Boards, "coder-agent")
}

// call pipes a JSON-RPC request through the server and returns the first
// response. The input is closed so Serve returns after processing.
func call(t *testing.T, srv *mcp.Server, req string) map[string]any {
	t.Helper()
	in := io.NopCloser(strings.NewReader(req + "\n"))
	out := new(bytes.Buffer)
	if err := srv.ServeOn(in, out); err != nil && err != io.EOF {
		t.Fatalf("serve: %v", err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			return m
		}
	}
	t.Fatalf("no response (out=%q)", out.String())
	return nil
}

func TestMCPToolsList_GeneratesPerTypeTools(t *testing.T) {
	srv := newMCPServer(t)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if resp["error"] != nil {
		t.Fatalf("tools/list error: %v", resp["error"])
	}
	res := resp["result"].(map[string]any)
	tools := res["tools"].([]any)
	names := map[string]bool{}
	for _, t := range tools {
		names[t.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{
		"create_programming-task", "update_programming-task",
		"create_research-goal", "update_research-goal",
		"workspace", "get_card", "list_cards", "claim", "take_next",
		"append_entry", "add_link", "add_comment", "history",
	} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestMCPCreateTool_RejectsBadEnum(t *testing.T) {
	srv := newMCPServer(t)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_programming-task","arguments":{"title":"x","status":"NOPE","description":"d","branch":"b"}}}`)
	res := resp["result"].(map[string]any)
	if !res["isError"].(bool) {
		t.Fatal("expected isError for bad enum")
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var ej map[string]any
	_ = json.Unmarshal([]byte(text), &ej)
	if ej["error"] != "unknown_enum" || ej["field"] != "status" {
		t.Errorf("error = %v field = %v", ej["error"], ej["field"])
	}
}

func TestMCPCreateTool_HappyPath(t *testing.T) {
	srv := newMCPServer(t)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_programming-task","arguments":{"title":"MCP task","status":"todo","description":"d","branch":"b"}}}`)
	res := resp["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("create failed: %v", res["content"])
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var c map[string]any
	_ = json.Unmarshal([]byte(text), &c)
	if c["title"] != "MCP task" || c["created_by"] != "coder-agent" {
		t.Errorf("card = %+v", c)
	}
}

func TestMCPTakeNextTool(t *testing.T) {
	srv := newMCPServer(t)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"take_next","arguments":{"type_id":"programming-task","assign_to":"coder-agent","status":"in_progress","filter":{"status":{"$eq":"todo"}}}}}`)
	res := resp["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var env map[string]any
	_ = json.Unmarshal([]byte(text), &env)
	card := env["card"]
	if card == nil {
		t.Fatal("take_next returned null card")
	}
	c := card.(map[string]any)
	if c["owner"] != "coder-agent" || c["status"] != "in_progress" {
		t.Errorf("claimed = %+v", c)
	}
}

// The breaches + events tools are advertised and callable, returning the
// same reports the CLI/HTTP surfaces expose.
func TestMCPToolsList_IncludesBreachesAndEvents(t *testing.T) {
	srv := newMCPServer(t)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	tools := resp["result"].(map[string]any)["tools"].([]any)
	got := map[string]bool{}
	for _, tr := range tools {
		got[tr.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"breaches", "events"} {
		if !got[want] {
			t.Errorf("tools/list missing %q", want)
		}
	}
}

func TestMCPBreachesTool(t *testing.T) {
	srv := newMCPServer(t)
	// Create A and B, block A on B (not done) → a card_blocked breach.
	mk := func(title string) string {
		r := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_task","arguments":{"title":"`+title+`"}}}`)
		res := r["result"].(map[string]any)
		if res["isError"] == true {
			t.Fatalf("create %s failed: %v", title, res["content"])
		}
		text := res["content"].([]any)[0].(map[string]any)["text"].(string)
		var c map[string]any
		_ = json.Unmarshal([]byte(text), &c)
		return c["id"].(string)
	}
	idA, idB := mk("A"), mk("B")
	call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"add_link","arguments":{"card_id":"`+idA+`","type_id":"blocked-by","target":"`+idB+`"}}}`)

	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"breaches","arguments":{}}}`)
	res := resp["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var report map[string]any
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("breaches result not JSON: %v", err)
	}
	items, _ := report["items"].([]any)
	found := false
	for _, it := range items {
		m := it.(map[string]any)
		if m["type"] == "card_blocked" && m["card_id"] == idA {
			found = true
		}
	}
	if !found {
		t.Fatalf("breaches missing card_blocked for %s: %v", idA, items)
	}
}

func TestMCPEventsTool(t *testing.T) {
	srv := newMCPServer(t)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"events","arguments":{"limit":5}}}`)
	res := resp["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("events failed: %v", res["content"])
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var page map[string]any
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("events result not JSON: %v", err)
	}
	if _, ok := page["items"]; !ok {
		t.Errorf("events result missing items envelope: %v", page)
	}
}

func TestMCPInitialize(t *testing.T) {
	srv := newMCPServer(t)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	res := resp["result"].(map[string]any)
	if res["protocolVersion"] == "" {
		t.Error("missing protocolVersion")
	}
}

// TestMCPMultiValueFieldSchema pins the array schema for multiple fields
// (frontend-rebuild Phase 3): a multiple enum introspects as
// {type:"array", items:{type:"string", enum:[...]}} so agents send arrays,
// not scalars. Uses the real demo workspace's frontend-task.platforms field.
func TestMCPMultiValueFieldSchema(t *testing.T) {
	srv := newMCPServer(t)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	res := resp["result"].(map[string]any)
	for _, tl := range res["tools"].([]any) {
		tool := tl.(map[string]any)
		if tool["name"] != "create_frontend-task" {
			continue
		}
		props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		p, ok := props["platforms"].(map[string]any)
		if !ok {
			t.Fatalf("create_frontend-task schema has no platforms property: %v", props)
		}
		if p["type"] != "array" {
			t.Fatalf("platforms schema type = %v, want array (multiple:true)", p["type"])
		}
		items, ok := p["items"].(map[string]any)
		if !ok || items["type"] != "string" {
			t.Fatalf("platforms items schema wrong: %v", p["items"])
		}
		if opts, ok := items["enum"].([]any); !ok || len(opts) != 3 {
			t.Fatalf("platforms items enum = %v, want the 3 options", items["enum"])
		}
		// And the single-value enum next to it stays scalar (regression).
		if s, ok := props["surface"].(map[string]any); !ok || s["type"] != "string" {
			t.Fatalf("surface (single enum) schema changed: %v", props["surface"])
		}
		return
	}
	t.Fatal("create_frontend-task tool not found")
}
