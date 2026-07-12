package core_test

import (
	"math"
	"testing"

	"github.com/somebox/cards/internal/core"
)

func TestContrastRatio_KnownPairs(t *testing.T) {
	// Black on white is 21:1.
	got, err := core.ContrastRatio("#000000", "#ffffff")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-21) > 0.01 {
		t.Errorf("black/white = %.3f, want 21", got)
	}

	// Demo option theme that must pass the floor.
	got, err = core.ContrastRatio("#005bd3", "#d9e8ff")
	if err != nil {
		t.Fatal(err)
	}
	if got < core.MinContrastRatio {
		t.Errorf("feature pair = %.2f, want >= %.1f", got, core.MinContrastRatio)
	}

	// Known-soft type theme (frontend-task) fails the floor — documents why
	// OptionThemes reject while legacy TypeTheme stays warn/out-of-scope (C).
	got, err = core.ContrastRatio("#cf7b00", "#f6e7cc")
	if err != nil {
		t.Fatal(err)
	}
	if got >= core.MinContrastRatio {
		t.Errorf("frontend-task pair = %.2f, expected below floor", got)
	}
}

func TestMeetsContrastFloor(t *testing.T) {
	if _, err := core.MeetsContrastFloor("#005bd3", "#d9e8ff"); err != nil {
		t.Fatalf("expected pass: %v", err)
	}
	if _, err := core.MeetsContrastFloor("#cf7b00", "#f6e7cc"); err == nil {
		t.Fatal("expected soft pair to fail floor")
	}
}

func TestParseHexColor_ShortForm(t *testing.T) {
	r, g, b, err := core.ParseHexColor("#00f")
	if err != nil {
		t.Fatal(err)
	}
	if r != 0 || g != 0 || b != 1 {
		t.Errorf("got %v %v %v, want 0 0 1", r, g, b)
	}
}
