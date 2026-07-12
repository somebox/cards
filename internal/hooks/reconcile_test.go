package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/hooks"
)

func TestServiceDeclFingerprintStableAndSensitive(t *testing.T) {
	base := config.Extension{
		ID: "svc", Kind: "service", Autostart: true,
		Run: []string{"node", "a.mjs"},
		Env: map[string]string{"B": "2", "A": "1"},
		Cwd: "ext",
		RestartPolicy: config.RestartOnFailure,
	}
	same := config.Extension{
		ID: "svc", Kind: "service", Autostart: true,
		Run: []string{"node", "a.mjs"},
		Env: map[string]string{"A": "1", "B": "2"}, // different map order
		Cwd: "ext",
		RestartPolicy: config.RestartOnFailure,
	}
	if hooks.ServiceDeclFingerprint(base) != hooks.ServiceDeclFingerprint(same) {
		t.Fatal("env key order must not change fingerprint")
	}
	changedRun := base
	changedRun.Run = []string{"node", "b.mjs"}
	if hooks.ServiceDeclFingerprint(base) == hooks.ServiceDeclFingerprint(changedRun) {
		t.Fatal("run change must change fingerprint")
	}
	changedEnv := base
	changedEnv.Env = map[string]string{"A": "1", "B": "9"}
	if hooks.ServiceDeclFingerprint(base) == hooks.ServiceDeclFingerprint(changedEnv) {
		t.Fatal("env change must change fingerprint")
	}
	// Identity is id — fingerprint ignores id/autostart/description.
	otherID := base
	otherID.ID = "other"
	otherID.Autostart = false
	otherID.Description = "x"
	if hooks.ServiceDeclFingerprint(base) != hooks.ServiceDeclFingerprint(otherID) {
		t.Fatal("id/autostart/description must not affect fingerprint")
	}
}

func waitServicePID(t *testing.T, sup *hooks.Supervisor, id string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pid := sup.ServicePID(id); pid > 0 {
			if err := syscall.Kill(pid, 0); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("service %q never started within %s", id, timeout)
	return 0
}

func longRunningExt(id, marker string) config.Extension {
	return config.Extension{
		ID: id, Kind: "service", Autostart: true,
		RestartPolicy: config.RestartNever,
		Run: []string{"bash", "-c",
			`echo $$ >` + marker + `; while true; do sleep 1; done`},
	}
}

func runSup(t *testing.T, exts []config.Extension) (*hooks.Supervisor, context.CancelFunc) {
	t.Helper()
	svc, ws, dir := newSvc(t)
	sup := hooks.New(func() *core.Service { return svc }, ws, exts, dir, "http://127.0.0.1:8787/v1")
	sup.SetDrainTimeout(500 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sup.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("supervisor Run did not return")
		}
	})
	return sup, cancel
}

// TestReconcileUnchangedZeroChurn: board-create-shaped reload (same decls)
// must leave the running child untouched.
func TestReconcileUnchangedZeroChurn(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "alive")
	ext := longRunningExt("keeper", marker)
	sup, _ := runSup(t, []config.Extension{ext})

	pid := waitServicePID(t, sup, "keeper", 3*time.Second)
	starts := sup.ServiceStarts("keeper")
	if starts < 1 {
		t.Fatalf("starts=%d want ≥1", starts)
	}

	// Same declaration snapshot (what board-create reload hands off).
	sup.Reconcile([]config.Extension{ext})
	time.Sleep(100 * time.Millisecond)

	if got := sup.ServicePID("keeper"); got != pid {
		t.Fatalf("unchanged reconcile churned pid: before=%d after=%d", pid, got)
	}
	if got := sup.ServiceStarts("keeper"); got != starts {
		t.Fatalf("unchanged reconcile restarted service: starts before=%d after=%d", starts, got)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("original child pid %d died on unchanged reconcile: %v", pid, err)
	}
}

// TestReconcileDeclarationChangedRestartsExactlyThatService: edited run/env
// drains+restarts only the changed id.
func TestReconcileDeclarationChangedRestartsExactlyThatService(t *testing.T) {
	dir := t.TempDir()
	m1 := filepath.Join(dir, "a")
	m2 := filepath.Join(dir, "b")
	a := longRunningExt("alpha", m1)
	b := longRunningExt("beta", m2)
	sup, _ := runSup(t, []config.Extension{a, b})

	pidA := waitServicePID(t, sup, "alpha", 3*time.Second)
	pidB := waitServicePID(t, sup, "beta", 3*time.Second)
	startsB := sup.ServiceStarts("beta")

	// Change only alpha's declaration (env bump → new fingerprint).
	a2 := a
	a2.Env = map[string]string{"MARK": "v2"}
	a2.Run = []string{"bash", "-c",
		`echo $$ >` + m1 + `.v2; while true; do sleep 1; done`}
	sup.Reconcile([]config.Extension{a2, b})

	deadline := time.Now().Add(5 * time.Second)
	var newA int
	for time.Now().Before(deadline) {
		newA = sup.ServicePID("alpha")
		if newA > 0 && newA != pidA {
			if err := syscall.Kill(newA, 0); err == nil {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		newA = 0
	}
	if newA == 0 {
		t.Fatal("alpha did not restart with a new pid after declaration change")
	}
	if err := syscall.Kill(pidA, 0); err == nil {
		t.Fatalf("old alpha pid %d still alive after declaration-changed restart", pidA)
	}
	if got := sup.ServicePID("beta"); got != pidB {
		t.Fatalf("beta churned on alpha edit: before=%d after=%d", pidB, got)
	}
	if got := sup.ServiceStarts("beta"); got != startsB {
		t.Fatalf("beta restarted on alpha edit: starts before=%d after=%d", startsB, got)
	}
}

// TestReconcileRemovedStops: removed declaration drains+stops that service.
func TestReconcileRemovedStops(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "gone")
	ext := longRunningExt("ephemeral", marker)
	sup, _ := runSup(t, []config.Extension{ext})

	pid := waitServicePID(t, sup, "ephemeral", 3*time.Second)
	sup.Reconcile(nil) // no services in snapshot

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sup.ServicePID("ephemeral") == 0 {
			if err := syscall.Kill(pid, 0); err != nil {
				return // stopped
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("removed service pid %d still running (or still tracked)", pid)
}

// TestReconcileAddedStarts: new autostart service id is started.
func TestReconcileAddedStarts(t *testing.T) {
	dir := t.TempDir()
	sup, _ := runSup(t, nil)
	time.Sleep(50 * time.Millisecond)

	marker := filepath.Join(dir, "new")
	ext := longRunningExt("newbie", marker)
	sup.Reconcile([]config.Extension{ext})
	_ = waitServicePID(t, sup, "newbie", 3*time.Second)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("added service did not write pid marker: %v", err)
	}
}
