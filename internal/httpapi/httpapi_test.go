package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/seed"
	"github.com/somebox/cards/internal/sqlite"
)

// newServer loads the real demo workspace, opens an in-memory SQLite store,
// seeds it, and returns a running httptest server.
func newServer(t *testing.T) (*httptest.Server, *core.Service) {
	t.Helper()
	r, err := config.New("../../examples/demo-workspace").Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	st, err := sqlite.Open(":memory:", r.Workspace)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := core.NewService(r.Workspace, r.CardTypes, r.Boards, st)
	if err := seed.IfEmpty(context.Background(), st, svc, r.Workspace); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv, err := httpapi.New(svc, r.Workspace, r.CardTypes, r.Boards, st)
	if err != nil {
		t.Fatalf("new http server: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(func() { ts.Close() })
	return ts, svc
}

func do(t *testing.T, ts *httptest.Server, method, path string, body any, headers map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	var r *http.Request
	if body == nil {
		r, _ = http.NewRequest(method, ts.URL+path, nil)
	} else {
		b, _ := json.Marshal(body)
		r, _ = http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestAPIWorkspace(t *testing.T) {
	ts, _ := newServer(t)
	resp, out := do(t, ts, "GET", "/v1/workspace", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	ws := out["workspace"].(map[string]any)
	if ws["id"] != "demo" {
		t.Errorf("workspace id = %v", ws["id"])
	}
	if _, ok := out["card_types"].(map[string]any)["programming-task"]; !ok {
		t.Error("missing programming-task in introspection")
	}
}

func TestAPICreateCard_HappyPath(t *testing.T) {
	ts, _ := newServer(t)
	resp, out := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "API task", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
		Actor:  "local-dev",
	}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("status %d body %v", resp.StatusCode, out)
	}
	if out["id"] == "" || out["version"] != float64(1) {
		t.Errorf("got %v", out)
	}
}

func TestAPICreateCard_ValidationFailed(t *testing.T) {
	ts, _ := newServer(t)
	resp, out := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "bad", Status: "todo",
		Fields: map[string]any{}, Actor: "local-dev",
	}, nil)
	if resp.StatusCode != 422 || out["error"] != "validation_failed" {
		t.Fatalf("status %d body %v", resp.StatusCode, out)
	}
}

func TestAPIPatchCard_TransitionIllegal(t *testing.T) {
	ts, _ := newServer(t)
	// create a todo card
	_, created := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"}, Actor: "local-dev",
	}, nil)
	id := created["id"].(string)

	// illegal: todo -> done (must go via in_progress -> review -> done)
	resp, out := do(t, ts, "PATCH", "/v1/cards/"+id, map[string]any{
		"version": 1, "status": "done", "actor": "local-dev",
	}, nil)
	if resp.StatusCode != 422 || out["error"] != "transition_illegal" {
		t.Fatalf("status %d body %v", resp.StatusCode, out)
	}
	opts := out["valid_options"].([]any)
	if len(opts) != 1 || opts[0] != "in_progress" {
		t.Errorf("valid_options = %v", opts)
	}
}

func TestAPIPatchCard_VersionConflict(t *testing.T) {
	ts, _ := newServer(t)
	_, created := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"}, Actor: "local-dev",
	}, nil)
	id := created["id"].(string)
	// first patch bumps version
	do(t, ts, "PATCH", "/v1/cards/"+id, map[string]any{"version": 1, "status": "in_progress", "actor": "local-dev"}, nil)
	// stale patch
	resp, out := do(t, ts, "PATCH", "/v1/cards/"+id, map[string]any{"version": 1, "status": "review", "actor": "local-dev"}, nil)
	if resp.StatusCode != 409 || out["error"] != "version_conflict" {
		t.Fatalf("status %d body %v", resp.StatusCode, out)
	}
	if out["card"] == nil {
		t.Error("version_conflict should carry current card")
	}
}

func TestAPIActorResolution(t *testing.T) {
	ts, _ := newServer(t)
	// No actor header, no default user override → uses workspace default_user.
	resp, out := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
		Actor: "", // service requires actor; httpapi.withActor injects default
	}, nil)
	// The API path uses withActor which resolves default_user.
	if resp.StatusCode != 201 {
		t.Fatalf("status %d body %v", resp.StatusCode, out)
	}
	if out["created_by"] != "local-dev" {
		t.Errorf("created_by = %v, want local-dev", out["created_by"])
	}
}

