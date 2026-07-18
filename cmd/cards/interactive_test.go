package main

// Sprint 2026-07-18 Phase 4: script-safety regression for the bare-`cards`
// TUI. A piped `cards </dev/null` (or any non-TTY stream) must print usage —
// never attempt to launch the terminal UI. interactive() reads the process
// streams, so tests re-point os.Stdin at a pipe (a pipe is not a character
// device, exactly like </dev/null or a shell pipeline).

import (
	"os"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/cli"
)

func TestInteractiveGuardPipedStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })

	if interactive(cli.Config{}) {
		t.Error("piped stdin must not be interactive (cards </dev/null must stay script-safe)")
	}
	if interactive(cli.Config{JSON: true}) {
		t.Error("--json must never be interactive")
	}
}

// A bare run() with piped stdin prints usage to stdout instead of entering
// the TUI path — the user-visible half of the guard.
func TestBareRunPipedStdinPrintsUsage(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	oldIn := os.Stdin
	os.Stdin = r

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("out pipe: %v", err)
	}
	oldOut := os.Stdout
	os.Stdout = outW

	t.Cleanup(func() {
		os.Stdin = oldIn
		os.Stdout = oldOut
	})

	if err := run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	outW.Close()
	buf := make([]byte, 64*1024)
	n, _ := outR.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, "Usage:") || !strings.Contains(out, "cards") {
		t.Errorf("bare piped run should print usage, got %.120q", out)
	}
}
