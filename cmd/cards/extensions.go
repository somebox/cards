// Command cards — extension subcommands: run-extensions, do, extensions.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/hooks"
	"github.com/somebox/cards/internal/sqlite"
)

// stringSlice is a flag.Value for repeatable string flags (--param k=v).
type stringSlice []string

func (s *stringSlice) String() string     { return fmt.Sprint(*s) }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

// runExtensionsCmd runs the hook supervisor against a workspace. It opens the
// store (read/write, so hooks can post back via the API) and subscribes to the
// bus. Blocks until interrupted.
func runExtensionsCmd(args []string) error {
	fs := flag.NewFlagSet("run-extensions", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "workspace directory")
	port := fs.Int("port", 8787, "cards API port (for CARDS_URL env to hooks)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("--workspace is required")
	}
	abs, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}
	result, err := config.New(abs).Load()
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}
	dbPath := filepath.Join(abs, "work-cards.db")
	st, err := sqlite.Open(dbPath, result.Workspace)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, st)

	hookCount := 0
	for _, e := range result.Extensions {
		if e.Kind == "hook" {
			hookCount++
		}
	}
	if hookCount == 0 {
		log.Printf("no hooks declared in workspace %s", abs)
	} else {
		log.Printf("supervising %d hook(s) for workspace %s", hookCount, abs)
		for _, e := range result.Extensions {
			if e.Kind == "hook" {
				log.Printf("  hook %s: on=%s run=%v", e.ID, e.On, e.Run)
			}
		}
	}
	cardsURL := fmt.Sprintf("http://127.0.0.1:%d/v1", *port)
	sup := hooks.New(svc, result.Workspace, result.Extensions, abs, cardsURL)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return sup.Run(ctx)
}

// doCmd invokes a `run` extension by id with --param k=v flags.
func doCmd(args []string) error {
	fs := flag.NewFlagSet("do", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "workspace directory")
	var params stringSlice
	fs.Var(&params, "param", "k=v parameter (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("--workspace is required")
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: cards do <extension_id> [--param k=v]")
	}
	extID := fs.Args()[0]
	abs, _ := filepath.Abs(*workspace)
	result, err := config.New(abs).Load()
	if err != nil {
		return err
	}
	var ext *config.Extension
	for i := range result.Extensions {
		if result.Extensions[i].ID == extID && result.Extensions[i].Kind == "run" {
			ext = &result.Extensions[i]
			break
		}
	}
	if ext == nil {
		return fmt.Errorf("no run extension %q", extID)
	}
	// Pass --param flags through to the command.
	cmdArgs := []string{}
	for _, p := range params {
		cmdArgs = append(cmdArgs, "--param", p)
	}
	cmd := exec.Command(ext.Run[0], append(ext.Run[1:], cmdArgs...)...)
	cmd.Dir = ext.Cwd
	if cmd.Dir == "" {
		cmd.Dir = abs
	}
	cmd.Env = append(os.Environ(),
		"CARDS_WORKSPACE="+abs,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// extensionsCmd lists or shows declared extensions.
func extensionsCmd(args []string) error {
	fs := flag.NewFlagSet("extensions", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("--workspace is required")
	}
	abs, _ := filepath.Abs(*workspace)
	result, err := config.New(abs).Load()
	if err != nil {
		return err
	}
	if len(result.Extensions) == 0 {
		fmt.Println("(no extensions declared)")
		return nil
	}
	if len(fs.Args()) > 0 && fs.Args()[0] == "show" {
		// Show one (or all if no id).
		if len(fs.Args()) < 2 {
			for _, e := range result.Extensions {
				printExt(e)
			}
			return nil
		}
		id := fs.Args()[1]
		for _, e := range result.Extensions {
			if e.ID == id {
				printExt(e)
				return nil
			}
		}
		return fmt.Errorf("no extension %q", id)
	}
	// List.
	for _, e := range result.Extensions {
		fmt.Printf("%-16s %-8s %s\n", e.ID, e.Kind, e.Description)
	}
	return nil
}

func printExt(e config.Extension) {
	b, _ := json.MarshalIndent(e, "", "  ")
	fmt.Println(string(b))
}
