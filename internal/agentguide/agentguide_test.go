package agentguide

import (
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Regenerate the skill's invariants region after editing invariants.md:
//
//	go test ./internal/agentguide -update
var updateSkill = flag.Bool("update", false, "rewrite skill/SKILL.md's invariants region from invariants.md")

const skillPath = "skill/SKILL.md"

// Hard cap on the MCP handshake. This string lands in the prompt prefix of every
// MCP session, including sessions that never touch the board, so it is charged
// to every agent. The cap is the enforcement of "the always-on slot stays
// short": the way to fit new guidance is to put it in the skill (loaded on
// demand) or to shorten the trunk — not to grow this.
const (
	maxInstructionBytes = 2048
	maxInstructionLines = 40
)

func TestSkillInvariantsRegionMatchesTrunk(t *testing.T) {
	body, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read %s: %v", skillPath, err)
	}
	want, err := spliceInvariants(string(body), Invariants())
	if err != nil {
		t.Fatalf("splice %s: %v", skillPath, err)
	}
	if *updateSkill {
		if want == string(body) {
			return
		}
		if err := os.WriteFile(skillPath, []byte(want), 0o644); err != nil {
			t.Fatalf("write %s: %v", skillPath, err)
		}
		t.Logf("updated %s from invariants.md", skillPath)
		return
	}
	if want != string(body) {
		t.Errorf("%s's invariants region is out of date with invariants.md.\nRegenerate:\n\n\tgo test ./internal/agentguide -update\n", skillPath)
	}
}

