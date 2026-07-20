package main

// TestReviewBotScript shells out to scripts/review-bot_test.sh — the
// automated proof of the review-bot cards-extension seed (Sprint 07-19
// Phase 3, docs/plans/2026-07-19-sprint-plan.md). The script builds the
// binary, provisions a temp workspace from examples/demo-workspace, and
// asserts the SSE → take-next → comment loop, mid-stream kill/restart
// resumption, and supervisor stability. It lives here so the proof runs
// under `go test ./...`. Skipped under -short, and when node or bash is
// unavailable (CI's go job has no Node — the frontend job does; the bot
// itself requires Node, see examples/demo-workspace/README.md).

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReviewBotScript(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping review-bot integration script in -short mode")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH — the review-bot service requires Node (examples/demo-workspace/README.md)")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("scripts", "review-bot_test.sh"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("scripts/review-bot_test.sh: %v", err)
	}
}
