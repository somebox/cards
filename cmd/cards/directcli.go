// Command cards — serverless CLI backend. When no CARDS_URL is set, the client
// commands run the real /v1 router in-process against the resolved workspace,
// so reads/writes work without a `cards serve` process. The HTTP backend is
// still used when CARDS_URL targets a running server (which keeps that server's
// event bus, SSE, and hooks intact).
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/somebox/cards/internal/httpapi"
)

// directBackend is a cli.Transport that dispatches to an in-process router.
type directBackend struct {
	h      http.Handler
	closer io.Closer
}

func (d directBackend) Do(method, path string, body []byte, header http.Header) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, "/v1"+path, r)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rr := httptest.NewRecorder()
	d.h.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes(), nil
}

func (d directBackend) Close() error {
	if d.closer != nil {
		return d.closer.Close()
	}
	return nil
}

// newDirectBackend opens a workspace and builds an in-process /v1 router. The
// directory is resolved in precedence order: an explicit --workspace override,
// then $CARDS_WORKSPACE, then auto-discovery (nearest .cards/ or the personal
// workspace). An explicit override or env value must already be a workspace
// (no auto-init); only auto-discovery scaffolds an empty personal workspace.
func newDirectBackend(workspaceOverride string) (directBackend, error) {
	dir := workspaceOverride
	if dir == "" {
		dir = os.Getenv("CARDS_WORKSPACE")
	}
	// Explicit and env paths go through the SAME resolver as serve and
	// discovery (normalizeWorkspaceDir: workspace dir or project root,
	// both-valid errors) so the "where is the workspace" rule cannot
	// silently diverge per entry point.
	d, autoInit, err := resolveWorkspaceDir(dir)
	if err != nil {
		return directBackend{}, err
	}
	if autoInit {
		if _, err := initWorkspace(d); err != nil {
			return directBackend{}, fmt.Errorf("initialize workspace: %w", err)
		}
	}
	dir = d

	st, svc, result, err := openWorkspace(dir)
	if err != nil {
		return directBackend{}, fmt.Errorf("workspace %s: %w", dir, err)
	}
	srv, err := httpapi.New(svc, result.Workspace, result.CardTypes, result.Boards, result.Themes, st)
	if err != nil {
		st.Close()
		return directBackend{}, fmt.Errorf("build router: %w", err)
	}
	return directBackend{h: srv.Router(), closer: st}, nil
}