// The splice must fail loudly on a mangled skill rather than appending, or the
// shared rules would silently vanish from the on-demand surface.
func TestSpliceRejectsMissingOrInvertedMarkers(t *testing.T) {
	cases := map[string]string{
		"no begin marker": "body\n" + markerEnd + "\n",
		"no end marker":   markerBegin + "\nbody\n",
		"inverted":        markerEnd + "\nbody\n" + markerBegin + "\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := spliceInvariants(body, "trunk"); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestSpliceReplacesRegionRatherThanAppending(t *testing.T) {
	body := "head\n" + markerBegin + "\nstale\n" + markerEnd + "\ntail\n"
	got, err := spliceInvariants(body, "fresh")
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	if strings.Contains(got, "stale") {
		t.Errorf("stale region survived the splice:\n%s", got)
	}
	for _, want := range []string{"head", "fresh", "tail"} {
		if !strings.Contains(got, want) {
			t.Errorf("spliced body lost %q:\n%s", want, got)
		}
	}
}

func TestMCPInstructionsStayShort(t *testing.T) {
	got := MCPInstructions()
	if strings.TrimSpace(got) == "" {
		t.Fatal("MCPInstructions() is empty")
	}
	if n := len(got); n > maxInstructionBytes {
		t.Errorf("MCP instructions are %d bytes, cap is %d.\nThis text is in every session's prompt prefix. Move guidance into the skill (skill/SKILL.md, loaded on demand) rather than raising the cap.", n, maxInstructionBytes)
	}
	if n := strings.Count(got, "\n"); n > maxInstructionLines {
		t.Errorf("MCP instructions are %d lines, cap is %d (see the byte-cap note above)", n, maxInstructionLines)
	}
}

// The trunk carries the rules that must be true on every surface. Losing one of
// these to an edit is the failure this package exists to prevent.
func TestTrunkStatesTheInvariants(t *testing.T) {
	trunk := Invariants()
	for _, want := range []string{
		"workspace",        // read the schema first
		"version_conflict", // optimistic concurrency, retry discipline
		"valid_options",    // self-correcting validation
		"screenshot",       // evidence norms
		"orchestrator",     // who owns card bookkeeping
		"--state-only",     // session-end persistence
		"import",           // never over a non-empty DB
	} {
		if !strings.Contains(trunk, want) {
			t.Errorf("invariants.md no longer mentions %q — the shared trunk lost a rule", want)
		}
	}
}

func TestSkillFSCarriesTheReference(t *testing.T) {
	sub, err := SkillFS()
	if err != nil {
		t.Fatalf("SkillFS: %v", err)
	}
	for _, name := range []string{"SKILL.md", "references/cli-reference.md"} {
		if _, err := sub.Open(name); err != nil {
			t.Errorf("skill tree is missing %s: %v", name, err)
		}
	}
}

// A failed or interrupted install must leave no destination at all. The old
// behaviour wrote straight into the final path, so a partial tree survived and
// every later run mistook it for a protected user skill and skipped it —
// turning one transient failure into a permanently broken skill.
func TestInstallSkillLeavesNoPartialDestination(t *testing.T) {
	root := t.TempDir()
	// Make the staging rename impossible by occupying the destination's parent
	// with a regular file, so MkdirAll fails after the root exists.
	parent := filepath.Join(root, ".claude", "skills")
	if err := os.MkdirAll(filepath.Dir(parent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest, created, err := InstallSkill(root)
	if err == nil {
		t.Fatal("expected an error when the skill parent cannot be created")
	}
	if created {
		t.Error("reported created=true on a failed install")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("a partial destination survived a failed install")
	}
}

// Debris from an interrupted install (or a hand-mangled directory) must not be
// mistaken for a user's protected skill.
func TestInstallSkillRejectsIncompleteDestination(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, filepath.FromSlash(SkillDirName))
	if err := os.MkdirAll(filepath.Join(dest, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, created, err := InstallSkill(root)
	if !errors.Is(err, ErrIncompleteSkill) {
		t.Fatalf("got %v, want ErrIncompleteSkill", err)
	}
	if created {
		t.Error("reported created=true for an incomplete destination")
	}
	if !strings.Contains(err.Error(), "SKILL.md") {
		t.Errorf("error should name the missing file, got: %v", err)
	}
}

func TestInstallSkillIsAtomicAndComplete(t *testing.T) {
	root := t.TempDir()
	dest, created, err := InstallSkill(root)
	if err != nil || !created {
		t.Fatalf("install: created=%v err=%v", created, err)
	}
	for _, rel := range []string{"SKILL.md", "references/cli-reference.md", "references/project-practices.md"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			t.Errorf("installed skill is missing %s: %v", rel, err)
		}
	}
	// No staging directory may survive a successful install.
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".cards-skill-staging-") {
			t.Errorf("staging directory %s survived a successful install", e.Name())
		}
	}
	// A complete skill is protected on re-run.
	if _, created, err := InstallSkill(root); err != nil || created {
		t.Errorf("re-install: created=%v err=%v, want false/nil", created, err)
	}
}

// Every relative link out of SKILL.md must resolve inside the embedded tree.
// A skill is loaded by a harness, not built by a doc pipeline, so a broken
// reference link is a dead end at runtime with nothing to catch it.
func TestSkillReferenceLinksResolve(t *testing.T) {
	sub, err := SkillFS()
	if err != nil {
		t.Fatalf("SkillFS: %v", err)
	}
	body, err := fs.ReadFile(sub, "SKILL.md")
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	links := regexp.MustCompile(`\]\((?:\./)?(references/[^)#]+)\)`).FindAllStringSubmatch(string(body), -1)
	if len(links) == 0 {
		t.Fatal("SKILL.md links to no references — the on-demand playbook is unreachable")
	}
	seen := map[string]bool{}
	for _, m := range links {
		target := m[1]
		if seen[target] {
			continue
		}
		seen[target] = true
		if _, err := fs.Stat(sub, target); err != nil {
			t.Errorf("SKILL.md links to %s, which is not in the embedded skill: %v", target, err)
		}
	}
	// The adoption playbook is the reason this skill is more than an operator
	// manual; losing the link would silently drop that half of the job.
	if !seen["references/project-practices.md"] {
		t.Error("SKILL.md no longer links to references/project-practices.md")
	}
}

// The description is the only part a harness always has in context, so it is
// what decides whether the skill loads at all. If it stops mentioning setup and
// migration, the adoption playbook ships but never fires.
func TestSkillDescriptionCoversAdoption(t *testing.T) {
	sub, err := SkillFS()
	if err != nil {
		t.Fatalf("SkillFS: %v", err)
	}
	body, err := fs.ReadFile(sub, "SKILL.md")
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	_, rest, found := strings.Cut(string(body), "---\n")
	if !found {
		t.Fatal("SKILL.md has no frontmatter")
	}
	front, _, found := strings.Cut(rest, "---\n")
	if !found {
		t.Fatal("SKILL.md frontmatter is unterminated")
	}
	for _, want := range []string{"set up", "card types", "migrating"} {
		if !strings.Contains(strings.ToLower(front), want) {
			t.Errorf("skill description no longer mentions %q — adoption tasks will not trigger it", want)
		}
	}
}

// The staging directory is created 0700 by MkdirTemp and Rename preserves the
// mode, so without an explicit chmod the skill root ends up owner-only while
// its own contents are world-readable — unreadable to a CI runner or container
// running as another uid.
func TestInstallSkillDirectoryIsReadable(t *testing.T) {
	root := t.TempDir()
	dest, _, err := InstallSkill(root)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o055 != 0o055 {
		t.Errorf("skill root is %v, want group/other read+execute (0755-style)", perm)
	}
}
