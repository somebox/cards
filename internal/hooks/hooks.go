// Package hooks is the optional extension supervisor. It subscribes to the
// core event bus and, for each declared hook whose filter matches a fired
// event, spawns the hook's run[] command with the event JSON on stdin and
// CARDS_* environment variables set. Delivery is at-most-once: a non-zero
// exit is logged, not retried. See docs/EXTENSIONS.md.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
)

// Supervisor subscribes to the event bus and spawns hook subprocesses.
type Supervisor struct {
	svc         *core.Service
	ws          *core.Workspace
	extensions  []config.Extension
	workspaceDir string
	cardsURL    string
	mu          sync.Mutex
	logs        map[string][]string // per-extension recent log lines
}

// New constructs a Supervisor bound to a service + workspace + declarations.
func New(svc *core.Service, ws *core.Workspace, exts []config.Extension, workspaceDir, cardsURL string) *Supervisor {
	return &Supervisor{
		svc: svc, ws: ws, extensions: exts,
		workspaceDir: workspaceDir, cardsURL: cardsURL,
		logs: map[string][]string{},
	}
}

// Hooks returns the declared hooks (kind == "hook").
func (s *Supervisor) Hooks() []config.Extension {
	out := []config.Extension{}
	for _, e := range s.extensions {
		if e.Kind == "hook" {
			out = append(out, e)
		}
	}
	return out
}

// Run blocks, dispatching matching events to hooks until ctx is cancelled.
// It subscribes to the bus with no filter (each hook applies its own filter)
// and spawns hooks asynchronously.
func (s *Supervisor) Run(ctx context.Context) error {
	hooks := s.Hooks()
	if len(hooks) == 0 {
		// Nothing to supervise; just wait for cancellation.
		<-ctx.Done()
		return ctx.Err()
	}
	// Subscribe to all events; filter per-hook.
	sub := s.svc.Bus().Subscribe(core.EventFilter{}, 128)
	defer s.svc.Bus().Unsubscribe(sub.ID)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-sub.Ch:
			if !ok {
				// Dropped (slow supervisor). Resubscribe.
				s.log("supervisor", "dropped by bus; resubscribing")
				sub = s.svc.Bus().Subscribe(core.EventFilter{}, 128)
				continue
			}
			for _, h := range hooks {
				if h.MatchesEvent(e, s.cardBoardMembership, s.cardTypeID) {
					go s.spawn(ctx, h, e) // async: spawn ordered, completion not
				}
			}
		}
	}
}

// spawn runs a hook subprocess with the event on stdin. At-most-once.
func (s *Supervisor) spawn(ctx context.Context, h config.Extension, e *core.Event) {
	if len(h.Run) == 0 {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"id":           e.ID,
		"type":         e.Type,
		"card_id":      e.CardID,
		"actor":        e.Actor,
		"at":           e.At,
		"diff":         e.Diff,
		"workspace_id": s.ws.ID,
	})
	cmd := exec.CommandContext(ctx, h.Run[0], h.Run[1:]...)
	cmd.Stdin = bytes.NewReader(payload)
	// Environment.
	cmd.Env = append(os.Environ(),
		"CARDS_URL="+s.cardsURL,
		"CARDS_WORKSPACE="+s.workspaceDir,
		"CARDS_USER="+s.ws.Settings.DefaultUser,
		"CARDS_EVENT_ID="+fmt.Sprintf("%d", e.ID),
		"CARDS_EVENT_TYPE="+string(e.Type),
	)
	for k, v := range h.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// Working directory.
	cwd := h.Cwd
	if cwd == "" {
		cwd = s.workspaceDir
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(s.workspaceDir, cwd)
	}
	cmd.Dir = cwd
	// Capture output.
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)
	if runErr != nil {
		s.log(h.ID, fmt.Sprintf("exit=%v dur=%s stderr=%s", runErr, dur, strings.TrimSpace(errOut.String())))
	} else {
		s.log(h.ID, fmt.Sprintf("ok dur=%s out=%s", dur, strings.TrimSpace(out.String())))
	}
	// Persist logs to .cards/logs/<id>.log as well.
	s.persistLog(h.ID, out.String(), errOut.String())
}

// cardBoardMembership returns the board id the card belongs to (first board
// whose card_type_ids contains the card's type), or "". Used by hook filters.
func (s *Supervisor) cardBoardMembership(cardID string) string {
	c, err := s.svc.GetCard(context.Background(), cardID)
	if err != nil {
		return ""
	}
	for _, b := range s.boards() {
		for _, t := range b.CardTypeIDs {
			if t == c.TypeID {
				return b.ID
			}
		}
	}
	return ""
}

// cardTypeID returns the card's type_id, or "" on lookup failure. Used by
// hook filters (filter.type_id).
func (s *Supervisor) cardTypeID(cardID string) string {
	c, err := s.svc.GetCard(context.Background(), cardID)
	if err != nil {
		return ""
	}
	return c.TypeID
}

// boards is a placeholder accessor; the supervisor holds boards via the
// service's introspection. We fetch once per call (cheap enough for POC).
func (s *Supervisor) boards() []*core.Board {
	snap, _ := s.svc.Workspace(context.Background())
	if snap == nil {
		return nil
	}
	out := make([]*core.Board, 0, len(snap.Boards))
	for _, b := range snap.Boards {
		out = append(out, b)
	}
	return out
}

// --- logs ---

func (s *Supervisor) log(extID, msg string) {
	line := fmt.Sprintf("[%s] %s", time.Now().UTC().Format(time.RFC3339), msg)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs[extID] = append(s.logs[extID], line)
	if len(s.logs[extID]) > 200 {
		s.logs[extID] = s.logs[extID][len(s.logs[extID])-200:]
	}
}

// Logs returns recent log lines for an extension.
func (s *Supervisor) Logs(extID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.logs[extID]))
	copy(out, s.logs[extID])
	return out
}

func (s *Supervisor) persistLog(extID, stdout, stderr string) {
	logDir := filepath.Join(s.workspaceDir, ".cards", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	f, err := os.OpenFile(filepath.Join(logDir, extID+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "--- %s ---\nstdout: %s\nstderr: %s\n", time.Now().UTC().Format(time.RFC3339), stdout, stderr)
}

// strings import lives here to avoid a separate import block churn.
var _ = io.EOF // keep io imported for future stream logs
