package main

import (
	"runtime"
	"strings"
	"testing"
)

// versionString always yields a non-empty "cards <ver> ..." line ending in the
// toolchain/platform, whether or not a version was injected via ldflags.
func TestVersionString(t *testing.T) {
	s := versionString()
	if !strings.HasPrefix(s, "cards ") {
		t.Errorf("version = %q, want it to start with %q", s, "cards ")
	}
	if !strings.Contains(s, runtime.Version()) {
		t.Errorf("version = %q, want it to mention the Go toolchain %q", s, runtime.Version())
	}
	if !strings.Contains(s, runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("version = %q, want it to mention %s/%s", s, runtime.GOOS, runtime.GOARCH)
	}
}
