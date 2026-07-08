package main

// UI sprint P4 (cards f6d2f5ea + 4b507da7): the workspace reload seam and the
// create-a-board write path, exercised through the full composition — a fresh
// t.TempDir workspace, the real loader, the real router, the swappable app.
// The load-bearing property: a failed reload NEVER leaves a half-loaded
// workspace; the old definitions keep serving.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/httpapi"
)

// stylesheetStamp fetches an HTML page and extracts the ?v=<stamp> the layout
// puts on the /ui/style.css link.
var stylesheetStampRe = regexp.MustCompile(`style\.css\?v=(\d+)`)

func stylesheetStamp(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d", url, resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	m := stylesheetStampRe.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no style.css?v= stamp in %s", url)
	}
	return string(m[1])
}

// TestReloadRotatesStylesheetStamp pins the P3 stamp seam: because assetStamp
// is per-Server (per composition generation) and reload builds a fresh Server,
// the /ui/style.css?v=<stamp> URL must change across a reload. Without this,
// a reload that changes the served CSS (e.g. file-loaded workspace themes)
// would be masked by the 24h Cache-Control on the unchanged URL. This lives in
// cmd/cards, not httpapi, because only the reloadable composition swaps
// generations — an httpapi unit test holds one Server and cannot observe it.
func TestReloadRotatesStylesheetStamp(t *testing.T) {
	ts, _ := newTestApp(t)

	ids := boardsServed(t, ts.URL)
	if len(ids) == 0 {
		t.Fatal("scaffolded workspace served no boards")
	}
	pageURL := ts.URL + "/ui/boards/" + ids[0]

	before := stylesheetStamp(t, pageURL)

	if code, out := appDo(t, "POST", ts.URL+"/v1/workspace/reload", ""); code != 200 {
		t.Fatalf("reload: %d %v", code, out)
	}

	after := stylesheetStamp(t, pageURL)
	if after == before {
		t.Errorf("stylesheet stamp did not rotate across reload: before=%s after=%s "+
			"(assetStamp is not per-generation)", before, after)
	}
}

// newTestApp scaffolds a fresh workspace in t.TempDir and mounts the
// reloadable composition around it — the same wiring `cards serve` uses.
func newTestApp(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".cards")
	if _, err := initWorkspace(dir); err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	st, svc, result, err := openWorkspace(dir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	t.Cleanup(func() { svc.Close(); st.Close() })
	srv, err := httpapi.New(svc, result.Workspace, result.CardTypes, result.Boards, st)
	if err != nil {
		t.Fatalf("http server: %v", err)
	}
	app := newReloadableApp(dir, st, svc, result, srv.Router())
	ts := httptest.NewServer(app)
	t.Cleanup(ts.Close)
	return ts, dir
}

