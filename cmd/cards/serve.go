// Command cards — serve subcommand. Loads one workspace, opens SQLite,
// optionally seeds, and serves the /v1 REST API + /ui web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/mcp"
	"github.com/somebox/cards/internal/seed"
)

// loopbackWarning returns a two-line stderr warning when host binds beyond the
// loopback interface — where the API and UI become reachable from the network
// with no authentication (the default deployment is unauthenticated;
// docs/design/AUTH.md is the proposed identity story). It returns "" for
// loopback binds, which stay quiet. Kept as a pure function so a test can
// exercise the decision without starting a server.
func loopbackWarning(host string) string {
	if isLoopbackHost(host) {
		return ""
	}
	return fmt.Sprintf(
		"WARNING: binding to a non-loopback address (%s) — the API and UI are reachable "+
			"from the network with no authentication.\n"+
			"         Bind to 127.0.0.1 for local-only use, or put an authenticating reverse "+
			"proxy in front. See docs/design/AUTH.md.",
		host)
}

// isLoopbackHost reports whether a --host value binds only the loopback
// interface. An empty host means "all interfaces" (net/http default) — not
// loopback. A non-IP, non-"localhost" hostname is treated as non-loopback: we
// warn rather than assume it resolves to 127.0.0.1.
func isLoopbackHost(host string) bool {
	switch host {
	case "localhost":
		return true
	case "":
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "workspace directory (contains definitions/)")
	port := fs.Int("port", 8787, "listen port")
	host := fs.String("host", "127.0.0.1", "listen host")
	seedFlag := fs.Bool("seed", false, "seed demo users/cards if DB empty")
	runExt := fs.Bool("run-extensions", false, "also run the hook supervisor in-process")
	watch := fs.Bool("watch", false, "poll definitions/ and reload on change (debounce; no fsnotify)")
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

	st, svc, result, err := openWorkspace(abs)
	if err != nil {
		return err
	}
	defer st.Close()
	defer svc.Close() // stops the seam 3d scheduler, if a temporal monitor started one
	log.Printf("loaded workspace %q: %d types, %d boards, %d columns",
		result.Workspace.ID, len(result.CardTypes), len(result.Boards), len(result.Workspace.Columns))

	// Seed if requested.
	if *seedFlag {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := seed.IfEmpty(ctx, st, svc, result.Workspace); err != nil {
			cancel()
			return fmt.Errorf("seed: %w", err)
		}
		cancel()
	}
	srv, err := httpapi.New(svc, result.Workspace, result.CardTypes, result.Boards, result.Themes, st)
	if err != nil {
		return fmt.Errorf("build http server: %w", err)
	}
	// The reloadable seam: POST /v1/workspace/reload re-runs the loader and
	// swaps the composition; POST /v1/boards writes a board definition file
	// and reloads. Store + bus are shared across generations (reload.go).
	app := newReloadableApp(abs, st, svc, result, srv.Router())
	addr := fmt.Sprintf("%s:%d", *host, *port)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           app,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("cards serving http://%s  (workspace: %s)", addr, abs)
	log.Printf("  UI:  http://%s/ui/boards/", addr)
	log.Printf("  API: http://%s/v1/workspace", addr)
	if w := loopbackWarning(*host); w != "" {
		log.Print(w)
	}
	// Bind before starting the supervisor so kind:service autostart waits on a
	// real accepting listener (listener-ready gate), not ListenAndServe's
	// internal bind. See LIFECYCLE-SCHEMA.md / P5b.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	ready := make(chan struct{})
	close(ready) // port bound; OS accepts into the listen backlog

	if *runExt {
		// Tie the supervisor's lifetime to the HTTP server's: when Serve
		// returns, cancel the supervisor's context and wait for Run to drain
		// in-flight hooks and stop services (bounded) before serveCmd returns.
		// Registered after the store/service defers so it runs first (LIFO).
		//
		// Service accessor: pass app.currentService, NOT the initial svc.
		// reloadLocked closes each prior generation; a captured pointer would
		// leave GetCard/board-membership evaluating against a closed Service.
		// Service decls reconcile after each successful reload (P5c); hooks stay frozen.
		ctx, cancel := context.WithCancel(context.Background())
		cardsURL := cardsURLForChildren(*host, *port)
		sup := newExtensionSupervisor(extensionSupervisorOpts{
			getSvc:       app.currentService,
			ws:           result.Workspace,
			exts:         result.Extensions,
			workspaceDir: abs,
			cardsURL:     cardsURL,
			ready:        ready,
		})
		app.setAfterReload(func(exts []config.Extension) {
			sup.Reconcile(exts)
		})
		supDone := make(chan struct{})
		go func() {
			defer close(supDone)
			if err := sup.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("extension supervisor stopped: %v", err)
			}
		}()
		defer func() {
			cancel()
			<-supDone // await bounded drain of hooks + services
		}()
		log.Printf("  extensions: supervisor running (%d hook(s), %d autostart service(s))",
			countHooks(result.Extensions), countAutostartServices(result.Extensions))
	}
	// Start the definitions watcher LAST — after setAfterReload above — so a
	// first-tick reload cannot race the plain afterReload field write (a
	// watcher started earlier had no happens-before edge to it and could
	// silently skip a service reconcile). Joined on shutdown like the
	// supervisor: a SIGINT mid-scan must not race reloadLocked against the
	// closing store.
	if *watch {
		watchCtx, watchCancel := context.WithCancel(context.Background())
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			newDefsWatcher(app, defaultWatchPoll, defaultWatchDebounce, nil).Run(watchCtx)
		}()
		defer func() {
			watchCancel()
			<-watchDone
		}()
		log.Printf("  watch: polling definitions/ (poll=%s debounce=%s)", defaultWatchPoll, defaultWatchDebounce)
	}
	return httpSrv.Serve(ln)
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
	st, svc, result, err := openWorkspace(abs)
	if err != nil {
		return err
	}
	defer st.Close()
	defer svc.Close()
	actor := os.Getenv("CARDS_USER")
	if actor == "" {
		actor = result.Workspace.Settings.DefaultUser
	}
	srv := mcp.New(svc, result.Workspace, result.CardTypes, result.Boards, actor)
	return srv.Serve()
}
