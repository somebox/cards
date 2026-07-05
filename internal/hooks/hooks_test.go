package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/hooks"
	"github.com/somebox/cards/internal/sqlite"
)

// newSvc builds an in-memory service for hook tests.
func newSvc(t *testing.T) (*core.Service, *core.Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	ws := &core.Workspace{
		ID:       "t",
		Name:     "T",
		Columns:  []core.Column{{ID: "todo", Name: "Todo"}, {ID: "review", Name: "Review"}, {ID: "done", Name: "Done"}},
		Settings: core.WorkspaceSettings{DefaultUser: "u", StrictFields: true},
	}
	types := map[string]*core.CardType{
		"task": {ID: "task", Name: "Task", SchemaVersion: 1,
			Fields:         []core.FieldDef{{ID: "description", Type: core.FieldText, Required: true}},
			AllowedColumns: []string{"todo", "review", "done"}},
		"bug": {ID: "bug", Name: "Bug", SchemaVersion: 1,
			Fields:         []core.FieldDef{{ID: "description", Type: core.FieldText, Required: true}},
			AllowedColumns: []string{"todo", "review", "done"}},
	}
	boards := map[string]*core.Board{
		"eng": {ID: "eng", Name: "Eng", Columns: []string{"todo", "review", "done"}, CardTypeIDs: []string{"task", "bug"}},
	}
	st, err := sqlite.Open(filepath.Join(dir, "test.db"), ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_ = st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human", CreatedAt: time.Now().UTC()})
	svc := core.NewService(ws, types, boards, st)
	return svc, ws, dir
}

// TestHookFiresOnMatchingEvent verifies the supervisor spawns the hook's
// command when an event matches, writing to a log file.
func TestHookFiresOnMatchingEvent(t *testing.T) {
	svc, ws, dir := newSvc(t)
	logFile := filepath.Join(dir, "fired.log")
	// A bash hook that appends the event card_id to a log.
	hook := config.Extension{
		ID: "fire", Kind: "hook", On: "status_changed",
		Filter: config.HookFilter{BoardID: "eng", ToStatus: "review"},
		Run:    []string{"bash", "-c", `echo "$(cat)" >> ` + logFile},
	}
	sup := hooks.New(svc, ws, []config.Extension{hook}, dir, "http://127.0.0.1:8787/v1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	// Create a card + move to review (triggers the hook).
	time.Sleep(100 * time.Millisecond) // let supervisor subscribe
	c, err := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u"})
	if err != nil {
		t.Fatal(err)
	}
	st := "review"
	_, err = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 1, Status: &st, Actor: "u"})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the hook subprocess to run.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(logFile); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("hook did not write log: %v", err)
	}
	if !strings.Contains(string(data), c.ID) {
		t.Errorf("hook log does not mention card id %s: %s", c.ID, data)
	}
}

// fireSlowHook creates a card and moves it to review, triggering a hook whose
// command sleeps for `sleep` before writing "done" to logFile. Returns after
// the mutation so the caller controls the shutdown timing.
func fireSlowHook(t *testing.T, sleep string, logFile string, drainTimeout time.Duration) (*hooks.Supervisor, context.CancelFunc, chan struct{}) {
	t.Helper()
	svc, ws, dir := newSvc(t)
	if logFile == "" {
		logFile = filepath.Join(dir, "slow.log")
	}
	hook := config.Extension{
		ID: "slow", Kind: "hook", On: "status_changed",
		Filter: config.HookFilter{ToStatus: "review"},
		Run:    []string{"bash", "-c", `sleep ` + sleep + `; echo done >> ` + logFile},
	}
	sup := hooks.New(svc, ws, []config.Extension{hook}, dir, "http://127.0.0.1:8787/v1")
	if drainTimeout > 0 {
		sup.SetDrainTimeout(drainTimeout)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); sup.Run(ctx) }()
	time.Sleep(100 * time.Millisecond) // let the supervisor subscribe

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u"})
	if err != nil {
		t.Fatal(err)
	}
	st := "review"
	if _, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 1, Status: &st, Actor: "u"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let the hook subprocess start (still sleeping)
	return sup, cancel, done
}

