package core_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

// artifactService builds a service whose task type has a local-policy artifact
// field ("screenshot") and a uri-policy one ("external"), with a temp-dir
// artifacts store wired in.
func artifactService(t *testing.T) *core.Service {
	t.Helper()
	ws, types, boards := testConfig()
	types["task"].Fields = append(types["task"].Fields,
		core.FieldDef{ID: "screenshot", Type: core.FieldArtifact},
		core.FieldDef{ID: "external", Type: core.FieldArtifact, ArtifactPolicy: "uri"},
	)
	st := sqlitetest.Open(t, ws, 1)
	if err := st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	svc := core.NewService(ws, types, boards, st)
	am, err := artifacts.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifacts: %v", err)
	}
	svc.SetArtifacts(am)
	return svc
}

// metaOf extracts an artifact field's stored metadata map from a card.
func metaOf(t *testing.T, c *core.Card, field string) map[string]any {
	t.Helper()
	fm, ok := c.Fields.(map[string]any)
	if !ok {
		t.Fatalf("card fields are not a map: %#v", c.Fields)
	}
	m, ok := fm[field].(map[string]any)
	if !ok {
		t.Fatalf("field %q is not artifact metadata: %#v", field, fm[field])
	}
	return m
}

func mkArtifactCard(t *testing.T, svc *core.Service, ctx context.Context) *core.Card {
	t.Helper()
	c, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return c
}

// AddArtifact stores the bytes, records {uri,mime,size,sha256} on the field,
// bumps the version, emits artifact_added, and the bytes round-trip via
// OpenArtifact.
func TestAddArtifactStoresEmitsAndRoundTrips(t *testing.T) {
	svc := artifactService(t)
	ctx := core.WithActor(context.Background(), "u")
	c := mkArtifactCard(t, svc, ctx)

	content := []byte("hello artifact bytes")
	updated, err := svc.AddArtifact(ctx, c.ID, "screenshot", bytes.NewReader(content), 0)
	if err != nil {
		t.Fatalf("AddArtifact: %v", err)
	}
	if updated.Version != c.Version+1 {
		t.Errorf("version = %d, want %d", updated.Version, c.Version+1)
	}
	meta := metaOf(t, updated, "screenshot")
	uri, _ := meta["uri"].(string)
	if uri == "" {
		t.Fatal("no uri recorded on the field")
	}
	if meta["sha256"] == "" || meta["sha256"] == nil {
		t.Error("no sha256 recorded")
	}

	evs, err := svc.ListEvents(ctx, core.EventQuery{CardID: c.ID, Types: []string{"artifact_added"}, Limit: 5})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d artifact_added events, want 1", len(evs))
	}

	rc, err := svc.OpenArtifact(uri)
	if err != nil {
		t.Fatalf("OpenArtifact: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, content) {
		t.Errorf("round-trip mismatch: got %q, want %q", got, content)
	}
}

// Overwrite semantics: re-attaching different content to the same field points
// it at new bytes and bumps the version again; the log keeps both facts.
// Content-addressing means different content yields a different URI.
func TestAddArtifactOverwrites(t *testing.T) {
	svc := artifactService(t)
	ctx := core.WithActor(context.Background(), "u")
	c := mkArtifactCard(t, svc, ctx)

	u1, err := svc.AddArtifact(ctx, c.ID, "screenshot", bytes.NewReader([]byte("first")), 0)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := svc.AddArtifact(ctx, c.ID, "screenshot", bytes.NewReader([]byte("second")), 0)
	if err != nil {
		t.Fatal(err)
	}
	uri1 := metaOf(t, u1, "screenshot")["uri"].(string)
	uri2 := metaOf(t, u2, "screenshot")["uri"].(string)
	if uri1 == uri2 {
		t.Error("different content should content-address to different URIs")
	}
	if u2.Version != u1.Version+1 {
		t.Errorf("overwrite version = %d, want %d", u2.Version, u1.Version+1)
	}
	evs, _ := svc.ListEvents(ctx, core.EventQuery{CardID: c.ID, Types: []string{"artifact_added"}, Limit: 5})
	if len(evs) != 2 {
		t.Errorf("got %d artifact_added events, want 2", len(evs))
	}
}

// Identical content deduplicates to the same URI (content-addressed store).
func TestAddArtifactDeduplicates(t *testing.T) {
	svc := artifactService(t)
	ctx := core.WithActor(context.Background(), "u")
	c1 := mkArtifactCard(t, svc, ctx)
	c2 := mkArtifactCard(t, svc, ctx)

	same := []byte("identical bytes")
	u1, _ := svc.AddArtifact(ctx, c1.ID, "screenshot", bytes.NewReader(same), 0)
	u2, _ := svc.AddArtifact(ctx, c2.ID, "screenshot", bytes.NewReader(same), 0)
	if metaOf(t, u1, "screenshot")["uri"] != metaOf(t, u2, "screenshot")["uri"] {
		t.Error("identical content should dedup to the same URI")
	}
}

// Validation: only a declared local-policy artifact field accepts an upload.
func TestAddArtifactRejectsBadFieldAndPolicy(t *testing.T) {
	svc := artifactService(t)
	ctx := core.WithActor(context.Background(), "u")
	c := mkArtifactCard(t, svc, ctx)

	cases := map[string]string{
		"non-artifact field": "description",
		"uri-policy field":   "external",
		"unknown field":      "nope",
	}
	for name, field := range cases {
		if _, err := svc.AddArtifact(ctx, c.ID, field, bytes.NewReader([]byte("x")), 0); err == nil {
			t.Errorf("%s: expected an error attaching to %q", name, field)
		}
	}
}

// A workspace with no artifacts store configured rejects uploads with a clear
// error rather than panicking.
func TestAddArtifactRequiresConfiguredStore(t *testing.T) {
	ws, types, boards := testConfig()
	types["task"].Fields = append(types["task"].Fields, core.FieldDef{ID: "screenshot", Type: core.FieldArtifact})
	st := sqlitetest.Open(t, ws, 1)
	_ = st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"})
	svc := core.NewService(ws, types, boards, st) // no SetArtifacts
	ctx := core.WithActor(context.Background(), "u")
	c := mkArtifactCard(t, svc, ctx)
	if _, err := svc.AddArtifact(ctx, c.ID, "screenshot", bytes.NewReader([]byte("x")), 0); err == nil {
		t.Error("expected an error when the artifacts store is not configured")
	}
}
