package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

func insertFiltered(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	cards := []*core.Card{
		{ID: "card_aaa", Title: "A", Status: "todo", Owner: "alice", Tags: []string{"urgent", "ui"},
			Fields: map[string]any{"points": float64(3)}},
		{ID: "card_bbb", Title: "B", Status: "done", Owner: "bob", Tags: []string{"ui"},
			Fields: map[string]any{"points": float64(8)}},
		{ID: "card_ccc", Title: "C", Status: "todo", Owner: "",
			Fields: map[string]any{}},
	}
	for _, c := range cards {
		c.WorkspaceID, c.TypeID, c.SchemaVersion, c.Version, c.CreatedBy = "t", "task", 1, 1, "u"
		c.CreatedAt, c.UpdatedAt = time.Now().UTC(), time.Now().UTC()
		if err := st.InsertCard(ctx, c, nil); err != nil {
			t.Fatalf("insert %s: %v", c.ID, err)
		}
	}
}

func listIDs(t *testing.T, st *Store, filter map[string]any) []string {
	t.Helper()
	page, err := st.ListCards(context.Background(), core.CardQuery{Filter: filter, Limit: 10})
	if err != nil {
		t.Fatalf("list with filter %v: %v", filter, err)
	}
	ids := make([]string, len(page.Items))
	for i, c := range page.Items {
		ids[i] = c.ID
	}
	return ids
}

func TestFilterDSLThroughListCards(t *testing.T) {
	st, _ := testStore(t)
	insertFiltered(t, st)

	cases := []struct {
		name   string
		filter map[string]any
		want   int
	}{
		{"eq status", map[string]any{"status": map[string]any{"$eq": "todo"}}, 2},
		{"ne status", map[string]any{"status": map[string]any{"$ne": "todo"}}, 1},
		{"in owner", map[string]any{"owner": map[string]any{"$in": []any{"alice", "bob"}}}, 2},
		{"empty in matches nothing", map[string]any{"owner": map[string]any{"$in": []any{}}}, 0},
		{"tag contains", map[string]any{"tags": map[string]any{"$contains": "urgent"}}, 1},
		{"tag nin", map[string]any{"tags": map[string]any{"$nin": []any{"urgent"}}}, 2},
		{"field gt", map[string]any{"fields.points": map[string]any{"$gt": float64(5)}}, 1},
		{"bare field path", map[string]any{"points": map[string]any{"$lte": float64(3)}}, 1},
		{"or", map[string]any{"$or": []any{
			map[string]any{"owner": map[string]any{"$eq": "alice"}},
			map[string]any{"status": map[string]any{"$eq": "done"}},
		}}, 2},
		{"and of field+tag", map[string]any{"$and": []any{
			map[string]any{"status": map[string]any{"$eq": "todo"}},
			map[string]any{"tags": map[string]any{"$eq": "ui"}},
		}}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := listIDs(t, st, tc.filter); len(got) != tc.want {
				t.Errorf("filter %v matched %v, want %d cards", tc.filter, got, tc.want)
			}
		})
	}
}

// Malformed DSL must come back as a core validation error (client error), not
// a bare SQL error — httpapi maps it to 422 via core.AsError.
func TestFilterDSLMalformedIsValidationError(t *testing.T) {
	st, _ := testStore(t)
	insertFiltered(t, st)

	bad := []map[string]any{
		{"status": "not-an-operator-object"},
		{"status": map[string]any{"$regex": "x"}},
		{"$and": "not-an-array"},
		{"$or": []any{"not-an-object"}},
		{"tags": map[string]any{"$gt": "x"}},
	}
	for _, f := range bad {
		_, err := st.ListCards(context.Background(), core.CardQuery{Filter: f, Limit: 10})
		if err == nil {
			t.Errorf("filter %v: expected error", f)
			continue
		}
		ce := core.AsError(err)
		if ce == nil || ce.Code != "validation_failed" || ce.Field != "filter" {
			t.Errorf("filter %v: got %v, want validation_failed on field filter", f, err)
		}
	}

	// ClaimAtomic shares buildCardWhere and must reject the same way.
	_, _, err := st.ClaimAtomic(context.Background(), core.CardQuery{Filter: bad[0], Unowned: true, Limit: 1}, "alice", "", "alice", time.Now().UTC())
	if ce := core.AsError(err); ce == nil || ce.Code != "validation_failed" {
		t.Errorf("ClaimAtomic with bad filter: got %v, want validation error", err)
	}
}
