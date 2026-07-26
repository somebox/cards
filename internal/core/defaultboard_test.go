package core_test

import (
	"testing"

	"github.com/somebox/cards/internal/core"
)

func boardSet(ids ...string) map[string]*core.Board {
	m := map[string]*core.Board{}
	for _, id := range ids {
		m[id] = &core.Board{ID: id, Name: id}
	}
	return m
}

// TestDefaultBoardIDPrefersSetting — the whole point of the setting: in a
// multi-board workspace the declared board wins over whichever id sorts first.
func TestDefaultBoardIDPrefersSetting(t *testing.T) {
	ws := &core.Workspace{Settings: core.WorkspaceSettings{DefaultBoard: "zeta"}}
	if got := core.DefaultBoardID(ws, boardSet("alpha", "zeta")); got != "zeta" {
		t.Errorf("DefaultBoardID = %q, want zeta (the declared default, not the first alphabetically)", got)
	}
}

// TestDefaultBoardIDFallsBackAlphabetically preserves the behavior every
// surface had before the setting existed.
func TestDefaultBoardIDFallsBackAlphabetically(t *testing.T) {
	ws := &core.Workspace{}
	if got := core.DefaultBoardID(ws, boardSet("zeta", "alpha")); got != "alpha" {
		t.Errorf("DefaultBoardID = %q, want alpha", got)
	}
}

// TestDefaultBoardIDIgnoresDanglingSetting — config load rejects a dangling
// default_board, so this guards the in-process-literal path: never hand a
// caller a board id that resolves to nothing.
func TestDefaultBoardIDIgnoresDanglingSetting(t *testing.T) {
	ws := &core.Workspace{Settings: core.WorkspaceSettings{DefaultBoard: "ghost"}}
	if got := core.DefaultBoardID(ws, boardSet("zeta", "alpha")); got != "alpha" {
		t.Errorf("DefaultBoardID = %q, want the alphabetical fallback", got)
	}
}

func TestDefaultBoardIDEmptyWorkspace(t *testing.T) {
	if got := core.DefaultBoardID(&core.Workspace{}, boardSet()); got != "" {
		t.Errorf("DefaultBoardID = %q, want empty for a workspace with no boards", got)
	}
}

// TestTakeNextUsesDefaultBoard — a worker that calls take-next with no board
// draws from the workspace's primary board. The card sitting outside that
// board must not be handed out.
func TestTakeNextUsesDefaultBoard(t *testing.T) {
	ws, types, boards := testConfig()
	ws.Settings.DefaultBoard = "eng"
	// A second board scoped to a type the eng board does not show.
	types["other"] = &core.CardType{
		ID: "other", Name: "Other", SchemaVersion: 1,
		Fields:         []core.FieldDef{},
		AllowedColumns: []string{"todo"},
	}
	boards["side"] = &core.Board{
		ID: "side", Name: "Side", Columns: []string{"todo"}, CardTypeIDs: []string{"other"},
	}
	svc, _ := newTestServiceWith(t, ws, types, boards)
	ctx := tagPolicyCtx()

	offBoard, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "other", Title: "off the default board", Actor: "u",
	})
	if err != nil {
		t.Fatalf("create off-board card: %v", err)
	}
	onBoard, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "task", Title: "on the default board", Actor: "u",
		Fields: map[string]any{"description": "d"},
	})
	if err != nil {
		t.Fatalf("create on-board card: %v", err)
	}

	got, err := svc.TakeNext(ctx, core.TakeNextRequest{Actor: "u"})
	if err != nil {
		t.Fatalf("take-next: %v", err)
	}
	if got == nil {
		t.Fatal("take-next returned no card; the default board had an unowned card")
	}
	if got.ID == offBoard.ID {
		t.Fatal("take-next handed out a card outside settings.default_board")
	}
	if got.ID != onBoard.ID {
		t.Fatalf("take-next returned %s, want the default board's card %s", got.ID, onBoard.ID)
	}
}

// TestTakeNextExplicitTypeIgnoresDefaultBoard — an explicit type_id is its own
// scope. Silently AND-ing the default board onto it would narrow a pool the
// caller believed they had chosen.
func TestTakeNextExplicitTypeIgnoresDefaultBoard(t *testing.T) {
	ws, types, boards := testConfig()
	ws.Settings.DefaultBoard = "eng"
	types["other"] = &core.CardType{
		ID: "other", Name: "Other", SchemaVersion: 1,
		Fields: []core.FieldDef{}, AllowedColumns: []string{"todo"},
	}
	boards["side"] = &core.Board{
		ID: "side", Name: "Side", Columns: []string{"todo"}, CardTypeIDs: []string{"other"},
	}
	svc, _ := newTestServiceWith(t, ws, types, boards)
	ctx := tagPolicyCtx()

	want, err := svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: "other", Title: "explicitly requested type", Actor: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.TakeNext(ctx, core.TakeNextRequest{TypeID: "other", Actor: "u"})
	if err != nil {
		t.Fatalf("take-next: %v", err)
	}
	if got == nil || got.ID != want.ID {
		t.Fatalf("explicit type_id must not be scoped by default_board; got %v", got)
	}
}
