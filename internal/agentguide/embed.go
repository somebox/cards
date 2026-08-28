// Package agentguide owns the project's agent-facing guidance: the short set of
// invariants served in the MCP handshake, and the CLI skill that `cards init`
// installs into a project's harness directory.
//
// invariants.md is the single shared trunk. It is served verbatim as the MCP
// instructions (behind a two-line preamble) and spliced into the marked region
// of skill/SKILL.md by `go test ./internal/agentguide -update`. Nothing else is
// generated. The skill's operational sections are authored, so the always-on
// handshake can stay short while the on-demand skill grows independently — the
// two surfaces have different audiences and different token budgets, and only
// the trunk is common to both.
package agentguide

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// mcpPreamble frames the trunk for an MCP client. It is deliberately two lines:
// the tool schemas already describe the surface, so the handshake only carries
// what they cannot.
const mcpPreamble = `You coordinate work through a Cards board.
The tool list is the manual; call ` + "`workspace`" + ` for the schema rather than guessing.`

// Splice markers delimiting the generated region inside skill/SKILL.md.
const (
	markerBegin = "<!-- invariants:begin -->"
	markerEnd   = "<!-- invariants:end -->"
)

//go:embed invariants.md
var invariants string

//go:embed skill
var skillFiles embed.FS

// Invariants returns the shared trunk: the rules every surface must state.
func Invariants() string { return strings.TrimSpace(invariants) }

// MCPInstructions returns the text served as the MCP `initialize` instructions.
// It is held under a hard size cap by TestMCPInstructionsStayShort — this string
// sits in every session's prompt prefix, so growth here is charged to every
// agent whether or not it ever touches the board.
func MCPInstructions() string {
	return mcpPreamble + "\n\n" + Invariants() + "\n"
}

// SkillFS returns the installable skill tree (SKILL.md plus references/),
// rooted so that walking it yields "SKILL.md", "references/...".
func SkillFS() (fs.FS, error) { return fs.Sub(skillFiles, "skill") }

// spliceInvariants replaces the marked region of a SKILL.md body with the
// trunk. It returns an error rather than appending when the markers are absent
// or inverted, so a mangled skill fails loudly instead of silently losing the
// shared rules.
func spliceInvariants(skill, trunk string) (string, error) {
	begin := strings.Index(skill, markerBegin)
	end := strings.Index(skill, markerEnd)
	switch {
	case begin < 0:
		return "", fmt.Errorf("skill body has no %s marker", markerBegin)
	case end < 0:
		return "", fmt.Errorf("skill body has no %s marker", markerEnd)
	case end < begin:
		return "", fmt.Errorf("%s appears before %s", markerEnd, markerBegin)
	}
	head := skill[:begin+len(markerBegin)]
	tail := skill[end:]
	return head + "\n" + strings.TrimSpace(trunk) + "\n" + tail, nil
}

// SkillDirName is the path, relative to a project or home directory, where
// Claude Code and compatible harnesses look for a project-scoped skill. Other
// harnesses should use the MCP handshake, which is harness-neutral.
const SkillDirName = ".claude/skills/cards"

// requiredSkillFiles must be present for a destination directory to count as a
// real skill. Only SKILL.md is checked: a user may legitimately prune the
// references, but a directory without SKILL.md is not a skill a harness can
// load — it is the debris of an install that did not finish.
var requiredSkillFiles = []string{"SKILL.md"}

// ErrIncompleteSkill reports a destination that exists but is not a loadable
// skill. It is deliberately not silently repaired: overwriting could destroy a
// user's own work, so the fix is named and left to them.
var ErrIncompleteSkill = errors.New("incomplete skill installation")

// InstallSkill writes the embedded skill tree into root/.claude/skills/cards
// and reports the path plus whether it created anything.
//
// The write is atomic: the tree is staged in a sibling temporary directory and
// renamed into place only once every file has landed. A failed or interrupted
// install therefore leaves no destination at all, rather than a partial one
// that the next run would mistake for a protected user skill and skip forever.
//
// An existing, complete skill is never overwritten — a user's local edits are
// worth more than a newer embed, and there is no --force yet. Note this is a
// separate decision from whether the *workspace* already exists: an established
// project has a workspace and no skill, and that is exactly the case that needs
// an install path.
func InstallSkill(root string) (path string, created bool, err error) {
	parent := filepath.Join(root, filepath.FromSlash(filepath.Dir(SkillDirName)))
	dest := filepath.Join(root, filepath.FromSlash(SkillDirName))

	switch complete, statErr := skillPresent(dest); {
	case statErr != nil:
		return dest, false, statErr
	case complete:
		return dest, false, nil
	}

	sub, err := SkillFS()
	if err != nil {
		return dest, false, err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return dest, false, err
	}
	// Staged in the destination's own parent so the rename stays on one
	// filesystem and is therefore atomic.
	staging, err := os.MkdirTemp(parent, ".cards-skill-staging-")
	if err != nil {
		return dest, false, err
	}
	defer os.RemoveAll(staging) // no-op once the rename has moved it

	if err := writeSkillTree(sub, staging); err != nil {
		return dest, false, err
	}
	if err := os.Rename(staging, dest); err != nil {
		// Another process may have won the race between our check and our
		// rename. A skill that is now present is the outcome we wanted.
		if complete, statErr := skillPresent(dest); statErr == nil && complete {
			return dest, false, nil
		}
		return dest, false, err
	}
	return dest, true, nil
}

// skillPresent reports whether dest holds a loadable skill. A destination that
// exists but lacks SKILL.md is reported as ErrIncompleteSkill rather than
// silently skipped, so leftover debris cannot masquerade as a user's skill.
func skillPresent(dest string) (bool, error) {
	info, err := os.Stat(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%w: %s exists but is not a directory; remove it and re-run", ErrIncompleteSkill, dest)
	}
	for _, name := range requiredSkillFiles {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(name))); err != nil {
			return false, fmt.Errorf("%w: %s exists but has no %s; remove the directory and re-run to reinstall", ErrIncompleteSkill, dest, name)
		}
	}
	return true, nil
}

func writeSkillTree(src fs.FS, dest string) error {
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
