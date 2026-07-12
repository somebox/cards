package hooks_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/hooks"
)

// Test helper children re-exec this test binary with HOOKS_TEST_CHILD set.
// Used for crash-loop and SIGTERM-ignore drain tests (mac+linux signal paths).
func TestMain(m *testing.M) {
	switch os.Getenv("HOOKS_TEST_CHILD") {
	case "crash":
		os.Exit(1)
	case "exit0":
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testChildArgs() []string {
	return []string{os.Args[0], "-test.run=^$", "-test.v=false"}
}

func testChildEnv(mode string) map[string]string {
	return map[string]string{"HOOKS_TEST_CHILD": mode}
}

func newServiceSup(t *testing.T, exts []config.Extension) (*hooks.Supervisor, *core.Service, string) {
	t.Helper()
	svc, ws, dir := newSvc(t)
	sup := hooks.New(func() *core.Service { return svc }, ws, exts, dir, "http://127.0.0.1:8787/v1")
	return sup, svc, dir
}

func TestBackoffDelayCapsAndEscalates(t *testing.T) {
	b := hooks.BackoffConfig{
		Initial: 100 * time.Millisecond,
		Max:     800 * time.Millisecond,
		Factor:  2,
	}
	if d := b.Delay(0); d != 100*time.Millisecond {
		t.Fatalf("Delay(0)=%s want 100ms", d)
	}
	if d := b.Delay(1); d != 200*time.Millisecond {
		t.Fatalf("Delay(1)=%s want 200ms", d)
	}
	if d := b.Delay(2); d != 400*time.Millisecond {
		t.Fatalf("Delay(2)=%s want 400ms", d)
	}
	if d := b.Delay(3); d != 800*time.Millisecond {
		t.Fatalf("Delay(3)=%s want 800ms (cap)", d)
	}
	if d := b.Delay(10); d != 800*time.Millisecond {
		t.Fatalf("Delay(10)=%s want 800ms (still capped)", d)
	}
}

func TestBackoffMinHealthyUptimeResetsStreak(t *testing.T) {
	b := hooks.BackoffConfig{MinHealthyUptime: time.Second}
	if s := b.NextStreak(3, 500*time.Millisecond); s != 4 {
		t.Fatalf("brief uptime: NextStreak=%d want 4", s)
	}
	if s := b.NextStreak(3, time.Second); s != 0 {
		t.Fatalf("healthy uptime: NextStreak=%d want 0", s)
	}
}

// TestServiceCrashLoopEscalatesBackoff verifies a runs-briefly-then-crashes
// service escalates toward the backoff cap (no CPU pin / tight restart loop).
// Uses an injectable sleep that records delays instead of wall-clock waits.
func TestServiceCrashLoopEscalatesBackoff(t *testing.T) {
	ext := config.Extension{
		ID: "crashy", Kind: "service", Autostart: true,
		RestartPolicy: config.RestartOnFailure,
		Run:           testChildArgs(),
		Env:           testChildEnv("crash"),
	}
	sup, _, _ := newServiceSup(t, []config.Extension{ext})
	sup.SetBackoff(hooks.BackoffConfig{
		Initial:          10 * time.Millisecond,
		Max:              80 * time.Millisecond,
		Factor:           2,
		MinHealthyUptime: time.Hour, // never reset on brief crashes
	})

	var delayMu sync.Mutex
	var delays []time.Duration
	sup.SetSleep(func(ctx context.Context, d time.Duration) error {
		delayMu.Lock()
		delays = append(delays, d)
		delayMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil // no real wait — drive the loop as fast as process spawn allows
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sup.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		delayMu.Lock()
		n := len(delays)
		delayMu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	delayMu.Lock()
	got := append([]time.Duration(nil), delays...)
	delayMu.Unlock()
	if len(got) < 4 {
		t.Fatalf("recorded %d backoff delays, want ≥4 (crash-loop)", len(got))
	}
	// Delays must escalate then cap: 10, 20, 40, 80, 80, ...
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("delay[%d]=%s want %s (full=%v)", i, got[i], w, got)
		}
	}
	// Further delays stay at cap (no reset on brief crashes).
	for i := 3; i < len(got); i++ {
		if got[i] != 80*time.Millisecond {
			t.Fatalf("delay[%d]=%s want cap 80ms (min-healthy-uptime must not reset)", i, got[i])
		}
	}
}

// TestServiceForceKillIgnoresSIGTERM: a child that ignores SIGTERM is
// SIGKILL'd within the drain grace window; Run always returns.
func TestServiceForceKillIgnoresSIGTERM(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "alive")
	// bash trap '' TERM ignores SIGTERM; sleep loop until SIGKILL.
	ext := config.Extension{
		ID: "hung", Kind: "service", Autostart: true,
		RestartPolicy: config.RestartNever,
		Run: []string{"bash", "-c",
			`trap '' TERM; echo $$ >` + marker + `; while true; do sleep 1; done`},
	}
	sup, _, _ := newServiceSup(t, []config.Extension{ext})
	sup.SetDrainTimeout(200 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sup.Run(ctx) }()

	var childPID int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(marker)
		if err == nil {
			fmt.Sscanf(string(data), "%d", &childPID)
			if childPID > 0 {
				if err := syscall.Kill(childPID, 0); err == nil {
					break
				}
			}
			childPID = 0
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID <= 0 {
		cancel()
		<-done
		t.Fatal("hanging child never started (no live pid marker)")
	}

	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return; hung child was not force-killed within grace")
	}
	elapsed := time.Since(start)
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("shutdown took %s; expected force-kill near 200ms drain timeout", elapsed)
	}
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Fatalf("child pid %d still alive after supervisor drain", childPID)
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("shutdown returned in %s; expected ~drainTimeout wait before SIGKILL of SIGTERM-ignoring child", elapsed)
	}
}

