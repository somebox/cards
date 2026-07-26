package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

// searchStore opens a store and inserts one card carrying both a field the
// type will declare searchable and one it will not.
func searchStore(t *testing.T) *sqlite.Store {
	t.Helper()
	ws := &core.Workspace{
		ID: "t", Name: "T",
		Columns:  []core.Column{{ID: "todo", Name: "Todo"}},
		Settings: core.WorkspaceSettings{StrictFields: true, TagPolicy: core.TagPolicyLocked, DefaultUser: "u"},
	}
	return sqlitetest.Open(t, ws, 1)
}

func insertSearchCard(t *testing.T, st *sqlite.Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	c := &core.Card{
		ID: id, WorkspaceID: "t", TypeID: "task", SchemaVersion: 1,
		Title: "a card", Status: "todo", Version: 1,
		CreatedAt: now, UpdatedAt: now, CreatedBy: "u",
		Fields: map[string]any{
			"description": "findmeinthedescription",
			"secret_note": "findmeinthesecret",
		},
	}
	if err := st.InsertCard(context.Background(), c, nil); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func searchHits(t *testing.T, st *sqlite.Store, q string) int {
	t.Helper()
	page, err := st.ListCards(context.Background(), core.CardQuery{Q: q})
	if err != nil {
		t.Fatalf("search %q: %v", q, err)
	}
	return len(page.Items)
}

// TestUndeclaredSearchableFieldsIndexesEverything — the backward-compatible
// reading. A type that declares nothing places no restriction; every field
// value stays searchable, as it was before the setting was honored.
func TestUndeclaredSearchableFieldsIndexesEverything(t *testing.T) {
	st := searchStore(t)
	insertSearchCard(t, st, "card_a")
	for _, q := range []string{"findmeinthedescription", "findmeinthesecret"} {
		if n := searchHits(t, st, q); n != 1 {
			t.Errorf("q=%q hits = %d, want 1 (no declaration = index everything)", q, n)
		}
	}
}

// TestDeclaredSearchableFieldsExcludeOthers is the card's headline: a field
// left out of searchable_fields must not match.
func TestDeclaredSearchableFieldsExcludeOthers(t *testing.T) {
	st := searchStore(t)
	if err := st.SetSearchableFields(map[string][]string{"task": {"description"}}); err != nil {
		t.Fatalf("set searchable: %v", err)
	}
	insertSearchCard(t, st, "card_a")

	if n := searchHits(t, st, "findmeinthedescription"); n != 1 {
		t.Errorf("declared field must still match, hits = %d, want 1", n)
	}
	if n := searchHits(t, st, "findmeinthesecret"); n != 0 {
		t.Errorf("undeclared field must NOT match, hits = %d, want 0", n)
	}
}

// TestTitleAlwaysSearchable — the restriction is over field values; the title
// is indexed regardless, as the card specified.
func TestTitleAlwaysSearchable(t *testing.T) {
	st := searchStore(t)
	if err := st.SetSearchableFields(map[string][]string{"task": {"description"}}); err != nil {
		t.Fatalf("set searchable: %v", err)
	}
	insertSearchCard(t, st, "card_a")
	if n := searchHits(t, st, "card"); n != 1 {
		t.Errorf("title must stay searchable, hits = %d, want 1", n)
	}
}

// TestSearchableChangeRebuildsExistingRows — rows written under the old rule
// are re-indexed when the declaration changes. Without the rebuild, a field
// the workspace has since excluded keeps matching forever on existing cards,
// which is exactly the migration hazard this card called out.
func TestSearchableChangeRebuildsExistingRows(t *testing.T) {
	st := searchStore(t)
	insertSearchCard(t, st, "card_a") // indexed with NO restriction
	if n := searchHits(t, st, "findmeinthesecret"); n != 1 {
		t.Fatalf("precondition: unrestricted index should match, hits = %d", n)
	}

	// Now restrict — the already-written row must stop matching.
	if err := st.SetSearchableFields(map[string][]string{"task": {"description"}}); err != nil {
		t.Fatalf("set searchable: %v", err)
	}
	if n := searchHits(t, st, "findmeinthesecret"); n != 0 {
		t.Errorf("existing row was not re-indexed: hits = %d, want 0", n)
	}
	if n := searchHits(t, st, "findmeinthedescription"); n != 1 {
		t.Errorf("declared field lost after rebuild: hits = %d, want 1", n)
	}

	// And relaxing it brings the field back.
	if err := st.SetSearchableFields(nil); err != nil {
		t.Fatalf("relax searchable: %v", err)
	}
	if n := searchHits(t, st, "findmeinthesecret"); n != 1 {
		t.Errorf("relaxing the declaration did not re-index: hits = %d, want 1", n)
	}
}

// TestSearchableDigestSkipsRedundantRebuild — re-applying the same
// declaration is a no-op, so ordinary startups and reloads don't pay for a
// full re-index.
func TestSearchableDigestSkipsRedundantRebuild(t *testing.T) {
	st := searchStore(t)
	decl := map[string][]string{"task": {"description"}}
	if err := st.SetSearchableFields(decl); err != nil {
		t.Fatalf("set: %v", err)
	}
	insertSearchCard(t, st, "card_a")

	// A second identical apply must not disturb the index.
	if err := st.SetSearchableFields(map[string][]string{"task": {"description"}}); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	if n := searchHits(t, st, "findmeinthedescription"); n != 1 {
		t.Errorf("redundant apply disturbed the index: hits = %d, want 1", n)
	}
}
