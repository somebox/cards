package core_test

import (
	"context"
	"testing"

	"github.com/somebox/cards/internal/core"
)

// RemoveLink and EditComment take no actor parameter and no version, so the
// only attribution is the context actor — a blank one must be a structured
// actor_required rejection, not an event committed by "" (P2 review finding;
// mirrors the guards in AddLink/AddComment/Release/UpgradeSchema).
func TestRemoveLinkAndEditComment_RequireActor(t *testing.T) {
	svc, _ := newTestService(t)
	actorCtx := core.WithActor(context.Background(), "u")

	a, err := svc.CreateCard(actorCtx, core.CreateCardRequest{
		TypeID: "task", Title: "A", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := svc.CreateCard(actorCtx, core.CreateCardRequest{
		TypeID: "task", Title: "B", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := svc.AddLink(actorCtx, a.ID, core.LinkInput{TypeID: "related", Target: b.ID, Actor: "u"}); err != nil {
		t.Fatalf("add link: %v", err)
	}
	withComment, err := svc.AddComment(actorCtx, a.ID, "hello")
	if err != nil {
		t.Fatalf("add comment: %v", err)
	}
	commentID := withComment.Comments[len(withComment.Comments)-1].ID

	bare := context.Background()
	if _, err := svc.RemoveLink(bare, a.ID, "related", b.ID); core.AsError(err) == nil || core.AsError(err).Code != "actor_required" {
		t.Fatalf("RemoveLink without actor: got %v, want actor_required", err)
	}
	if _, err := svc.EditComment(bare, a.ID, commentID, "edited"); core.AsError(err) == nil || core.AsError(err).Code != "actor_required" {
		t.Fatalf("EditComment without actor: got %v, want actor_required", err)
	}

	// With an actor both succeed — the guard rejects only blank attribution.
	if _, err := svc.EditComment(actorCtx, a.ID, commentID, "edited"); err != nil {
		t.Fatalf("EditComment with actor: %v", err)
	}
	if _, err := svc.RemoveLink(actorCtx, a.ID, "related", b.ID); err != nil {
		t.Fatalf("RemoveLink with actor: %v", err)
	}
}