func appDo(t *testing.T, method, url string, body string) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body != "" {
		req, _ = http.NewRequest(method, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest(method, url, nil)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func boardsServed(t *testing.T, tsURL string) []string {
	t.Helper()
	code, out := appDo(t, "GET", tsURL+"/v1/workspace", "")
	if code != 200 {
		t.Fatalf("workspace: %d", code)
	}
	var ids []string
	switch bs := out["boards"].(type) {
	case map[string]any:
		for id := range bs {
			ids = append(ids, id)
		}
	case []any:
		for _, b := range bs {
			if m, ok := b.(map[string]any); ok {
				ids = append(ids, m["id"].(string))
			}
		}
	}
	return ids
}

func TestCreateBoardWritesFileAndReloads(t *testing.T) {
	ts, dir := newTestApp(t)

	code, out := appDo(t, "POST", ts.URL+"/v1/boards",
		`{"name":"Design Review","columns":["todo","doing","done"],"card_type_ids":["task"],"wip_limits":{"doing":2}}`)
	if code != 201 {
		t.Fatalf("create board: %d %v", code, out)
	}
	if out["id"] != "design-review" {
		t.Errorf("slug id = %v, want design-review", out["id"])
	}

	// The file exists, is valid JSON, and matches what the loader accepts.
	path := filepath.Join(dir, "definitions", "boards", "design-review.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("board file not written: %v", err)
	}
	var b map[string]any
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("board file is not valid JSON: %v", err)
	}
	if b["name"] != "Design Review" {
		t.Errorf("file name = %v", b["name"])
	}

	// The new workspace generation serves it — API and UI.
	found := false
	for _, id := range boardsServed(t, ts.URL) {
		if id == "design-review" {
			found = true
		}
	}
	if !found {
		t.Error("created board not in the served workspace")
	}
	if code, _ := appDo(t, "GET", ts.URL+"/ui/boards/design-review", ""); code != 200 {
		t.Errorf("UI board page: %d", code)
	}

	// Duplicate id → 409, file untouched.
	code, out = appDo(t, "POST", ts.URL+"/v1/boards",
		`{"name":"Design Review","columns":["todo"],"card_type_ids":["task"]}`)
	if code != 409 {
		t.Errorf("duplicate create: %d %v (want 409)", code, out)
	}
}

func TestReloadFailureKeepsOldDefinitions(t *testing.T) {
	ts, dir := newTestApp(t)

	// Create a board so the current generation has recognizable state.
	if code, out := appDo(t, "POST", ts.URL+"/v1/boards",
		`{"name":"Stable","columns":["todo"],"card_type_ids":["task"]}`); code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}

	// Corrupt a definition ON DISK, then ask for a reload: 422, and the
	// last-good generation (including "stable") keeps serving.
	badPath := filepath.Join(dir, "definitions", "boards", "broken.json")
	if err := os.WriteFile(badPath, []byte(`{"id":"broken","columns":["no-such-column"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := appDo(t, "POST", ts.URL+"/v1/workspace/reload", "")
	if code != 422 || out["error"] != "validation_failed" {
		t.Fatalf("reload with corrupt defs: %d %v (want 422 validation_failed)", code, out)
	}
	served := boardsServed(t, ts.URL)
	if len(served) == 0 {
		t.Fatal("old definitions no longer served after failed reload")
	}
	hasStable := false
	for _, id := range served {
		if id == "stable" {
			hasStable = true
		}
		if id == "broken" {
			t.Error("half-loaded state: broken board is being served")
		}
	}
	if !hasStable {
		t.Error("failed reload lost the previous good state")
	}

	// Fix the file → reload succeeds and the board appears.
	if err := os.WriteFile(badPath, []byte(`{"id":"broken","name":"Fixed","columns":["todo"],"card_type_ids":["task"]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = appDo(t, "POST", ts.URL+"/v1/workspace/reload", "")
	if code != 200 || out["reloaded"] != true {
		t.Fatalf("reload after fix: %d %v", code, out)
	}
	found := false
	for _, id := range boardsServed(t, ts.URL) {
		if id == "broken" {
			found = true
		}
	}
	if !found {
		t.Error("fixed board not served after successful reload")
	}
}

func TestCreateBoardValidatesThroughLoader(t *testing.T) {
	ts, dir := newTestApp(t)
	// An unknown column passes the thin handler but must be REJECTED by the
	// loader — and the written file rolled back.
	code, out := appDo(t, "POST", ts.URL+"/v1/boards",
		`{"name":"Bad Cols","columns":["no-such-column"],"card_type_ids":["task"]}`)
	if code != 422 {
		t.Fatalf("bad columns: %d %v (want 422 from the loader)", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "definitions", "boards", "bad-cols.json")); !os.IsNotExist(err) {
		t.Error("invalid board file was not rolled back")
	}
	// The workspace still serves (no half-loaded state).
	if code, _ := appDo(t, "GET", ts.URL+"/v1/workspace", ""); code != 200 {
		t.Errorf("workspace unavailable after rolled-back create: %d", code)
	}
}

func TestBoardCreateFragmentRenders(t *testing.T) {
	ts, _ := newTestApp(t)
	resp, err := http.Get(ts.URL + "/ui/boards/new/modal")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("fragment: %d", resp.StatusCode)
	}
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, resp.Body); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{"data-board-create", "data-board-input=\"name\"",
		"data-board-columns", "data-board-types", "data-board-save"} {
		if !strings.Contains(html, want) {
			t.Errorf("fragment missing %q", want)
		}
	}
}
