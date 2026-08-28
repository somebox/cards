package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/agentguide"
)

func skillDir(root string) string {
	return filepath.Join(root, filepath.FromSlash(agentguide.SkillDirName))
}

func TestInitCmd_InstallsSkillBesideWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := initCmd([]string{"--quiet", root}); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, rel := range []string{"SKILL.md", "references/cli-reference.md"} {
		if _, err := os.Stat(filepath.Join(skillDir(root), rel)); err != nil {
			t.Errorf("skill file %s not installed: %v", rel, err)
		}
	}
	// Beside .cards, never inside it — the skill belongs to the harness, not
	// to the board's data directory.
	if _, err := os.Stat(filepath.Join(root, ".cards", ".claude")); err == nil {
		t.Error("skill was installed inside .cards/")
	}
}

// The case with no other install path: a project that already has a board.
func TestInitCmd_InstallsSkillWhenWorkspaceAlreadyExists(t *testing.T) {
	root := t.TempDir()
	if err := initCmd([]string{"--quiet", "--no-skill", root}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(skillDir(root)); err == nil {
		t.Fatal("--no-skill still installed the skill")
	}
	// Workspace now exists, so initWorkspace reports created=false. The skill
	// must still land.
	if err := initCmd([]string{"--quiet", root}); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir(root), "SKILL.md")); err != nil {
		t.Errorf("skill not installed into an existing workspace: %v", err)
	}
}

func TestInitCmd_SkillIsNeverClobbered(t *testing.T) {
	root := t.TempDir()
	if err := initCmd([]string{"--quiet", root}); err != nil {
		t.Fatalf("init: %v", err)
	}
	marker := filepath.Join(skillDir(root), "SKILL.md")
	if err := os.WriteFile(marker, []byte("locally edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := initCmd([]string{"--quiet", root}); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "locally edited" {
		t.Error("re-init overwrote a locally edited skill")
	}
}

// $CARDS_HOME relocates the board, not the user's harness directory. Deriving
// the skill path from globalHome() would scatter .claude/skills/ next to
// whichever workspace happened to be configured.
func TestInitCmd_GlobalSkillFollowsHomeNotCardsHome(t *testing.T) {
	cardsHome := t.TempDir()
	userHome := t.TempDir()
	t.Setenv("CARDS_HOME", cardsHome)
	t.Setenv("HOME", userHome)

	if err := initCmd([]string{"--quiet", "--global"}); err != nil {
		t.Fatalf("init --global: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir(userHome), "SKILL.md")); err != nil {
		t.Errorf("skill not installed under the user home: %v", err)
	}
	if _, err := os.Stat(skillDir(cardsHome)); err == nil {
		t.Error("skill was installed under CARDS_HOME instead of the user home")
	}
}

func TestMCPPrintInstructionsMatchesTheHandshake(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := mcpCmd([]string{"--print-instructions"})
	_ = w.Close()
	os.Stdout = old
	if runErr != nil {
		t.Fatalf("mcp --print-instructions: %v", runErr)
	}
	printed, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if got, want := string(printed), agentguide.MCPInstructions(); got != want {
		t.Errorf("printed instructions differ from the served handshake\ngot %d bytes, want %d", len(got), len(want))
	}
}

// Debris from an interrupted install must fail loudly, but must not swallow the
// workspace result — by that point the workspace is already scaffolded, and the
// user needs both facts.
func TestInitCmd_ReportsWorkspaceEvenWhenSkillInstallFails(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(skillDir(root), "references"), 0o755); err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	initErr := initCmd([]string{root})
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	_ = r.Close()

	if !errors.Is(initErr, agentguide.ErrIncompleteSkill) {
		t.Fatalf("got %v, want ErrIncompleteSkill", initErr)
	}
	if !strings.Contains(string(out), "initialized workspace at") {
		t.Errorf("workspace result was swallowed by the skill failure:\n%s", out)
	}
	if !isWorkspaceDir(filepath.Join(root, ".cards")) {
		t.Error("workspace was not scaffolded")
	}
}
