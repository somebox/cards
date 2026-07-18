package httpapi_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

// newArtifactServer builds a minimal workspace whose task type has a local
// artifact field, wires a temp artifacts store, and returns a running server
// plus a created card id.
func newArtifactServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
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
	st := sqlitetest.Open(t, ws, 1)
	ctx := context.Background()
	if err := st.InsertUser(ctx, core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatalf("user: %v", err)
	}
	svc := core.NewService(ws, types, boards, st)
	am, err := artifacts.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifacts: %v", err)
	}
	svc.SetArtifacts(am)
	srv, err := httpapi.New(svc, ws, types, boards, nil, st)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)

	c, err := svc.CreateCard(core.WithActor(ctx, "u"), core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	return ts, c.ID
}

func rawPost(t *testing.T, url string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Work-Cards-Actor", "u")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

// Upload via POST then serve via GET returns byte-identical content, and the
// artifact_added flows so the card detail renders a thumbnail for an image.
func TestArtifactUploadServeAndRender(t *testing.T) {
	ts, id := newArtifactServer(t)

	// A tiny PNG (magic header) so the server sniffs image/png.
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("fake-image-bytes")...)
	resp, body := rawPost(t, ts.URL+"/v1/cards/"+id+"/artifacts/screenshot", png)
	if resp.StatusCode != 201 {
		t.Fatalf("upload status %d: %s", resp.StatusCode, body)
	}

	// The card detail page renders a thumbnail linking to the serve route.
	detail, err := http.Get(ts.URL + "/ui/cards/" + id)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(detail.Body)
	detail.Body.Close()
	if !strings.Contains(string(html), "artifact-thumb") || !strings.Contains(string(html), "/v1/artifacts/") {
		t.Errorf("card detail did not render an artifact thumbnail:\n%s", excerpt(string(html), "screenshot"))
	}

	// Pull the stored URI off the card and GET it back byte-identical.
	uri := artifactURI(t, ts, id)
	got, err := http.Get(ts.URL + "/v1/artifacts/" + uri)
	if err != nil {
		t.Fatal(err)
	}
	served, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if got.StatusCode != 200 {
		t.Fatalf("serve status %d", got.StatusCode)
	}
	if !bytes.Equal(served, png) {
		t.Errorf("served bytes differ from uploaded (%d vs %d)", len(served), len(png))
	}
	if ct := got.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
}

// Encoded traversal, absolute paths, and missing URIs all resolve to non-2xx —
// the serve route never escapes the artifacts root.
func TestArtifactServeRejectsTraversal(t *testing.T) {
	ts, _ := newArtifactServer(t)
	for _, bad := range []string{
		"/v1/artifacts/..%2f..%2f..%2fetc%2fpasswd",
		"/v1/artifacts/%2e%2e/%2e%2e/etc/passwd",
		"/v1/artifacts/aa/does-not-exist",
		"/v1/artifacts/",
	} {
		resp, err := http.Get(ts.URL + bad)
		if err != nil {
			t.Fatalf("get %s: %v", bad, err)
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			t.Errorf("%s returned %d, want a non-2xx (no escape)", bad, resp.StatusCode)
		}
	}
}

// artifactURI reads the card's stored screenshot URI via the API.
func artifactURI(t *testing.T, ts *httptest.Server, id string) string {
	t.Helper()
	resp, err := http.Get(ts.URL + "/v1/cards/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// Cheap extraction: find "uri":"<...>" inside the screenshot metadata.
	s := string(body)
	i := strings.Index(s, `"uri":"`)
	if i < 0 {
		t.Fatalf("no artifact uri in card: %s", s)
	}
	rest := s[i+len(`"uri":"`):]
	j := strings.IndexByte(rest, '"')
	return rest[:j]
}

func excerpt(s, around string) string {
	i := strings.Index(s, around)
	if i < 0 {
		return s
	}
	lo := max(0, i-80)
	hi := min(len(s), i+160)
	return s[lo:hi]
}
