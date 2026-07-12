package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/mcp"
)

// Fixed MCP tools that mirror core.Service coordination methods.
// Dynamic create_<T>/update_<T> are generated per type.
var fixedMCPTools = []string{
	"workspace", "get_card", "list_cards", "search_cards",
	"claim", "release", "take_next",
	"append_entry", "update_entry", "remove_entry",
	"add_link", "remove_link",
	"add_comment", "edit_comment",
	"upgrade_schema",
	"attach_artifact", "get_artifact",
	"history", "breaches", "events",
}

// intentionallyUnmirroredMutations lists core.Service methods that mutate
// state or process lifecycle but are deliberately NOT MCP tools. Each entry
// needs a one-line reason — treat allowlist edits as contract changes.
var intentionallyUnmirroredMutations = map[string]string{
	"Close":        "process lifecycle of the Service, not agent coordination",
	"DeleteCard":   "destructive; REST/CLI only until an explicit MCP decision",
	"SetArtifacts": "server wiring for the artifacts manager, not a card mutation",
	"ResolveActor": "HTTP header / env resolution helper",
	"Bus":          "internal pub/sub accessor for hooks/SSE",
	"Emitter":      "internal event emitter accessor",
}

func TestMCPToolsList_IncludesP2aMutations(t *testing.T) {
	srv := newMCPServer(t)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	got := toolNames(t, resp)
	for _, want := range []string{
		"release", "remove_entry", "remove_link", "edit_comment", "update_entry", "upgrade_schema",
	} {
		if !got[want] {
			t.Errorf("tools/list missing %q", want)
		}
	}
}

// TestMCPParity_AllowlistAndREADME asserts fixed tools match buildTools + README,
// and that intentionally-unmirrored Service methods stay documented.
func TestMCPParity_AllowlistAndREADME(t *testing.T) {
	srv := newMCPServer(t)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	got := toolNames(t, resp)
	for _, want := range fixedMCPTools {
		if !got[want] {
			t.Errorf("buildTools missing fixed tool %q — add it or remove from fixedMCPTools", want)
		}
	}

	readmePath := filepath.Join(repoRoot(t), "internal", "mcp", "README.md")
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	readme := string(raw)
	for _, want := range fixedMCPTools {
		if !strings.Contains(readme, "`"+want+"`") {
			t.Errorf("internal/mcp/README.md missing tool %q — keep README in sync with buildTools", want)
		}
	}

	for name, reason := range intentionallyUnmirroredMutations {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("intentionallyUnmirroredMutations[%q] needs a non-empty reason", name)
		}
		snake := camelToSnake(name)
		if got[snake] {
			t.Errorf("allowlisted Service method %q is registered as MCP tool %q", name, snake)
		}
	}
}

func TestMCPRelease_HappyAndVersionConflict(t *testing.T) {
	srv := newMCPServer(t)
	card := createProgrammingTask(t, srv, "release-me")
	id := card["id"].(string)
	ver := int(card["version"].(float64))

	claimed := toolCard(t, toolsCall(t, srv, "claim", map[string]any{
		"card_id": id, "version": ver, "status": "in_progress",
	}))
	ver = int(claimed["version"].(float64))

	released := toolCard(t, toolsCall(t, srv, "release", map[string]any{
		"card_id": id, "version": ver,
	}))
	if owner, _ := released["owner"].(string); owner != "" {
		t.Fatalf("release left owner=%q", owner)
	}

	ej := toolErrJSON(t, toolsCall(t, srv, "release", map[string]any{
		"card_id": id, "version": ver,
	}))
	if ej["error"] != "version_conflict" {
		t.Fatalf("stale release error = %v, want version_conflict", ej["error"])
	}
}

