package httpapi

import "time"

// SetSSEKeepaliveForTest overrides the keepalive interval and returns a
// restore function. Not for production callers — the tag-guarded test seam
// pattern would need a build tag; this exported name + Test suffix keeps
// the intent explicit without a tag.
func SetSSEKeepaliveForTest(d time.Duration) func() {
	prev := sseKeepaliveInterval
	sseKeepaliveInterval = d
	return func() { sseKeepaliveInterval = prev }
}
