// Package hooks is the optional extension supervisor. It is deliberately
// bimodal (see docs/architecture/lifecycle-schema.md):
//
//   - kind:hook — subscribe to the core event bus; on filter match, spawn
//     run[] with event JSON on stdin (at-most-once, not retried).
//   - kind:service — pure process lifecycle (start / restart per
//     RestartPolicy / bounded drain). No in-process event feeding; services
//     dial /v1/events/stream themselves.
//
// See docs/extensions/index.md.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
)

// defaultDrainTimeout bounds how long Run waits for in-flight hook subprocesses
// (and SIGTERM'd services) after its context is cancelled, before killing
// stragglers.
const defaultDrainTimeout = 5 * time.Second

// ServiceFunc returns the *core.Service the supervisor should use for
// condition evaluation (GetCard, Workspace boards) and bus subscription.
// Callers that outlive a workspace reload must return the current generation
// on every call — never a closed/stale pointer captured at construction.
type ServiceFunc func() *core.Service

// Supervisor runs declared hooks and autostart services.
//
// Lifecycle / graceful drain: Run(ctx) accepts events until ctx is cancelled,
// then drains — stops accepting new events, waits up to drainTimeout for
// in-flight hooks, and SIGTERM→grace→SIGKILL's supervised services. The caller
// cancels the context and waits for Run to return; nothing is left running
// past that point.
//
// Generation provenance: getSvc is consulted on every condition-evaluation
// path so a reload that closes the prior Service cannot leave the supervisor
// reading a dead generation. Hook/run declarations stay frozen at
// construction; kind:service decls are reconciled after each successful
// reload (see Reconcile / docs/architecture/reload.md).
type Supervisor struct {
	getSvc       ServiceFunc
	ws           *core.Workspace
	extensions   []config.Extension
	workspaceDir string
	cardsURL     string
	drainTimeout time.Duration
	ready        <-chan struct{} // listener-ready gate; nil = start services immediately
	backoff      BackoffConfig
	sleep        sleepFunc

	wg          sync.WaitGroup // in-flight hook subprocesses
	svcWG       sync.WaitGroup // supervised service loops
	mu          sync.Mutex
	logs        map[string][]string // per-extension recent log lines
	services    []*managedService
	svcCtx      context.Context // set in Run after ready; parent for service loops
	reconcileMu sync.Mutex      // serializes Run's initial autostart ↔ Reconcile
}

// New constructs a Supervisor. getSvc must be non-nil and return a live
// Service on every call; for a fixed (non-reloading) process pass
// `func() *core.Service { return svc }`.
func New(getSvc ServiceFunc, ws *core.Workspace, exts []config.Extension, workspaceDir, cardsURL string) *Supervisor {
	if getSvc == nil {
		panic("hooks.New: getSvc is required")
	}
	return &Supervisor{
		getSvc: getSvc, ws: ws, extensions: exts,
		workspaceDir: workspaceDir, cardsURL: cardsURL,
		drainTimeout: defaultDrainTimeout,
		backoff:      DefaultBackoff(),
		sleep:        defaultSleep,
		logs:         map[string][]string{},
	}
}

// svc returns the current generation's Service.
func (s *Supervisor) svc() *core.Service { return s.getSvc() }

// SetDrainTimeout overrides how long Run waits for in-flight hooks on shutdown
// before killing stragglers. Call before Run.
func (s *Supervisor) SetDrainTimeout(d time.Duration) { s.drainTimeout = d }

// Hooks returns the declared hooks (kind == "hook").
func (s *Supervisor) Hooks() []config.Extension {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []config.Extension{}
	for _, e := range s.extensions {
		if e.Kind == "hook" {
			out = append(out, e)
		}
	}
	return out
}

