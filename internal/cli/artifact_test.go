package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/sqlite"
)

// `cards attach <id> <field> <file>` uploads the file as the artifact field's
// bytes (raw body) and prints the updated card.
func TestAttachCommand(t *testing.T) {
	ws := &core.Workspace{
		ID: "t", Name: "T",
		Columns:  []core.Column{{ID: "todo", Name: "Todo"}},
		Settings: core.WorkspaceSettings{DefaultUser: "u"},
	}
	types := map[string]*core.CardType{
		"task": {ID: "task", Name: "Task", SchemaVersion: 1,
			Fields: []core.FieldDef{
				{ID: "description", Type: core.FieldText, Required: true},
				{ID: "screenshot", Type: core.FieldArtifact},
			},
			AllowedColumns: []string{"todo"}},
	}
	boards := map[string]*core.Board{
		"eng": {ID: "eng", Name: "Eng", Columns: []string{"todo"}, CardTypeIDs: []string{"task"}},
	}
	st, err := sqlite.Open(":memory:", ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	_ = st.InsertUser(ctx, core.User{ID: "u", Kind: "human"})
	svc := core.NewService(ws, types, boards, st)
	am, _ := artifacts.New(t.TempDir())
	svc.SetArtifacts(am)
	c, err := svc.CreateCard(core.WithActor(ctx, "u"), core.CreateCardRequest{
		TypeID: "task", Title: "T", Status: "todo",
		Fields: map[string]any{"description": "d"}, Actor: "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpapi.New(svc, ws, types, boards, st)
	if err != nil {
		t.Fatal(err)
	}
	client := NewWithTransport(Config{As: "u"}, inprocTransport{h: srv.Router()})

	f := filepath.Join(t.TempDir(), "shot.png")
	payload := append([]byte("\x89PNG\r\n\x1a\n"), []byte("bytes")...)
	if err := os.WriteFile(f, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, client, "attach", c.ID, "screenshot", f)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if !strings.Contains(out, c.ID) {
		t.Errorf("attach output missing card id: %s", out)
	}

	updated, err := svc.GetCard(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := updated.Fields.(map[string]any)["screenshot"].(map[string]any); !ok {
		t.Errorf("screenshot field not set after attach: %#v", updated.Fields)
	}
}
