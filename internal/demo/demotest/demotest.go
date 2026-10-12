// Package demotest installs the demo workspace for tests: a fresh copy per
// test in t.TempDir(), which Go removes when the test ends. Nothing reads a
// shared on-disk demo, so a test can mutate its copy freely.
package demotest

import (
	"testing"

	"github.com/somebox/cards/internal/demo"
)

// Dir installs the demo workspace into a new temp dir and returns its path.
func Dir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	if err := demo.Install(dir); err != nil {
		t.Fatalf("install demo workspace: %v", err)
	}
	return dir
}
