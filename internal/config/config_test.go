package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadDemoWorkspace loads examples/demo-workspace and asserts the shape
// documented in the spec.
func TestLoadDemoWorkspace(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "demo-workspace")
	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if r.Workspace.ID != "demo" {
		t.Errorf("workspace id = %q, want demo", r.Workspace.ID)
	}
	if got := len(r.Workspace.Columns); got != 5 {
		t.Errorf("columns = %d, want 5", got)
	}
	if got := len(r.Workspace.TagSet); got != 3 {
		t.Errorf("tag_set = %v, want 3", got)
	}
	if got := len(r.Workspace.LinkTypes); got != 5 {
		t.Errorf("link_types = %d, want 5", got)
	}
	if got := len(r.CardTypes); got != 7 {
		t.Fatalf("card types = %d, want 7", got)
	}
	for _, id := range []string{"programming-task", "research-goal", "task",
		"api-task", "frontend-task", "infra-task", "data-task"} {
		if _, ok := r.CardTypes[id]; !ok {
			t.Errorf("missing %s type", id)
		}
	}
	// The granular dev-loop types carry their visual identity in config.
	if th := r.CardTypes["api-task"].TypeTheme; th.Icon != "target" || th.Accent == "" {
		t.Errorf("api-task type_theme not loaded: %+v", th)
	}
	if got := len(r.Boards); got != 2 {
		t.Fatalf("boards = %d, want 2", got)
	}
	if _, ok := r.Boards["welcome"]; !ok {
		t.Error("missing welcome board")
	}
	b := r.Boards["engineering"]
	if b == nil || !b.Settings.EnforceTransitions {
		t.Error("engineering board should enforce transitions")
	}
	if got := b.Transitions["todo"]; len(got) != 1 || got[0] != "in_progress" {
		t.Errorf("todo transitions = %v, want [in_progress]", got)
	}
	// default_user seed
	if r.Workspace.Settings.DefaultUser != "local-dev" {
		t.Errorf("default_user = %q, want local-dev", r.Workspace.Settings.DefaultUser)
	}
}

// TestRejectBoardUnknownColumn ensures a board referencing an unknown column
// fails at load.
func TestRejectBoardUnknownColumn(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "definitions", "workspace.json"), `{
		"id":"t","name":"T",
		"columns":[{"id":"a","name":"A"}],
		"settings":{"default_user":"u"}
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a","nope"]
	}`)
	if _, err := New(dir).Load(); err == nil {
		t.Fatal("expected error for unknown column, got nil")
	}
}

func TestRejectBoardBadLaneSort(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "definitions", "workspace.json"), `{
		"id":"t","name":"T",
		"columns":[{"id":"a","name":"A"}],
		"settings":{"default_user":"u"}
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"presentation":{"lane_sort":"owner"}
	}`)
	if _, err := New(dir).Load(); err == nil {
		t.Fatal("expected error for unsupported lane_sort key, got nil")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := writeFile(path, content); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newMinimalWorkspaceDir writes a workspace.json with the given settings JSON
// fragment plus an empty card-types dir, so Load() succeeds without also
// exercising card-type/board validation.
func newMinimalWorkspaceDir(t *testing.T, settingsJSON string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "definitions", "workspace.json"), `{
		"id":"t","name":"T",
		"columns":[{"id":"a","name":"A"}],
		"settings":`+settingsJSON+`
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", ".keep"), "")
	return dir
}

// EVENTS.md §12 Step 3 cross-cutting hardening: a persist_conditions typo
// warns instead of silently no-op'ing.
func TestPersistConditionsUnknownTypeWarns(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u","persist_conditions":["wip_exceded"]}`)
	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(r.Warnings) != 1 {
		t.Fatalf("warnings = %v, want 1 (typo'd persist_conditions entry)", r.Warnings)
	}
	if !strings.Contains(r.Warnings[0], "wip_exceded") {
		t.Errorf("warning %q does not mention the bad entry", r.Warnings[0])
	}
}

func TestPersistConditionsKnownTypeNoWarning(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u","persist_conditions":["wip_exceeded"]}`)
	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("unexpected warnings for a known condition type: %v", r.Warnings)
	}
}

// A board's monitors.alert_when_empty must reference real columns, matching
// the existing hard-fail convention for columns/transitions/card_type_ids.
func TestRejectMonitorsAlertWhenEmptyUnknownColumn(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],
		"monitors":{"alert_when_empty":["nope"]}
	}`)
	if _, err := New(dir).Load(); err == nil {
		t.Fatal("expected error for unknown alert_when_empty column, got nil")
	}
}

func TestAcceptMonitorsTemporalFields(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],
		"monitors":{"max_time_in_status":{"a":"7d"},"idle_after":"72h"}
	}`)
	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	b := r.Boards["b"]
	if b.Monitors == nil || b.Monitors.MaxTimeInStatus["a"] != "7d" || b.Monitors.IdleAfter != "72h" {
		t.Errorf("board monitors = %+v", b.Monitors)
	}
}

