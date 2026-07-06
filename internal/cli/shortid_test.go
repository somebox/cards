package cli

// CLI-transport half of the sprint 2026-07-06 Phase 1 parity contract:
// mutating verbs accept short ids (the live bug: get/list resolved them but
// patch/comment/attach demanded the full card_ id), and an ambiguous
// reference prints the candidate ids so the caller can pick one and retry.

import (
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core/coretest"
)

func TestCLIPatch_ShortIDResolves(t *testing.T) {
	c, st := newTestClientStore(t, Config{As: "local-dev"})
	full := coretest.CardID("CLIHIT01", "q")
	coretest.SeedCard(t, st, "demo", "programming-task", full,
		map[string]any{"description": "d", "branch": "b"})

	out, err := runCmd(t, c, "patch", "CLIHIT01", "--version", "1", "--title", "Renamed via CLI short id")
	if err != nil {
		t.Fatalf("patch by short id: %v", err)
	}
	if !strings.Contains(out, full) {
		t.Errorf("output missing full id %s:\n%s", full, out)
	}
}

func TestCLIPatch_AmbiguousListsCandidates(t *testing.T) {
	c, st := newTestClientStore(t, Config{As: "local-dev"})
	idA, idB := coretest.SeedCollidingCards(t, st, "demo", "programming-task", "CLIAMBI1")

	_, err := runCmd(t, c, "patch", "CLIAMBI1", "--version", "1", "--title", "x")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ambiguous") {
		t.Errorf("error not ambiguous: %v", msg)
	}
	// The renderer must surface BOTH candidate full ids, not just a count.
	if !strings.Contains(msg, idA) || !strings.Contains(msg, idB) {
		t.Errorf("candidates missing from CLI error:\n%s", msg)
	}
	// Conflict-class exit code (409 → 4) so scripts can branch.
	if code := ExitCode(err); code != 4 {
		t.Errorf("ExitCode = %d, want 4", code)
	}
}