func TestMCPEntryLinkComment_HappyAndConflicts(t *testing.T) {
	srv := newMCPServer(t)
	card := createProgrammingTask(t, srv, "entry-link-comment")
	id := card["id"].(string)
	ver := int(card["version"].(float64))

	c1 := toolCard(t, toolsCall(t, srv, "append_entry", map[string]any{
		"card_id": id, "field": "work_log", "version": ver,
		"entry": map[string]any{
			"commit_hash": "abc1234", "notes": "n", "author": "coder-agent",
			"timestamp": "2026-07-11",
		},
	}))
	ver = int(c1["version"].(float64))
	entryID := firstEntryID(t, c1, "work_log")

	c2 := toolCard(t, toolsCall(t, srv, "update_entry", map[string]any{
		"card_id": id, "field": "work_log", "entry_id": entryID, "version": ver,
		"entry": map[string]any{
			"commit_hash": "abc1234", "notes": "updated", "author": "coder-agent",
			"timestamp": "2026-07-11",
		},
	}))
	ver = int(c2["version"].(float64))

	if ej := toolErrJSON(t, toolsCall(t, srv, "update_entry", map[string]any{
		"card_id": id, "field": "work_log", "entry_id": entryID, "version": ver - 1,
		"entry": map[string]any{
			"commit_hash": "abc1234", "notes": "stale", "author": "coder-agent",
			"timestamp": "2026-07-11",
		},
	})); ej["error"] != "version_conflict" {
		t.Fatalf("stale update_entry = %v", ej["error"])
	}

	other := createProgrammingTask(t, srv, "link-target")
	toolCard(t, toolsCall(t, srv, "add_link", map[string]any{
		"card_id": id, "type_id": "related", "target": other["id"].(string),
	}))
	toolCard(t, toolsCall(t, srv, "remove_link", map[string]any{
		"card_id": id, "type_id": "related", "target": other["id"].(string),
	}))

	c4 := toolCard(t, toolsCall(t, srv, "add_comment", map[string]any{
		"card_id": id, "body": "hello",
	}))
	toolCard(t, toolsCall(t, srv, "edit_comment", map[string]any{
		"card_id": id, "comment_id": firstCommentID(t, c4), "body": "hello edited",
	}))

	cur := toolCard(t, toolsCall(t, srv, "get_card", map[string]any{"card_id": id}))
	ver = int(cur["version"].(float64))
	toolCard(t, toolsCall(t, srv, "remove_entry", map[string]any{
		"card_id": id, "field": "work_log", "entry_id": entryID, "version": ver,
	}))

	if ej := toolErrJSON(t, toolsCall(t, srv, "remove_entry", map[string]any{
		"card_id": id, "field": "work_log", "entry_id": entryID, "version": ver,
	})); ej["error"] != "version_conflict" {
		t.Fatalf("stale remove_entry = %v", ej["error"])
	}
}

func TestMCPUpgradeSchema_DryRunByDefault(t *testing.T) {
	srv := newMCPServer(t)
	card := createProgrammingTask(t, srv, "upgrade-dry")
	id := card["id"].(string)
	verBefore := int(card["version"].(float64))

	toolCard(t, toolsCall(t, srv, "upgrade_schema", map[string]any{"card_id": id}))

	live := toolCard(t, toolsCall(t, srv, "get_card", map[string]any{"card_id": id}))
	if int(live["version"].(float64)) != verBefore {
		t.Fatalf("dry-run upgraded live version %v → %v", verBefore, live["version"])
	}

	toolCard(t, toolsCall(t, srv, "upgrade_schema", map[string]any{
		"card_id": id, "confirm": true,
	}))
}

func createProgrammingTask(t *testing.T, srv *mcp.Server, title string) map[string]any {
	t.Helper()
	return toolCard(t, toolsCall(t, srv, "create_programming-task", map[string]any{
		"title": title, "status": "todo", "description": "d", "branch": "b",
	}))
}

func toolNames(t *testing.T, resp map[string]any) map[string]bool {
	t.Helper()
	tools := resp["result"].(map[string]any)["tools"].([]any)
	got := map[string]bool{}
	for _, tr := range tools {
		got[tr.(map[string]any)["name"].(string)] = true
	}
	return got
}

func toolCard(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	res := resp["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("tool error: %v", res["content"])
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var c map[string]any
	if err := json.Unmarshal([]byte(text), &c); err != nil {
		t.Fatalf("card JSON: %v (%s)", err, text)
	}
	return c
}

func toolErrJSON(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	res := resp["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("expected isError, got %v", res)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var ej map[string]any
	if err := json.Unmarshal([]byte(text), &ej); err != nil {
		t.Fatalf("error JSON: %v (%s)", err, text)
	}
	return ej
}

func firstEntryID(t *testing.T, card map[string]any, field string) string {
	t.Helper()
	fields := card["fields"].(map[string]any)
	arr, ok := fields[field].([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("no entries in %s: %v", field, fields[field])
	}
	entry := arr[0].(map[string]any)
	id, _ := entry["entry_id"].(string)
	if id == "" {
		t.Fatalf("entry missing entry_id: %v", entry)
	}
	return id
}

func firstCommentID(t *testing.T, card map[string]any) string {
	t.Helper()
	comments, ok := card["comments"].([]any)
	if !ok || len(comments) == 0 {
		t.Fatalf("no comments: %v", card["comments"])
	}
	id, _ := comments[0].(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("comment missing id: %v", comments[0])
	}
	return id
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}
