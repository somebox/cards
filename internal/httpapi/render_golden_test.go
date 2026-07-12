package httpapi_test

import (
	"context"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/sqlite"
)

// Regenerate after an intentional render change:
//
//	go test ./internal/httpapi -run Golden -update
var updateRenderGolden = flag.Bool("update", false, "update golden HTML render fixtures")

// fixedTime keeps golden HTML free of wall-clock drift.
var goldenAt = time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

// TestRender_GoldenTypeThemedBoards snapshots type-themed demo board + modal
// HTML through the live template pipeline. One card per engineering type pins
// TypeTheme resolution (card_partial precompute + card_head) so a later
// unification must stay byte-identical. Fallback scope: themed demo types
// only — not the full backlog.
func TestRender_GoldenTypeThemedBoards(t *testing.T) {
	ts, themedIDs := newGoldenServer(t)

	t.Run("board_fragment", func(t *testing.T) {
		body := goldenGET(t, ts, "/ui/boards/engineering", map[string]string{
			"X-Cards-Partial": "true",
		})
		compareGolden(t, "testdata/golden/engineering_board_fragment.html", body)
	})

	// One modal per type-themed card type — covers card_head's typeTheme path.
	for _, id := range themedIDs {
		id := id
		t.Run("modal_"+id, func(t *testing.T) {
			body := goldenGET(t, ts, "/ui/cards/"+id+"/modal", nil)
			compareGolden(t, "testdata/golden/modal_"+id+".html", body)
		})
	}

	// Option-themed modal WITH board context (?board= is what card links pass;
	// pins the boardFromRequest + per-value OptionThemes render path).
	t.Run("modal_option_themed_with_board", func(t *testing.T) {
		body := goldenGET(t, ts, "/ui/cards/card_golden_programming/modal?board=engineering", nil)
		compareGolden(t, "testdata/golden/modal_card_golden_programming.html", body)
	})
}

func newGoldenServer(t *testing.T) (*httptest.Server, []string) {
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

	ctx := context.Background()
	// Fixed fixtures — one card per engineering type. Type-themed types
	// (api/frontend/infra/data) exercise config TypeTheme; programming-task
	// and research-goal fall through to CSS [data-type] defaults.
	fixtures := []struct {
		id     string
		typeID string
		title  string
		status string
		fields map[string]any
	}{
		{
			id: "card_golden_programming", typeID: "programming-task",
			title: "Golden programming task", status: "todo",
			// kind exercises the option-theme branch (engineering board sets
			// presentation.style_field=kind; option wins over [data-type] CSS).
			fields: map[string]any{"description": "d", "branch": "feat/golden-prog", "kind": "bug"},
		},
		{
			id: "card_golden_research", typeID: "research-goal",
			title: "Golden research goal", status: "backlog",
			fields: map[string]any{"hypothesis": "CSS defaults still apply without type_theme"},
		},
		{
			id: "card_golden_api", typeID: "api-task",
			title: "Golden API task", status: "in_progress",
			fields: map[string]any{
				"description": "d", "endpoint": "GET /v1/cards", "api_change": "additive",
				"branch": "feat/golden-api",
			},
		},
		{
			id: "card_golden_frontend", typeID: "frontend-task",
			title: "Golden frontend task", status: "review",
			fields: map[string]any{
				"description": "d", "surface": "board", "branch": "feat/golden-fe",
			},
		},
		{
			id: "card_golden_infra", typeID: "infra-task",
			title: "Golden infra task", status: "todo",
			fields: map[string]any{
				"description": "d", "environment": "ci", "branch": "feat/golden-infra",
			},
		},
		{
			id: "card_golden_data", typeID: "data-task",
			title: "Golden data task", status: "done",
			fields: map[string]any{
				"description": "d", "migration": "none", "branch": "feat/golden-data",
			},
		},
	}
	themedIDs := []string{
		"card_golden_api", "card_golden_frontend", "card_golden_infra", "card_golden_data",
	}
	for i, f := range fixtures {
		// Distinct UpdatedAt so default -updated_at lane order is stable.
		at := goldenAt.Add(time.Duration(i) * time.Minute)
		c := &core.Card{
			ID: f.id, WorkspaceID: r.Workspace.ID, TypeID: f.typeID,
			SchemaVersion: 1, Title: f.title, Status: f.status,
			Fields: f.fields, Version: 1,
			CreatedAt: at, UpdatedAt: at, CreatedBy: "local-dev",
			StatusSince: at,
		}
		if err := st.InsertCard(ctx, c, nil); err != nil {
			t.Fatalf("insert %s: %v", f.id, err)
		}
	}

	srv, err := httpapi.New(svc, r.Workspace, r.CardTypes, r.Boards, r.Themes, st)
	if err != nil {
		t.Fatalf("new http server: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(func() { ts.Close() })
	return ts, themedIDs
}

func goldenGET(t *testing.T, ts *httptest.Server, path string, headers map[string]string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", path, resp.StatusCode, body)
	}
	return string(body)
}

func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if *updateRenderGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (regenerate: go test ./internal/httpapi -run Golden -update): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("render drifted from golden %s\n--- got (%d bytes) ---\n%s\n--- want (%d bytes) ---\n%s",
			path, len(got), got, len(want), want)
	}
}
