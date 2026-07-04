// Package clocktest provides a deterministic core.Clock fake for tests that
// need to drive event stamping or the seam 3d deadline scheduler without
// real sleeps or wall-clock races.
package clocktest

import (
	"sync"
	"time"

	"github.com/somebox/cards/internal/core"
)

// Fake is a settable, advanceable core.Clock. Now() returns the current fake
// time; After(d) fires only once Advance moves the clock to or past the
// deadline.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	deadline time.Time
	ch       chan time.Time
}

var _ core.Clock = (*Fake)(nil)

// New returns a Fake initialized to start.
func New(start time.Time) *Fake {
	return &Fake{now: start}
}

// Now returns the fake clock's current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After returns a channel that fires (with the deadline time) once Advance
// moves the clock to or past now+d. Buffered like time.After, so Advance
// never blocks on a receiver that hasn't read yet. A non-positive d fires
// immediately, matching time.After's behavior.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	deadline := f.now.Add(d)
	if !deadline.After(f.now) {
		ch <- deadline
		return ch
	}
	f.waiters = append(f.waiters, waiter{deadline: deadline, ch: ch})
	return ch
}

// Advance moves the clock forward by d, firing every waiter whose deadline
// is now due (in whatever order they were registered).
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	var due, remaining []waiter
	for _, w := range f.waiters {
		if !w.deadline.After(f.now) {
			due = append(due, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	f.waiters = remaining
	now := f.now
	f.mu.Unlock()
	for _, w := range due {
		w.ch <- now
	}
}
