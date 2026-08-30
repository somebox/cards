//go:build smoke

// End-to-end adoption smoke test: drop a real agent into an empty project with
// nothing but `cards init` run, ask it to set up a board, and assert over what
// it leaves behind.
//
//	CARDS_AGENT_CMD='claude -p' go test -tags smoke ./internal/smoke/
//	CARDS_AGENT_CMD='pi -p' CARDS_SMOKE_RUNS=5 go test -tags smoke -v ./internal/smoke/
//
// Skips (does not fail) when no agent command or cards binary is available, so
// `go test ./...` stays the one command and CI needs no secrets.
//
// Two design rules, both load-bearing:
//
//   - Assert over ARTIFACTS, never the transcript. The agent's path is
//     nondeterministic; the workspace it leaves is not. The transcript is
//     diagnostic output for a failed check, nothing more.
//   - Assert PROPERTIES, not mimicry. Checking that it produced exactly
//     epic/story/task would test whether it copied the playbook. Checking that
//     no type carries more than a handful of required fields tests whether it
//     understood the advice.
//
// The runner is deliberately swappable via CARDS_AGENT_CMD, because the same
// oracle across several harnesses is the only way to read a failure: every
// runner failing one check means the guidance is wrong; one runner failing it
// means that harness never loaded the skill.
package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixture deliberately withholds the vocabulary. It never says
// "epic/story/task", never names the `part-of` link type, and never names the
// `default_board` setting — all of that has to arrive from the installed skill.
// If the prompt taught the answer, the test would only prove the model can
// follow instructions it was just given.
const fixture = `This project needs a work board so we can track what we're building.

Set it up:
- Replace the starter workspace with card types that suit a small software project.
- Make the board this project's default.
- Add the first few real pieces of work, with the hierarchy between them wired up.

The project is a command-line tool that converts subtitle files between formats
(SRT, WebVTT, ASS). Nothing is built yet.`

const agentTimeout = 15 * time.Minute

func cardsBin(t *testing.T) string {
	t.Helper()
	if b := os.Getenv("CARDS_BIN"); b != "" {
		return b
	}
	if abs, err := filepath.Abs("../../cards"); err == nil {
		if _, statErr := os.Stat(abs); statErr == nil {
			return abs
		}
	}
	b, err := exec.LookPath("cards")
	if err != nil {
		t.Skip("no cards binary: set CARDS_BIN or build ./cmd/cards")
	}
	return b
}

// ─── the oracle ──────────────────────────────────────────────────────────────

// board is the parsed result of `cards workspace show`.
type board struct {
	Workspace struct {
		LinkTypes []struct {
			ID string `json:"id"`
		} `json:"link_types"`
		Settings map[string]any `json:"settings"`
	} `json:"workspace"`
	Boards    map[string]json.RawMessage `json:"boards"`
	CardTypes map[string]struct {
		Fields []struct {
			ID       string `json:"id"`
			Required bool   `json:"required"`
		} `json:"fields"`
	} `json:"card_types"`
}

type card struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	TypeID string         `json:"type_id"`
	Fields map[string]any `json:"fields"`
	// type_id, not type — the wire name. Getting this wrong makes the
	// hierarchy canary silently unmatchable on every run.
	Links []struct {
		TypeID string `json:"type_id"`
		Target string `json:"target"`
	} `json:"links"`
}

type state struct {
	dir   string
	board board
	cards []card
}

type check struct {
	name string
	fn   func(*state) error
}

// maxRequiredFields is the "start minimal" advice expressed as a number. The
// reference ladder tops out at one required field on an epic and two on a
// story; four leaves room for a different-but-still-lean design without
// admitting a ported Jira taxonomy.
const maxRequiredFields = 4

