// Command cards — init subcommand. Scaffolds a fresh workspace (starter
// definitions + a welcome board) either locally under ./.cards or globally at
// the personal workspace location, and installs the agent skill beside it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/somebox/cards/internal/agentguide"
)

func initCmd(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	global := fs.Bool("global", false, "initialize the personal workspace (~/.cards or $CARDS_HOME)")
	quiet := fs.Bool("quiet", false, "suppress post-init instructions (like import/export summaries)")
	noSkill := fs.Bool("no-skill", false, "do not install the cards agent skill into .claude/skills/")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// dir is where the workspace goes; harnessRoot is where the agent skill
	// goes. They are deliberately different roots — see below.
	var dir, harnessRoot string
	if *global {
		h, err := globalHome()
		if err != nil {
			return err
		}
		dir = h
		// NOT globalHome(): that honors $CARDS_HOME, which relocates the board.
		// It does not relocate the user's harness directory, so deriving the
		// skill path from it would scatter .claude/skills/ next to whichever
		// workspace happened to be configured.
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		harnessRoot = home
	} else {
		target := "."
		if fs.NArg() > 0 {
			target = fs.Arg(0)
		}
		abs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		// Same rule as --workspace resolution (isWorkspaceDir): if the target
		// is ALREADY a workspace dir, scaffolding a .cards child inside it
		// would create the nested-ambiguous layout normalizeWorkspaceDir
		// refuses. Say so instead of building the trap.
		if isWorkspaceDir(abs) {
			return fmt.Errorf("%s is already a workspace (it has definitions/workspace.json) — nothing to init; use it with: cards --workspace %s", abs, abs)
		}
		dir = filepath.Join(abs, ".cards")
		// Beside .cards, never inside it: the skill belongs to the project's
		// harness, not to the board's data.
		harnessRoot = abs
	}

	created, err := initWorkspace(dir)
	if err != nil {
		return fmt.Errorf("initialize workspace: %w", err)
	}

	// Installing the skill is independent of whether the workspace was created.
	// An established project already has a workspace and no skill — the common
	// case, and the one with no other install path.
	// A skill failure must not swallow the workspace result: by this point the
	// workspace is already scaffolded, and the user needs to be told both facts.
	// The error is still returned, so scripts see a non-zero exit.
	skillPath, skillCreated := "", false
	var skillErr error
	if !*noSkill {
		skillPath, skillCreated, skillErr = agentguide.InstallSkill(harnessRoot)
	}

	if *quiet {
		return skillErr
	}
	if !created {
		fmt.Printf("workspace already initialized at %s\n", dir)
	} else {
		fmt.Printf("initialized workspace at %s\n", dir)
	}
	reportSkill(skillPath, skillCreated, *noSkill, skillErr)
	if skillErr != nil {
		return fmt.Errorf("install agent skill: %w", skillErr)
	}
	if !created {
		return nil
	}
	fmt.Println()
	fmt.Println("Next:")
	if *global {
		fmt.Println("  cards                 # serve it (zero-config)")
	} else {
		fmt.Println("  cards                 # serve it from this directory")
	}
	fmt.Println("  open http://127.0.0.1:8787/ui/boards/welcome")
	return nil
}

// reportSkill says what happened to the agent skill. An existing skill is
// reported, not silently skipped: the user needs to know why their install did
// not take effect.
func reportSkill(path string, created, skipped bool, err error) {
	switch {
	case skipped, err != nil:
		return // the error itself carries the detail and the remedy
	case created:
		fmt.Printf("installed the cards agent skill at %s\n", path)
	default:
		fmt.Printf("cards skill already exists at %s; not overwritten\n", path)
	}
}
