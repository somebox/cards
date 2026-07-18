package core_test

// Phase 3 (sprint 2026-07-06): AddArtifact's optional optimistic-concurrency
// guard and the orphan-free staging order. These pin two properties the
// browser upload path (Phase 4) depends on:
//
//   - a supplied ?version that does not match is rejected BEFORE any bytes are
//     published (a stale attach writes no blob);
//   - a lost CAS race leaves NO orphan blob behind (bytes are staged to a temp
//     file and only published after the card write commits).
//
// Everything runs in t.TempDir(); the artifacts root is scanned directly so
// the no-orphan assertion is self-contained.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

// artifactServiceRooted is artifactService with the artifacts root exposed so
// a test can enumerate stored blobs.
func artifactServiceRooted(t *testing.T) (*core.Service, string) {
	t.Helper()
	ws, types, boards := testConfig()
	types["task"].Fields = append(types["task"].Fields,
		core.FieldDef{ID: "screenshot", Type: core.FieldArtifact},
	)
	st := sqlitetest.Open(t, ws, 1)
	if err := st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	svc := core.NewService(ws, types, boards, st)
	root := t.TempDir()
	am, err := artifacts.New(root)
	if err != nil {
		t.Fatalf("artifacts: %v", err)
	}
	svc.SetArtifacts(am)
	return svc, root
}

// storedBlobs counts committed content-addressed blobs under root, ignoring
// the shard directories and any leftover .ingest-* temp files (an orphan
// would show up as such a temp file, or as an extra committed blob).
func storedBlobs(t *testing.T, root string) (committed int, temps int) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasPrefix(d.Name(), ".ingest-") {
			temps++
		} else {
			committed++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk artifacts root: %v", err)
	}
	return committed, temps
}

func TestAddArtifact_VersionGuard(t *testing.T) {
	svc, root := artifactServiceRooted(t)
	ctx := core.WithActor(context.Background(), "u")
	c := mkArtifactCard(t, svc, ctx) // version 1

	// Stale version → version_conflict, and NOTHING is written to the store.
	_, err := svc.AddArtifact(ctx, c.ID, "screenshot", strings.NewReader("stale"), 999)
	if ce := core.AsError(err); ce == nil || ce.Code != "version_conflict" {
		t.Fatalf("stale version: want version_conflict, got %v", err)
	}
	if committed, temps := storedBlobs(t, root); committed != 0 || temps != 0 {
		t.Fatalf("a rejected attach left bytes on disk: committed=%d temps=%d", committed, temps)
	}

	// Matching version → succeeds; exactly one blob committed.
	if _, err := svc.AddArtifact(ctx, c.ID, "screenshot", strings.NewReader("good"), c.Version); err != nil {
		t.Fatalf("matching version attach: %v", err)
	}
	if committed, temps := storedBlobs(t, root); committed != 1 || temps != 0 {
		t.Fatalf("after one attach: committed=%d temps=%d (want 1, 0)", committed, temps)
	}

	// version 0 still works (unguarded), for `cards attach <id> <file>`.
	cur, _ := svc.ResolveCard(ctx, c.ID)
	if _, err := svc.AddArtifact(ctx, cur.ID, "screenshot", strings.NewReader("unguarded"), 0); err != nil {
		t.Fatalf("unguarded attach: %v", err)
	}
}

// TestAddArtifact_ConcurrentRaceLeavesNoOrphan runs two attaches at the same
// version with DISTINCT content: exactly one wins, the other gets
// version_conflict, and the loser's staged bytes are discarded — so the store
// holds only the winner's single blob, never the loser's orphan.
func TestAddArtifact_ConcurrentRaceLeavesNoOrphan(t *testing.T) {
	svc, root := artifactServiceRooted(t)
	ctx := core.WithActor(context.Background(), "u")
	c := mkArtifactCard(t, svc, ctx) // version 1

	var wg sync.WaitGroup
	errs := make([]error, 2)
	payloads := []string{"content-from-writer-A", "content-from-writer-B"}
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.AddArtifact(ctx, c.ID, "screenshot", strings.NewReader(payloads[i]), c.Version)
		}(i)
	}
	wg.Wait()

	winners, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			winners++
		case core.AsError(err) != nil && core.AsError(err).Code == "version_conflict":
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("want exactly 1 winner + 1 version_conflict, got winners=%d conflicts=%d", winners, conflicts)
	}
	// The loser must not have orphaned its distinct-content blob, and no temp
	// file may linger: exactly the winner's single committed blob remains.
	if committed, temps := storedBlobs(t, root); committed != 1 || temps != 0 {
		t.Fatalf("race left an orphan: committed=%d temps=%d (want 1, 0)", committed, temps)
	}
}
