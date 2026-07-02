package core_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
)

// testConfig builds the shared engineering-like workspace/type/board config used
// by the service tests: a board that enforces transitions (todo -> in_progress
// -> review -> done) with WIPLimits{in_progress: 1}.
func testConfig() (*core.Workspace, map[string]*core.CardType, map[string]*core.Board) {
	ws := &core.Workspace{
		ID:       "t",
		Name:     "T",
		Columns:  []core.Column{{ID: "todo", Name: "Todo"}, {ID: "in_progress", Name: "In Progress"}, {ID: "review", Name: "Review"}, {ID: "done", Name: "Done"}},
		TagSet:   []string{"bug", "feature"},
		LinkTypes: []core.LinkType{
			{ID: "depends-on", Name: "Depends on", Type: "directional"},
			{ID: "blocked-by", Name: "Blocked by", Type: "directional"},
			{ID: "related", Name: "Related", Type: "bidirectional"},
		},
		Settings: core.WorkspaceSettings{StrictFields: true, TagPolicy: "propose", DefaultUser: "u"},
	}
	types := map[string]*core.CardType{
		"task": {
			ID: "task", Name: "Task", SchemaVersion: 1,
			Fields: []core.FieldDef{
				{ID: "description", Type: core.FieldText, Required: true},
				{ID: "branch", Type: core.FieldString, Required: false},
				{ID: "priority", Type: core.FieldEnum, Options: []string{"low", "high"}},
				{ID: "estimate", Type: core.FieldNumber, Min: ptrFloat(0)},
				{ID: "work_log", Type: core.FieldRepeating, ItemFields: []core.FieldDef{
					{ID: "commit_hash", Type: core.FieldString, Required: true},
					{ID: "note", Type: core.FieldText, Required: false},
				}},
			},
			AllowedColumns: []string{"todo", "in_progress", "review", "done"},
		},
	}
	boards := map[string]*core.Board{
		"eng": {
			ID: "eng", Name: "Engineering",
			Columns:     []string{"todo", "in_progress", "review", "done"},
			CardTypeIDs: []string{"task"},
			WIPLimits:   map[string]int{"in_progress": 1},
			Transitions: map[string][]string{
				"todo":        {"in_progress"},
				"in_progress": {"review"},
				"review":      {"done", "in_progress"},
				"done":        {},
			},
		},
		"hipri": {
			ID: "hipri", Name: "High priority",
			Columns:       []string{"todo", "in_progress", "review", "done"},
			CardTypeIDs:   []string{"task"},
			DefaultFilter: map[string]any{"fields.priority": map[string]any{"$eq": "high"}},
		},
	}
	boards["eng"].Settings.EnforceTransitions = true
	return ws, types, boards
}

// newTestService builds an in-memory store + service from the default testConfig.
func newTestService(t *testing.T) (*core.Service, *sqlite.Store) {
	t.Helper()
	ws, types, boards := testConfig()
	return newTestServiceWith(t, ws, types, boards)
}

// newTestServiceWithSettings is newTestService with the workspace settings
// replaced (e.g. to opt condition types into persistence — seam 3b).
func newTestServiceWithSettings(t *testing.T, s core.WorkspaceSettings) (*core.Service, *sqlite.Store) {
	t.Helper()
	ws, types, boards := testConfig()
	ws.Settings = s
	return newTestServiceWith(t, ws, types, boards)
}

func newTestServiceWith(t *testing.T, ws *core.Workspace, types map[string]*core.CardType, boards map[string]*core.Board) (*core.Service, *sqlite.Store) {
	t.Helper()
	st, err := sqlite.Open(":memory:", ws)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	// Seed the default user so user-field validation can pass.
	_ = st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human", CreatedAt: time.Now().UTC()})
	_ = st.InsertUser(context.Background(), core.User{ID: "alice", Kind: "human", CreatedAt: time.Now().UTC()})

	return core.NewService(ws, types, boards, st), st
}

func ptrFloat(f float64) *float64 { return &f }

func TestCreateCard_HappyPath(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "Do thing", Status: "todo",
		Fields: map[string]any{"description": "go", "priority": "high", "estimate": 3},
		Tags:   []string{"bug"},
		Actor:  "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.Version != 1 || c.Status != "todo" {
		t.Errorf("got %+v", c)
	}
}

