package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
)

// testWorkspace + newStore mirror internal/sqlite's test setup: an in-memory DB
// with a minimal workspace so we can exercise export/import without disk or a
// definitions/ directory.
func newStore(t *testing.T) (*sqlite.Store, *core.Workspace) {
	t.Helper()
	ws := &core.Workspace{
		ID:   "demo",
		Name: "Demo workspace",
		Columns: []core.Column{
			{ID: "todo", Name: "To Do"}, {ID: "in_progress", Name: "In Progress"}, {ID: "done", Name: "Done"},
		},
		Settings: core.WorkspaceSettings{StrictFields: true, TagPolicy: "propose", DefaultUser: "u"},
	}
	st, err := sqlite.Open(":memory:", ws)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, ws
}

// seed populates a store with a representative slice of state: two users, two
// cards (one with a repeating field, a comment, and an outgoing link), plus a
// couple of events. Returned so tests can assert against the originals.
func seedStore(t *testing.T, st *sqlite.Store) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)

	for _, u := range []core.User{
		{ID: "pi", DisplayName: "Pi", Kind: "agent", CreatedAt: at},
		{ID: "foz", DisplayName: "Foz", Kind: "human", CreatedAt: at},
	} {
		if err := st.InsertUser(ctx, u); err != nil {
			t.Fatalf("seed user %s: %v", u.ID, err)
		}
	}

	c1 := &core.Card{
		ID: "card_a", WorkspaceID: "demo", TypeID: "programming-task", SchemaVersion: 1,
		Title: "Finish import command", Status: "in_progress", Owner: "pi",
		Tags: []string{"feature"},
		Fields: map[string]any{
			"branch": "feat/import",
			"work_log": []any{
				map[string]any{"entry_id": "ent_1", "author": "pi", "notes": "wrote importJSONL"},
			},
		},
		Version: 3, CreatedAt: at, UpdatedAt: at.Add(time.Hour), CreatedBy: "foz",
	}
	if err := st.InsertCard(ctx, c1, &core.Event{CardID: "card_a", Type: core.EventCardCreated, Actor: "foz", At: at}); err != nil {
		t.Fatalf("seed card_a: %v", err)
	}
	if err := st.InsertComment(ctx, "card_a", core.Comment{ID: "cm_1", Author: "pi", Body: "import is the remaining half", CreatedAt: at.Add(30 * time.Minute), EditedAt: at.Add(45 * time.Minute)}); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	c2 := &core.Card{
		ID: "card_b", WorkspaceID: "demo", TypeID: "programming-task", SchemaVersion: 1,
		Title: "Export JSONL", Status: "done", Fields: map[string]any{"branch": "feat/export"},
		Version: 1, CreatedAt: at, UpdatedAt: at, CreatedBy: "pi",
	}
	if err := st.InsertCard(ctx, c2, nil); err != nil {
		t.Fatalf("seed card_b: %v", err)
	}
	// card_a depends-on card_b.
	if err := st.InsertLink(ctx, "card_a", core.Link{TypeID: "depends-on", Target: "card_b", CreatedBy: "pi", CreatedAt: at}); err != nil {
		t.Fatalf("seed link: %v", err)
	}
	if err := st.InsertEventRaw(ctx, &core.Event{CardID: "card_a", Type: core.EventStatusChanged, Actor: "pi", At: at.Add(time.Hour), Diff: map[string]any{"before": "todo", "after": "in_progress"}}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

// TestExportIsCompleteBeyondPageCap guards the export data-loss bug: a
// workspace with more cards than ListCards' page ceiling must export ALL of
// them (export cursor-paginates), not silently truncate to one page.
func TestExportIsCompleteBeyondPageCap(t *testing.T) {
	ctx := context.Background()
	src, ws := newStore(t)
	const total = 510 // just past the 500 page ceiling → two pages
	base := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	for i := range total {
		id := "card_" + strconv.Itoa(i)
		if err := src.InsertCard(ctx, &core.Card{
			ID: id, WorkspaceID: "demo", TypeID: "task", SchemaVersion: 1,
			Title: id, Status: "todo", Fields: map[string]any{}, Version: 1,
			CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Millisecond), CreatedBy: "u",
		}, nil); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	var buf bytes.Buffer
	exp, err := exportJSONL(ctx, src, &buf, ws, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exp.Cards != total {
		t.Fatalf("export tallied %d cards, want %d", exp.Cards, total)
	}
	if got := strings.Count(buf.String(), `"type":"card"`); got != total {
		t.Errorf("export wrote %d card lines, want %d", got, total)
	}

	// Round-trips completely into a fresh store.
	dst, _ := newStore(t)
	imp, err := importJSONL(ctx, dst, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imp.Cards != total {
		t.Errorf("import restored %d cards, want %d", imp.Cards, total)
	}
}

// A state-only export omits the mutation log (keeping only card_deleted
// tombstones, of which the seed has none) but still round-trips card state;
// importing it reconstructs no mutation history.
func TestExportStateOnly(t *testing.T) {
	ctx := context.Background()
	src, ws := newStore(t)
	seedStore(t, src)

	var buf bytes.Buffer
	exp, err := exportJSONL(ctx, src, &buf, ws, true)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exp.Events != 0 {
		t.Errorf("state-only export tallied %d events, want 0", exp.Events)
	}
	if strings.Contains(buf.String(), `"type":"event"`) {
		t.Errorf("state-only export wrote event lines")
	}
	if exp.Cards != 2 {
		t.Errorf("state-only export tallied %d cards, want 2", exp.Cards)
	}

	dst, _ := newStore(t)
	imp, err := importJSONL(ctx, dst, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imp.Cards != 2 || imp.Events != 0 {
		t.Errorf("import restored %+v, want 2 cards / 0 events", imp)
	}
}

// TestExportImportRoundTrip is the core fidelity guarantee: export a populated
// store, import the bytes into a fresh store, and assert every entity matches —
// ids, versions, timestamps, fields, comments, links, and the event log.
func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src, ws := newStore(t)
	seedStore(t, src)

	var buf bytes.Buffer
	exp, err := exportJSONL(ctx, src, &buf, ws, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	dst, _ := newStore(t)
	imp, err := importJSONL(ctx, dst, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if exp != imp {
		t.Fatalf("stats differ: export=%+v import=%+v", exp, imp)
	}
	if exp.Cards != 2 || exp.Users != 2 || exp.Comments != 1 || exp.Links != 1 || exp.Events != 2 {
		t.Fatalf("unexpected counts: %+v", exp)
	}

	// Card identity, version, timestamps, owner, tags preserved verbatim.
	got, err := dst.GetCard(ctx, "card_a")
	if err != nil {
		t.Fatalf("get card_a: %v", err)
	}
	if got.Title != "Finish import command" || got.Status != "in_progress" || got.Owner != "pi" {
		t.Errorf("card_a envelope wrong: %+v", got)
	}
	if got.Version != 3 {
		t.Errorf("version not preserved: got %d want 3", got.Version)
	}
	if !got.UpdatedAt.Equal(time.Date(2026, 6, 26, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("updated_at not preserved: %v", got.UpdatedAt)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "feature" {
		t.Errorf("tags not preserved: %v", got.Tags)
	}

	// Repeating field entry survived (including its stable entry_id).
	fields, _ := got.Fields.(map[string]any)
	wl, ok := fields["work_log"].([]any)
	if !ok || len(wl) != 1 {
		t.Fatalf("work_log not preserved: %#v", fields["work_log"])
	}
	if entry := wl[0].(map[string]any); entry["entry_id"] != "ent_1" {
		t.Errorf("entry_id not preserved: %v", entry["entry_id"])
	}

	// Comments and links restored — including edited_at (regression: import
	// used to hardcode edited_at to NULL, dropping comment edit timestamps).
	if len(got.Comments) != 1 || got.Comments[0].ID != "cm_1" {
		t.Errorf("comment not restored: %+v", got.Comments)
	}
	if len(got.Comments) == 1 && !got.Comments[0].EditedAt.Equal(time.Date(2026, 6, 26, 12, 45, 0, 0, time.UTC)) {
		t.Errorf("comment edited_at not preserved through round-trip: %v", got.Comments[0].EditedAt)
	}
	if len(got.Links) != 1 || got.Links[0].Target != "card_b" || got.Links[0].TypeID != "depends-on" {
		t.Errorf("link not restored: %+v", got.Links)
	}

	// Event log restored in order.
	evs, err := dst.List(ctx, core.EventQuery{CardID: "card_a", Limit: 100})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(evs) != 2 || evs[0].Type != core.EventCardCreated || evs[1].Type != core.EventStatusChanged {
		t.Fatalf("events not restored in order: %+v", evs)
	}
}

// TestImportHeaderOnly verifies the header line alone is a valid (empty) import.
func TestImportHeaderOnly(t *testing.T) {
	ctx := context.Background()
	st, _ := newStore(t)
	in := `{"type":"export","version":1,"workspace_id":"demo","workspace":"Demo workspace"}` + "\n"
	stats, err := importJSONL(ctx, st, strings.NewReader(in))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if (stats != portStats{}) {
		t.Errorf("expected zero stats, got %+v", stats)
	}
}

// TestImportRejectsUnknownType ensures a malformed/foreign record fails loudly
// rather than being silently skipped.
func TestImportRejectsUnknownType(t *testing.T) {
	ctx := context.Background()
	st, _ := newStore(t)
	in := `{"type":"widget","data":{}}` + "\n"
	if _, err := importJSONL(ctx, st, strings.NewReader(in)); err == nil {
		t.Fatal("expected error for unknown record type, got nil")
	}
}

// TestImportDuplicateCardFailsLoudly is the no-silent-overwrite guarantee: a
// second import of the same card id must error, not clobber.
func TestImportDuplicateCardFailsLoudly(t *testing.T) {
	ctx := context.Background()
	st, _ := newStore(t)
	line := `{"type":"card","data":{"id":"card_a","workspace_id":"demo","type_id":"programming-task","schema_version":1,"title":"X","status":"todo","fields":{},"version":1,"created_at":"2026-06-26T12:00:00Z","updated_at":"2026-06-26T12:00:00Z","created_by":"u"}}` + "\n"
	if _, err := importJSONL(ctx, st, strings.NewReader(line)); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := importJSONL(ctx, st, strings.NewReader(line)); err == nil {
		t.Fatal("expected duplicate card id to error, got nil")
	}
}

// TestExportImportMultiValueField pins the portable-snapshot contract for
// multiple fields (frontend-rebuild Phase 3): a JSON-array field value
// survives export → import byte-for-byte (as []any), and a card with the
// field unset stays unset (key absent) — never null, never [].
func TestExportImportMultiValueField(t *testing.T) {
	ctx := context.Background()
	src, ws := newStore(t)
	now := time.Now().UTC()
	withMulti := &core.Card{
		ID: "card_mv1", WorkspaceID: "demo", TypeID: "task", SchemaVersion: 1,
		Title: "multi", Status: "todo", Version: 1, CreatedBy: "u",
		CreatedAt: now, UpdatedAt: now,
		Fields: map[string]any{"platforms": []any{"desktop", "mobile"}},
	}
	without := &core.Card{
		ID: "card_mv2", WorkspaceID: "demo", TypeID: "task", SchemaVersion: 1,
		Title: "plain", Status: "todo", Version: 1, CreatedBy: "u",
		CreatedAt: now, UpdatedAt: now, Fields: map[string]any{},
	}
	for _, c := range []*core.Card{withMulti, without} {
		if err := src.InsertCard(ctx, c, nil); err != nil {
			t.Fatalf("insert %s: %v", c.ID, err)
		}
	}

	var buf bytes.Buffer
	if _, err := exportJSONL(ctx, src, &buf, ws, false); err != nil {
		t.Fatalf("export: %v", err)
	}
	dst, _ := newStore(t)
	if _, err := importJSONL(ctx, dst, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("import: %v", err)
	}

	got, err := dst.GetCard(ctx, "card_mv1")
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := got.Fields.(map[string]any)["platforms"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "desktop" || arr[1] != "mobile" {
		t.Fatalf("platforms after round-trip = %#v, want [desktop mobile]", got.Fields)
	}
	plain, err := dst.GetCard(ctx, "card_mv2")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := plain.Fields.(map[string]any)["platforms"]; present {
		t.Fatal("unset multiple field appeared after round-trip — must stay absent")
	}
}
