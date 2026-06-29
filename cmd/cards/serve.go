// Command cards — serve subcommand. Loads one workspace, opens SQLite,
// optionally seeds, and serves the /v1 REST API + /ui htmx web UI.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/hooks"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/mcp"
	"github.com/somebox/cards/internal/seed"
	"github.com/somebox/cards/internal/sqlite"
)

func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "workspace directory (contains definitions/)")
	port := fs.Int("port", 8787, "listen port")
	host := fs.String("host", "127.0.0.1", "listen host")
	seedFlag := fs.Bool("seed", false, "seed demo users/cards if DB empty")
	runExt := fs.Bool("run-extensions", false, "also run the hook supervisor in-process")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, autoInit, err := resolveWorkspaceDir(*workspace)
	if err != nil {
		return err
	}
	if autoInit {
		created, ierr := initWorkspace(abs)
		if ierr != nil {
			return fmt.Errorf("initialize workspace: %w", ierr)
		}
		if created {
			log.Printf("no workspace given; created a personal workspace at %s", abs)
		} else {
			log.Printf("no workspace given; using personal workspace at %s", abs)
		}
	}

	// 1. Load definitions.
	loader := config.New(abs)
	result, err := loader.Load()
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}
	log.Printf("loaded workspace %q: %d types, %d boards, %d columns",
		result.Workspace.ID, len(result.CardTypes), len(result.Boards), len(result.Workspace.Columns))

	// 2. Open SQLite (creates work-cards.db in the workspace dir).
	dbPath := filepath.Join(abs, "work-cards.db")
	st, err := sqlite.Open(dbPath, result.Workspace)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// 3. Service.
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, st)

	// 4. Seed if requested.
	if *seedFlag {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := seed.IfEmpty(ctx, st, svc, result.Workspace); err != nil {
			cancel()
			return fmt.Errorf("seed: %w", err)
		}
		cancel()
	}
	srv, err := httpapi.New(svc, result.Workspace, result.CardTypes, result.Boards, st)
	if err != nil {
		return fmt.Errorf("build http server: %w", err)
	}
	addr := fmt.Sprintf("%s:%d", *host, *port)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("work-cards serving http://%s  (workspace: %s)", addr, abs)
	log.Printf("  UI:  http://%s/ui/boards/", addr)
	log.Printf("  API: http://%s/v1/workspace", addr)
	if *runExt {
		cardsURL := fmt.Sprintf("http://%s/v1", addr)
		sup := hooks.New(svc, result.Workspace, result.Extensions, abs, cardsURL)
		go func() {
			if err := sup.Run(context.Background()); err != nil {
				log.Printf("hook supervisor stopped: %v", err)
			}
		}()
		log.Printf("  hooks: supervisor running (%d declared)", countHooks(result.Extensions))
	}
	return httpSrv.ListenAndServe()
}

// countHooks returns the number of hook-kind extensions declared.
func countHooks(exts []config.Extension) int {
	n := 0
	for _, e := range exts {
		if e.Kind == "hook" {
			n++
		}
	}
	return n
}

// mcpCmd runs the stdio MCP server against a workspace.
func mcpCmd(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "workspace directory (contains definitions/)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, autoInit, err := resolveWorkspaceDir(*workspace)
	if err != nil {
		return err
	}
	if autoInit {
		if _, ierr := initWorkspace(abs); ierr != nil {
			return fmt.Errorf("initialize workspace: %w", ierr)
		}
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
	actor := os.Getenv("CARDS_USER")
	if actor == "" {
		actor = result.Workspace.Settings.DefaultUser
	}
	srv := mcp.New(svc, result.Workspace, result.CardTypes, result.Boards, actor)
	return srv.Serve()
}