func TestRejectMonitorsMaxTimeInStatusUnknownColumn(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],
		"monitors":{"max_time_in_status":{"nope":"7d"}}
	}`)
	if _, err := New(dir).Load(); err == nil {
		t.Fatal("expected error for unknown max_time_in_status column, got nil")
	}
}

func TestRejectMonitorsBadDuration(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],
		"monitors":{"idle_after":"not-a-duration"}
	}`)
	if _, err := New(dir).Load(); err == nil {
		t.Fatal("expected error for unparseable idle_after, got nil")
	}
}

// TestYAMLCardTypeDefinitionIsNotLoaded pins the JSON-only contract for
// definitions (sprint 2026-07-06 Phase 3). The docs once claimed definitions
// accept JSON *or* YAML; the loader only ever reads .json (a .yaml card type
// is silently ignored). This decision is deliberate — no yaml dependency, and
// the structs are json-tagged, so a YAML parser would drop fields. Only
// definitions/extensions.{yaml,json} accepts YAML, and that is unaffected.
func TestYAMLCardTypeDefinitionIsNotLoaded(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "definitions", "workspace.json"), `{
		"id":"t","name":"T",
		"columns":[{"id":"a","name":"A"}],
		"settings":{"default_user":"u"}
	}`)
	// A valid JSON card type that loads.
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "json-type.json"), `{
		"id":"json-type","name":"JSON Type","fields":[]
	}`)
	// A YAML card type that MUST be ignored (not loaded, not an error).
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "yaml-type.yaml"),
		"id: yaml-type\nname: YAML Type\nfields: []\n")

	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := r.CardTypes["yaml-type"]; ok {
		t.Error("a .yaml card-type definition was loaded — definitions are JSON-only")
	}
	if _, ok := r.CardTypes["json-type"]; !ok {
		t.Error("the .json card-type definition should still load")
	}
	if len(r.CardTypes) != 1 {
		t.Errorf("card types = %d, want 1 (only the JSON one)", len(r.CardTypes))
	}
}

// --- P2b semantic validation ---

func TestRejectFieldMinGreaterThanMax(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "definitions", "workspace.json"), `{
		"id":"t","name":"T",
		"columns":[{"id":"a","name":"A"}],
		"settings":{"default_user":"u"}
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{"id":"n","type":"number","min":10,"max":1}]
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for min > max, got nil")
	}
	if !strings.Contains(err.Error(), "min") || !strings.Contains(err.Error(), "max") {
		t.Errorf("error %q should mention min/max", err)
	}
}

func TestRejectDateFieldMinGreaterThanMax(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "definitions", "workspace.json"), `{
		"id":"t","name":"T",
		"columns":[{"id":"a","name":"A"}],
		"settings":{"default_user":"u"}
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{"id":"due","type":"date","min":200,"max":100}]
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for date min > max, got nil")
	}
	if !strings.Contains(err.Error(), "min") {
		t.Errorf("error %q should mention min", err)
	}
}

func TestAcceptNumberMinEqualsMax(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{"id":"n","type":"number","min":5,"max":5}]
	}`)
	if _, err := New(dir).Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
}

func TestRejectUnknownIconAlias(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[],
		"type_theme":{"icon":"rocket"}
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for unknown icon, got nil")
	}
	if !strings.Contains(err.Error(), "rocket") || !strings.Contains(err.Error(), "type_theme.icon") {
		t.Errorf("error %q should name the bad icon and field", err)
	}
	if !strings.Contains(err.Error(), "wrench") {
		t.Errorf("error %q should list allowed aliases", err)
	}
}

func TestRejectLegacyUnknownIcon(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[],
		"icon":"emoji"
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for unknown legacy icon, got nil")
	}
	if !strings.Contains(err.Error(), "emoji") {
		t.Errorf("error %q should name the bad icon", err)
	}
}

func TestAcceptKnownIconAlias(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[],
		"type_theme":{"icon":"bug"}
	}`)
	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if r.CardTypes["task"].TypeTheme.Icon != "bug" {
		t.Errorf("icon = %q, want bug", r.CardTypes["task"].TypeTheme.Icon)
	}
}

func TestRejectOptionThemesUnknownKey(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{
			"id":"kind","type":"enum","options":["feature","bug"],
			"option_themes":{
				"feature":{"icon":"star","accent":"#005bd3","muted":"#d9e8ff"},
				"ghost":{"icon":"bug","accent":"#b00000","muted":"#ffd9d6"}
			}
		}]
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for option_themes key not in options")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q should name the bad key", err)
	}
}

func TestRejectOptionThemesSoftContrast(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{
			"id":"kind","type":"enum","options":["design"],
			"option_themes":{
				"design":{"icon":"pen","accent":"#cf7b00","muted":"#ffe8bf"}
			}
		}]
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for accent/muted below 4.5:1")
	}
	if !strings.Contains(err.Error(), "contrast") && !strings.Contains(err.Error(), "4.5") {
		t.Errorf("error %q should mention contrast floor", err)
	}
}

