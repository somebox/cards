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
	"path/filepath"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/sqlite"
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
	if header != nil {
		for k, vs := range header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
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

// newDirectBackend opens the workspace ($CARDS_WORKSPACE, else the nearest
// .cards/ or the personal workspace) and builds an in-process /v1 router.
func newDirectBackend() (directBackend, error) {
	dir := os.Getenv("CARDS_WORKSPACE")
	if dir == "" {
		d, autoInit, err := resolveWorkspaceDir("")
		if err != nil {
			return directBackend{}, err
		}
		if autoInit {
			if _, err := initWorkspace(d); err != nil {
				return directBackend{}, fmt.Errorf("initialize workspace: %w", err)
			}
		}
		dir = d
	} else {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return directBackend{}, err
		}
		dir = abs
	}

	result, err := config.New(dir).Load()
	if err != nil {
		return directBackend{}, fmt.Errorf("load workspace %s: %w", dir, err)
	}
	st, err := sqlite.Open(filepath.Join(dir, "work-cards.db"), result.Workspace)
	if err != nil {
		return directBackend{}, fmt.Errorf("open store: %w", err)
	}
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, st)
	srv, err := httpapi.New(svc, result.Workspace, result.CardTypes, result.Boards, st)
	if err != nil {
		st.Close()
		return directBackend{}, fmt.Errorf("build router: %w", err)
	}
	return directBackend{h: srv.Router(), closer: st}, nil
}
