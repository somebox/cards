// Command cards — init subcommand. Scaffolds a fresh workspace (starter
// definitions + a welcome board) either locally under ./.cards or globally at
// the personal workspace location.
package main

import (
	"flag"
	"fmt"
	"path/filepath"
)

func initCmd(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	global := fs.Bool("global", false, "initialize the personal workspace (~/.cards or $CARDS_HOME)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var dir string
	if *global {
		h, err := globalHome()
		if err != nil {
			return err
		}
		dir = h
	} else {
		target := "."
		if fs.NArg() > 0 {
			target = fs.Arg(0)
		}
		abs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		dir = filepath.Join(abs, ".cards")
	}

	created, err := initWorkspace(dir)
	if err != nil {
		return fmt.Errorf("initialize workspace: %w", err)
	}
	if !created {
		fmt.Printf("workspace already initialized at %s\n", dir)
		return nil
	}
	fmt.Printf("initialized workspace at %s\n\n", dir)
	fmt.Println("Next:")
	if *global {
		fmt.Println("  cards                 # serve it (zero-config)")
	} else {
		fmt.Println("  cards                 # serve it from this directory")
	}
	fmt.Println("  open http://127.0.0.1:8787/ui/boards/welcome")
	return nil
}
