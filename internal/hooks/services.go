package hooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/somebox/cards/internal/config"
)

// sleepFunc is injectable so crash-loop tests can drive backoff without wall
// clock waits. Production uses time.Sleep (respecting ctx).
type sleepFunc func(ctx context.Context, d time.Duration) error

func defaultSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// managedService is one supervised kind:service child.
type managedService struct {
	ext    config.Extension
	fp     string // ServiceDeclFingerprint at start (reconcile identity of decl)
	cancel context.CancelFunc
	done   chan struct{} // closed when supervise loop exits
	starts atomic.Int32  // successful Start count (tests / observability)
	mu     sync.Mutex
	cmd    *exec.Cmd
	pid    int
}

// Services returns declared kind:service extensions (all of them; Autostart
// gates which ones the supervisor starts).
func (s *Supervisor) Services() []config.Extension {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []config.Extension{}
	for _, e := range s.extensions {
		if e.Kind == "service" {
			out = append(out, e)
		}
	}
	return out
}

// AutostartServices returns kind:service extensions with Autostart true.
func (s *Supervisor) AutostartServices() []config.Extension {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []config.Extension{}
	for _, e := range s.extensions {
		if e.Kind == "service" && e.Autostart {
			out = append(out, e)
		}
	}
	return out
}

// SetReady installs a listener-ready gate: Run waits for ready to close (or
// receive) before starting autostart services. Nil means start immediately
// (standalone run-extensions dialing an already-up server). Call before Run.
func (s *Supervisor) SetReady(ready <-chan struct{}) { s.ready = ready }

// SetBackoff overrides restart backoff. Call before Run.
func (s *Supervisor) SetBackoff(b BackoffConfig) { s.backoff = b }

// SetSleep replaces the backoff sleeper (tests). Call before Run.
func (s *Supervisor) SetSleep(fn sleepFunc) {
	if fn == nil {
		s.sleep = defaultSleep
		return
	}
	s.sleep = fn
}