// On shutdown, Run drains in-flight hooks: a hook already running when the
// context is cancelled is awaited to completion, not killed.
func TestSupervisorDrainsInFlightHooks(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "drain.log")
	_, cancel, done := fireSlowHook(t, "0.3", logFile, 5*time.Second)

	cancel() // hook is mid-sleep; Run must drain (await) it before returning
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel (drain hung)")
	}
	data, err := os.ReadFile(logFile)
	if err != nil || !strings.Contains(string(data), "done") {
		t.Fatalf("in-flight hook was not drained to completion: err=%v data=%q", err, data)
	}
}

// A hook that overruns the drain timeout is killed, and Run returns promptly
// (bounded shutdown, not an indefinite wait).
func TestSupervisorDrainTimeoutKillsStragglers(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "straggler.log")
	_, cancel, done := fireSlowHook(t, "3", logFile, 100*time.Millisecond)

	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return; drain was not bounded")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("drain took %s; expected it bounded near the 100ms timeout", elapsed)
	}
	// The straggler was killed before its 3s sleep finished, so it never wrote.
	if data, err := os.ReadFile(logFile); err == nil && strings.Contains(string(data), "done") {
		t.Error("straggler completed; expected it killed at the drain timeout")
	}
}

// TestHookDoesNotFireOnNonMatching verifies the filter rejects non-matching events.
func TestHookDoesNotFireOnNonMatching(t *testing.T) {
	svc, ws, dir := newSvc(t)
	logFile := filepath.Join(dir, "fired2.log")
	hook := config.Extension{
		ID: "fire2", Kind: "hook", On: "status_changed",
		Filter: config.HookFilter{ToStatus: "review"},
		Run:    []string{"bash", "-c", `echo hit >> ` + logFile},
	}
	sup := hooks.New(svc, ws, []config.Extension{hook}, dir, "http://127.0.0.1:8787/v1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	time.Sleep(100 * time.Millisecond)
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u"})
	// Move to done (not review) — should NOT fire.
	st := "done"
	_, _ = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 1, Status: &st, Actor: "u"})
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(logFile); err == nil {
		t.Errorf("hook fired for non-matching status (to=done, filter=review)")
	}
}

// TestHookFilterTypeID verifies the type_id filter only fires for matching
// card types (was previously parsed but never applied in MatchesEvent).
func TestHookFilterTypeID(t *testing.T) {
	svc, ws, dir := newSvc(t)
	logFile := filepath.Join(dir, "fired_type.log")
	// Hook filtered to type_id=bug only.
	hook := config.Extension{
		ID: "bug-only", Kind: "hook", On: "status_changed",
		Filter: config.HookFilter{TypeID: "bug", ToStatus: "review"},
		Run:    []string{"bash", "-c", `echo hit >> ` + logFile},
	}
	sup := hooks.New(svc, ws, []config.Extension{hook}, dir, "http://127.0.0.1:8787/v1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)
	time.Sleep(100 * time.Millisecond) // let supervisor subscribe

	// Create a task card and move to review — should NOT fire (filter is bug).
	task, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u"})
	st := "review"
	_, _ = svc.PatchCard(ctx, task.ID, core.PatchCardRequest{Version: 1, Status: &st, Actor: "u"})
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(logFile); err == nil {
		t.Error("hook fired for task card (filter is type_id=bug)")
	}

	// Create a bug card and move to review — SHOULD fire.
	bug, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "bug", Title: "B", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u"})
	_, _ = svc.PatchCard(ctx, bug.ID, core.PatchCardRequest{Version: 1, Status: &st, Actor: "u"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(logFile); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(logFile); err != nil {
		t.Fatal("hook did not fire for bug card matching type_id=bug")
	}
}
