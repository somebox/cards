// Package docaudit holds documentation-integrity checks that run as ordinary Go
// tests (so they execute in the existing `go test ./...` CI with no extra
// workflow). It has no non-test code.
package docaudit

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// shortID matches an 8-hex card short-id token as cited in the docs (e.g.
// `dafd0873`). A full card id is `card_<32 hex>`; the docs cite the leading 8.
var shortID = regexp.MustCompile(`\b[0-9a-f]{8}\b`)

// TestROADMAPCardIDsResolve guards against the "dangling card reference" rot:
// every card short-id cited in docs/roadmap.md must resolve against the
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
	roadmap := readRepoFile(t, "docs/roadmap.md")

	seen := map[string]bool{}
	for _, id := range shortID.FindAllString(roadmap, -1) {
		if seen[id] {
			continue
		}
		seen[id] = true
		if !strings.Contains(backlog, id) {
			t.Errorf("docs/roadmap.md cites card short-id %q, which resolves to no card in "+
				"examples/demo-workspace/backlog.jsonl — a dangling reference, or the snapshot is stale "+
				"(regenerate with: cards export --workspace examples/demo-workspace --state-only --out examples/demo-workspace/backlog.jsonl)", id)
		}
	}
	if len(seen) == 0 {
		t.Error("no card short-ids found in docs/roadmap.md — the extraction regex may be broken")
	}
}

// guardMarker is the extraction contract between
// docs/reference/implementation-status.md and TestImplStatusAnchorsResolve.
// Each live code citation in that doc carries an explicit, parseable
// HTML-comment marker on its own line or inline beside it:
//
//	<!-- guard: <path>:<line>[-<end>] symbol=<ident> -->
//
// The guard parses the marker — never prose regex — opens the file, and
// asserts the named symbol appears at the cited line/range. A weak
// "file exists + line in range" check is explicitly rejected: it would
// green-light the moved-and-renumbered rot class (a symbol that moved, with
// the cited line bumped to land on an unrelated function).
var guardMarker = regexp.MustCompile(`<!--\s*guard:\s+([A-Za-z0-9_./-]+):(\d+)(?:-(\d+))?\s+symbol=([A-Za-z_][A-Za-z0-9_]*)\s*-->`)

// implStatusPath is the code-verified audit doc these tests guard.
const implStatusPath = "docs/reference/implementation-status.md"

// TestImplStatusAnchorsResolve enforces the anchor contract of
// docs/reference/implementation-status.md: every `<!-- guard: ... -->` marker
// in that doc must resolve — the cited file exists and the named symbol
// appears at the cited line/range. It runs as an ordinary Go test because the
// package-doc guarantee for internal/docaudit is "no non-test code": do NOT
// promote this into a build-time check or move it into package code; the
// whole point is that `go test ./...` is the one enforcement seam.
func TestImplStatusAnchorsResolve(t *testing.T) {
	doc := readRepoFile(t, implStatusPath)

	matches := guardMarker.FindAllStringSubmatch(doc, -1)
	if len(matches) < 3 {
		t.Fatalf("found %d guard markers in %s, expected at least 3 (the take-next anchors) — "+
			"the extraction contract (`<!-- guard: <path>:<line>[-<end>] symbol=<ident> -->`) may be broken, "+
			"or markers were dropped", len(matches), implStatusPath)
	}

	for _, m := range matches {
		path, symbol := m[1], m[4]
		start, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("guard marker %q: bad start line: %v", m[0], err)
		}
		end := start
		if m[3] != "" {
			if end, err = strconv.Atoi(m[3]); err != nil {
				t.Fatalf("guard marker %q: bad end line: %v", m[0], err)
			}
		}

		body, err := os.ReadFile("../../" + path)
		if err != nil {
			t.Errorf("guard marker cites %s, which does not resolve: %v — re-pin the anchor in %s", path, err, implStatusPath)
			continue
		}
		lines := strings.Split(string(body), "\n")
		if start < 1 || end < start || end > len(lines) {
			t.Errorf("guard marker cites %s:%d-%d, but the file has %d lines — the anchor rotted; "+
				"re-pin the citation and its marker in %s", path, start, end, len(lines), implStatusPath)
			continue
		}
		window := strings.Join(lines[start-1:end], "\n")
		if !strings.Contains(window, symbol) {
			got := strings.TrimSpace(lines[start-1])
			t.Errorf("guard marker %s:%d (symbol %q) does not resolve: the expected symbol %q is not at the cited line "+
				"(line reads: %q) — the anchor rotted; re-pin the citation and its marker in %s",
				path, start, symbol, symbol, got, implStatusPath)
		}
	}
}

// docPathRef matches a docs-tree markdown path (docs/<dirs>/<name>.md)
// anywhere it appears.
var docPathRef = regexp.MustCompile(`docs/[A-Za-z0-9_./-]+\.md`)

