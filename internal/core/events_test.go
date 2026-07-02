package core_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core"
)

var updateGolden = flag.Bool("update", false, "update golden event-contract fixtures")

// TestEventContracts_GoldenFixtures pins the wire JSON of every built-in event
// so a diff-shape change is a visible, reviewed diff (Events seam 1g). Events
// are constructed un-stamped (zero id/actor/at) for a deterministic shape.
// Regenerate after an intentional change: go test ./internal/core -run Golden -update
func TestEventContracts_GoldenFixtures(t *testing.T) {
	cases := []struct {
		name string
		ev   *core.Event
	}{
		{"card_created", core.CardCreated(&core.Card{ID: "c1", TypeID: "task", Title: "Do thing", Status: "todo"})},
		{"field_updated", core.FieldChanged("c1", "title", "old", "new")},
		{"status_changed", core.StatusChanged("c1", "todo", "in_progress")},
		{"owner_changed", core.OwnerChanged("c1", "", "alice")},
		{"tags_changed", core.TagsChanged("c1", []string{"bug"}, []string{"wip"})},
		{"item_appended", core.ItemAppended("c1", "work_log", "e1", map[string]any{"note": "did it"}, 0)},
		{"item_updated", core.ItemUpdated("c1", "work_log", "e1", map[string]any{"note": "old"}, map[string]any{"note": "new"})},
		{"item_removed", core.ItemRemoved("c1", "work_log", "e1", map[string]any{"note": "gone"})},
		{"link_added", core.LinkAdded("c1", "depends-on", "c2", "because")},
		{"link_removed", core.LinkRemoved("c1", "depends-on", "c2")},
		{"comment_added", core.CommentAdded("c1", "cm1")},
		{"comment_edited", core.CommentEdited("c1", "cm1", "before", "after")},
		{"schema_upgraded", core.SchemaUpgraded("c1", 1, 2, map[string]any{"newField": "x"}, []string{"oldField"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Every built-in event carries the contract version.
			if tc.ev.Version != 1 {
				t.Fatalf("event %s missing version=1 (got %d)", tc.name, tc.ev.Version)
			}
			got, err := json.MarshalIndent(tc.ev, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", "events", tc.name+".json")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (regenerate: go test ./internal/core -run Golden -update): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("event %q JSON drifted from its golden fixture:\n--- got ---\n%s\n--- want ---\n%s", tc.name, got, want)
			}
		})
	}
}

// TestNoRawEventLiterals guards the event contract (Events seam 1g): outside the
// constructors (events.go) and tests, events must be built via CardEvent /
// StatusChanged / … — never a raw Event{...} composite literal — so every diff
// shape stays typed and pinned by the golden fixtures. Slice literals that carry
// an already-constructed event ([]*Event{ev}) are fine.
func TestNoRawEventLiterals(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	lit := regexp.MustCompile(`Event\{`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "events.go" {
			continue // constructors + tests may build raw events
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, loc := range lit.FindAllIndex(src, -1) {
			if i := loc[0]; i > 0 && (src[i-1] == '*' || src[i-1] == ']') {
				continue // []*Event{ev} / []Event{ev} — a slice, not a literal
			}
			line := 1 + strings.Count(string(src[:loc[0]]), "\n")
			t.Errorf("%s:%d: raw Event{...} literal outside constructors/tests — build events via CardEvent/StatusChanged/…", f, line)
		}
	}
}
