// Definitions file watcher for `cards serve --watch` (sprint P3b /
// card_ec61b093). Dependency-free poller: fingerprint-hash the definitions/
// tree on an interval (same idea as scripts/dev-server.sh), debounce until the
// fingerprint is stable, then call reloadableApp.reload(). No fsnotify.
//
// Testability: an injectable clock + synchronously-drivable scanOnce so
// debounce / coalescing / self-write tests assert without real sleeps.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Default poll / debounce for --watch. Debounce is measured from the last
// observed fingerprint change (editor save-all / atomic rename bursts coalesce).
const (
	defaultWatchPoll     = 500 * time.Millisecond
	defaultWatchDebounce = 300 * time.Millisecond
)

// clock is injectable so tests advance time without sleeping.
type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// manualClock is a test clock: Now() returns the current value; Advance moves it.
type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func newManualClock(t time.Time) *manualClock { return &manualClock{t: t} }

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// defsWatcher polls definitions/ and reloads when the content fingerprint changes.
type defsWatcher struct {
	app       *reloadableApp
	defsDir   string
	clock     clock
	pollEvery time.Duration
	debounce  time.Duration

	lastFP       string
	pendingFP    string
	pendingSince time.Time
}

func newDefsWatcher(app *reloadableApp, pollEvery, debounce time.Duration, clk clock) *defsWatcher {
	if clk == nil {
		clk = realClock{}
	}
	if pollEvery <= 0 {
		pollEvery = defaultWatchPoll
	}
	if debounce <= 0 {
		debounce = defaultWatchDebounce
	}
	return &defsWatcher{
		app:       app,
		defsDir:   filepath.Join(app.dir, "definitions"),
		clock:     clk,
		pollEvery: pollEvery,
		debounce:  debounce,
	}
}

// Run polls until ctx is cancelled. Production path for --watch.
func (w *defsWatcher) Run(ctx context.Context) {
	w.lastFP = definitionsFingerprint(w.defsDir)
	ticker := time.NewTicker(w.pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scanOnce()
		}
	}
}

// scanOnce is the synchronously-drivable unit: tests call it after writing
// files and advancing the clock. It never sleeps.
func (w *defsWatcher) scanOnce() {
	fp := definitionsFingerprint(w.defsDir)
	if fp == w.lastFP {
		return
	}
	// Self-write (create-board) already reloaded — absorb without a second fire.
	if w.app.selfWrite.take(fp) {
		w.lastFP = fp
		w.pendingFP = ""
		return
	}
	now := w.clock.Now()
	if fp != w.pendingFP {
		w.pendingFP = fp
		w.pendingSince = now
		return
	}
	if now.Sub(w.pendingSince) < w.debounce {
		return
	}
	// Fingerprint stable for the debounce window — reload once.
	w.lastFP = fp
	w.pendingFP = ""
	if _, err := w.app.reload(); err != nil {
		// reload() already published definition_reload_failed; keep last-good
		// generation. lastFP is the broken tree so we do not spin-retry until
		// the next edit changes the fingerprint again.
		log.Printf("definition reload failed (last-good still serving): %v", err)
	}
}

// definitionsFingerprint returns a stable content hash of every regular file
// under defsDir (sorted paths). Missing dir → empty fingerprint.
func definitionsFingerprint(defsDir string) string {
	var paths []string
	_ = filepath.WalkDir(defsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // disappear mid-walk: treat as absent this tick
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		rel, err := filepath.Rel(defsDir, p)
		if err != nil {
			rel = p
		}
		_, _ = io.WriteString(h, rel)
		_, _ = h.Write([]byte{0})
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		_, _ = io.Copy(h, f)
		_ = f.Close()
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// selfWriteGate suppresses the poller's reaction to handleCreateBoard's own
// write-then-reload. It uses its OWN mutex — never reloadableApp.mu — so the
// poll loop cannot block the HTTP write path (and vice versa).
type selfWriteGate struct {
	mu     sync.Mutex
	active int    // >0 while create-board holds the write+reload critical section
	skipFP string // fingerprint to absorb once after the section ends
}

func (g *selfWriteGate) begin() {
	g.mu.Lock()
	g.active++
	g.mu.Unlock()
}

// end clears one begin() nesting level. If fp is non-empty, the next matching
// poller observation is absorbed (create-board already reloaded that tree).
func (g *selfWriteGate) end(fp string) {
	g.mu.Lock()
	if g.active > 0 {
		g.active--
	}
	if fp != "" {
		g.skipFP = fp
	}
	g.mu.Unlock()
}

// take reports whether this fingerprint should be absorbed (no reload).
// Mid-write (active>0) always suppresses; after end(fp), the matching fp
// is suppressed once.
func (g *selfWriteGate) take(fp string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active > 0 {
		return true
	}
	if g.skipFP != "" && g.skipFP == fp {
		g.skipFP = ""
		return true
	}
	return false
}