func TestCreateCard_UnknownEnum(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.CreateCard(ctx2(), core.CreateCardRequest{
		TypeID: "task", Title: "X", Status: "todo",
		Fields: map[string]any{"description": "go", "priority": "URGENT"},
		Actor:  "u",
	})
	ce := core.AsError(err)
	if ce == nil || ce.Code != "unknown_enum" {
		t.Fatalf("expected unknown_enum, got %v", err)
	}
	if len(ce.ValidOptions) != 2 {
		t.Errorf("valid_options = %v", ce.ValidOptions)
	}
}

func TestCreateCard_MissingRequired(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.CreateCard(ctx2(), core.CreateCardRequest{
		TypeID: "task", Title: "X", Status: "todo",
		Fields: map[string]any{},
		Actor:  "u",
	})
	ce := core.AsError(err)
	if ce == nil || ce.Code != "validation_failed" || ce.Field != "description" {
		t.Fatalf("expected validation_failed/description, got %v", err)
	}
}

func TestCreateCard_UnknownFieldStrict(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.CreateCard(ctx2(), core.CreateCardRequest{
		TypeID: "task", Title: "X", Status: "todo",
		Fields: map[string]any{"description": "go", "mystery": "x"},
		Actor:  "u",
	})
	ce := core.AsError(err)
	if ce == nil || ce.Code != "unknown_field" {
		t.Fatalf("expected unknown_field, got %v", err)
	}
}

func TestCreateCard_NumberMin(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.CreateCard(ctx2(), core.CreateCardRequest{
		TypeID: "task", Title: "X", Status: "todo",
		Fields: map[string]any{"description": "go", "estimate": -1},
		Actor:  "u",
	})
	ce := core.AsError(err)
	if ce == nil || ce.Code != "validation_failed" {
		t.Fatalf("expected validation_failed for min, got %v", err)
	}
}

func TestPatchCard_TransitionLegalAndIllegal(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := ctx2()
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "go"}, Actor: "u",
	})

	// legal: todo -> in_progress
	status := "in_progress"
	updated, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 1, Status: &status, Actor: "u"})
	if err != nil {
		t.Fatalf("legal move: %v", err)
	}
	if updated.Status != "in_progress" || updated.Version != 2 {
		t.Errorf("got status=%s v=%d", updated.Status, updated.Version)
	}

	// illegal: in_progress -> done (must go via review)
	bad := "done"
	_, err = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 2, Status: &bad, Actor: "u"})
	ce := core.AsError(err)
	if ce == nil || ce.Code != "transition_illegal" {
		t.Fatalf("expected transition_illegal, got %v", err)
	}
	if len(ce.ValidOptions) != 1 || ce.ValidOptions[0] != "review" {
		t.Errorf("valid_options = %v, want [review]", ce.ValidOptions)
	}
}

func TestPatchCard_ForceBypassesTransition(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := ctx2()
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "in_progress",
		Fields: map[string]any{"description": "go"}, Actor: "u",
	})

	// illegal without force: in_progress -> done (must go via review)
	bad := "done"
	_, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 1, Status: &bad, Actor: "u"})
	if core.AsError(err) == nil || core.AsError(err).Code != "transition_illegal" {
		t.Fatalf("expected transition_illegal without force, got %v", err)
	}

	// force bypasses the enforced-transition check
	updated, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 1, Status: &bad, Force: true, Actor: "u"})
	if err != nil {
		t.Fatalf("force move: %v", err)
	}
	if updated.Status != "done" {
		t.Errorf("got status=%s, want done", updated.Status)
	}
}

func TestRelease_ClearsOwner(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := ctx2()
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "go"}, Actor: "u",
	})
	claimed, _ := svc.Claim(ctx, c.ID, core.ClaimRequest{Version: 1, Status: "in_progress", Actor: "u"})
	if claimed.Owner != "u" {
		t.Fatalf("claim: owner=%s", claimed.Owner)
	}

	released, err := svc.Release(ctx, c.ID, core.ReleaseRequest{Version: claimed.Version, Actor: "u"})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released.Owner != "" {
		t.Errorf("owner=%s, want empty", released.Owner)
	}
}