var checks = []check{
	{"workspace_loads", func(s *state) error {
		if len(s.board.CardTypes) == 0 {
			return fmt.Errorf("workspace has no card types")
		}
		return nil
	}},

	{"default_board_set", func(s *state) error {
		id, _ := s.board.Workspace.Settings["default_board"].(string)
		if id == "" {
			return fmt.Errorf("settings.default_board is unset")
		}
		if _, ok := s.board.Boards[id]; !ok {
			return fmt.Errorf("default_board %q names no board (have: %s)", id, keys(s.board.Boards))
		}
		return nil
	}},

	{"starter_replaced", func(s *state) error {
		if _, ok := s.board.Boards["welcome"]; ok {
			return fmt.Errorf("the starter 'welcome' board is still present")
		}
		for _, c := range s.cards {
			if strings.Contains(c.Title, "Welcome to Cards") {
				return fmt.Errorf("starter tutorial cards were never cleared (%q)", c.Title)
			}
		}
		return nil
	}},

	{"schema_stayed_minimal", func(s *state) error {
		for id, ct := range s.board.CardTypes {
			n := 0
			for _, f := range ct.Fields {
				if f.Required {
					n++
				}
			}
			if n > maxRequiredFields {
				return fmt.Errorf("card type %q has %d required fields (max %d) — the ladder was not kept minimal", id, n, maxRequiredFields)
			}
		}
		return nil
	}},

	{"work_was_created", func(s *state) error {
		if len(s.cards) < 3 {
			return fmt.Errorf("only %d cards created, want >= 3", len(s.cards))
		}
		for _, c := range s.cards {
			for _, f := range s.board.CardTypes[c.TypeID].Fields {
				if !f.Required {
					continue
				}
				if v, ok := c.Fields[f.ID]; !ok || v == nil || v == "" {
					return fmt.Errorf("card %q leaves required field %q empty", c.Title, f.ID)
				}
			}
		}
		return nil
	}},

	// The canary. `cards init` ships "link_types": [] — zero — so `part-of`
	// cannot have been copied off the scaffold, and it is not what a model
	// reaches for unaided (that is a `parent` field or a `subtasks` array).
	// Declaring AND using it is the strongest available evidence that the
	// installed skill was actually read.
	{"hierarchy_uses_links_not_fields", func(s *state) error {
		declared := ""
		for _, lt := range s.board.Workspace.LinkTypes {
			if lt.ID == "part-of" || strings.Contains(lt.ID, "part") {
				declared = lt.ID
			}
		}
		if declared == "" {
			return fmt.Errorf("no part-of-style link type declared (have: %v) — hierarchy was probably modelled as a field", linkIDs(s.board.Workspace.LinkTypes))
		}
		for _, c := range s.cards {
			for _, l := range c.Links {
				if l.TypeID == declared {
					return nil
				}
			}
		}
		return fmt.Errorf("link type %q is declared but no card uses it", declared)
	}},

	{"no_parent_pointer_fields", func(s *state) error {
		for id, ct := range s.board.CardTypes {
			for _, f := range ct.Fields {
				switch f.ID {
				case "parent", "parent_id", "subtasks", "children", "epic_id", "story_id":
					return fmt.Errorf("card type %q models hierarchy as field %q; hierarchy belongs in links", id, f.ID)
				}
			}
		}
		return nil
	}},

	{"snapshot_roundtrips", func(s *state) error {
		return nil // filled in by run(), which needs the binary
	}},
}

func keys(m map[string]json.RawMessage) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ", ")
}

func linkIDs(lts []struct {
	ID string `json:"id"`
}) []string {
	out := make([]string, 0, len(lts))
	for _, lt := range lts {
		out = append(out, lt.ID)
	}
	return out
}

// ─── the runner ──────────────────────────────────────────────────────────────

func TestAgentCanAdoptCards(t *testing.T) {
	agentCmd := os.Getenv("CARDS_AGENT_CMD")
	if agentCmd == "" {
		t.Skip("set CARDS_AGENT_CMD to the agent to drive, e.g. CARDS_AGENT_CMD='claude -p'")
	}
	bin := cardsBin(t)

	runs := 1
	if n := os.Getenv("CARDS_SMOKE_RUNS"); n != "" {
		if _, err := fmt.Sscanf(n, "%d", &runs); err != nil || runs < 1 {
			t.Fatalf("CARDS_SMOKE_RUNS=%q is not a positive integer", n)
		}
	}

	// Per-check tallies rather than a binary verdict: the system under test is
	// stochastic, so "check 4 went 5/5 to 1/5" is the actionable signal and a
	// hard gate would just be a flaky red build.
	passed := map[string]int{}
	for i := 0; i < runs; i++ {
		runOnce(t, bin, agentCmd, i, passed)
	}

	t.Logf("=== adoption smoke: %d run(s) ===", runs)
	for _, c := range checks {
		t.Logf("  %-34s %d/%d", c.name, passed[c.name], runs)
	}
}