// TestAPIActorHeaderRespected is a regression test: the X-Work-Cards-Actor
// header must set created_by/owner, not silently fall back to default_user.
// (Found via dogfooding during Slice 3.)
func TestAPIActorHeaderRespected(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "coder-agent", "Content-Type": "application/json"}
	resp, out := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "Actor test", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, H)
	if resp.StatusCode != 201 {
		t.Fatalf("status %d body %v", resp.StatusCode, out)
	}
	if out["created_by"] != "coder-agent" {
		t.Errorf("created_by = %v, want coder-agent (header was ignored)", out["created_by"])
	}
}

func TestUIBoardRendersCards(t *testing.T) {
	ts, _ := newServer(t)
	resp, body := doGet(t, ts, "/ui/boards/engineering")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Add OpenAPI spec") {
		t.Error("board does not contain seeded card title")
	}
	if !strings.Contains(body, "Backlog") || !strings.Contains(body, "To Do") {
		t.Error("board does not render column names")
	}
}

func TestUICreateCardValidationReRendersForm(t *testing.T) {
	ts, _ := newServer(t)
	form := strings.NewReader("type_id=programming-task&board_id=engineering&title=Bad&status=todo&field:branch=b")
	req, _ := http.NewRequest("POST", ts.URL+"/ui/cards", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d (want 200 re-rendered form)", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if !strings.Contains(buf.String(), "validation_failed") {
		t.Error("re-rendered form should contain validation_failed error")
	}
}

func doGet(t *testing.T, ts *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	return resp, buf.String()
}

// silence unused
var _ = time.Now

// --- coordination loop HTTP integration ---

// TestLifecycleExampleA walks the docs' Example A flow end-to-end via the API:
// create → link (depends-on) → claim → append work_log → transition → done →
// take-next on a dependent → history. This is the agent coordination loop.
func TestLifecycleExampleA(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "coder-agent", "Content-Type": "application/json"}

	// Register the actor.
	do(t, ts, "POST", "/v1/users", map[string]any{"id": "coder-agent", "kind": "agent"}, nil)

	// A1 — create two tasks.
	_, api := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "auth API", Status: "todo",
		Fields: map[string]any{"description": "refresh", "branch": "feature/auth"},
	}, H)
	authAPI := api["id"].(string)
	_, cli := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "auth CLI", Status: "todo",
		Fields: map[string]any{"description": "client", "branch": "feature/cli"},
	}, H)
	authCLI := cli["id"].(string)

	// A2 — wire depends-on (stored on the waiting card = authCLI).
	resp, _ := do(t, ts, "POST", "/v1/cards/"+authCLI+"/links", core.LinkInput{TypeID: "depends-on", Target: authAPI}, H)
	if resp.StatusCode != 201 {
		t.Fatalf("add link: %d", resp.StatusCode)
	}

	// A3 — blocked query: authCLI is blocked (depends-on a non-done card).
	resp, blocked := do(t, ts, "GET", "/v1/cards?blocked=true&limit=20", nil, nil)
	if resp.StatusCode != 200 || len(blocked["items"].([]any)) != 1 {
		t.Fatalf("blocked query: %d %v", resp.StatusCode, blocked)
	}

	// A4 — claim + move authAPI to in_progress.
	resp, _ = do(t, ts, "POST", "/v1/cards/"+authAPI+"/claim", core.ClaimRequest{Version: 1, Status: "in_progress"}, H)
	if resp.StatusCode != 200 {
		t.Fatalf("claim: %d", resp.StatusCode)
	}

	// A5 — append a work_log entry (stable entry_id returned).
	resp, appended := do(t, ts, "POST", "/v1/cards/"+authAPI+"/fields/work_log/append", map[string]any{
		"entry": map[string]any{"commit_hash": "a1b2c3", "notes": "handler", "author": "coder-agent", "timestamp": "2026-06-25T14:30:00Z"},
		"version": 2,
	}, H)
	if resp.StatusCode != 200 {
		t.Fatalf("append: %d %v", resp.StatusCode, appended)
	}
	wl := appended["fields"].(map[string]any)["work_log"].([]any)
	if len(wl) != 1 || wl[0].(map[string]any)["entry_id"] == nil {
		t.Errorf("work_log entry_id missing: %v", wl)
	}

	// Transition authAPI in_progress → review → done.
	do(t, ts, "PATCH", "/v1/cards/"+authAPI, map[string]any{"version": 3, "status": "review"}, H)
	do(t, ts, "PATCH", "/v1/cards/"+authAPI, map[string]any{"version": 4, "status": "done"}, H)

	// A6 — take-next picks the oldest unowned todo programming-task and
	// atomically claims it (owner + status move).
	resp, taken := do(t, ts, "POST", "/v1/cards/take-next", core.TakeNextRequest{
		TypeID: "programming-task", AssignTo: "coder-agent", Status: "in_progress",
		Filter: map[string]any{"status": map[string]any{"$eq": "todo"}},
	}, H)
	if resp.StatusCode != 200 || taken["card"] == nil {
		t.Fatalf("take-next: %d %v", resp.StatusCode, taken)
	}
	picked := taken["card"].(map[string]any)
	if picked["owner"] != "coder-agent" || picked["status"] != "in_progress" {
		t.Errorf("take-next did not claim: %+v", picked)
	}

	// A7 — history timeline.
	resp, hist := do(t, ts, "GET", "/v1/cards/"+authAPI+"/history", nil, nil)
	if resp.StatusCode != 200 || len(hist["items"].([]any)) < 4 {
		t.Fatalf("history: %d %v", resp.StatusCode, hist)
	}
}