// serviceLogFile opens the append-mode per-service log sink. Handing the child
// a real *os.File (not a buffer/pipe) is load-bearing twice over: exec passes
// the fd straight through, so a chatty long-running service streams to disk
// instead of growing supervisor memory, and cmd.Wait never blocks on I/O — a
// grandchild that escaped the process group while holding stdout cannot wedge
// shutdown. Returns nil (child gets /dev/null) if the log dir is unusable.
func (s *Supervisor) serviceLogFile(extID string) *os.File {
	logDir := filepath.Join(s.workspaceDir, ".cards", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(logDir, extID+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}

// superviseService runs one service until ctx is cancelled or the restart
// policy says stop. Backoff escalates when uptime is below MinHealthyUptime.
func (s *Supervisor) superviseService(ctx context.Context, ms *managedService) {
	ext := ms.ext
	if len(ext.Run) == 0 {
		return
	}
	policy := ext.RestartPolicy
	if policy == "" {
		policy = config.RestartOnFailure
	}
	streak := 0
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		s.log(ext.ID, "starting")
		exitErr := s.runServiceOnce(ctx, ms)
		uptime := time.Since(start)
		if ctx.Err() != nil {
			// Shutdown drain owns terminate; just exit the loop.
			return
		}
		if exitErr != nil {
			s.log(ext.ID, fmt.Sprintf("exited err=%v uptime=%s", exitErr, uptime))
		} else {
			s.log(ext.ID, fmt.Sprintf("exited ok uptime=%s", uptime))
		}
		if !shouldRestart(policy, exitErr) {
			s.log(ext.ID, fmt.Sprintf("not restarting (policy=%s)", policy))
			return
		}
		streak = s.backoff.NextStreak(streak, uptime)
		// Delay uses streak-1 equivalent: after first unhealthy exit streak
		// becomes 1, but Delay(0) is Initial. Map: delay index = streak-1
		// when streak>0, else 0.
		delayIdx := streak - 1
		if delayIdx < 0 {
			delayIdx = 0
		}
		delay := s.backoff.Delay(delayIdx)
		s.log(ext.ID, fmt.Sprintf("restart in %s (streak=%d)", delay, streak))
		if err := s.sleep(ctx, delay); err != nil {
			return
		}
	}
}

// shouldRestart reports whether RestartPolicy wants another start after exitErr
// (nil means clean exit 0).
func shouldRestart(policy string, exitErr error) bool {
	switch policy {
	case config.RestartNever:
		return false
	case config.RestartAlways:
		return true
	case config.RestartOnFailure, "":
		return exitErr != nil
	default:
		// Validated at load; treat unknown as on-failure.
		return exitErr != nil
	}
}

// runServiceOnce starts the child, waits for exit or ctx cancel. On cancel it
// does not itself signal — stopAllServices owns SIGTERM→grace→SIGKILL so a
// single drain path covers all children. Returns the Wait error (nil = exit 0).
func (s *Supervisor) runServiceOnce(ctx context.Context, ms *managedService) error {
	ext := ms.ext
	cmd := exec.Command(ext.Run[0], ext.Run[1:]...)
	setServiceProcessGroup(cmd)
	cmd.Env = append(os.Environ(),
		"CARDS_URL="+s.cardsURL,
		"CARDS_WORKSPACE="+s.workspaceDir,
		"CARDS_USER="+s.ws.Settings.DefaultUser,
	)
	for k, v := range ext.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cwd := ext.Cwd
	if cwd == "" {
		cwd = s.workspaceDir
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(s.workspaceDir, cwd)
	}
	cmd.Dir = cwd
	logf := s.serviceLogFile(ext.ID)
	if logf != nil {
		fmt.Fprintf(logf, "--- %s start ---\n", time.Now().UTC().Format(time.RFC3339))
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	// With *os.File stdio exec creates no pipes, so Wait is bounded by the
	// process alone; WaitDelay backstops that invariant if the sink ever
	// changes (mirrors the hook path in hooks.go).
	cmd.WaitDelay = 5 * time.Second

	if err := cmd.Start(); err != nil {
		if logf != nil {
			logf.Close()
		}
		s.log(ext.ID, fmt.Sprintf("start failed: %v", err))
		return err
	}
	ms.starts.Add(1)
	ms.mu.Lock()
	ms.cmd = cmd
	ms.pid = cmd.Process.Pid
	ms.mu.Unlock()

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var err error
	cancelled := false
	select {
	case err = <-waitDone:
	case <-ctx.Done():
		// Parent is shutting down; leave signalling to stopAllServices/stopOne.
		// Wait is bounded (file stdio + WaitDelay), so this cannot wedge.
		cancelled = true
		err = <-waitDone
	}
	ms.mu.Lock()
	ms.cmd = nil
	ms.pid = 0
	ms.mu.Unlock()
	if logf != nil {
		fmt.Fprintf(logf, "--- %s exit err=%v ---\n", time.Now().UTC().Format(time.RFC3339), err)
		logf.Close()
	}
	if cancelled && err == nil {
		return ctx.Err()
	}
	return err
}

// stopAllServices SIGTERMs every managed child, waits up to drainTimeout, then
// SIGKILLs stragglers' process groups. Always returns (bounded). Called from
// Run after ctx cancel; cancel of the service ctx alone is not enough for
// SIGTERM-ignoring children.
func (s *Supervisor) stopAllServices() {
	s.mu.Lock()
	list := append([]*managedService(nil), s.services...)
	s.mu.Unlock()

	for _, ms := range list {
		ms.mu.Lock()
		pid := ms.pid
		ms.mu.Unlock()
		if pid > 0 {
			s.log(ms.ext.ID, "stopping (SIGTERM)")
			_ = terminateProcessGroup(pid)
		}
	}

	done := make(chan struct{})
	go func() {
		s.svcWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(s.drainTimeout):
		s.log("supervisor", fmt.Sprintf("service drain timeout (%s); SIGKILL", s.drainTimeout))
		for _, ms := range list {
			ms.mu.Lock()
			pid := ms.pid
			ms.mu.Unlock()
			if pid > 0 {
				s.log(ms.ext.ID, "force kill (SIGKILL)")
				_ = killProcessGroup(pid)
			}
		}
		// Post-SIGKILL wait is bounded too: an unreapable child (kernel
		// D-state) must not turn "shutdown always returns" into a lie. If it
		// trips, the waiter goroutine is leaked deliberately and loudly.
		select {
		case <-done:
		case <-time.After(s.drainTimeout):
			s.log("supervisor", "service drain incomplete after SIGKILL; leaking waiter goroutine")
		}
	}
}
