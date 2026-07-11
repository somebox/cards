package main

// Sprint P3b (card_ec61b093): definitions --watch poller tests. Driven by an
// injectable clock + scanOnce — no real sleeps, no fsnotify, no -race flake.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/httpapi"
)

func newWatchFixture(t *testing.T) (*reloadableApp, string, *manualClock, *defsWatcher) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".cards")
	if _, err := initWorkspace(dir); err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	st, svc, result, err := openWorkspace(dir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	t.Cleanup(func() { svc.Close(); st.Close() })
	srv, err := httpapi.New(svc, result.Workspace, result.CardTypes, result.Boards, result.Themes, st)
	if err != nil {
		t.Fatalf("http server: %v", err)
	}
	app := newReloadableApp(dir, st, svc, result, srv.Router())
	clk := newManualClock(time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC))
	w := newDefsWatcher(app, time.Second, 300*time.Millisecond, clk)
	w.lastFP = definitionsFingerprint(w.defsDir)
	return app, dir, clk, w
}

func collectBus(t *testing.T, bus core.Bus, types ...string) (<-chan *core.Event, func()) {
	t.Helper()
	sub := bus.Subscribe(core.EventFilter{Types: types}, 64)
	ch := make(chan *core.Event, 64)
	done := make(chan struct{})
	go func() {
		defer close(ch)
		for {
			select {
			case <-done:
				return
			case e, ok := <-sub.Ch:
				if !ok {
					return
				}
				select {
				case ch <- e:
				case <-done:
					return
				}
			}
		}
	}()
	return ch, func() {
		close(done)
		bus.Unsubscribe(sub.ID)
	}
}

func writeBoardDef(t *testing.T, dir, id, body string) {
	t.Helper()
	path := filepath.Join(dir, "definitions", "boards", id+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func drainReloads(ch <-chan *core.Event) int {
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			return n
		}
	}
}

func waitEvent(t *testing.T, ch <-chan *core.Event, timeout time.Duration) *core.Event {
	t.Helper()
	select {
	case e := <-ch:
		if e == nil {
			t.Fatal("nil event")
		}
		return e
	case <-time.After(timeout):
		t.Fatal("timed out waiting for bus event")
		return nil
	}
}

// TestWatchDebounceCoalesces: a burst of fingerprint changes within the
// debounce window produces exactly one reload after the window elapses.
func TestWatchDebounceCoalesces(t *testing.T) {
	app, dir, clk, w := newWatchFixture(t)
	ch, stop := collectBus(t, app.bus, string(core.EventDefinitionReload))
	defer stop()

	writeBoardDef(t, dir, "alpha", `{"id":"alpha","name":"Alpha","columns":["todo"],"card_type_ids":["task"]}`+"\n")
	w.scanOnce() // arm pending
	writeBoardDef(t, dir, "alpha", `{"id":"alpha","name":"Alpha 2","columns":["todo"],"card_type_ids":["task"]}`+"\n")
	w.scanOnce() // reset pending to new fp
	writeBoardDef(t, dir, "alpha", `{"id":"alpha","name":"Alpha 3","columns":["todo"],"card_type_ids":["task"]}`+"\n")
	w.scanOnce() // reset again — still inside debounce

	if n := drainReloads(ch); n != 0 {
		t.Fatalf("reload fired before debounce elapsed: %d", n)
	}

	clk.Advance(299 * time.Millisecond)
	w.scanOnce()
	if n := drainReloads(ch); n != 0 {
		t.Fatalf("reload fired 1ms early: %d", n)
	}

	clk.Advance(1 * time.Millisecond)
	w.scanOnce()
	e := waitEvent(t, ch, time.Second)
	if e.Type != core.EventDefinitionReload {
		t.Fatalf("got %s, want definition_reloaded", e.Type)
	}
	// One logical reload fans out one event per board currently served.
	n := 1 + drainReloads(ch)
	boards := len(app.cur.Load().result.Boards)
	if n != boards {
		t.Fatalf("want %d fan-out events (one logical reload), got %d", boards, n)
	}

	found := false
	for id := range app.cur.Load().result.Boards {
		if id == "alpha" {
			found = true
		}
	}
	if !found {
		t.Error("alpha board not served after watch reload")
	}
}