func runOnce(t *testing.T, bin, agentCmd string, i int, passed map[string]int) {
	t.Helper()
	dir, err := os.MkdirTemp("", fmt.Sprintf("cards-smoke-%d-", i))
	if err != nil {
		t.Fatal(err)
	}
	keep := false
	defer func() {
		if keep {
			t.Logf("run %d: workspace kept for inspection at %s", i, dir)
			return
		}
		_ = os.RemoveAll(dir)
	}()

	// The only setup the agent gets: the same `cards init` a real user runs.
	if out, err := exec.Command(bin, "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatalf("cards init: %v: %s", err, out)
	}

	transcript, agentErr := runAgent(t, dir, agentCmd)
	_ = os.WriteFile(filepath.Join(dir, "agent-transcript.txt"), []byte(transcript), 0o644)
	if agentErr != nil {
		keep = true
		t.Errorf("run %d: agent command failed: %v\n--- transcript tail ---\n%s", i, agentErr, tail(transcript, 40))
		return
	}

	s, err := observe(bin, dir)
	if err != nil {
		keep = true
		t.Errorf("run %d: could not read the workspace the agent left behind: %v\n"+
			"(a workspace that no longer loads is itself the finding — e.g. settings.default_board "+
			"naming a board whose file was not written yet is a hard load failure)\n--- transcript tail ---\n%s",
			i, err, tail(transcript, 40))
		return
	}

	for _, c := range checks {
		var cerr error
		if c.name == "snapshot_roundtrips" {
			cerr = snapshotRoundtrips(bin, dir, s)
		} else {
			cerr = c.fn(s)
		}
		if cerr != nil {
			keep = true
			t.Errorf("run %d: %s: %v", i, c.name, cerr)
			continue
		}
		passed[c.name]++
	}
}

func runAgent(t *testing.T, dir, agentCmd string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", agentCmd)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(fixture)
	// Some harnesses read the prompt from the environment rather than stdin.
	cmd.Env = append(os.Environ(), "CARDS_SMOKE_PROMPT="+fixture)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// observe reads the world through the real binary, so the oracle exercises the
// same load path a user would rather than parsing definitions by hand.
func observe(bin, dir string) (*state, error) {
	ws := filepath.Join(dir, ".cards")
	raw, err := exec.Command(bin, "--workspace", ws, "workspace", "show").Output()
	if err != nil {
		return nil, fmt.Errorf("workspace show: %w", err)
	}
	s := &state{dir: dir}
	if err := json.Unmarshal(raw, &s.board); err != nil {
		return nil, fmt.Errorf("parse workspace: %w", err)
	}
	listed, err := exec.Command(bin, "--workspace", ws, "list", "--include", "links", "--limit", "200").Output()
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	for _, line := range strings.Split(string(listed), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var c card
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("parse card: %w", err)
		}
		s.cards = append(s.cards, c)
	}
	return s, nil
}

// snapshotRoundtrips proves the board is actually portable: export, restore
// into a fresh workspace built from the same definitions, and compare the card
// id sets. A board that cannot survive a machine move is not persisted, however
// good it looks locally.
func snapshotRoundtrips(bin, dir string, s *state) error {
	ws := filepath.Join(dir, ".cards")
	snap := filepath.Join(dir, "roundtrip.jsonl")
	if out, err := exec.Command(bin, "--workspace", ws, "export", "--state-only", "--out", snap).CombinedOutput(); err != nil {
		return fmt.Errorf("export: %v: %s", err, out)
	}
	fresh := filepath.Join(dir, "restored")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		return err
	}
	if out, err := exec.Command("cp", "-R", filepath.Join(ws, "definitions"), filepath.Join(fresh, "definitions")).CombinedOutput(); err != nil {
		return fmt.Errorf("copy definitions: %v: %s", err, out)
	}
	if out, err := exec.Command(bin, "--workspace", fresh, "import", "--in", snap).CombinedOutput(); err != nil {
		return fmt.Errorf("import into a fresh workspace: %v: %s", err, out)
	}
	restored, err := exec.Command(bin, "--workspace", fresh, "list", "--limit", "200").Output()
	if err != nil {
		return fmt.Errorf("list restored: %w", err)
	}
	got := map[string]bool{}
	for _, line := range strings.Split(string(restored), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var c card
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return err
		}
		got[c.ID] = true
	}
	for _, c := range s.cards {
		if !got[c.ID] {
			return fmt.Errorf("card %q (%s) did not survive export/import", c.Title, c.ID)
		}
	}
	return nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
