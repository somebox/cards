// Command cards — the workspace reload seam (UI sprint P4, ROADMAP §7 card
// 4b507da7). Definitions are FILES; applying an edit without a restart means
// re-running the loader and atomically swapping the Service + router that
// were built from the old files. The store and the event bus are the only
// long-lived resources: both are shared across swaps, so SQLite state, live
// SSE subscribers, and the hook supervisor all survive a reload.
//
// The seam also carries the one write path the create-a-board UI needs:
// POST /v1/boards writes definitions/boards/<id>.json, validates it by
// running the SAME loader (rolling the file back on failure), then reloads.
// The UI writes a reviewable file — it does not grow a parallel board store.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/sqlite"
)

// appState is one immutable generation of the composition: everything built
// from one successful config load.
type appState struct {
	svc    *core.Service
	router http.Handler
	result *config.Result
}

// reloadableApp is the swappable composition root `cards serve` mounts:
// requests flow to the CURRENT generation's router; reload builds the next
// generation and swaps the pointer. A failed load never swaps — the old
// definitions keep serving, and the caller gets the loader's error.
type reloadableApp struct {
	dir string
	st  *sqlite.Store
	bus core.Bus

	mu  sync.Mutex // serializes reload/create-board (file writes + swaps)
	cur atomic.Pointer[appState]
}

// newReloadableApp wraps the initially-composed workspace. The initial
// service's bus becomes THE bus for every future generation.
func newReloadableApp(dir string, st *sqlite.Store, svc *core.Service, result *config.Result, router http.Handler) *reloadableApp {
	a := &reloadableApp{dir: dir, st: st, bus: svc.Bus()}
	a.cur.Store(&appState{svc: svc, router: router, result: result})
	return a
}

func (a *reloadableApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/v1/workspace/reload" {
		a.handleReload(w, r)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/boards" {
		a.handleCreateBoard(w, r)
		return
	}
	a.cur.Load().router.ServeHTTP(w, r)
}

// reload re-runs the loader and swaps in a new generation. On a load error
// the current generation is untouched and the error is returned — the server
// is never left half-loaded.
func (a *reloadableApp) reload() (*config.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reloadLocked()
}

func (a *reloadableApp) reloadLocked() (*config.Result, error) {
	result, err := config.New(a.dir).Load()
	if err != nil {
		return nil, err
	}
	for _, w := range result.Warnings {
		log.Printf("WARN: %s", w)
	}
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, a.st, core.WithBus(a.bus))
	am, err := artifacts.New(artifactsRoot(a.dir))
	if err != nil {
		svc.Close()
		return nil, fmt.Errorf("artifacts root: %w", err)
	}
	svc.SetArtifacts(am)
	srv, err := httpapi.New(svc, result.Workspace, result.CardTypes, result.Boards, a.st)
	if err != nil {
		svc.Close()
		return nil, fmt.Errorf("build http server: %w", err)
	}
	old := a.cur.Swap(&appState{svc: svc, router: srv.Router(), result: result})
	if old != nil {
		old.svc.Close() // stop the old generation's deadline scheduler
	}
	// Tell every live board stream (old and new board set) that definitions
	// changed; board filters require a matching board_id, so fan out per id.
	ids := map[string]bool{}
	if old != nil {
		for id := range old.result.Boards {
			ids[id] = true
		}
	}
	for id := range result.Boards {
		ids[id] = true
	}
	now := time.Now().UTC()
	for id := range ids {
		a.bus.Publish(&core.Event{Type: core.EventDefinitionReload, BoardID: id, Actor: "system", At: now})
	}
	log.Printf("workspace reloaded: %d types, %d boards", len(result.CardTypes), len(result.Boards))
	return result, nil
}

func (a *reloadableApp) handleReload(w http.ResponseWriter, r *http.Request) {
	result, err := a.reload()
	if err != nil {
		appWriteJSON(w, 422, map[string]any{
			"error":   "validation_failed",
			"message": err.Error(),
			"hint":    "the previous definitions are still being served",
		})
		return
	}
	appWriteJSON(w, 200, map[string]any{
		"reloaded": true,
		"types":    len(result.CardTypes),
		"boards":   len(result.Boards),
	})
}

// createBoardRequest is the thin create-a-board write: everything else about
// a board (transitions, monitors, presentation) is edited in the FILE this
// endpoint writes.
type createBoardRequest struct {
	ID          string         `json:"id,omitempty"`
	Name        string         `json:"name"`
	Columns     []string       `json:"columns"`
	CardTypeIDs []string       `json:"card_type_ids"`
	WIPLimits   map[string]int `json:"wip_limits,omitempty"`
}

var boardIDRe = regexp.MustCompile(`[^a-z0-9-]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	return strings.Trim(boardIDRe.ReplaceAllString(s, ""), "-")
}

func (a *reloadableApp) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	var req createBoardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		appWriteJSON(w, 400, map[string]any{"error": "validation_failed", "field": "body", "message": "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		appWriteJSON(w, 422, map[string]any{"error": "validation_failed", "field": "name", "message": "board name is required"})
		return
	}
	if len(req.Columns) == 0 {
		appWriteJSON(w, 422, map[string]any{"error": "validation_failed", "field": "columns", "message": "pick at least one column"})
		return
	}
	if len(req.CardTypeIDs) == 0 {
		appWriteJSON(w, 422, map[string]any{"error": "validation_failed", "field": "card_type_ids", "message": "pick at least one card type"})
		return
	}
	id := req.ID
	if id == "" {
		id = slugify(req.Name)
	}
	if id == "" || slugify(id) != id {
		appWriteJSON(w, 422, map[string]any{"error": "validation_failed", "field": "id", "message": "board id must be lowercase letters/digits/dashes"})
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	path := filepath.Join(a.dir, "definitions", "boards", id+".json")
	if _, err := os.Stat(path); err == nil {
		appWriteJSON(w, 409, map[string]any{"error": "validation_failed", "field": "id", "message": "board " + id + " already exists", "hint": "edit " + path + " instead"})
		return
	}
	board := map[string]any{
		"id": id, "name": strings.TrimSpace(req.Name),
		"columns": req.Columns, "card_type_ids": req.CardTypeIDs,
	}
	if len(req.WIPLimits) > 0 {
		board["wip_limits"] = req.WIPLimits
	}
	data, _ := json.MarshalIndent(board, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		appWriteJSON(w, 500, map[string]any{"error": "internal_error", "message": "write board file: " + err.Error()})
		return
	}
	// Validate by running the real loader; a bad board must not survive as a
	// file that breaks the next restart.
	result, err := a.reloadLocked()
	if err != nil {
		_ = os.Remove(path)
		appWriteJSON(w, 422, map[string]any{"error": "validation_failed", "message": err.Error(), "hint": "the board file was rolled back"})
		return
	}
	_ = result
	appWriteJSON(w, 201, map[string]any{"id": id, "path": path})
}

func appWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