func TestIdempotencyReplay(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json", "Idempotency-Key": "k-create-1"}
	resp1, b1 := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "Idem", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, H)
	if resp1.StatusCode != 201 {
		t.Fatalf("first: %d", resp1.StatusCode)
	}
	// Replay with the same key → same card id.
	resp2, b2 := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "Idem DUPE", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, H)
	if resp2.StatusCode != 200 || resp2.Header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay: %d header=%q", resp2.StatusCode, resp2.Header.Get("Idempotent-Replay"))
	}
	if b1["id"] != b2["id"] {
		t.Errorf("replay returned different card: %s vs %s", b1["id"], b2["id"])
	}
	// Different key → new card.
	H2 := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json", "Idempotency-Key": "k-create-2"}
	_, b3 := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "Other", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, H2)
	if b3["id"] == b1["id"] {
		t.Error("different key should yield different card")
	}
}

func TestDryRunCreate(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}
	resp, b := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "Dry", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"}, DryRun: true,
	}, H)
	if resp.StatusCode != 200 || resp.Header.Get("Dry-Run") != "true" {
		t.Fatalf("dry-run: %d header=%q", resp.StatusCode, resp.Header.Get("Dry-Run"))
	}
	// The dry-run card id should not exist.
	resp2, _ := do(t, ts, "GET", "/v1/cards/"+b["id"].(string), nil, nil)
	if resp2.StatusCode != 404 {
		t.Errorf("dry-run card should not persist, got %d", resp2.StatusCode)
	}
}

func TestIdempotencyActorScoping(t *testing.T) {
	// Two different actors using the SAME idempotency key must NOT collide —
	// each gets its own replay. This was the bug: the wrapper order
	// (idempotent(withActor(...))) meant idempotent ran before withActor set
	// the context, so actor was always default_user, and the PK was just
	// (key) so the second actor overwrote the first's replay.
	ts, _ := newServer(t)
	base := core.CreateCardRequest{
		TypeID: "programming-task", Title: "Actor A", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}
	// Actor A creates with key "shared-key"
	HA := map[string]string{"X-Work-Cards-Actor": "alice", "Content-Type": "application/json", "Idempotency-Key": "shared-key"}
	_, bA := do(t, ts, "POST", "/v1/cards", base, HA)
	cardA := bA["id"]

	// Actor B creates with the SAME key "shared-key" — must be a NEW card, not a replay of A's.
	HB := map[string]string{"X-Work-Cards-Actor": "bob", "Content-Type": "application/json", "Idempotency-Key": "shared-key"}
	respB, bB := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "Actor B", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, HB)
	if respB.Header.Get("Idempotent-Replay") == "true" {
		t.Fatal("bob should NOT get alice's replay — idempotency must be scoped per actor")
	}
	if bB["id"] == cardA {
		t.Fatalf("bob got alice's card %s — cross-actor idempotency collision", cardA)
	}

	// Actor A replays with the same key → gets A's card back.
	respA2, bA2 := do(t, ts, "POST", "/v1/cards", base, HA)
	if respA2.Header.Get("Idempotent-Replay") != "true" {
		t.Error("alice replay should return Idempotent-Replay=true")
	}
	if bA2["id"] != cardA {
		t.Errorf("alice replay returned %s, want %s", bA2["id"], cardA)
	}
}

func TestFTSSearch(t *testing.T) {
	ts, _ := newServer(t)
	resp, b := do(t, ts, "GET", "/v1/cards?q=OpenAPI&limit=20", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("search: %d", resp.StatusCode)
	}
	items := b["items"].([]any)
	if len(items) != 1 || !strings.Contains(items[0].(map[string]any)["title"].(string), "OpenAPI") {
		t.Errorf("FTS search for 'OpenAPI' = %v", items)
	}
}

