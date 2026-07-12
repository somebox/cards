package hooks

import "time"

// Default restart-backoff knobs (process-global; not per-extension schema
// fields yet — see LIFECYCLE-SCHEMA.md). Tests override via SetBackoff.
const (
	defaultInitialBackoff   = 200 * time.Millisecond
	defaultMaxBackoff       = 30 * time.Second
	defaultBackoffFactor    = 2.0
	defaultMinHealthyUptime = 10 * time.Second
)

// BackoffConfig controls restart delays for supervised services.
//
// After each exit that warrants a restart, the supervisor waits Delay(streak)
// before the next start. streak increments on every restartable exit whose
// prior uptime was shorter than MinHealthyUptime; once a run lives at least
// MinHealthyUptime, streak resets to 0. That way a process that runs briefly
// then crashes escalates toward Max instead of resetting every cycle.
type BackoffConfig struct {
	Initial          time.Duration
	Max              time.Duration
	Factor           float64
	MinHealthyUptime time.Duration
}

// DefaultBackoff returns production defaults.
func DefaultBackoff() BackoffConfig {
	return BackoffConfig{
		Initial:          defaultInitialBackoff,
		Max:              defaultMaxBackoff,
		Factor:           defaultBackoffFactor,
		MinHealthyUptime: defaultMinHealthyUptime,
	}
}

// Delay returns the wait before the next restart for the given failure streak
// (0 = first restart after an unhealthy exit). Capped at Max.
func (b BackoffConfig) Delay(streak int) time.Duration {
	if b.Initial <= 0 {
		b.Initial = defaultInitialBackoff
	}
	if b.Max <= 0 {
		b.Max = defaultMaxBackoff
	}
	if b.Factor < 1 {
		b.Factor = defaultBackoffFactor
	}
	if streak < 0 {
		streak = 0
	}
	d := float64(b.Initial)
	for i := 0; i < streak; i++ {
		d *= b.Factor
		if d >= float64(b.Max) {
			return b.Max
		}
	}
	if d > float64(b.Max) {
		return b.Max
	}
	return time.Duration(d)
}

// NextStreak returns the streak to use for the *next* restart after a run that
// lasted uptime and then exited. Healthy runs reset to 0 (so the following
// Delay is Initial); brief crash-loops increment.
func (b BackoffConfig) NextStreak(prevStreak int, uptime time.Duration) int {
	minH := b.MinHealthyUptime
	if minH <= 0 {
		minH = defaultMinHealthyUptime
	}
	if uptime >= minH {
		return 0
	}
	if prevStreak < 0 {
		prevStreak = 0
	}
	return prevStreak + 1
}
