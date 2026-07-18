package core_test

// Multi-value field contract (frontend-rebuild Phase 3, SPEC-DATA-MODEL
// "Multi-value fields"): a multiple enum/user field is ALWAYS a JSON array on
// the wire; unset means ABSENT — never null, never []. Writes of null/[]
// unset the key. Single-value behavior must be bit-for-bit unchanged.

import (
	"context"
	"testing"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

// newMultiService builds a service whose "task" type carries a multiple enum
// (platforms) and a multiple user (reviewers), plus the usual scalar fields.
func newMultiService(t *testing.T) (*core.Service, *sqlite.Store) {
	t.Helper()
	ws, types, boards := testConfig()
	types["task"].Fields = append(types["task"].Fields,
		core.FieldDef{ID: "platforms", Type: core.FieldEnum, Multiple: true, Options: []string{"desktop", "mobile", "tablet"}},
		core.FieldDef{ID: "reviewers", Type: core.FieldUser, Multiple: true},
		core.FieldDef{ID: "audience", Type: core.FieldEnum, Multiple: true, Required: true, Options: []string{"dev", "ops"}},
	)
	st := sqlitetest.Open(t, ws, 1)
	if err := st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatal(err)
	}
	svc := core.NewService(ws, types, boards, st)
	t.Cleanup(svc.Close)
	return svc, st
}

func mkMulti(t *testing.T, svc *core.Service, fields map[string]any) *core.Card {
	t.Helper()
	if fields == nil {
		fields = map[string]any{}
	}
	if _, ok := fields["description"]; !ok {
		fields["description"] = "d"
	}
	if _, ok := fields["audience"]; !ok {
		fields["audience"] = []any{"dev"}
	}
	c, err := svc.CreateCard(context.Background(), core.CreateCardRequest{
		TypeID: "task", Title: "m", Status: "todo", Actor: "u", Fields: fields,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return c
}

func TestMultiValueCreateStoresArray(t *testing.T) {
	svc, _ := newMultiService(t)
	c := mkMulti(t, svc, map[string]any{"platforms": []any{"desktop", "mobile"}})
	got, ok := c.Fields.(map[string]any)["platforms"].([]any)
	if !ok || len(got) != 2 || got[0] != "desktop" || got[1] != "mobile" {
		t.Fatalf("platforms = %#v, want [desktop mobile]", c.Fields.(map[string]any)["platforms"])
	}
}

func TestMultiValueUnsetContractOnCreate(t *testing.T) {
	svc, _ := newMultiService(t)
	// [] and nil both mean absent — the key must not exist in stored fields.
	for _, v := range []any{[]any{}, nil} {
		c := mkMulti(t, svc, map[string]any{"platforms": v})
		if _, present := c.Fields.(map[string]any)["platforms"]; present {
			t.Errorf("platforms present after create with %#v — unset must be ABSENT", v)
		}
	}
}

func TestMultiValueRequiredNeedsNonEmpty(t *testing.T) {
	svc, _ := newMultiService(t)
	_, err := svc.CreateCard(context.Background(), core.CreateCardRequest{
		TypeID: "task", Title: "m", Status: "todo", Actor: "u",
		Fields: map[string]any{"description": "d", "audience": []any{}},
	})
	if err == nil {
		t.Fatal("required multiple field accepted an empty array")
	}
	if ce := core.AsError(err); ce == nil || ce.Field != "audience" {
		t.Errorf("want structured required error on audience, got %v", err)
	}
}

func TestMultiValuePatchEmptyUnsetsKey(t *testing.T) {
	svc, _ := newMultiService(t)
	c := mkMulti(t, svc, map[string]any{"platforms": []any{"tablet"}})
	upd, err := svc.PatchCard(context.Background(), c.ID, core.PatchCardRequest{
		Version: c.Version, Actor: "u", Fields: map[string]any{"platforms": []any{}},
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if _, present := upd.Fields.(map[string]any)["platforms"]; present {
		t.Fatal("platforms still present after []-patch — must be unset (absent)")
	}
	// The change is a real mutation: version bumped and a field_updated fact.
	if upd.Version != c.Version+1 {
		t.Errorf("version = %d, want %d", upd.Version, c.Version+1)
	}
	evs, err := svc.ListEvents(context.Background(), core.EventQuery{CardID: c.ID, Types: []string{"field_updated"}, Limit: 10})
	if err != nil || len(evs) == 0 {
		t.Fatalf("no field_updated event after unset: %v", err)
	}
}

func TestMultiValueScalarRejected(t *testing.T) {
	svc, _ := newMultiService(t)
	_, err := svc.CreateCard(context.Background(), core.CreateCardRequest{
		TypeID: "task", Title: "m", Status: "todo", Actor: "u",
		Fields: map[string]any{"description": "d", "audience": []any{"dev"}, "platforms": "desktop"},
	})
	if err == nil {
		t.Fatal("scalar accepted for a multiple field")
	}
}

func TestMultiValueBadElementStructuredError(t *testing.T) {
	svc, _ := newMultiService(t)
	_, err := svc.CreateCard(context.Background(), core.CreateCardRequest{
		TypeID: "task", Title: "m", Status: "todo", Actor: "u",
		Fields: map[string]any{"description": "d", "audience": []any{"dev"}, "platforms": []any{"desktop", "watch"}},
	})
	ce := core.AsError(err)
	if ce == nil || ce.Code != "unknown_enum" || ce.Field != "platforms" {
		t.Fatalf("want unknown_enum on platforms, got %v", err)
	}
	if len(ce.ValidOptions) != 3 {
		t.Errorf("valid_options = %v, want the 3 options", ce.ValidOptions)
	}
}

func TestMultiValueDuplicatesRejected(t *testing.T) {
	svc, _ := newMultiService(t)
	_, err := svc.CreateCard(context.Background(), core.CreateCardRequest{
		TypeID: "task", Title: "m", Status: "todo", Actor: "u",
		Fields: map[string]any{"description": "d", "audience": []any{"dev"}, "platforms": []any{"desktop", "desktop"}},
	})
	if err == nil {
		t.Fatal("duplicate values accepted (must be rejected loudly, not deduped)")
	}
}

func TestMultiValueUserArray(t *testing.T) {
	svc, _ := newMultiService(t)
	c := mkMulti(t, svc, map[string]any{"reviewers": []any{"u", "someone-else"}})
	got, ok := c.Fields.(map[string]any)["reviewers"].([]any)
	if !ok || len(got) != 2 {
		t.Fatalf("reviewers = %#v", c.Fields.(map[string]any)["reviewers"])
	}
}

// TestSingleValueEnumUnchanged pins the regression contract: non-multiple
// enum/user behave exactly as before this feature.
func TestSingleValueEnumUnchanged(t *testing.T) {
	svc, _ := newMultiService(t)
	// scalar accepted
	c := mkMulti(t, svc, map[string]any{"priority": "low"})
	if c.Fields.(map[string]any)["priority"] != "low" {
		t.Fatal("scalar enum broken")
	}
	// array rejected for single-value enum
	_, err := svc.CreateCard(context.Background(), core.CreateCardRequest{
		TypeID: "task", Title: "m", Status: "todo", Actor: "u",
		Fields: map[string]any{"description": "d", "audience": []any{"dev"}, "priority": []any{"low"}},
	})
	if err == nil {
		t.Fatal("array accepted for a single-value enum")
	}
}
