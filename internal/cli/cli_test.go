package cli

// The harness runs every command against the real /v1 router in-process —
// the same pattern cmd/cards' serverless backend uses — so command parsing,
// request shaping, and output modes are tested end-to-end without a server.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// blockOneCard creates A and B in c's store and links A blocked-by B (B not
// done), returning A's id — the setup for a card_blocked breach.
func blockOneCard(t *testing.T, c *Client) string {
	t.Helper()
	q := &Client{cfg: Config{Quiet: true, As: "demo"}, t: c.t} // quiet id output, same store
	a := strings.TrimSpace(mustRun(t, q, "create", "--type", "task", "--title", "A"))
	b := strings.TrimSpace(mustRun(t, q, "create", "--type", "task", "--title", "B"))
	mustRun(t, q, "link", "add", a, "--type", "blocked-by", "--target", b)
	return a
}

func mustRun(t *testing.T, c *Client, name string, args ...string) string {
	t.Helper()
	out, err := runCmd(t, c, name, args...)
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return out
}

// cards breaches surfaces a blocked card in both jsonl and quiet modes.
func TestBreachesShowsBlockedCard(t *testing.T) {
	// jsonl mode: the blocked card A appears with card_blocked.
	cj := newTestClient(t, Config{As: "demo"})
	idA := blockOneCard(t, cj)
	out := mustRun(t, cj, "breaches")
	if !strings.Contains(out, idA) || !strings.Contains(out, `"card_blocked"`) {
		t.Fatalf("breaches missing blocked card %s: %q", idA, out)
	}

	// quiet mode prints the bare blocked card id.
	cq := newTestClient(t, Config{Quiet: true, As: "demo"})
	idA2 := blockOneCard(t, cq)
	qout := mustRun(t, cq, "breaches")
	if !strings.Contains(qout, idA2) {
		t.Errorf("quiet breaches missing %s: %q", idA2, qout)
	}
}

// cards delete removes a card; a subsequent get fails.
func TestDeleteCommand(t *testing.T) {
	c := newTestClient(t, Config{As: "demo"})
	q := &Client{cfg: Config{Quiet: true, As: "demo"}, t: c.t} // same store, bare id output
	id := strings.TrimSpace(mustRun(t, q, "create", "--type", "task", "--title", "Doomed"))

	if _, err := runCmd(t, c, "delete", id); err != nil {
		t.Fatalf("delete %s: %v", id, err)
	}
	if _, err := runCmd(t, c, "get", id); err == nil {
		t.Errorf("get after delete should fail, got nil error")
	}
}

// cards feed returns the durable workspace event feed as JSONL, filterable
// by type.
func TestFeedShowsEvents(t *testing.T) {
	c := newTestClient(t, Config{As: "demo"})
	id, err := runCmd(t, newTestClient(t, Config{Quiet: true, As: "demo"}), "create", "--type", "task", "--title", "X")
	_ = id
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The seeded workspace already has events; assert the feed returns
	// well-formed JSONL event lines.
	out, err := runCmd(t, c, "feed", "--limit", "5")
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("feed returned no events: %q", out)
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("feed line is not JSON: %q", lines[0])
	}
	if ev["type"] == nil || ev["id"] == nil {
		t.Errorf("feed event missing type/id: %v", ev)
	}

	// Type filter narrows the feed.
	filtered, err := runCmd(t, newTestClient(t, Config{As: "demo"}), "feed", "--types", "card_created", "--limit", "50")
	if err != nil {
		t.Fatalf("feed filtered: %v", err)
	}
	for _, l := range strings.Split(strings.TrimSpace(filtered), "\n") {
		if l == "" {
			continue
		}
		var e map[string]any
		if json.Unmarshal([]byte(l), &e) == nil && e["type"] != "card_created" {
			t.Errorf("type filter leaked a %v event", e["type"])
		}
	}
}

