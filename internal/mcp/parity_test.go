package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/mcp"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
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

// mirroredServiceMethods maps every core.Service method that IS exposed over
// MCP to the tool that serves it ("dynamic:" entries are generated per type).
// Together with intentionallyUnmirroredMutations this forms a total partition
// of core.Service's exported method set, enforced by reflection in
// TestMCPParity_ServiceMethodPartition — an unclassified new method fails CI
// until a human decides mirror-or-exclude.
var mirroredServiceMethods = map[string]string{
	"Workspace":      "workspace",
	"GetCard":        "get_card",
	"ListCards":      "list_cards", // also backs search_cards (Q param)
	"Claim":          "claim",
	"Release":        "release",
	"TakeNext":       "take_next",
	"AppendEntry":    "append_entry",
	"UpdateEntry":    "update_entry",
	"RemoveEntry":    "remove_entry",
	"AddLink":        "add_link",
	"RemoveLink":     "remove_link",
	"AddComment":     "add_comment",
	"EditComment":    "edit_comment",
	"UpgradeSchema":  "upgrade_schema",
	"AddArtifact":    "attach_artifact",
	"OpenArtifact":   "get_artifact",
	"History":        "history",
	"Breaches":       "breaches",
	"ListEventsPage": "events",
	"CreateCard":     "dynamic:create_<type>",
	"PatchCard":      "dynamic:update_<type>",
}

// intentionallyUnmirroredMutations lists core.Service methods that are
// deliberately NOT MCP tools. Each entry needs a one-line reason — treat
// allowlist edits as contract changes.
var intentionallyUnmirroredMutations = map[string]string{
	"Close":        "process lifecycle of the Service, not agent coordination",
	"DeleteCard":   "destructive; REST/CLI only until an explicit MCP decision",
	"SetArtifacts": "server wiring for the artifacts manager, not a card mutation",
	"ResolveActor": "HTTP header / env resolution helper",
	"ResolveCard":  "id/prefix resolution helper used inside other operations",
	"Bus":          "internal pub/sub accessor for hooks/SSE",
	"Emitter":      "internal event emitter accessor",
	"ListEvents":   "unpaged variant; the events tool serves ListEventsPage",
}

// TestMCPParity_ServiceMethodPartition reflects over *core.Service and asserts
// every exported method is classified exactly once: mirrored to a registered
// tool, or allowlisted with a reason. This is the drift guard the P2a card
// promised — hand-maintained lists alone cannot fail on a new Service method.
func TestMCPParity_ServiceMethodPartition(t *testing.T) {
	srv := newMCPServer(t)
	got := toolNames(t, call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))

	typ := reflect.TypeOf(&core.Service{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		seen[name] = true
		tool, mirrored := mirroredServiceMethods[name]
		_, excluded := intentionallyUnmirroredMutations[name]
		switch {
		case mirrored && excluded:
			t.Errorf("core.Service.%s is in both mirroredServiceMethods and the allowlist — pick one", name)
		case !mirrored && !excluded:
			t.Errorf("core.Service.%s is UNCLASSIFIED — add it to mirroredServiceMethods (with its MCP tool) or intentionallyUnmirroredMutations (with a reason)", name)
		case mirrored && !strings.HasPrefix(tool, "dynamic:") && !got[tool]:
			t.Errorf("core.Service.%s claims MCP tool %q, but tools/list does not register it", name, tool)
		}
	}
	// Stale entries rot the partition just as silently as missing ones.
	for name := range mirroredServiceMethods {
		if !seen[name] {
			t.Errorf("mirroredServiceMethods[%q] names a method core.Service no longer has", name)
		}
	}
	for name := range intentionallyUnmirroredMutations {
		if !seen[name] {
			t.Errorf("intentionallyUnmirroredMutations[%q] names a method core.Service no longer has", name)
		}
	}
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

// newMCPServerWithStaleCard builds an MCP server whose programming-task type
// is at schema_version 2 (with the "branch" field REMOVED), over a store
// holding one card still at schema_version 1 with branch set. The upgrade is
// therefore a real migration: dry-run must report would_drop=["branch"]
// without persisting, confirm:true must persist it. A card already at the
// current version would hit UpgradeSchema's no-op early-return and prove
// nothing (the prior version of this test did exactly that).
func newMCPServerWithStaleCard(t *testing.T) (*mcp.Server, string) {
	t.Helper()
	r, err := config.New("../../examples/demo-workspace").Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	st := sqlitetest.Open(t, r.Workspace, 1)

	// Generation 1: stock types; create the card at schema_version 1.
	svc1 := core.NewService(r.Workspace, r.CardTypes, r.Boards, st)
	card, err := svc1.CreateCard(context.Background(), core.CreateCardRequest{
		TypeID: "programming-task", Title: "stale-schema", Actor: "tester",
		Fields: map[string]any{"description": "d", "branch": "feat/x"},
	})
	if err != nil {
		t.Fatalf("create v1 card: %v", err)
	}

	// Generation 2: same store, programming-task bumped to v2 without "branch".
	v2 := *r.CardTypes["programming-task"]
	v2.SchemaVersion = 2
	fields := make([]core.FieldDef, 0, len(v2.Fields))
	for _, f := range v2.Fields {
		if f.ID != "branch" {
			fields = append(fields, f)
		}
	}
	v2.Fields = fields
	types2 := map[string]*core.CardType{}
	for k, v := range r.CardTypes {
		types2[k] = v
	}
	types2["programming-task"] = &v2
	svc2 := core.NewService(r.Workspace, types2, r.Boards, st)
	return mcp.New(svc2, r.Workspace, types2, r.Boards, "coder-agent"), card.ID
}

func TestMCPUpgradeSchema_DryRunByDefault(t *testing.T) {
	srv, id := newMCPServerWithStaleCard(t)

	live := toolCard(t, toolsCall(t, srv, "get_card", map[string]any{"card_id": id}))
	verBefore := int(live["version"].(float64))
	if sv := int(live["schema_version"].(float64)); sv != 1 {
		t.Fatalf("fixture card schema_version = %d, want 1 (stale)", sv)
	}

	// Default (no confirm) → preview only, with the promised drop list.
	preview := toolCard(t, toolsCall(t, srv, "upgrade_schema", map[string]any{"card_id": id}))
	if preview["dry_run"] != true {
		t.Fatalf("dry-run response missing dry_run:true: %v", preview)
	}
	drops, _ := preview["would_drop"].([]any)
	if len(drops) != 1 || drops[0] != "branch" {
		t.Fatalf("would_drop = %v, want [branch]", preview["would_drop"])
	}

	live = toolCard(t, toolsCall(t, srv, "get_card", map[string]any{"card_id": id}))
	if int(live["version"].(float64)) != verBefore || int(live["schema_version"].(float64)) != 1 {
		t.Fatalf("dry-run mutated the card: version %v schema_version %v", live["version"], live["schema_version"])
	}

	// confirm:true → applies.
	applied := toolCard(t, toolsCall(t, srv, "upgrade_schema", map[string]any{"card_id": id, "confirm": true}))
	if sv := int(applied["schema_version"].(float64)); sv != 2 {
		t.Fatalf("confirm did not upgrade: schema_version = %d, want 2", sv)
	}
	if _, hasBranch := applied["fields"].(map[string]any)["branch"]; hasBranch {
		t.Fatal("confirm kept dropped field 'branch'")
	}
	live = toolCard(t, toolsCall(t, srv, "get_card", map[string]any{"card_id": id}))
	if sv := int(live["schema_version"].(float64)); sv != 2 {
		t.Fatalf("upgrade not persisted: schema_version = %d, want 2", sv)
	}
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