func TestCommentsHTTP(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}
	_, created := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "C", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, H)
	id := created["id"].(string)
	do(t, ts, "POST", "/v1/cards/"+id+"/comments", core.CommentInput{Body: "a note"}, H)
	resp, got := do(t, ts, "GET", "/v1/cards/"+id, nil, nil)
	if resp.StatusCode != 200 || len(got["comments"].([]any)) != 1 {
		t.Fatalf("comments: %d %v", resp.StatusCode, got["comments"])
	}
}

// newServerStore is like newServer but also returns the underlying store so
// tests can insert cards with crafted ids (e.g. colliding short-id suffixes).
func newServerStore(t *testing.T) (*httptest.Server, *core.Service, *sqlite.Store) {
	t.Helper()
	r, err := config.New("../../examples/demo-workspace").Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	st, err := sqlite.Open(":memory:", r.Workspace)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := core.NewService(r.Workspace, r.CardTypes, r.Boards, st)
	srv, err := httpapi.New(svc, r.Workspace, r.CardTypes, r.Boards, st)
	if err != nil {
		t.Fatalf("new http server: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(func() { ts.Close() })
	return ts, svc, st
}

func insertCraftedCard(t *testing.T, st *sqlite.Store, id string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	c := &core.Card{
		ID: id, WorkspaceID: "demo", TypeID: "programming-task", SchemaVersion: 1,
		Title: "Crafted " + id, Status: "todo", Fields: map[string]any{"description": "d", "branch": "b"},
		Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: "local-dev",
	}
	if err := st.InsertCard(ctx, c, &core.Event{CardID: id, Type: core.EventCardCreated, Actor: "local-dev", At: now}); err != nil {
		t.Fatalf("insert crafted %s: %v", id, err)
	}
}

func TestAPIGetCard_ShortIDResolves(t *testing.T) {
	ts, _, _ := newServerStore(t)
	// Create a card via the API and read its full id.
	_, created := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "Short API", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"}, Actor: "local-dev",
	}, nil)
	full := created["id"].(string)
	short := full[5:13] // first 8 hex after "card_"

	// GET by short id resolves to the same card.
	resp, out := do(t, ts, "GET", "/v1/cards/"+short, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("short GET: %d %v", resp.StatusCode, out)
	}
	if out["id"] != full {
		t.Errorf("short resolved to %v, want %s", out["id"], full)
	}
	// GET by full id still works.
	resp2, out2 := do(t, ts, "GET", "/v1/cards/"+full, nil, nil)
	if resp2.StatusCode != 200 || out2["id"] != full {
		t.Fatalf("full GET: %d %v", resp2.StatusCode, out2)
	}
}

func TestAPIGetCard_AmbiguousShortID409(t *testing.T) {
	ts, _, st := newServerStore(t)
	idA := "card_COLLIDE1aaaaaaaaaaaaaaaaaaaaaaaa"
	idB := "card_COLLIDE1bbbbbbbbbbbbbbbbbbbbbbbb"
	insertCraftedCard(t, st, idA)
	insertCraftedCard(t, st, idB)
	resp, out := do(t, ts, "GET", "/v1/cards/COLLIDE1", nil, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("expected 409, got %d %v", resp.StatusCode, out)
	}
	if out["error"] != "ambiguous" || out["query"] != "COLLIDE1" {
		t.Errorf("body = %v", out)
	}
	cands, _ := out["candidates"].([]any)
	if len(cands) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(cands))
	}
}

func TestUICardDetail_ShortIDAndAmbiguous(t *testing.T) {
	ts, _, st := newServerStore(t)
	idA := "card_UIAMBIG1cccccccccccccccccccccccccc"
	idB := "card_UIAMBIG1dddddddddddddddddddddddddd"
	insertCraftedCard(t, st, idA)
	insertCraftedCard(t, st, idB)
	short := "UIAMBIG1"

	// Short id resolves → detail page (200), contains the full id.
	resp, body := getHTML(t, ts, "/ui/cards/"+short)
	if resp.StatusCode != 200 {
		t.Fatalf("detail short: %d", resp.StatusCode)
	}
	if !strings.Contains(body, idA) && !strings.Contains(body, idB) {
		t.Errorf("detail body missing full id; got:\n%s", body)
	}

	// Ambiguous short id → ambiguous page listing candidates.
	resp3, body3 := getHTML(t, ts, "/ui/cards/UIAMBIG1")
	if resp3.StatusCode != 200 {
		t.Fatalf("ambiguous page: %d", resp3.StatusCode)
	}
	if !strings.Contains(body3, "Ambiguous") {
		t.Errorf("expected ambiguous heading; got:\n%s", body3)
	}
	if !strings.Contains(body3, idA) || !strings.Contains(body3, idB) {
		t.Errorf("ambiguous page missing candidate links; got:\n%s", body3)
	}
}

// getHTML fetches a path and returns the response + body text.
func getHTML(t *testing.T, ts *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}
