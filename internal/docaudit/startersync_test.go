package docaudit

// Starter-vs-demo definition sync (sprint 2026-07-06 Phase 2): for every card
// type that exists in BOTH internal/starter/assets (what `cards init` ships)
// and examples/demo-workspace (what the docs/demo show), the `fields` array
// must be identical — otherwise a fresh init and the demo board silently
// fork (e.g. one gains an artifact field the other lacks, and `cards attach`
// works out of the box on one but not the other). Workspace-specific keys
// (allowed_columns) are deliberately NOT compared.
//
// Runs as an ordinary Go test (docaudit pattern): `go test ./...` is the one
// command, locally and in CI, no extra workflow.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func loadCardTypeFields(t *testing.T, path string) (id string, fields []any) {
	t.Helper()
	raw, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var d struct {
		ID     string `json:"id"`
		Fields []any  `json:"fields"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return d.ID, d.Fields
}

func TestStarterAndDemoCardTypeFieldsStayInSync(t *testing.T) {
	starterDir := "internal/starter/assets/definitions/card-types"
	demoDir := "examples/demo-workspace/definitions/card-types"

	starterFiles, err := filepath.Glob("../../" + starterDir + "/*.json")
	if err != nil || len(starterFiles) == 0 {
		t.Fatalf("no starter card types found: %v", err)
	}

	// Index demo card types by type id.
	demoFiles, _ := filepath.Glob("../../" + demoDir + "/*.json")
	demoFields := map[string][]any{}
	for _, f := range demoFiles {
		id, fields := loadCardTypeFields(t, demoDir+"/"+filepath.Base(f))
		demoFields[id] = fields
	}

	for _, f := range starterFiles {
		id, sf := loadCardTypeFields(t, starterDir+"/"+filepath.Base(f))
		df, shared := demoFields[id]
		if !shared {
			continue // demo doesn't ship this type; nothing to sync
		}
		if !reflect.DeepEqual(sf, df) {
			sj, _ := json.MarshalIndent(sf, "", "  ")
			dj, _ := json.MarshalIndent(df, "", "  ")
			t.Errorf("card type %q fields diverge between the starter assets and the demo workspace.\n"+
				"starter (%s):\n%s\ndemo (%s):\n%s\n"+
				"Update BOTH in the same change — a fresh `cards init` and the demo board must agree.",
				id, starterDir, sj, demoDir, dj)
		}
	}
}
