package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/themecss"
)

// themeContractVersion is the theme-contract version this build implements. A
// manifest declaring a different version still loads, but warns (THEMES.md).
const themeContractVersion = 1

// themeManifest is the optional <name>.json sidecar for a theme CSS file.
type themeManifest struct {
	Name        string `json:"name"`
	Contract    int    `json:"contract"`
	Fonts       string `json:"fonts"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// loadThemes reads definitions/themes/<name>.css (+ optional <name>.json) into
// validated core.Theme values. A theme whose CSS violates the contract
// (themecss.Validate) is SKIPPED with a warning naming file/line/rule — never
// fatal — so one broken theme degrades to "absent," never to a failed load
// (THEMES.md guarantee 3). The workspace keeps serving; the caller surfaces the
// warnings. Missing themes/ dir is not an error.
func loadThemes(workspaceDir string) (map[string]*core.Theme, []string, error) {
	dir := filepath.Join(workspaceDir, "definitions", "themes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return map[string]*core.Theme{}, nil, nil // no themes dir → none
	}
	themes := map[string]*core.Theme{}
	var warnings []string
	for _, e := range entries {
		if e.IsDir() || !hasExt(e.Name(), ".css") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".css")
		if name == "" {
			continue
		}
		cssPath := filepath.Join(dir, e.Name())
		css, err := os.ReadFile(cssPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read theme %s: %w", e.Name(), err)
		}
		rel := filepath.Join("definitions", "themes", e.Name())
		if vs := themecss.Validate(name, rel, string(css)); len(vs) > 0 {
			for _, v := range vs {
				warnings = append(warnings, "theme rejected: "+v.String())
			}
			continue // skip the bad theme; keep serving everything else
		}
		th := &core.Theme{Name: name, CSS: string(css), Contract: themeContractVersion}
		if man, w, err := readThemeManifest(dir, name); err != nil {
			return nil, nil, err
		} else {
			if man != nil {
				th.Fonts, th.Description, th.Source = man.Fonts, man.Description, man.Source
				if man.Contract != 0 {
					th.Contract = man.Contract
				}
			}
			warnings = append(warnings, w...)
		}
		themes[name] = th
	}
	return themes, warnings, nil
}

// readThemeManifest loads the optional <name>.json sidecar. Absent is fine
// (nil, nil). A contract mismatch warns but does not fail.
func readThemeManifest(dir, name string) (*themeManifest, []string, error) {
	path := filepath.Join(dir, name+".json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read theme manifest %s.json: %w", name, err)
	}
	var man themeManifest
	if err := json.Unmarshal(data, &man); err != nil {
		return nil, nil, fmt.Errorf("parse theme manifest %s.json: %w", name, err)
	}
	var warnings []string
	if man.Contract != 0 && man.Contract != themeContractVersion {
		warnings = append(warnings, fmt.Sprintf(
			"theme %q: manifest contract %d != this build's %d — loading anyway; behavior may differ",
			name, man.Contract, themeContractVersion))
	}
	return &man, warnings, nil
}
