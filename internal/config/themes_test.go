package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core"
)

// writeTheme drops a theme css (and optional json) into <dir>/definitions/themes.
func writeThemeFiles(t *testing.T, dir, name, css, json string) {
	t.Helper()
	td := filepath.Join(dir, "definitions", "themes")
	if err := os.MkdirAll(td, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, name+".css"), []byte(css), 0o644); err != nil {
		t.Fatal(err)
	}
	if json != "" {
		if err := os.WriteFile(filepath.Join(td, name+".json"), []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadThemesValidWithManifest(t *testing.T) {
	dir := t.TempDir()
	writeThemeFiles(t, dir, "sunset",
		`html[data-theme="sunset"] { --c-accent: #e0662c; }
html[data-theme="sunset"] .card { border-radius: 8px; }`,
		`{"name":"Sunset","fonts":"https://fonts.example/sunset.css","description":"warm dusk","source":"https://github.com/x/sunset","contract":1}`)

	themes, warnings, err := loadThemes(dir)
	if err != nil {
		t.Fatalf("loadThemes: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	th, ok := themes["sunset"]
	if !ok {
		t.Fatalf("sunset theme not loaded; got %v", keys(themes))
	}
	if th.Fonts == "" || th.Description != "warm dusk" || th.Source == "" || th.Contract != 1 {
		t.Errorf("manifest not applied: %+v", th)
	}
	if !strings.Contains(th.CSS, "--c-accent") {
		t.Errorf("theme CSS not captured")
	}
}

func TestLoadThemesRejectsUnscopedButKeepsGood(t *testing.T) {
	dir := t.TempDir()
	// good theme
	writeThemeFiles(t, dir, "good", `html[data-theme="good"] .card { color: red }`, "")
	// bad theme: an unscoped rule leaks into every theme — must be skipped
	writeThemeFiles(t, dir, "bad", `html[data-theme="bad"] {} body { color: red }`, "")

	themes, warnings, err := loadThemes(dir)
	if err != nil {
		t.Fatalf("loadThemes: %v", err)
	}
	if _, ok := themes["good"]; !ok {
		t.Errorf("good theme should still load; got %v", keys(themes))
	}
	if _, ok := themes["bad"]; ok {
		t.Errorf("bad theme must be skipped, not loaded")
	}
	if len(warnings) == 0 || !containsSub(warnings, "theme rejected") {
		t.Errorf("expected a rejection warning naming the theme; got %v", warnings)
	}
}

func TestLoadThemesNoDir(t *testing.T) {
	themes, warnings, err := loadThemes(t.TempDir()) // no definitions/themes
	if err != nil {
		t.Fatalf("loadThemes: %v", err)
	}
	if len(themes) != 0 || len(warnings) != 0 {
		t.Errorf("expected no themes/warnings for missing dir; got %v / %v", themes, warnings)
	}
}

func TestLoadThemesContractMismatchWarns(t *testing.T) {
	dir := t.TempDir()
	writeThemeFiles(t, dir, "future",
		`html[data-theme="future"] .card { color: red }`,
		`{"contract":99}`)
	themes, warnings, err := loadThemes(dir)
	if err != nil {
		t.Fatalf("loadThemes: %v", err)
	}
	if _, ok := themes["future"]; !ok {
		t.Errorf("theme with mismatched contract should still load (with a warning)")
	}
	if !containsSub(warnings, "contract") {
		t.Errorf("expected a contract-mismatch warning; got %v", warnings)
	}
}

func keys(m map[string]*core.Theme) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func containsSub(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