// Run blocks until ctx is cancelled, then drains hooks and stops services
// (bounded). Autostart services start only after the optional ready channel
// fires (listener-ready gate for serve --run-extensions).
//
// Hook subprocesses run under spawnCtx, a separate context that outlives ctx's
// cancellation, so a shutdown gives in-flight hooks a bounded grace period to
// finish rather than SIGKILLing them the instant the server stops accepting
// requests. drain cancels spawnCtx only if the grace period elapses.
func (s *Supervisor) Run(ctx context.Context) error {
	if s.ready != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.ready:
		}
	}

	svcCtx, stopServices := context.WithCancel(context.Background())
	defer stopServices()
	// Publish svcCtx and launch the initial autostart set inside ONE
	// reconcileMu section: a reload's Reconcile that observes svcCtx before
	// the initial set is registered would start every autostart service
	// itself, and the unconditional starts here would then duplicate them.
	s.reconcileMu.Lock()
	s.mu.Lock()
	s.svcCtx = svcCtx
	s.mu.Unlock()
	for _, ext := range s.AutostartServices() {
		s.startOne(svcCtx, ext)
	}
	s.reconcileMu.Unlock()

	hooks := s.Hooks()
	spawnCtx, killSpawns := context.WithCancel(context.Background())
	defer killSpawns()

	shutdown := func() {
		s.drain(killSpawns)
		// Hold reconcileMu so a late Reconcile cannot start children during drain.
		s.reconcileMu.Lock()
		stopServices()
		s.mu.Lock()
		s.svcCtx = nil
		s.mu.Unlock()
		s.stopAllServices()
		s.reconcileMu.Unlock()
	}

	if len(hooks) == 0 {
		<-ctx.Done()
		shutdown()
		return ctx.Err()
	}

	// Subscribe to all events; filter per-hook. The bus is shared across
	// reload generations, so the current Service's Bus() is always the same
	// long-lived bus — we still go through getSvc so a nil/racy generation
	// surfaces loudly rather than using a captured pointer.
	bus := s.svc().Bus()
	sub := bus.Subscribe(core.EventFilter{}, 128)
	defer bus.Unsubscribe(sub.ID)

	for {
		select {
		case <-ctx.Done():
			shutdown()
			return ctx.Err()
		case e, ok := <-sub.Ch:
			if !ok {
				// Dropped (slow supervisor). Resubscribe.
				s.log("supervisor", "dropped by bus; resubscribing")
				sub = s.svc().Bus().Subscribe(core.EventFilter{}, 128)
				continue
			}
			for _, h := range hooks {
				if h.MatchesEvent(e, s.cardBoardMembership, s.cardTypeID) {
					s.wg.Add(1)
					go func(h config.Extension, e *core.Event) {
						defer s.wg.Done()
						s.spawn(spawnCtx, h, e) // async: spawn ordered, completion not
					}(h, e)
				}
			}
		}
	}
}

// drain waits up to drainTimeout for in-flight hook subprocesses to finish; if
// the grace period elapses, it cancels their context (SIGKILLing stragglers via
// exec.CommandContext) and waits for them to exit. This is the shutdown template
// the Sprint B outbox tailer and webhook worker reuse: a WaitGroup of in-flight
// work + a bounded drain + a kill fallback, so shutdown neither hangs nor
// abandons live subprocesses.
func (s *Supervisor) drain(killSpawns context.CancelFunc) {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		// All in-flight hooks finished within the grace period.
	case <-time.After(s.drainTimeout):
		s.log("supervisor", fmt.Sprintf("drain timeout (%s) exceeded; killing in-flight hooks", s.drainTimeout))
		killSpawns()
		<-done
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
	setProcessGroup(cmd) // kill the whole group on cancel, not just the shell
	// Backstop: if a hook exits but leaves a child holding its output pipes,
	// don't let Wait block indefinitely — force-close after a bounded delay.
	cmd.WaitDelay = 5 * time.Second
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
// Always reads the current generation via getSvc — never a closed Service.
func (s *Supervisor) cardBoardMembership(cardID string) string {
	svc := s.svc()
	if svc == nil {
		return ""
	}
	c, err := svc.GetCard(context.Background(), cardID)
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
// hook filters (filter.type_id). Always reads the current generation.
func (s *Supervisor) cardTypeID(cardID string) string {
	svc := s.svc()
	if svc == nil {
		return ""
	}
	c, err := svc.GetCard(context.Background(), cardID)
	if err != nil {
		return ""
	}
	return c.TypeID
}

// boards returns the current generation's boards via Workspace introspection.
func (s *Supervisor) boards() []*core.Board {
	svc := s.svc()
	if svc == nil {
		return nil
	}
	snap, _ := svc.Workspace(context.Background())
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