// TestCodeCommentDocPathsResolve asserts that every `docs/*.md` path cited in
// a Go code comment under internal/ AND cmd/ resolves to a real file in the
// split docs tree. This is the ratchet against the dead-doc-ref rot class
// (the retired top-level SPEC.md / ARCHITECTURE.md / DEVELOPER-REFERENCE.md /
// MCP.md citations and the pre-lowercase filenames) — the re-point churn cannot
// recur once this test is strict everywhere. The existence check is
// case-exact (a walked set of real paths, not os.Stat) so a wrong-case
// citation fails on case-sensitive CI filesystems too.
func TestCodeCommentDocPathsResolve(t *testing.T) {
	live := liveDocsPaths(t)
	roots := []string{filepath.Join("../../", "internal"), filepath.Join("../../", "cmd")}

	fset := token.NewFileSet()
	checked := 0
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, cg := range f.Comments {
				for _, ref := range docPathRef.FindAllString(cg.Text(), -1) {
					checked++
					if !live[ref] {
						pos := fset.Position(cg.Pos())
						t.Errorf("%s: code comment cites %s, which resolves to no file in the docs tree — "+
							"re-point it to the split tree (docs/spec/, docs/architecture/, docs/extensions/, ...)",
							pos, ref)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if checked == 0 {
		t.Error("no docs/*.md paths found in code comments under internal/ and cmd/ — the extraction regex may be broken")
	}
}

// boundaryMaxLag is how far behind HEAD the implementation-status.md audit
// boundary may fall before the tripwire fires. 20 commits ≈ one sprint of
// churn: long enough that ordinary PRs never trip it, short enough that the
// doc cannot silently fossilize.
const boundaryMaxLag = 20

// boundaryLineRe finds the "Current boundary" sentence; backtickCommitRe then
// extracts the cited commits (backtick-quoted, so prose words can never be
// misread as hashes).
var (
	// The boundary sentence may wrap across physical lines; take a bounded
	// window so the cited commits are found regardless of wrapping.
	boundaryLineRe   = regexp.MustCompile(`(?s)\*\*Current boundary:\*\*.{0,300}`)
	backtickCommitRe = regexp.MustCompile("`([0-9a-f]{7,40})`")
)

// TestImplStatusBoundaryCommit asserts the audit-changelog boundary cited in
// docs/reference/implementation-status.md is a real commit and is within
// boundaryMaxLag commits of HEAD. A missing commit is a hard failure
// everywhere (the doc cites something that never existed). The within-N lag
// check is a WARNING in normal `go test ./...` runs — an unrelated PR must
// not go red purely because 20 commits accumulated — and STRICT only in the
// dedicated docaudit CI job (`go test ./internal/docaudit -tags=strictdoc`).
func TestImplStatusBoundaryCommit(t *testing.T) {
	doc := readRepoFile(t, implStatusPath)

	line := boundaryLineRe.FindString(doc)
	if line == "" {
		t.Fatalf("no '**Current boundary:**' line found in %s — the audit changelog header drifted", implStatusPath)
	}
	commits := backtickCommitRe.FindAllStringSubmatch(line, -1)
	if len(commits) < 2 {
		t.Fatalf("boundary line cites %d commits, expected 2 (boundary → HEAD): %q", len(commits), line)
	}
	boundary, citedHead := commits[0][1], commits[1][1]

	revList, err := gitRevListHEAD(t)
	if err != nil {
		t.Skipf("cannot assert boundary freshness without git history: %v", err)
	}

	indexOf := func(hash string) int {
		for i, h := range revList {
			if strings.HasPrefix(h, hash) {
				return i
			}
		}
		return -1
	}

	boundaryLineNo := 1 + strings.Count(doc[:strings.Index(doc, line)], "\n")
	fix := fmt.Sprintf("roll the boundary at %s:%d (the '**Current boundary:**' line) to HEAD "+
		"and re-pin anchors; see Phase 1 of docs/plans/2026-07-19-sprint-plan.md", implStatusPath, boundaryLineNo)

	if indexOf(citedHead) < 0 {
		t.Errorf("boundary line cites HEAD commit %q, which is not in `git rev-list HEAD` — %s", citedHead, fix)
	}
	lag := indexOf(boundary)
	if lag < 0 {
		t.Errorf("boundary commit %q is not in `git rev-list HEAD` (history rewritten? typo?) — %s", boundary, fix)
		return
	}
	if lag > boundaryMaxLag {
		msg := fmt.Sprintf("boundary commit %q is %d commits behind HEAD (max %d) — the audit doc is fossilizing: %s",
			boundary, lag, boundaryMaxLag, fix)
		if strictDoc {
			t.Error(msg)
		} else {
			t.Logf("WARNING (strict under -tags=strictdoc): %s", msg)
		}
	}
}

// liveDocsPaths walks docs/ and returns the set of real, case-exact
// slash-separated doc paths (e.g. "docs/spec/index.md").
func liveDocsPaths(t *testing.T) map[string]bool {
	t.Helper()
	live := map[string]bool{}
	err := filepath.WalkDir("../../docs", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		live[filepath.ToSlash(strings.TrimPrefix(path, "../../"))] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs/: %v", err)
	}
	if len(live) == 0 {
		t.Fatal("docs/ tree appears empty — walked the wrong root?")
	}
	return live
}

// gitRevListHEAD returns the full hashes of `git rev-list HEAD`, HEAD first.
func gitRevListHEAD(t *testing.T) ([]string, error) {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "HEAD")
	cmd.Dir = "../../"
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var hashes []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if h := strings.TrimSpace(sc.Text()); h != "" {
			hashes = append(hashes, h)
		}
	}
	if len(hashes) == 0 {
		return nil, fmt.Errorf("git rev-list HEAD returned no commits")
	}
	return hashes, nil
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