// normalizeBase appends /v1 to a bare host so --url http://host reaches the
// API mount instead of 404ing.
func TestNormalizeBase(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:9100":     "http://127.0.0.1:9100/v1",
		"http://127.0.0.1:9100/":    "http://127.0.0.1:9100/v1",
		"http://127.0.0.1:9100/v1":  "http://127.0.0.1:9100/v1",
		"http://127.0.0.1:9100/v1/": "http://127.0.0.1:9100/v1",
		"":                          "",
	}
	for in, want := range cases {
		if got := normalizeBase(in); got != want {
			t.Errorf("normalizeBase(%q) = %q, want %q", in, got, want)
		}
	}
}

// ExitCode classifies server errors by HTTP status so scripts branch on exit
// code, not stderr text.
func TestExitCode(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{http.StatusNotFound, 3},
		{http.StatusConflict, 4},
		{http.StatusBadRequest, 5},
		{http.StatusUnprocessableEntity, 5},
		{http.StatusInternalServerError, 1},
	}
	for _, tc := range cases {
		err := &cliError{Code: "x", Message: "m", Status: tc.status}
		if got := ExitCode(err); got != tc.want {
			t.Errorf("ExitCode(status=%d) = %d, want %d", tc.status, got, tc.want)
		}
	}
	if got := ExitCode(nil); got != 0 {
		t.Errorf("ExitCode(nil) = %d, want 0", got)
	}
	if got := ExitCode(fmt.Errorf("local")); got != 1 {
		t.Errorf("ExitCode(local) = %d, want 1", got)
	}
}

// --help returns ErrHelp (a successful help request) rather than a parse error.
func TestParseHelpReturnsErrHelp(t *testing.T) {
	// printHelp writes to os.Stdout; redirect it so the test log stays clean.
	old := os.Stdout
	_, pw, _ := os.Pipe()
	os.Stdout = pw
	defer func() { os.Stdout = old; pw.Close() }()

	for _, tok := range []string{"--help", "-h"} {
		fs := NewFlagSet()
		fs.String("title", "")
		if err := fs.Parse([]string{tok}); err != ErrHelp {
			t.Errorf("Parse(%q) = %v, want ErrHelp", tok, err)
		}
	}
	// Unknown flags are still rejected (not treated as help).
	fs := NewFlagSet()
	if err := fs.Parse([]string{"--nope"}); err == nil || err == ErrHelp {
		t.Errorf("Parse(--nope) = %v, want an unknown-flag error", err)
	}
}

// patch --title renames a card under the same optimistic-concurrency fence.
func TestPatchTitle(t *testing.T) {
	c := newTestClient(t, Config{As: "demo"})
	q := &Client{cfg: Config{Quiet: true, As: "demo"}, t: c.t}
	id := strings.TrimSpace(mustRun(t, q, "create", "--type", "task", "--title", "Old"))

	if _, err := runCmd(t, c, "patch", id, "--version", "1", "--title", "New"); err != nil {
		t.Fatalf("patch --title: %v", err)
	}
	if got := newTestClientJSONGet(t, c, id)["title"]; got != "New" {
		t.Errorf("title after patch = %v, want New", got)
	}
}

// list --include=links eager-loads relations so an agent reads the dependency
// graph in one call; unknown include values are rejected.
func TestListIncludeLinks(t *testing.T) {
	c := newTestClient(t, Config{As: "demo"})
	idA := blockOneCard(t, c) // A blocked-by B; A is now a blocked card

	// Without --include, list rows omit links.
	plain := mustRun(t, c, "list", "--blocked")
	if !strings.Contains(plain, idA) {
		t.Fatalf("blocked list missing card %s: %q", idA, plain)
	}
	if strings.Contains(plain, `"blocked-by"`) {
		t.Errorf("plain list unexpectedly carried links: %q", plain)
	}

	// With --include=links, card A's blocked-by link is present.
	withLinks := mustRun(t, c, "list", "--blocked", "--include", "links")
	if !strings.Contains(withLinks, `"blocked-by"`) {
		t.Errorf("list --include=links missing the blocked-by link: %q", withLinks)
	}

	// An unknown include value is a validation error.
	if _, err := runCmd(t, c, "list", "--include", "bogus"); err == nil {
		t.Errorf("list --include=bogus should error")
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