func TestRelease_ForceMoveBacklog(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := ctx2()
	// Card in todo; enforced board has no todo->done edge (must go via in_progress,review).
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "go"}, Actor: "u",
	})

	// Without force: todo -> done should be illegal.
	done := "done"
	_, err := svc.Release(ctx, c.ID, core.ReleaseRequest{Version: 1, Status: done, Actor: "u"})
	if core.AsError(err) == nil || core.AsError(err).Code != "transition_illegal" {
		t.Fatalf("expected transition_illegal, got %v", err)
	}

	// With force: clears owner AND moves to done (bypassing the transition graph).
	released, err := svc.Release(ctx, c.ID, core.ReleaseRequest{Version: 1, Status: done, Force: true, Actor: "u"})
	if err != nil {
		t.Fatalf("force release: %v", err)
	}
	if released.Status != "done" {
		t.Errorf("status=%s, want done", released.Status)
	}
	if released.Owner != "" {
		t.Errorf("owner=%s, want empty", released.Owner)
	}
}

func TestPatchCard_VersionConflict(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := ctx2()
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "go"}, Actor: "u",
	})
	// first successful patch bumps to v2
	s := "in_progress"
	_, _ = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 1, Status: &s, Actor: "u"})
	// stale patch from v1 → 409
	s2 := "review"
	_, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 1, Status: &s2, Actor: "u"})
	ce := core.AsError(err)
	if ce == nil || ce.Code != "version_conflict" {
		t.Fatalf("expected version_conflict, got %v", err)
	}
	if ce.CurrentCard == nil || ce.CurrentCard.Version != 2 {
		t.Errorf("conflict did not carry current card v2")
	}
}

func TestGetCard_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := ctx2()
	_, err := svc.GetCard(ctx, "card_doesnotexist")
	ce := core.AsError(err)
	if ce == nil || ce.Code != "not_found" || ce.HTTPStatus != 404 {
		t.Fatalf("expected 404 not_found, got %v", err)
	}
}

func TestPatchCard_FieldUpdate(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := ctx2()
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "go", "branch": "b1"}, Actor: "u",
	})
	updated, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 1, Fields: map[string]any{"branch": "b2"}, Actor: "u"})
	if err != nil {
		t.Fatalf("patch field: %v", err)
	}
	fm := updated.Fields.(map[string]any)
	if fm["branch"] != "b2" || fm["description"] != "go" {
		t.Errorf("fields = %#v", fm)
	}
}

func TestResolveActor(t *testing.T) {
	svc, _ := newTestService(t)
	if a, e := svc.ResolveActor("alice", ""); a != "alice" || e != nil {
		t.Errorf("header should win: %q %v", a, e)
	}
	if a, e := svc.ResolveActor("", "bob"); a != "bob" || e != nil {
		t.Errorf("env should win: %q %v", a, e)
	}
	if a, e := svc.ResolveActor("", ""); a != "u" || e != nil {
		t.Errorf("default should win: %q %v", a, e)
	}
}

func ctx2() context.Context { return context.Background() }

// --- Slice 1: coordination loop primitives ---

func TestAppendEntry_DeepValidationAndEntryID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "go"}, Actor: "u",
	})
	// Valid append returns a card whose work_log now has one entry with an entry_id.
	updated, err := svc.AppendEntry(ctx, c.ID, "work_log", map[string]any{"commit_hash": "abc123", "note": "first"}, c.Version)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	arr, _ := updated.Fields.(map[string]any)["work_log"].([]any)
	if len(arr) != 1 {
		t.Fatalf("work_log len = %d, want 1", len(arr))
	}
	entry := arr[0].(map[string]any)
	if entry["entry_id"] == "" {
		t.Error("entry_id not set")
	}
	if entry["commit_hash"] != "abc123" {
		t.Errorf("commit_hash = %v", entry["commit_hash"])
	}
	// Missing required item field.
	_, err = svc.AppendEntry(ctx, c.ID, "work_log", map[string]any{"note": "no hash"}, updated.Version)
	if ce := core.AsError(err); ce == nil || ce.Code != "validation_failed" {
		t.Fatalf("expected validation_failed for missing item field, got %v", err)
	}
	// Unknown item field.
	_, err = svc.AppendEntry(ctx, c.ID, "work_log", map[string]any{"commit_hash": "abc", "bogus": 1}, updated.Version)
	if ce := core.AsError(err); ce == nil || ce.Code != "validation_failed" {
		t.Fatalf("expected validation_failed for unknown item field, got %v", err)
	}
	// Non-repeating field rejected (use current version; card is now v2).
	_, err = svc.AppendEntry(ctx, c.ID, "description", map[string]any{}, updated.Version)
	if ce := core.AsError(err); ce == nil || ce.Code != "validation_failed" {
		t.Fatalf("expected validation_failed for non-repeating field, got %v", err)
	}
}

