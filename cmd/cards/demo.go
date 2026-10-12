// Command cards — demo subcommand. Installs the sample workspace embedded in
// the binary (internal/demo) into a new directory, fills it with sample cards,
// and says how to open it. The demo is opt-in: nothing else in the binary or
// the repo treats it as a default workspace.
package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/somebox/cards/internal/demo"
	"github.com/somebox/cards/internal/seed"
)

func demoCmd(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	withSeed := fs.Bool("seed", true, "fill the boards with sample cards and users (--seed=false for empty boards)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target := "cards-demo"
	if fs.NArg() > 0 {
		target = fs.Arg(0)
	}
	dir, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := demo.Install(dir); err != nil {
		return fmt.Errorf("install demo workspace: %w", err)
	}
	if *withSeed {
		st, svc, result, err := openWorkspace(dir)
		if err != nil {
			return err
		}
		defer st.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := seed.IfEmpty(ctx, st, svc, result.Workspace); err != nil {
			return fmt.Errorf("seed demo workspace: %w", err)
		}
	}
	fmt.Printf("installed the demo workspace at %s\n", dir)
	fmt.Println()
	fmt.Println("Open it:")
	fmt.Printf("  cards serve --workspace %s\n", target)
	fmt.Println("  open http://127.0.0.1:8787/ui/boards/engineering")
	fmt.Println()
	fmt.Println("Its README.md explains the two example extensions (--run-extensions starts them).")
	return nil
}
