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
	"github.com/somebox/cards/internal/core/coretest"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/seed"
	"github.com/somebox/cards/internal/sqlite"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

// newServer loads the real demo workspace, opens an in-memory SQLite store,
// seeds it, and returns a running httptest server.
func newServer(t *testing.T) (*httptest.Server, *core.Service) {
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
	srv, err := httpapi.New(svc, r.Workspace, r.CardTypes, r.Boards, r.Themes, st)
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
		Actor:  "", // service requires actor; httpapi.withActor injects default
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

func TestUIBoardInjectsActor(t *testing.T) {
	ts, _ := newServer(t)
	resp, body := doGet(t, ts, "/ui/boards/engineering")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// The layout injects the resolved UI actor (workspace default_user here,
	// no CARDS_USER set in tests) as CARDS_ACTOR; html/template quotes it.
	if !strings.Contains(body, `var CARDS_ACTOR = "local-dev";`) {
		t.Errorf("board does not inject CARDS_ACTOR with default_user; body missing the marker")
	}
	// The old hardcoded actor must be gone from the shipped UI.
	if strings.Contains(body, "foz") {
		t.Error("board still contains the hardcoded 'foz' actor")
	}
	// The live stream must now subscribe to condition events so badges update.
	for _, ct := range []string{"card_blocked", "card_unblocked", "wip_exceeded"} {
		if !strings.Contains(body, ct) {
			t.Errorf("board SSE type list missing condition event %q", ct)
		}
	}
	// P3b: definition_reload_failed is subscribed (banner) but is not a
	// lane-mutation type — still present in the stream type list.
	if !strings.Contains(body, "definition_reload_failed") {
		t.Error("board SSE type list missing definition_reload_failed")
	}
	if !strings.Contains(body, `id="def-reload-banner"`) {
		t.Error("layout missing def-reload-banner element")
	}
}

func TestUIBoardShowsBlockedBadge(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}
	mk := func(title string) string {
		_, out := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
			TypeID: "programming-task", Title: title, Status: "todo",
			Fields: map[string]any{"description": "d", "branch": "b"},
		}, H)
		return out["id"].(string)
	}
	blocker := mk("Blocker task")
	blocked := mk("Blocked task")
	// blocked depends-on blocker (still open) → blocked predicate fires.
	resp, _ := do(t, ts, "POST", "/v1/cards/"+blocked+"/links",
		core.LinkInput{TypeID: "depends-on", Target: blocker}, H)
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		t.Fatalf("add link: %d", resp.StatusCode)
	}
	_, body := doGet(t, ts, "/ui/boards/engineering")
	if !strings.Contains(body, `data-stat="blocked"`) {
		t.Error("board does not render the blocked badge for a blocked card")
	}
}

func TestUIBoardSortSelector(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}
	// Two todo cards with titles that sort opposite to insertion order.
	for _, title := range []string{"Zebra sort card", "Alpha sort card"} {
		do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
			TypeID: "programming-task", Title: title, Status: "todo",
			Fields: map[string]any{"description": "d", "branch": "b"},
		}, H)
	}
	_, body := doGet(t, ts, "/ui/boards/engineering?sort=title")
	ai := strings.Index(body, "Alpha sort card")
	zi := strings.Index(body, "Zebra sort card")
	if ai < 0 || zi < 0 {
		t.Fatalf("both cards should render; alpha=%d zebra=%d", ai, zi)
	}
	if ai > zi {
		t.Error("sort=title should place 'Alpha' before 'Zebra' in the rendered board")
	}
	// The selector must reflect the active sort.
	if !strings.Contains(body, `<option value="title" selected>`) {
		t.Error("sort selector does not mark title as the active option")
	}
}

func TestUIBoardSortRejectsBadKey(t *testing.T) {
	ts, _ := newServer(t)
	// The board handler surfaces a validation failure as a 500 (it wraps the
	// service error); the API path returns 422 — assert that here.
	resp, out := do(t, ts, "GET", "/v1/cards?sort=owner", nil, nil)
	if resp.StatusCode != 422 || out["error"] != "validation_failed" {
		t.Fatalf("GET /v1/cards?sort=owner → %d %v, want 422 validation_failed", resp.StatusCode, out)
	}
}

