package httpapi

import (
	"testing"

	"github.com/somebox/cards/internal/core"
)

func TestResolveCardTheme_OptionOverType(t *testing.T) {
	ct := &core.CardType{
		ID: "task", Name: "Task", SchemaVersion: 1,
		TypeTheme: core.TypeTheme{Icon: "code", Accent: "#0f5d78", Muted: "#d9e6ec"},
		Fields: []core.FieldDef{{
			ID: "kind", Type: core.FieldEnum,
			Options: []string{"feature", "bug"},
			OptionThemes: map[string]core.TypeTheme{
				"feature": {Icon: "star", Accent: "#005bd3", Muted: "#d9e8ff"},
			},
		}},
	}
	s := &Server{types: map[string]*core.CardType{"task": ct}}
	board := &core.Board{
		ID: "b", Presentation: &core.BoardPresentation{StyleField: "kind"},
	}
	withKind := &core.Card{
		ID: "c1", TypeID: "task",
		Fields: map[string]any{"kind": "feature"},
	}
	noKind := &core.Card{
		ID: "c2", TypeID: "task",
		Fields: map[string]any{},
	}

	th := s.resolveCardTheme(withKind, board)
	if th.Icon != "star" || th.Accent != "#005bd3" || th.Muted != "#d9e8ff" {
		t.Fatalf("option theme = %+v, want star/#005bd3/#d9e8ff", th)
	}

	th = s.resolveCardTheme(noKind, board)
	if th.Icon != "code" || th.Accent != "#0f5d78" || th.Muted != "#d9e6ec" {
		t.Fatalf("type fallback = %+v, want code/#0f5d78/#d9e6ec", th)
	}

	th = s.resolveCardTheme(withKind, &core.Board{ID: "plain"})
	if th.Icon != "code" || th.Accent != "#0f5d78" {
		t.Fatalf("without style_field = %+v, want type theme", th)
	}

	// Unthemed option value falls through to type theme.
	other := &core.Card{
		ID: "c3", TypeID: "task",
		Fields: map[string]any{"kind": "bug"},
	}
	th = s.resolveCardTheme(other, board)
	if th.Icon != "code" || th.Accent != "#0f5d78" {
		t.Fatalf("unthemed option = %+v, want type theme fallthrough", th)
	}
}

func TestCardView_EmitsOptionThemeHooks(t *testing.T) {
	ct := &core.CardType{
		ID: "task", Name: "Task", SchemaVersion: 1,
		TypeTheme: core.TypeTheme{Icon: "code", Accent: "#0f5d78", Muted: "#d9e6ec"},
		Fields: []core.FieldDef{{
			ID: "kind", Type: core.FieldEnum,
			Options: []string{"feature"},
			OptionThemes: map[string]core.TypeTheme{
				"feature": {Icon: "star", Accent: "#005bd3", Muted: "#d9e8ff"},
			},
		}},
	}
	s := &Server{
		types: map[string]*core.CardType{"task": ct},
		ws: &core.Workspace{
			Columns: []core.Column{{ID: "todo", Name: "Todo"}},
		},
	}
	board := &core.Board{
		ID: "b", Presentation: &core.BoardPresentation{StyleField: "kind"},
	}
	c := &core.Card{
		ID: "c1", TypeID: "task", Title: "x", Status: "todo",
		Fields: map[string]any{"kind": "feature"},
	}
	cv := s.cardView(c, board, nil)
	if cv.TypeIcon != "star" || cv.TypeAccent != "#005bd3" || cv.TypeMuted != "#d9e8ff" {
		t.Fatalf("CardView theme = icon=%q accent=%q muted=%q", cv.TypeIcon, cv.TypeAccent, cv.TypeMuted)
	}
}
