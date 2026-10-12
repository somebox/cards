package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core"
)

// demoCardCount opens an installed demo workspace and counts its cards.
func demoCardCount(t *testing.T, dir string) int {
	t.Helper()
	st, _, _, err := openWorkspace(dir)
	if err != nil {
		t.Fatalf("open demo workspace: %v", err)
	}
	defer st.Close()
	page, err := st.ListCards(context.Background(), core.CardQuery{Limit: 100})
	if err != nil {
		t.Fatalf("list cards: %v", err)
	}
	return len(page.Items)
}

func TestDemoCmd_SeedsByDefault(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cards-demo")
	if err := demoCmd([]string{dir}); err != nil {
		t.Fatalf("demo: %v", err)
	}
	if n := demoCardCount(t, dir); n == 0 {
		t.Error("demo installed with no sample cards; want the seed set")
	}
}

// TestDemoCmd_SeedFlagEitherSideOfDir — the documented order is
// `cards demo [dir] [--seed=false]`. Go's flag package stops at the first
// positional, so a flag after the directory used to be silently ignored and
// the boards were seeded anyway.
func TestDemoCmd_SeedFlagEitherSideOfDir(t *testing.T) {
	for name, args := range map[string]func(dir string) []string{
		"flag after dir":  func(dir string) []string { return []string{dir, "--seed=false"} },
		"flag before dir": func(dir string) []string { return []string{"--seed=false", dir} },
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "cards-demo")
			if err := demoCmd(args(dir)); err != nil {
				t.Fatalf("demo: %v", err)
			}
			if n := demoCardCount(t, dir); n != 0 {
				t.Errorf("--seed=false still seeded %d cards", n)
			}
		})
	}
}

// TestDemoCmd_RejectsExtraArgument — a second positional is an error, not
// silently dropped, and nothing is installed.
func TestDemoCmd_RejectsExtraArgument(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cards-demo")
	err := demoCmd([]string{dir, "extra"})
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "extra"`) {
		t.Fatalf("err = %v, want an unexpected-argument error", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("rejected invocation still created %s", dir)
	}
}