func TestUpdateAndRemoveEntry(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	added, _ := svc.AppendEntry(ctx, c.ID, "work_log", map[string]any{"commit_hash": "a"}, c.Version)
	entryID := added.Fields.(map[string]any)["work_log"].([]any)[0].(map[string]any)["entry_id"].(string)
	// Update.
	updated, err := svc.UpdateEntry(ctx, c.ID, "work_log", entryID, map[string]any{"commit_hash": "b"}, added.Version)
	if err != nil {
		t.Fatalf("update entry: %v", err)
	}
	entry := updated.Fields.(map[string]any)["work_log"].([]any)[0].(map[string]any)
	if entry["commit_hash"] != "b" || entry["entry_id"] != entryID {
		t.Errorf("entry = %+v", entry)
	}
	// Remove.
	removed, err := svc.RemoveEntry(ctx, c.ID, "work_log", entryID, updated.Version)
	if err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	arr, _ := removed.Fields.(map[string]any)["work_log"].([]any)
	if len(arr) != 0 {
		t.Errorf("work_log len = %d, want 0", len(arr))
	}
}

func TestAddLink_TargetMissingAndTypeMismatch(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	a, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "A", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	// Target does not exist.
	_, err := svc.AddLink(ctx, a.ID, core.LinkInput{TypeID: "depends-on", Target: "card_nope", Actor: "u"})
	if ce := core.AsError(err); ce == nil || ce.Code != "target_card_missing" {
		t.Fatalf("expected target_card_missing, got %v", err)
	}
	b, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "B", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	// Valid link.
	linked, err := svc.AddLink(ctx, a.ID, core.LinkInput{TypeID: "depends-on", Target: b.ID, Actor: "u"})
	if err != nil {
		t.Fatalf("add link: %v", err)
	}
	if len(linked.Links) != 1 || linked.Links[0].Target != b.ID {
		t.Errorf("links = %+v", linked.Links)
	}
}

func TestCommentAddAndEdit(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	added, err := svc.AddComment(ctx, c.ID, "first comment")
	if err != nil || len(added.Comments) != 1 {
		t.Fatalf("add comment: %v %+v", err, added)
	}
	cmID := added.Comments[0].ID
	edited, err := svc.EditComment(ctx, c.ID, cmID, "edited body")
	if err != nil {
		t.Fatalf("edit comment: %v", err)
	}
	if edited.Comments[0].Body != "edited body" {
		t.Errorf("body = %q", edited.Comments[0].Body)
	}
}

func TestClaim_ConflictIfOwnedByAnother(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	// alice claims.
	aliceCtx := core.WithActor(ctx2(), "alice")
	claimed, err := svc.Claim(aliceCtx, c.ID, core.ClaimRequest{Version: 1, Actor: "alice"})
	if err != nil || claimed.Owner != "alice" {
		t.Fatalf("alice claim: %v %+v", err, claimed)
	}
	// bob tries to claim the same card at the now-current version → 409.
	bobCtx := core.WithActor(ctx2(), "bob")
	_, err = svc.Claim(bobCtx, c.ID, core.ClaimRequest{Version: 2, Actor: "bob"})
	if ce := core.AsError(err); ce == nil || ce.Code != "version_conflict" {
		t.Fatalf("expected version_conflict, got %v", err)
	}
}

func TestTakeNext_PicksOldestUnowned(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	c1, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T1", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	c2, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T2", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	_ = c2
	ctxA := core.WithActor(ctx2(), "alice")
	got, err := svc.TakeNext(ctxA, core.TakeNextRequest{TypeID: "task", AssignTo: "alice", Actor: "alice"})
	if err != nil || got == nil {
		t.Fatalf("take-next: %v %v", got, err)
	}
	if got.ID != c1.ID {
		t.Errorf("expected oldest %q, got %q", c1.ID, got.ID)
	}
	if got.Owner != "alice" {
		t.Errorf("owner = %q", got.Owner)
	}
	// Take the second unowned card too.
	got2, _ := svc.TakeNext(ctxA, core.TakeNextRequest{TypeID: "task", AssignTo: "alice", Actor: "alice"})
	if got2 == nil {
		t.Fatal("expected second card")
	}
	// Nothing left unowned.
	got3, _ := svc.TakeNext(ctxA, core.TakeNextRequest{TypeID: "task", AssignTo: "alice", Actor: "alice"})
	if got3 != nil {
		t.Errorf("expected nil, got %+v", got3)
	}
}