func TestRejectOptionThemesMissingIcon(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{
			"id":"kind","type":"enum","options":["feature"],
			"option_themes":{
				"feature":{"accent":"#005bd3","muted":"#d9e8ff"}
			}
		}]
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error when option theme omits icon")
	}
	if !strings.Contains(err.Error(), "icon") {
		t.Errorf("error %q should require icon", err)
	}
}

func TestRejectOptionThemesOnNonEnum(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{
			"id":"branch","type":"string",
			"option_themes":{"x":{"icon":"star","accent":"#005bd3","muted":"#d9e8ff"}}
		}]
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for option_themes on non-enum")
	}
	if !strings.Contains(err.Error(), "enum") {
		t.Errorf("error %q should mention enum", err)
	}
}

func TestAcceptOptionThemesAndStyleField(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{
			"id":"kind","type":"enum","options":["feature","bug"],
			"option_themes":{
				"feature":{"icon":"star","accent":"#005bd3","muted":"#d9e8ff"},
				"bug":{"icon":"bug","accent":"#b00000","muted":"#ffd9d6"}
			}
		}]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"presentation":{"style_field":"kind"}
	}`)
	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if r.Boards["b"].Presentation.StyleField != "kind" {
		t.Errorf("style_field = %q", r.Boards["b"].Presentation.StyleField)
	}
}

func TestRejectStyleFieldUnknown(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[{"id":"notes","type":"text"}]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"presentation":{"style_field":"kind"}
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for unknown style_field")
	}
	if !strings.Contains(err.Error(), "style_field") || !strings.Contains(err.Error(), "kind") {
		t.Errorf("error %q should name style_field and kind", err)
	}
}

func TestRejectStyleFieldNonEnum(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[{"id":"branch","type":"string"}]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"presentation":{"style_field":"branch"}
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for non-enum style_field")
	}
	if !strings.Contains(err.Error(), "enum") {
		t.Errorf("error %q should require enum", err)
	}
}

func TestRejectLaneSortUnknownField(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{"id":"priority","type":"enum","options":["low","high"]}]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"presentation":{"lane_sort":"-fields.nope"}
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for dangling lane_sort field, got nil")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error %q should name the missing field", err)
	}
}

func TestAcceptLaneSortKnownField(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{"id":"priority","type":"enum","options":["low","high"]}]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"presentation":{"lane_sort":"-fields.priority"}
	}`)
	if _, err := New(dir).Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
}

func TestRejectCardPreviewUnknownField(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task",
		"fields":[{"id":"branch","type":"string"}]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"presentation":{"card_preview":{"task":["missing"]}}
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for dangling card_preview field, got nil")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error %q should name the missing field", err)
	}
}

func TestRejectLaneGroupByUnknownField(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"presentation":{"lane_group_by":"priority"}
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for unknown lane_group_by, got nil")
	}
	if !strings.Contains(err.Error(), "priority") {
		t.Errorf("error %q should name the missing field", err)
	}
}

func TestRejectDetailSectionUnknownField(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[{"id":"branch","type":"string"}]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"presentation":{"detail_sections":[{"title":"Main","fields":["ghost"]}]}
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for dangling detail_sections field, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q should name the missing field", err)
	}
}

func TestRejectDefaultFilterUnknownField(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[{"id":"priority","type":"enum","options":["low"]}]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"default_filter":{"fields.nope":{"$eq":"x"}}
	}`)
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("expected error for dangling default_filter field, got nil")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error %q should name the missing field", err)
	}
}

func TestWarnUnrecognizedDefaultFilterKey(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"default_filter":{"mystery_key":{"$eq":"x"}}
	}`)
	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load should warn not fail: %v", err)
	}
	if len(r.Warnings) == 0 {
		t.Fatal("expected a warning for unrecognized filter key")
	}
	if !strings.Contains(r.Warnings[0], "mystery_key") {
		t.Errorf("warning %q should name the key", r.Warnings[0])
	}
}

func TestWarnOrphanCardPreviewType(t *testing.T) {
	dir := newMinimalWorkspaceDir(t, `{"default_user":"u"}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "task.json"), `{
		"id":"task","name":"Task","fields":[{"id":"branch","type":"string"}]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "card-types", "other.json"), `{
		"id":"other","name":"Other","fields":[{"id":"branch","type":"string"}]
	}`)
	mustWrite(t, filepath.Join(dir, "definitions", "boards", "b.json"), `{
		"id":"b","name":"B","columns":["a"],"card_type_ids":["task"],
		"presentation":{"card_preview":{"other":["branch"]}}
	}`)
	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load should warn not fail: %v", err)
	}
	found := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "other") && strings.Contains(w, "card_preview") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want orphan card_preview note", r.Warnings)
	}
}

func TestLoadDemoWorkspaceNoSemanticWarnings(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "demo-workspace")
	r, err := New(dir).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "hard-reject next sprint") ||
			strings.Contains(w, "unknown field") ||
			strings.Contains(w, "icon") {
			t.Errorf("demo-workspace should load clean; unexpected warning: %s", w)
		}
	}
}