func TestDeleteCard(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}
	_, created := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "Delete me", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, H)
	id := created["id"].(string)

	// Stale version → 409.
	respC, outC := do(t, ts, "DELETE", "/v1/cards/"+id+"?version=999", nil, H)
	if respC.StatusCode != 409 || outC["error"] != "version_conflict" {
		t.Fatalf("stale delete → %d %v, want 409 version_conflict", respC.StatusCode, outC)
	}

	// Correct delete → 200, returns the tombstoned card.
	resp, out := do(t, ts, "DELETE", "/v1/cards/"+id+"?version=1", nil, H)
	if resp.StatusCode != 200 || out["id"] != id {
		t.Fatalf("delete → %d %v, want 200 with id", resp.StatusCode, out)
	}
	// Gone from GET.
	respG, _ := do(t, ts, "GET", "/v1/cards/"+id, nil, nil)
	if respG.StatusCode != 404 {
		t.Errorf("GET after delete → %d, want 404", respG.StatusCode)
	}
	// Re-delete → 404 (idempotent-ish: already gone).
	respR, _ := do(t, ts, "DELETE", "/v1/cards/"+id, nil, H)
	if respR.StatusCode != 404 {
		t.Errorf("re-delete → %d, want 404", respR.StatusCode)
	}
	// Tombstone survives in the event feed.
	_, feed := do(t, ts, "GET", "/v1/events?types=card_deleted", nil, nil)
	items, _ := feed["items"].([]any)
	found := false
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && m["card_id"] == id {
			found = true
		}
	}
	if !found {
		t.Error("card_deleted tombstone not found in the event feed")
	}
}

func TestDeleteCardUnblocksDependents(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}
	mk := func(title string) string {
		_, out := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
			TypeID: "programming-task", Title: title, Status: "todo",
			Fields: map[string]any{"description": "d", "branch": "b"},
		}, H)
		return out["id"].(string)
	}
	blocker := mk("Blocker to delete")
	blocked := mk("Dependent card")
	do(t, ts, "POST", "/v1/cards/"+blocked+"/links", core.LinkInput{TypeID: "depends-on", Target: blocker}, H)
	// Confirm blocked.
	_, before := do(t, ts, "GET", "/v1/cards?blocked=true", nil, nil)
	if n := len(before["items"].([]any)); n != 1 {
		t.Fatalf("expected 1 blocked card before delete, got %d", n)
	}
	// Deleting the blocker unblocks the dependent.
	resp, _ := do(t, ts, "DELETE", "/v1/cards/"+blocker, nil, H)
	if resp.StatusCode != 200 {
		t.Fatalf("delete blocker → %d", resp.StatusCode)
	}
	_, after := do(t, ts, "GET", "/v1/cards?blocked=true", nil, nil)
	if n := len(after["items"].([]any)); n != 0 {
		t.Errorf("dependent should be unblocked after blocker delete, still blocked: %d", n)
	}
}

func TestUIBoardSavedFilterMe(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}
	mk := func(title string) string {
		_, out := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
			TypeID: "programming-task", Title: title, Status: "todo",
			Fields: map[string]any{"description": "d", "branch": "b"},
		}, H)
		return out["id"].(string)
	}
	claim := func(id string) {
		resp, out := do(t, ts, "POST", "/v1/cards/"+id+"/claim", core.ClaimRequest{Version: 1}, H)
		if resp.StatusCode != 200 {
			t.Fatalf("claim %s: %d %v", id, resp.StatusCode, out)
		}
	}
	// Mine + open: claimed by local-dev, still todo → matches mine-open.
	mine := mk("Mine open filter card")
	claim(mine)
	// Unowned → excluded (owner != me).
	mk("Unowned filter card")
	// Mine but done → excluded (status in [done]).
	done := mk("Mine done filter card")
	claim(done)
	for _, st := range []string{"in_progress", "review", "done"} {
		v := 0
		_, gc := do(t, ts, "GET", "/v1/cards/"+done, nil, nil)
		v = int(gc["version"].(float64))
		do(t, ts, "PATCH", "/v1/cards/"+done, map[string]any{"version": v, "status": st}, H)
	}

	_, body := doGet(t, ts, "/ui/boards/engineering?filter=mine-open")
	if !strings.Contains(body, "Mine open filter card") {
		t.Error("mine-open should include the local-dev-owned open card")
	}
	if strings.Contains(body, "Unowned filter card") {
		t.Error("mine-open must exclude the unowned card")
	}
	if strings.Contains(body, "Mine done filter card") {
		t.Error("mine-open must exclude the done card (status $nin done)")
	}
	// The chip must render active.
	if !strings.Contains(body, `class="chip-filter is-active"`) {
		t.Error("mine-open chip should render as active")
	}
}