func TestTakeNext_StatusRespectsTransitions(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	// Create a card already in 'in_progress' (so take-next to 'review' is legal
	// only from in_progress, not from todo).
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T", Status: "in_progress", Fields: map[string]any{"description": "d"}, Actor: "u"})
	ctxA := core.WithActor(ctx2(), "alice")
	got, err := svc.TakeNext(ctxA, core.TakeNextRequest{TypeID: "task", AssignTo: "alice", Status: "review", Actor: "alice"})
	if err != nil || got == nil {
		t.Fatalf("take-next: %v %v", got, err)
	}
	if got.ID != c.ID {
		t.Errorf("expected %q, got %q", c.ID, got.ID)
	}
	if got.Status != "review" {
		t.Errorf("status = %q, want review", got.Status)
	}
}

func TestHistoryAndEvents(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	st := "in_progress"
	_, _ = svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: 1, Status: &st, Actor: "u"})
	evs, err := svc.ListEvents(ctx, core.EventQuery{CardID: c.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) < 2 {
		t.Fatalf("events = %d, want >=2", len(evs))
	}
	hist, err := svc.History(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != len(evs) {
		t.Errorf("history len %d != events len %d", len(hist), len(evs))
	}
	// Summaries are non-empty.
	for _, h := range hist {
		if h.Summary == "" {
			t.Error("empty summary")
		}
	}
}

func TestDryRunCreateWritesNothing(t *testing.T) {
	svc, st := newTestService(t)
	ctx := ctx2()
	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "Dry", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "Dry" {
		t.Errorf("got %q", c.Title)
	}
	// Nothing persisted.
	got, _ := st.GetCard(ctx, c.ID)
	if got != nil {
		t.Error("dry_run should not persist the card")
	}
}

func TestCreateCard_SchemaVersionMismatch(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.CreateCard(ctx2(), core.CreateCardRequest{
		TypeID: "task", Title: "X", Status: "todo", SchemaVersion: 99,
		Fields: map[string]any{"description": "d"}, Actor: "u",
	})
	if ce := core.AsError(err); ce == nil || ce.Code != "schema_version_mismatch" {
		t.Fatalf("expected schema_version_mismatch, got %v", err)
	}
}

func TestCardLinkValidation_TargetMissing(t *testing.T) {
	ws := &core.Workspace{ID: "t", Columns: []core.Column{{ID: "todo", Name: "Todo"}, {ID: "done", Name: "Done"}}, Settings: core.WorkspaceSettings{StrictFields: true, DefaultUser: "u"}}
	types := map[string]*core.CardType{
		"job": {ID: "job", Name: "Job", SchemaVersion: 1, Fields: []core.FieldDef{
			{ID: "description", Type: core.FieldText, Required: true},
			{ID: "assigned", Type: core.FieldCardLink, TargetType: "printer"},
		}, AllowedColumns: []string{"todo", "done"}},
	}
	st, _ := sqlite.Open(":memory:", ws)
	defer st.Close()
	_ = st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human", CreatedAt: time.Now().UTC()})
	svc := core.NewService(ws, types, map[string]*core.Board{}, st)

	_, err := svc.CreateCard(ctx2(), core.CreateCardRequest{TypeID: "job", Title: "J", Status: "todo",
		Fields: map[string]any{"description": "d", "assigned": "card_no_printer"}, Actor: "u"})
	if ce := core.AsError(err); ce == nil || ce.Code != "target_card_missing" {
		t.Fatalf("expected target_card_missing, got %v", err)
	}
}

func TestFilterDSL_BasicOps(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	for _, title := range []string{"alpha", "beta", "gamma"} {
		_, _ = svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: title, Status: "todo", Fields: map[string]any{"description": "d", "branch": title + "-b"}, Actor: "u"})
	}
	page, err := svc.ListCards(ctx, core.CardQuery{Limit: 10, Filter: map[string]any{
		"fields.branch": map[string]any{"$eq": "beta-b"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "beta" {
		t.Errorf("filter result = %+v", page.Items)
	}
}

// TestBoardDefaultFilterScope verifies that a board's default_filter is
// enforced as a hard isolation boundary on board_id queries: cards outside the
// filter never appear, and a caller filter is AND-ed (narrows, cannot widen).
func TestBoardDefaultFilterScope(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	_, _ = svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "H", Status: "todo", Fields: map[string]any{"description": "d", "priority": "high"}, Actor: "u"})
	_, _ = svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "L", Status: "todo", Fields: map[string]any{"description": "d", "priority": "low"}, Actor: "u"})

	// Board scope alone: only the high-priority card is visible.
	page, err := svc.ListCards(ctx, core.CardQuery{BoardID: "hipri", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "H" {
		t.Fatalf("board scope = %+v, want only [H]", titlesOf(page.Items))
	}

	// A caller filter for the excluded value cannot escape the board boundary:
	// high AND low = nothing.
	page, err = svc.ListCards(ctx, core.CardQuery{BoardID: "hipri", Limit: 10, Filter: map[string]any{
		"fields.priority": map[string]any{"$eq": "low"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("caller filter widened board scope: %+v", titlesOf(page.Items))
	}
}

func titlesOf(cards []core.Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Title
	}
	return out
}

// TestUpgradeSchema exercises re-pinning a card to a newer type version:
// applying a migration's field_defaults, dropping a field removed in the new
// version, dry-run previews, and the already-current no-op.
func TestUpgradeSchema(t *testing.T) {
	ws := &core.Workspace{
		ID: "t", Name: "T",
		Columns:  []core.Column{{ID: "todo", Name: "Todo"}},
		Settings: core.WorkspaceSettings{StrictFields: true, DefaultUser: "u"},
	}
	// v1 schema: description + a legacy field that v2 will remove.
	taskType := &core.CardType{
		ID: "task", Name: "Task", SchemaVersion: 1,
		Fields: []core.FieldDef{
			{ID: "description", Type: core.FieldText, Required: true},
			{ID: "legacy", Type: core.FieldString, Required: false},
		},
		AllowedColumns: []string{"todo"},
	}
	types := map[string]*core.CardType{"task": taskType}
	st, err := sqlite.Open(":memory:", ws)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	_ = st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human", CreatedAt: time.Now().UTC()})
	svc := core.NewService(ws, types, map[string]*core.Board{}, st)
	ctx := core.WithActor(context.Background(), "u")

	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d", "legacy": "x"}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Evolve the type to v2: drop 'legacy', add a required 'priority' with a
	// migration default.
	taskType.SchemaVersion = 2
	taskType.Fields = []core.FieldDef{
		{ID: "description", Type: core.FieldText, Required: true},
		{ID: "priority", Type: core.FieldEnum, Options: []string{"low", "high"}, Required: true},
	}
	taskType.Migrations = map[string]core.Migration{
		"2": {From: 1, Summary: "drop legacy; add priority", FieldDefaults: map[string]any{"priority": "low"}},
	}

	// Dry-run: previews v2 without persisting.
	preview, err := svc.UpgradeSchema(ctx, c.ID, core.UpgradeSchemaRequest{DryRun: true, Actor: "u"})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if preview.SchemaVersion != 2 {
		t.Errorf("dry-run schema_version = %d, want 2", preview.SchemaVersion)
	}
	if got, _ := svc.GetCard(ctx, c.ID); got.SchemaVersion != 1 {
		t.Errorf("dry-run persisted: stored schema_version = %d, want 1", got.SchemaVersion)
	}

	// Real upgrade.
	up, err := svc.UpgradeSchema(ctx, c.ID, core.UpgradeSchemaRequest{Actor: "u"})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if up.SchemaVersion != 2 {
		t.Errorf("schema_version = %d, want 2", up.SchemaVersion)
	}
	if up.Version != c.Version+1 {
		t.Errorf("version = %d, want %d", up.Version, c.Version+1)
	}
	f := up.Fields.(map[string]any)
	if f["priority"] != "low" {
		t.Errorf("priority = %v, want low (migration default)", f["priority"])
	}
	if _, ok := f["legacy"]; ok {
		t.Errorf("legacy should be dropped on upgrade, got %v", f["legacy"])
	}

	// No-op: already at the current version.
	again, err := svc.UpgradeSchema(ctx, c.ID, core.UpgradeSchemaRequest{Actor: "u"})
	if err != nil {
		t.Fatalf("re-upgrade: %v", err)
	}
	if again.Version != up.Version {
		t.Errorf("no-op upgrade bumped version: %d -> %d", up.Version, again.Version)
	}

	// Downgrade is rejected.
	if _, err := svc.UpgradeSchema(ctx, c.ID, core.UpgradeSchemaRequest{TargetVersion: 1, Actor: "u"}); err == nil {
		t.Error("expected downgrade to be rejected")
	}
}

// TestListCardsPagination walks the keyset pages and asserts no page exceeds
// the limit (the "one extra row" guard), pages don't overlap or skip, and the
// last page clears the cursor.
func TestListCardsPagination(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	const n = 5
	for i := 0; i < n; i++ {
		if _, err := svc.CreateCard(ctx, core.CreateCardRequest{
			TypeID: "task", Title: fmt.Sprintf("c%d", i), Status: "todo",
			Fields: map[string]any{"description": "d"}, Actor: "u",
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := svc.ListCards(ctx, core.CardQuery{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(page.Items) > 2 {
			t.Fatalf("page returned %d items, want <= limit 2", len(page.Items))
		}
		for _, c := range page.Items {
			if seen[c.ID] {
				t.Fatalf("card %s returned on more than one page", c.ID)
			}
			seen[c.ID] = true
		}
		pages++
		if pages > n+2 {
			t.Fatal("pagination did not terminate")
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != n {
		t.Errorf("paged %d unique cards, want %d", len(seen), n)
	}
}

// TestTakeNextConcurrent hammers take-next from many goroutines and asserts
// every card is claimed exactly once (no double-claim, no lost cards). Guards
// the atomic-claim CAS + IMMEDIATE-transaction behavior under contention.
func TestTakeNextConcurrent(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	const n = 30
	for i := 0; i < n; i++ {
		if _, err := svc.CreateCard(ctx, core.CreateCardRequest{
			TypeID: "task", Title: fmt.Sprintf("t%d", i), Status: "todo",
			Fields: map[string]any{"description": "d"}, Actor: "u",
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	var mu sync.Mutex
	claimed := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				c, err := svc.TakeNext(ctx, core.TakeNextRequest{AssignTo: "u", Actor: "u"})
				if err != nil {
					t.Errorf("take-next: %v", err)
					return
				}
				if c == nil {
					return // exhausted
				}
				mu.Lock()
				claimed[c.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(claimed) != n {
		t.Errorf("claimed %d distinct cards, want %d", len(claimed), n)
	}
	for id, cnt := range claimed {
		if cnt != 1 {
			t.Errorf("card %s claimed %d times (double-claim)", id, cnt)
		}
	}
}

func TestBlockedQuery(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	// 'todo' columns are not 'done', so a depends-on link to a todo card blocks.
	a, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "A", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	b, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "B", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	_, _ = svc.AddLink(ctx, a.ID, core.LinkInput{TypeID: "depends-on", Target: b.ID, Actor: "u"})

	page, err := svc.ListCards(ctx, core.CardQuery{Blocked: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != a.ID {
		t.Errorf("blocked = %+v, want [A]", page.Items)
	}
}

func TestRemoveEntry_RequiresVersion(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := core.WithActor(ctx2(), "u")
	c, _ := svc.CreateCard(ctx, core.CreateCardRequest{TypeID: "task", Title: "T", Status: "todo", Fields: map[string]any{"description": "d"}, Actor: "u"})
	added, _ := svc.AppendEntry(ctx, c.ID, "work_log", map[string]any{"commit_hash": "a"}, c.Version)
	entryID := added.Fields.(map[string]any)["work_log"].([]any)[0].(map[string]any)["entry_id"].(string)

	// version=0 → rejected (was previously silently allowed, skipping CAS).
	_, err := svc.RemoveEntry(ctx, c.ID, "work_log", entryID, 0)
	if ce := core.AsError(err); ce == nil || ce.Code != "validation_failed" {
		t.Fatalf("expected validation_failed for version=0, got %v", err)
	}

	// stale version → version_conflict
	_, err = svc.RemoveEntry(ctx, c.ID, "work_log", entryID, 999)
	if ce := core.AsError(err); ce == nil || ce.Code != "version_conflict" {
		t.Fatalf("expected version_conflict for stale version, got %v", err)
	}

	// correct version → success
	_, err = svc.RemoveEntry(ctx, c.ID, "work_log", entryID, added.Version)
	if err != nil {
		t.Fatalf("remove with correct version: %v", err)
	}
}

func TestListCards_InvalidCursor(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := ctx2()
	_, err := svc.ListCards(ctx, core.CardQuery{Cursor: "not-a-valid-cursor!!"})
	if ce := core.AsError(err); ce == nil || ce.Code != "validation_failed" || ce.HTTPStatus != 422 {
		t.Fatalf("expected 422 validation_failed for bad cursor, got %v", err)
	}
	if ce := core.AsError(err); ce.Field != "cursor" {
		t.Errorf("error field = %q, want cursor", ce.Field)
	}
}

func TestResolveCard_FullIDFirst(t *testing.T) {
	// A full id that also suffixes another card still resolves to itself.
	svc, st := newTestService(t)
	ctx := context.Background()
	// Two cards whose last-8 suffix collides; the older one is the one we'll
	// resolve by full id.
	idA := "card_ABCD1234aaaaaaaaaaaaaaaaaaaaaaaa"
	idB := "card_ABCD1234bbbbbbbbbbbbbbbbbbbbbbbb"
	now := time.Now().UTC()
	older := now.Add(-time.Hour)
	ca := &core.Card{ID: idA, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "A-full", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: older, UpdatedAt: older, CreatedBy: "u"}
	cb := &core.Card{ID: idB, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "B-full", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: "u"}
	_ = st.InsertCard(ctx, ca, nil)
	_ = st.InsertCard(ctx, cb, nil)
	// Resolve the older card by its FULL id (not the short suffix).
	got, err := svc.ResolveCard(ctx, idA)
	if err != nil {
		t.Fatalf("resolve full: %v", err)
	}
	if got.ID != idA {
		t.Errorf("expected %s, got %s", idA, got.ID)
	}
}

func TestResolveCard_ShortIDUnique(t *testing.T) {
	svc, st := newTestService(t)
	ctx := context.Background()
	idA := "card_UNIQU599cccccccccccccccccccccccccc"
	now := time.Now().UTC()
	ca := &core.Card{ID: idA, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "Solo", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: "u"}
	_ = st.InsertCard(ctx, ca, nil)
	got, err := svc.ResolveCard(ctx, "UNIQU599")
	if err != nil {
		t.Fatalf("resolve short: %v", err)
	}
	if got.ID != idA {
		t.Errorf("expected %s, got %s", idA, got.ID)
	}
}

func TestResolveCard_AmbiguousError(t *testing.T) {
	svc, st := newTestService(t)
	ctx := context.Background()
	idA := "card_AMBIG008dddddddddddddddddddddddddd"
	idB := "card_AMBIG008eeeeeeeeeeeeeeeeeeeeeeeeee"
	now := time.Now().UTC()
	older := now.Add(-time.Hour)
	_ = st.InsertCard(ctx, &core.Card{ID: idA, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "A-amb", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: older, UpdatedAt: older, CreatedBy: "u"}, nil)
	_ = st.InsertCard(ctx, &core.Card{ID: idB, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1, Title: "B-amb", Status: "todo", Fields: map[string]any{}, Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: "u"}, nil)
	_, err := svc.ResolveCard(ctx, "AMBIG008")
	var amb *core.AmbiguousIDError
	if !errors.As(err, &amb) {
		t.Fatalf("expected *AmbiguousIDError, got %v", err)
	}
	if len(amb.Candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(amb.Candidates))
	}
	// Verify both ids and titles are present.
	got := map[string]string{}
	for _, c := range amb.Candidates {
		got[c.ID] = c.Title
	}
	if got[idA] != "A-amb" || got[idB] != "B-amb" {
		t.Errorf("candidates = %v", got)
	}
}

func TestResolveCard_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	_, err := svc.ResolveCard(ctx, "NOPE0000")
	ce := core.AsError(err)
	if ce == nil || ce.Code != "not_found" {
		t.Fatalf("expected not_found, got %v", err)
	}
}

// errorsAs removed: errors.As is used directly after importing "errors".
