// Package core — clock.go
//
// Clock abstracts time for the Service so tests can drive both event
// stamping (Emitter.stamp) and the seam 3d deadline scheduler deterministically,
// with no real sleeps. wallClock is the production default.
package core

import "time"

// Clock provides the current time and a way to wait for a duration —
// waitable, not just readable, since the seam 3d scheduler needs to be driven
// by a fake clock in tests. See internal/core/clocktest for the fake.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// wallClock is the real-time Clock used in production.
type wallClock struct{}

func (wallClock) Now() time.Time                         { return time.Now().UTC() }
func (wallClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
