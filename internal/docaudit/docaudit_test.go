// Package docaudit holds documentation-integrity checks that run as ordinary Go
// tests (so they execute in the existing `go test ./...` CI with no extra
// workflow). It has no non-test code.
package docaudit

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// shortID matches an 8-hex card short-id token as cited in the docs (e.g.
// `dafd0873`). A full card id is `card_<32 hex>`; the docs cite the leading 8.
var shortID = regexp.MustCompile(`\b[0-9a-f]{8}\b`)

// TestROADMAPCardIDsResolve guards against the "dangling card reference" rot:
// every card short-id cited in docs/ROADMAP.md must resolve against the
// committed backlog snapshot (examples/demo-workspace/backlog.jsonl), which
// carries current cards plus card_deleted tombstones — so a reference to a
// since-retired card still resolves, but a typo'd or invented id fails.
//
// Refresh the snapshot after board changes with:
//
//	cards export --workspace examples/demo-workspace --state-only \
//	  --out examples/demo-workspace/backlog.jsonl
func TestROADMAPCardIDsResolve(t *testing.T) {
	backlog := readRepoFile(t, "examples/demo-workspace/backlog.jsonl")
	roadmap := readRepoFile(t, "docs/ROADMAP.md")

	seen := map[string]bool{}
	for _, id := range shortID.FindAllString(roadmap, -1) {
		if seen[id] {
			continue
		}
		seen[id] = true
		if !strings.Contains(backlog, id) {
			t.Errorf("docs/ROADMAP.md cites card short-id %q, which resolves to no card in "+
				"examples/demo-workspace/backlog.jsonl — a dangling reference, or the snapshot is stale "+
				"(regenerate with: cards export --workspace examples/demo-workspace --state-only --out examples/demo-workspace/backlog.jsonl)", id)
		}
	}
	if len(seen) == 0 {
		t.Error("no card short-ids found in docs/ROADMAP.md — the extraction regex may be broken")
	}
}

// readRepoFile reads a path relative to the repository root (this package lives
// two directories below it).
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}
