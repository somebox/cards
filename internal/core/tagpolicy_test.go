package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core"
)

// tagPolicyService builds a service whose workspace declares the given
// tag_policy, so each mode can be exercised through the real write paths.
func tagPolicyService(t *testing.T, policy string) *core.Service {
	t.Helper()
	ws, types, boards := testConfig()
	ws.Settings.TagPolicy = policy
	svc, _ := newTestServiceWith(t, ws, types, boards)
	return svc
}

func tagPolicyCtx() context.Context { return context.Background() }

// TestTagPolicyLockedRejectsUnknown — the strict mode, and the behavior every
// workspace had while the core ignored this setting entirely.
func TestTagPolicyLockedRejectsUnknown(t *testing.T) {
	svc := tagPolicyService(t, core.TagPolicyLocked)
	_, err := svc.CreateCard(tagPolicyCtx(), core.CreateCardRequest{
		TypeID: "task", Title: "t", Actor: "u", Tags: []string{"nope"},
		Fields: map[string]any{"description": "d"},
	})
	var cerr *core.Error
	if !errors.As(err, &cerr) || cerr.Code != "unknown_tag" {
		t.Fatalf("locked policy must reject an unknown tag, got %v", err)
	}
	if !strings.Contains(cerr.Hint, "'"+core.TagPolicyLocked+"'") {
		t.Errorf("hint must name the configured policy, got %q", cerr.Hint)
	}
}

// TestTagPolicyOpenAcceptsFreeTags — the mode the UI chip control has always
// offered. Before this landed, the core rejected these writes regardless of
// the setting, so a fresh `cards init` workspace could not accept any tag.
func TestTagPolicyOpenAcceptsFreeTags(t *testing.T) {
	svc := tagPolicyService(t, core.TagPolicyOpen)
	c, err := svc.CreateCard(tagPolicyCtx(), core.CreateCardRequest{
		TypeID: "task", Title: "t", Actor: "u", Tags: []string{"brand-new-tag"},
		Fields: map[string]any{"description": "d"},
	})
	if err != nil {
		t.Fatalf("open policy must accept a free tag: %v", err)
	}
	if len(c.Tags) != 1 || c.Tags[0] != "brand-new-tag" {
		t.Fatalf("tags = %v, want [brand-new-tag]", c.Tags)
	}
}

// TestTagPolicyOpenAppliesToPatch — both write paths read the policy;
// validateTags is called from CreateCard and PatchCard alike.
func TestTagPolicyOpenAppliesToPatch(t *testing.T) {
	svc := tagPolicyService(t, core.TagPolicyOpen)
	ctx := tagPolicyCtx()
	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "t", Actor: "u", Fields: map[string]any{"description": "d"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tags := []string{"freshly-invented"}
	got, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Tags: &tags, Actor: "u"})
	if err != nil {
		t.Fatalf("patch under open policy: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "freshly-invented" {
		t.Fatalf("tags = %v, want [freshly-invented]", got.Tags)
	}
}

// TestTagPolicyUnsetFailsClosed — an in-process Workspace literal with no
// policy (config load would have defaulted it) must behave as locked, never
// as open. Failing open here would silently loosen every embedded caller.
func TestTagPolicyUnsetFailsClosed(t *testing.T) {
	svc := tagPolicyService(t, "")
	_, err := svc.CreateCard(tagPolicyCtx(), core.CreateCardRequest{
		TypeID: "task", Title: "t", Actor: "u", Tags: []string{"nope"},
		Fields: map[string]any{"description": "d"},
	})
	var cerr *core.Error
	if !errors.As(err, &cerr) || cerr.Code != "unknown_tag" {
		t.Fatalf("unset policy must fail closed to locked, got %v", err)
	}
}

// TestTagPolicyLockedStillAcceptsDeclaredTags — the restriction is to TagSet,
// not a blanket refusal.
func TestTagPolicyLockedStillAcceptsDeclaredTags(t *testing.T) {
	svc := tagPolicyService(t, core.TagPolicyLocked)
	c, err := svc.CreateCard(tagPolicyCtx(), core.CreateCardRequest{
		TypeID: "task", Title: "t", Actor: "u", Tags: []string{"bug"},
		Fields: map[string]any{"description": "d"},
	})
	if err != nil {
		t.Fatalf("locked policy must accept a declared tag: %v", err)
	}
	if len(c.Tags) != 1 || c.Tags[0] != "bug" {
		t.Fatalf("tags = %v, want [bug]", c.Tags)
	}
}