// TestServiceReadyGateBlocksAutostart until the ready channel closes.
func TestServiceReadyGateBlocksAutostart(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	ext := config.Extension{
		ID: "gated", Kind: "service", Autostart: true,
		RestartPolicy: config.RestartNever,
		Run:           []string{"bash", "-c", "touch " + marker},
	}
	sup, _, _ := newServiceSup(t, []config.Extension{ext})
	ready := make(chan struct{})
	sup.SetReady(ready)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _ = sup.Run(ctx) }()

	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("service started before ready gate opened")
	}
	close(ready)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			cancel()
			<-done
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("service did not start after ready gate opened")
}

// TestServiceRestartNeverDoesNotRestart on clean or unclean exit.
func TestServiceOnFailureSkipsCleanExit(t *testing.T) {
	ext := config.Extension{
		ID: "once", Kind: "service", Autostart: true,
		RestartPolicy: config.RestartOnFailure,
		Run:           testChildArgs(),
		Env:           testChildEnv("exit0"),
	}
	sup, _, _ := newServiceSup(t, []config.Extension{ext})
	var sleeps atomic.Int32
	sup.SetSleep(func(ctx context.Context, d time.Duration) error {
		sleeps.Add(1)
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sup.Run(ctx) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done
	if sleeps.Load() != 0 {
		t.Fatalf("on-failure restarted after clean exit 0 (sleeps=%d)", sleeps.Load())
	}
}

func TestShouldRestartPolicy(t *testing.T) {
	// Exercised via integration above; keep a focused policy table via
	// restart-always crashing once then cancel.
	ext := config.Extension{
		ID: "always", Kind: "service", Autostart: true,
		RestartPolicy: config.RestartAlways,
		Run:           testChildArgs(),
		Env:           testChildEnv("exit0"),
	}
	sup, _, _ := newServiceSup(t, []config.Extension{ext})
	var sleeps atomic.Int32
	sup.SetSleep(func(ctx context.Context, d time.Duration) error {
		n := sleeps.Add(1)
		if n >= 2 {
			return context.Canceled
		}
		return nil
	})
	sup.SetBackoff(hooks.BackoffConfig{
		Initial: 1 * time.Millisecond, Max: 1 * time.Millisecond, Factor: 2,
		MinHealthyUptime: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sup.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && sleeps.Load() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run hung")
	}
	if sleeps.Load() < 2 {
		t.Fatalf("restart_policy=always did not restart on exit 0 (sleeps=%d)", sleeps.Load())
	}
}

// TestServiceGrandchildHoldingStdoutDoesNotWedgeShutdown: a SIGTERM-ignoring
// child uses job control (set -m) to spawn a grandchild in its OWN process
// group that inherits stdout, so the drain's SIGKILL of the service's pgroup
// misses it. Because service stdio is a real file (not an exec-managed pipe),
// cmd.Wait returns as soon as the child itself is reaped and shutdown stays
// bounded. With buffer/pipe stdio this test wedges Run past the 5s guard.
func TestServiceGrandchildHoldingStdoutDoesNotWedgeShutdown(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "pids")
	ext := config.Extension{
		ID: "holder", Kind: "service", Autostart: true,
		RestartPolicy: config.RestartNever,
		Run: []string{"bash", "-c",
			`set -m; sleep 30 & echo "$$ $!" >` + marker + `; trap '' TERM; while true; do sleep 1; done`},
	}
	sup, _, _ := newServiceSup(t, []config.Extension{ext})
	sup.SetDrainTimeout(200 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sup.Run(ctx) }()

	var childPID, grandPID int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil {
			if n, _ := fmt.Sscanf(string(data), "%d %d", &childPID, &grandPID); n == 2 && childPID > 0 && grandPID > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID <= 0 || grandPID <= 0 {
		cancel()
		<-done
		t.Fatal("holder child/grandchild never wrote pid marker")
	}
	defer func() { _ = syscall.Kill(grandPID, syscall.SIGKILL) }()

	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return: shutdown wedged by grandchild holding stdout")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("shutdown took %s; want bounded by drain+kill (<3s)", elapsed)
	}
	// The scenario is only proven if the grandchild actually outlived the
	// pgroup SIGKILL while holding the inherited stdout fd.
	if err := syscall.Kill(grandPID, 0); err != nil {
		t.Fatalf("grandchild %d did not survive pgroup kill — scenario not exercised: %v", grandPID, err)
	}
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Fatalf("service child %d still alive after shutdown", childPID)
	}
}
