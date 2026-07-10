package main

import (
	"strings"
	"testing"
)

// TestLoopbackWarning pins the P2 bind-warning carve-out: loopback binds stay
// quiet; anything reachable from the network warns, and the warning names the
// offending host.
func TestLoopbackWarning(t *testing.T) {
	quiet := []string{"127.0.0.1", "localhost", "::1", "127.0.0.2"}
	for _, h := range quiet {
		if w := loopbackWarning(h); w != "" {
			t.Errorf("loopbackWarning(%q) = %q; want quiet (loopback)", h, w)
		}
	}

	warn := []string{"0.0.0.0", "::", "", "192.168.1.10", "10.0.0.5", "cards.local"}
	for _, h := range warn {
		w := loopbackWarning(h)
		if w == "" {
			t.Errorf("loopbackWarning(%q) = quiet; want a warning (non-loopback bind)", h)
			continue
		}
		// The empty-host case can't echo a hostname, but every named host must
		// appear in the warning so the operator sees what they bound.
		if h != "" && !strings.Contains(w, h) {
			t.Errorf("loopbackWarning(%q) = %q; warning must name the host", h, w)
		}
	}
}
