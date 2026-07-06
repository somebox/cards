package main

// The Phase 2 out-of-the-box proof (sprint 2026-07-06): a FRESH `cards init`
// ships a card type with an artifact field, so the full agent loop —
// init → create/take a card → attach evidence — works with no manual schema
// edit. Runs entirely in t.TempDir(); teardown is automatic.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

func TestInitThenAttach_OutOfTheBox(t *testing.T) {
	dir := t.TempDir() + "/.cards"
	created, err := initWorkspace(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !created {
		t.Fatal("expected a fresh workspace")
	}

	st, svc, result, err := openWorkspace(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	defer svc.Close()

	// The starter task type must declare an artifact field — this is the
	// contract that makes `cards attach` reachable from a fresh install.
	taskType := result.CardTypes["task"]
	if taskType == nil {
		t.Fatal("starter workspace has no 'task' type")
	}
	var artifactField string
	for _, f := range taskType.Fields {
		if f.Type == core.FieldArtifact {
			artifactField = f.ID
		}
	}
	if artifactField == "" {
		t.Fatal("starter 'task' type declares no artifact field — cards attach is unreachable out of the box")
	}

	// Attach to a welcome-board card by SHORT id (Phase 1 + Phase 2 together:
	// the exact loop an agent runs on day one).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	actor := result.Workspace.Settings.DefaultUser
	if actor == "" {
		actor = "starter"
	}
	ctx = core.WithActor(ctx, actor)
	page, err := svc.ListCards(ctx, core.CardQuery{TypeID: "task", Limit: 1})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("no seeded task cards: %v", err)
	}
	shortRef := page.Items[0].ID[5:13]
	got, err := svc.AddArtifact(ctx, shortRef, artifactField, strings.NewReader("attached from a fresh init"), 0)
	if err != nil {
		t.Fatalf("attach by short id on a fresh init: %v", err)
	}
	meta, ok := got.Fields.(map[string]any)[artifactField].(map[string]any)
	if !ok || meta["uri"] == "" || meta["sha256"] == "" {
		t.Fatalf("artifact metadata not recorded: %v", got.Fields)
	}
}
