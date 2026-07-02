package cli

// The harness runs every command against the real /v1 router in-process —
// the same pattern cmd/cards' serverless backend uses — so command parsing,
// request shaping, and output modes are tested end-to-end without a server.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/seed"
	"github.com/somebox/cards/internal/sqlite"
)

type inprocTransport struct{ h http.Handler }

func (t inprocTransport) Do(method, path string, body []byte, header http.Header) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, "/v1"+path, r)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rr := httptest.NewRecorder()
	t.h.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes(), nil
}

func newTestClient(t *testing.T, cfg Config) *Client {
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
		t.Fatalf("new server: %v", err)
	}
	return NewWithTransport(cfg, inprocTransport{h: srv.Router()})
}

// runCmd dispatches a subcommand by name and captures its stdout.
func runCmd(t *testing.T, c *Client, name string, args ...string) (string, error) {
	t.Helper()
	var cmd *Command
	for _, cc := range Commands() {
		if cc.Name == name {
			cmd = &cc
			break
		}
	}
	if cmd == nil {
		t.Fatalf("no command %q", name)
	}
	old := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(pr)
		done <- string(b)
	}()
	err := cmd.Run(c, args)
	pw.Close()
	os.Stdout = old
	return <-done, err
}

func TestListOutputModes(t *testing.T) {
	t.Run("default is jsonl for collections", func(t *testing.T) {
		c := newTestClient(t, Config{})
		out, err := runCmd(t, c, "list")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) < 2 {
			t.Fatalf("expected multiple JSONL lines, got %q", out)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &m); err != nil || m["id"] == nil {
			t.Errorf("line 0 is not a card object: %q", lines[0])
		}
	})
	t.Run("quiet prints ids only", func(t *testing.T) {
		c := newTestClient(t, Config{Quiet: true})
		out, err := runCmd(t, c, "list")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if !strings.HasPrefix(line, "card_") {
				t.Errorf("quiet line is not a bare id: %q", line)
			}
		}
	})
	t.Run("json pretty-prints the envelope", func(t *testing.T) {
		c := newTestClient(t, Config{JSON: true})
		out, err := runCmd(t, c, "list")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var env struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Items) == 0 {
			t.Errorf("not a JSON envelope: %v (out %.80q)", err, out)
		}
	})
}

// DEBT-33: --quiet take-next must print the claimed card's id (the payload is
// {"card": {...}}, so the id lives at the dotted path card.id).
func TestTakeNextQuietPrintsCardID(t *testing.T) {
	c := newTestClient(t, Config{Quiet: true, As: "demo"})
	out, err := runCmd(t, c, "take-next")
	if err != nil {
		t.Fatalf("take-next: %v", err)
	}
	got := strings.TrimSpace(out)
	if !strings.HasPrefix(got, "card_") || strings.ContainsAny(got, "{}\n") {
		t.Errorf("quiet take-next printed %q, want a bare card id", got)
	}
}

// DEBT-34: patch without --tag must not send "tags" at all — existing tags
// survive a status-only patch.
func TestPatchWithoutTagsPreservesTags(t *testing.T) {
	c := newTestClient(t, Config{Quiet: true, As: "demo"})
	out, err := runCmd(t, c, "create", "--type", "task", "--title", "T", "--tag", "feature")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := strings.TrimSpace(out)

	if _, err := runCmd(t, c, "patch", id, "--version", "1", "--status", "in_progress"); err != nil {
		t.Fatalf("patch: %v", err)
	}
	cj := newTestClientJSONGet(t, c, id)
	tags, _ := cj["tags"].([]any)
	if len(tags) != 1 || tags[0] != "feature" {
		t.Errorf("tags after tag-less patch = %v, want [feature]", cj["tags"])
	}
	if cj["status"] != "in_progress" {
		t.Errorf("status = %v, want in_progress", cj["status"])
	}
}

// newTestClientJSONGet fetches one card through the client transport and
// decodes it (avoids stdout parsing for assertions).
func newTestClientJSONGet(t *testing.T, c *Client, id string) map[string]any {
	t.Helper()
	data, _, err := c.get("/cards/"+id, nil)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// DEBT-35: boards show <id> returns that board, not the whole workspace.
func TestBoardsShowReturnsOneBoard(t *testing.T) {
	c := newTestClient(t, Config{JSON: true})
	out, err := runCmd(t, c, "boards", "show", "engineering")
	if err != nil {
		t.Fatalf("boards show: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("decode: %v (out %.80q)", err, out)
	}
	if m["id"] != "engineering" {
		t.Errorf("id = %v, want engineering", m["id"])
	}
	if _, isWorkspace := m["card_types"]; isWorkspace {
		t.Errorf("boards show returned the whole workspace")
	}
}

func TestIDOfDottedPath(t *testing.T) {
	m := map[string]any{"card": map[string]any{"id": "card_x"}, "id": "top"}
	if got := idOf(m, "card.id"); got != "card_x" {
		t.Errorf(`idOf(card.id) = %q, want card_x`, got)
	}
	if got := idOf(m, ""); got != "top" {
		t.Errorf(`idOf("") = %q, want top`, got)
	}
	if got := idOf(m, "card.missing"); got != "" {
		t.Errorf(`idOf(card.missing) = %q, want ""`, got)
	}
}