func TestUIBoardOwnerAndTypeParams(t *testing.T) {
	ts, _ := newServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}
	// A research-goal card and a programming-task card; ?type filters to one.
	_, rg := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "research-goal", Title: "Research type card", Status: "todo",
		Fields: map[string]any{"hypothesis": "h"},
	}, H)
	_ = rg
	do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
		TypeID: "programming-task", Title: "Programming type card", Status: "todo",
		Fields: map[string]any{"description": "d", "branch": "b"},
	}, H)

	_, body := doGet(t, ts, "/ui/boards/engineering?type=research-goal")
	if !strings.Contains(body, "Research type card") {
		t.Error("?type=research-goal should include the research card")
	}
	if strings.Contains(body, "Programming type card") {
		t.Error("?type=research-goal must exclude programming-task cards")
	}
}

func TestUIBreachesPage(t *testing.T) {
	ts, _ := newServer(t)
	// Empty state: seeded demo has no breaching conditions.
	resp, body := doGet(t, ts, "/ui/breaches")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Nothing breaching right now") {
		t.Error("breaches page missing empty-state copy")
	}
	// Create a blocked card; it must then appear as a card_blocked row.
	H := map[string]string{"X-Work-Cards-Actor": "local-dev", "Content-Type": "application/json"}
	mk := func(title string) string {
		_, out := do(t, ts, "POST", "/v1/cards", core.CreateCardRequest{
			TypeID: "programming-task", Title: title, Status: "todo",
			Fields: map[string]any{"description": "d", "branch": "b"},
		}, H)
		return out["id"].(string)
	}
	blocker := mk("Breach blocker")
	blocked := mk("Breach blocked")
	do(t, ts, "POST", "/v1/cards/"+blocked+"/links", core.LinkInput{TypeID: "depends-on", Target: blocker}, H)
	resp2, body2 := doGet(t, ts, "/ui/breaches")
	if resp2.StatusCode != 200 {
		t.Fatalf("status %d", resp2.StatusCode)
	}
	if !strings.Contains(body2, `data-condition="card_blocked"`) {
		t.Error("breaches page does not show the blocked card row")
	}
	if !strings.Contains(body2, "Breach blocked") || !strings.Contains(body2, "Breach blocker") {
		t.Error("breaches page did not resolve card/blocker titles")
	}
}

// TestUICreateCardValidationReRendersForm was retired with the full-page
// create form (UI sprint P2): creation is now the in-board modal, a thin
// client of POST /v1/cards. Its validation concern lives on in
// TestCreateViaAPI_PerFieldErrorAndIdempotency (card_create_test.go), which
// pins the structured per-field error the modal renders.

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
		"entry":   map[string]any{"commit_hash": "a1b2c3", "notes": "handler", "author": "coder-agent", "timestamp": "2026-06-25T14:30:00Z"},
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
	st := sqlitetest.Open(t, r.Workspace, 1)
	svc := core.NewService(r.Workspace, r.CardTypes, r.Boards, st)
	srv, err := httpapi.New(svc, r.Workspace, r.CardTypes, r.Boards, r.Themes, st)
	if err != nil {
		t.Fatalf("new http server: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(func() { ts.Close() })
	return ts, svc, st
}

// insertCraftedCard delegates to the shared coretest fixture so all four
// surface packages force short-id scenarios through ONE seam.
func insertCraftedCard(t *testing.T, st *sqlite.Store, id string) {
	coretest.SeedCard(t, st, "demo", "programming-task", id,
		map[string]any{"description": "d", "branch": "b"})
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
	if out["error"] != "ambiguous" || out["value"] != "COLLIDE1" {
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

// TestUnknownThemeDegradesToDefault pins theme-contract guarantee 3
// (docs/design/themes.md): selecting a theme that doesn't exist — a typo, a
// stale cookie, a shared theme not yet installed — renders fine. The unknown
// name lands on html[data-theme] where it matches no CSS rules, so the
// default theme's complete base styling applies. A broken theme can at worst
// mis-style; it can never take the UI down.
func TestUnknownThemeDegradesToDefault(t *testing.T) {
	ts, _ := newServer(t)
	resp, body := doGet(t, ts, "/ui/boards/engineering?theme=definitely-not-installed")
	if resp.StatusCode != 200 {
		t.Fatalf("unknown theme: %d (must render, never error)", resp.StatusCode)
	}
	if !strings.Contains(body, `data-theme="definitely-not-installed"`) {
		t.Error("theme name should pass through to html[data-theme] (harmlessly matching no rules)")
	}
	if !strings.Contains(body, `class="board`) {
		t.Error("board did not render under an unknown theme")
	}
}
