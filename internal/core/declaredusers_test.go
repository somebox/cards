package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/somebox/cards/internal/core"
)

// A user declared in the workspace definitions (workspace.users, or the
// settings.default_user seed the loader materializes) never reaches the
// users table on its own. It must still count as registered for ownership,
// or a fresh `cards init` workspace's default user can create cards it cannot
// claim.
func TestClaim_DefinitionDeclaredUserCanOwn(t *testing.T) {
	ws, types, boards := testConfig()
	ws.Users = []core.User{{ID: "declared", Kind: "human"}}
	svc, _ := newTestServiceWith(t, ws, types, boards)
	ctx := context.Background()

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "Own me", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "declared",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.Claim(ctx, c.ID, core.ClaimRequest{Version: c.Version, Actor: "declared"})
	if err != nil {
		t.Fatalf("claim as definition-declared user: %v", err)
	}
	if got.Owner != "declared" {
		t.Errorf("owner = %q, want declared", got.Owner)
	}

	// An id that is neither declared nor registered is still rejected.
	owner := "nobody"
	_, err = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: got.Version, Owner: &owner, Actor: "declared"})
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != "unknown_user" {
		t.Fatalf("patch owner=nobody: err = %v, want unknown_user", err)
	}

	// The introspection snapshot lists both registered and declared users.
	snap, err := svc.Workspace(ctx)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	seen := map[string]bool{}
	for _, u := range snap.Workspace.Users {
		seen[u.ID] = true
	}
	for _, want := range []string{"u", "alice", "declared"} {
		if !seen[want] {
			t.Errorf("snapshot users missing %q: %v", want, snap.Workspace.Users)
		}
	}
}
