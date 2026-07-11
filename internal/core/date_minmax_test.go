package core_test

import (
	"strings"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

// TestCreateCard_DateMinMax enforces FieldDate Min/Max as Unix seconds
// (same *float64 slots as FieldNumber).
func TestCreateCard_DateMinMax(t *testing.T) {
	min := float64(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Unix())
	max := float64(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Unix())
	ws, types, boards := testConfig()
	types["task"].Fields = append(types["task"].Fields, core.FieldDef{
		ID: "due", Type: core.FieldDate, Min: &min, Max: &max,
	})
	svc, _ := newTestServiceWith(t, ws, types, boards)

	_, err := svc.CreateCard(ctx2(), core.CreateCardRequest{
		TypeID: "task", Title: "Too early", Status: "todo",
		Fields: map[string]any{"description": "go", "due": "2019-06-01"},
		Actor:  "u",
	})
	ce := core.AsError(err)
	if ce == nil || ce.Code != "validation_failed" {
		t.Fatalf("expected validation_failed for date below min, got %v", err)
	}
	if ce.Field != "due" {
		t.Errorf("field = %q, want due", ce.Field)
	}

	_, err = svc.CreateCard(ctx2(), core.CreateCardRequest{
		TypeID: "task", Title: "Too late", Status: "todo",
		Fields: map[string]any{"description": "go", "due": "2031-06-01"},
		Actor:  "u",
	})
	ce = core.AsError(err)
	if ce == nil || ce.Code != "validation_failed" {
		t.Fatalf("expected validation_failed for date above max, got %v", err)
	}

	c, err := svc.CreateCard(ctx2(), core.CreateCardRequest{
		TypeID: "task", Title: "In range", Status: "todo",
		Fields: map[string]any{"description": "go", "due": "2025-06-15"},
		Actor:  "u",
	})
	if err != nil {
		t.Fatalf("in-range date: %v", err)
	}
	fields, _ := c.Fields.(map[string]any)
	if fields["due"] != "2025-06-15" {
		t.Errorf("due = %v", fields["due"])
	}
}

func TestIsKnownIconAlias(t *testing.T) {
	if !core.IsKnownIconAlias("bug") {
		t.Error("bug should be known")
	}
	if core.IsKnownIconAlias("rocket") {
		t.Error("rocket should not be known")
	}
	if core.IsKnownIconAlias("") {
		t.Error("empty should not be known")
	}
	list := core.KnownIconAliasList()
	if !strings.Contains(list, "card") || !strings.Contains(list, "wrench") {
		t.Errorf("KnownIconAliasList = %q", list)
	}
}