// TestWatchSelfWriteSuppressed: handleCreateBoard's write-then-reload must not
// cause a second poller reload for the same fingerprint.
func TestWatchSelfWriteSuppressed(t *testing.T) {
	app, dir, clk, w := newWatchFixture(t)
	ch, stop := collectBus(t, app.bus, string(core.EventDefinitionReload))
	defer stop()

	req, _ := http.NewRequest("POST", "/v1/boards", strings.NewReader(
		`{"name":"From UI","columns":["todo"],"card_type_ids":["task"]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != 201 {
		t.Fatalf("create board: %d %s", rr.Code, rr.Body.String())
	}
	_ = waitEvent(t, ch, time.Second)
	_ = drainReloads(ch)

	// Poller observes the new fingerprint; selfWriteGate must absorb it.
	w.scanOnce()
	clk.Advance(500 * time.Millisecond)
	w.scanOnce()
	if n := drainReloads(ch); n != 0 {
		t.Fatalf("poller double-fired after create-board: %d extra reloads", n)
	}

	// An unrelated later edit still reloads once.
	writeBoardDef(t, dir, "from-ui", `{"id":"from-ui","name":"From UI edited","columns":["todo"],"card_type_ids":["task"]}`+"\n")
	w.scanOnce()
	clk.Advance(300 * time.Millisecond)
	w.scanOnce()
	_ = waitEvent(t, ch, time.Second)
}

// TestWatchBrokenEditEmitsFailedKeepsLastGood: a bad JSON edit publishes
// definition_reload_failed, keeps the prior generation, and a fix clears via
// definition_reloaded. Subscribe on the bus (not board-scoped SSE) — live SSE
// board_id filtering is type-membership based for card events; these tests
// do not assume board-scoped delivery.
func TestWatchBrokenEditEmitsFailedKeepsLastGood(t *testing.T) {
	app, dir, clk, w := newWatchFixture(t)

	writeBoardDef(t, dir, "stable", `{"id":"stable","name":"Stable","columns":["todo"],"card_type_ids":["task"]}`+"\n")
	if _, err := app.reload(); err != nil {
		t.Fatalf("seed reload: %v", err)
	}
	w.lastFP = definitionsFingerprint(w.defsDir)

	failCh, stopFail := collectBus(t, app.bus, string(core.EventDefinitionReloadFailed))
	defer stopFail()
	okCh, stopOK := collectBus(t, app.bus, string(core.EventDefinitionReload))
	defer stopOK()
	_ = drainReloads(okCh)

	badPath := filepath.Join(dir, "definitions", "boards", "broken.json")
	if err := os.WriteFile(badPath, []byte(`{"id":"broken","columns":["no-such-column"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	w.scanOnce()
	clk.Advance(300 * time.Millisecond)
	w.scanOnce()

	fe := waitEvent(t, failCh, time.Second)
	if fe.Type != core.EventDefinitionReloadFailed {
		t.Fatalf("got %s", fe.Type)
	}
	diff, _ := fe.Diff.(map[string]any)
	if diff == nil || diff["error"] != "validation_failed" {
		t.Fatalf("structured diff missing: %#v", fe.Diff)
	}
	_ = drainReloads(failCh) // absorb per-board fan-out

	served := app.cur.Load().result.Boards
	if _, ok := served["stable"]; !ok {
		t.Error("lost stable board after failed watch reload")
	}
	if _, ok := served["broken"]; ok {
		t.Error("half-loaded: broken board is being served")
	}

	if err := os.WriteFile(badPath, []byte(`{"id":"broken","name":"Fixed","columns":["todo"],"card_type_ids":["task"]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.scanOnce()
	clk.Advance(300 * time.Millisecond)
	w.scanOnce()
	_ = waitEvent(t, okCh, time.Second)
	_ = drainReloads(okCh)
	if _, ok := app.cur.Load().result.Boards["broken"]; !ok {
		t.Error("fixed board not served after successful watch reload")
	}
	if n := drainReloads(failCh); n != 0 {
		t.Fatalf("unexpected extra failure events after fix: %d", n)
	}
}

// TestReloadHTTPFailureEmitsBusEvent: POST reload with corrupt defs keeps
// last-good and publishes definition_reload_failed (banner surface).
func TestReloadHTTPFailureEmitsBusEvent(t *testing.T) {
	app, dir, _, _ := newWatchFixture(t)
	ch, stop := collectBus(t, app.bus, string(core.EventDefinitionReloadFailed))
	defer stop()

	badPath := filepath.Join(dir, "definitions", "boards", "nope.json")
	if err := os.WriteFile(badPath, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", "/v1/workspace/reload", nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != 422 {
		t.Fatalf("want 422, got %d %s", rr.Code, rr.Body.String())
	}
	_ = waitEvent(t, ch, time.Second)
}
