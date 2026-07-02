// Package config loads and validates workspace definitions from JSON files
// in definitions/ (only definitions/extensions.{yaml,json} accepts YAML).
// There is no reload-on-change: restart the process to pick up edits.
//
// See docs/ARCHITECTURE.md (Config Contexts) and docs/DEVELOPER-REFERENCE.md.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/somebox/cards/internal/core"
)

// Loader reads definitions from one workspace directory and produces a
// normalized workspace + card types + boards. POC subset: JSON only, single
// context, no file watching, no merge.
type Loader struct {
	workspaceDir string
}

// New returns a Loader bound to the given workspace directory.
func New(workspaceDir string) *Loader {
	return &Loader{workspaceDir: workspaceDir}
}

// Result is the normalized output of Load.
type Result struct {
	Workspace  *core.Workspace
	CardTypes  map[string]*core.CardType
	Boards     map[string]*core.Board
	Extensions []Extension
}

// Load reads definitions/{workspace.json,card-types/*.json,boards/*.json}
// and returns a normalized Result. Validates cross-references at load time.
func (l *Loader) Load() (*Result, error) {
	ws, err := l.loadWorkspace()
	if err != nil {
		return nil, err
	}
	types, err := l.loadCardTypes(ws)
	if err != nil {
		return nil, err
	}
	boards, err := l.loadBoards(ws, types)
	if err != nil {
		return nil, err
	}
	exts, err := LoadExtensions(l.workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("load extensions: %w", err)
	}
	// Seed users from settings.default_user if users list is empty.
	if len(ws.Users) == 0 && ws.Settings.DefaultUser != "" {
		ws.Users = []core.User{
			{ID: ws.Settings.DefaultUser, Kind: "human", CreatedAt: nowUTC()},
		}
	}
	return &Result{Workspace: ws, CardTypes: types, Boards: boards, Extensions: exts}, nil
}

func (l *Loader) loadWorkspace() (*core.Workspace, error) {
	path := filepath.Join(l.workspaceDir, "definitions", "workspace.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workspace.json: %w", err)
	}
	var ws core.Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("parse workspace.json: %w", err)
	}
	if ws.ID == "" {
		return nil, fmt.Errorf("workspace.json: missing id")
	}
	// Defaults.
	if ws.Settings.TagPolicy == "" {
		ws.Settings.TagPolicy = "propose"
	}
	return &ws, nil
}

func (l *Loader) loadCardTypes(ws *core.Workspace) (map[string]*core.CardType, error) {
	dir := filepath.Join(l.workspaceDir, "definitions", "card-types")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read card-types dir: %w", err)
	}
	types := map[string]*core.CardType{}
	for _, e := range entries {
		if e.IsDir() || !hasExt(e.Name(), ".json") {
			continue
		}
		// Skip versioned snapshots like foo.v1.json.
		if isVersionedSnapshot(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var ct core.CardType
		if err := json.Unmarshal(data, &ct); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if ct.ID == "" {
			return nil, fmt.Errorf("%s: missing id", e.Name())
		}
		if _, dup := types[ct.ID]; dup {
			return nil, fmt.Errorf("duplicate card type id: %s", ct.ID)
		}
		if err := validateCardType(&ct, ws); err != nil {
			return nil, fmt.Errorf("card type %s: %w", ct.ID, err)
		}
		types[ct.ID] = &ct
	}
	return types, nil
}

func (l *Loader) loadBoards(ws *core.Workspace, types map[string]*core.CardType) (map[string]*core.Board, error) {
	dir := filepath.Join(l.workspaceDir, "definitions", "boards")
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No boards dir is not fatal; return empty.
		return map[string]*core.Board{}, nil
	}
	boards := map[string]*core.Board{}
	for _, e := range entries {
		if e.IsDir() || !hasExt(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var b core.Board
		if err := json.Unmarshal(data, &b); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if b.ID == "" {
			return nil, fmt.Errorf("%s: missing id", e.Name())
		}
		if _, dup := boards[b.ID]; dup {
			return nil, fmt.Errorf("duplicate board id: %s", b.ID)
		}
		if err := validateBoard(&b, ws, types); err != nil {
			return nil, fmt.Errorf("board %s: %w", b.ID, err)
		}
		boards[b.ID] = &b
	}
	return boards, nil
}

// validateCardType checks allowed_columns reference workspace columns.
func validateCardType(ct *core.CardType, ws *core.Workspace) error {
	colSet := colSet(ws)
	for _, c := range ct.AllowedColumns {
		if !colSet[c] {
			return fmt.Errorf("allowed_columns references unknown column %q", c)
		}
	}
	// Field id uniqueness.
	seen := map[string]bool{}
	for _, f := range ct.Fields {
		if seen[f.ID] {
			return fmt.Errorf("duplicate field id %q", f.ID)
		}
		seen[f.ID] = true
		if err := validateField(&f); err != nil {
			return fmt.Errorf("field %q: %w", f.ID, err)
		}
	}
	return nil
}

func validateField(f *core.FieldDef) error {
	switch f.Type {
	case core.FieldEnum:
		if len(f.Options) == 0 {
			return fmt.Errorf("enum field requires options")
		}
	case core.FieldRepeating:
		if len(f.ItemFields) == 0 {
			return fmt.Errorf("repeating field requires item_fields")
		}
		seen := map[string]bool{}
		for _, sf := range f.ItemFields {
			if seen[sf.ID] {
				return fmt.Errorf("duplicate item_field id %q", sf.ID)
			}
			seen[sf.ID] = true
		}
	case core.FieldString, core.FieldText, core.FieldNumber, core.FieldDate,
		core.FieldTags, core.FieldUser, core.FieldCardLink, core.FieldArtifact:
		// ok
	default:
		return fmt.Errorf("unknown field type %q", f.Type)
	}
	return nil
}

func validateBoard(b *core.Board, ws *core.Workspace, types map[string]*core.CardType) error {
	colSet := colSet(ws)
	for _, c := range b.Columns {
		if !colSet[c] {
			return fmt.Errorf("columns references unknown column %q", c)
		}
	}
	for _, tid := range b.CardTypeIDs {
		if _, ok := types[tid]; !ok {
			return fmt.Errorf("card_type_ids references unknown type %q", tid)
		}
	}
	// Transitions reference columns.
	for from, nexts := range b.Transitions {
		if !colSet[from] {
			return fmt.Errorf("transitions: unknown from-status %q", from)
		}
		for _, n := range nexts {
			if !colSet[n] {
				return fmt.Errorf("transitions[%s]: unknown to-status %q", from, n)
			}
		}
	}
	return nil
}

func colSet(ws *core.Workspace) map[string]bool {
	m := map[string]bool{}
	for _, c := range ws.Columns {
		m[c.ID] = true
	}
	return m
}

func hasExt(name, ext string) bool {
	return filepath.Ext(name) == ext
}

// isVersionedSnapshot detects files like "foo.v1.json" — skipped; the current
// file (foo.json) is the live schema.
func isVersionedSnapshot(name string) bool {
	// simple heuristic: contains ".v" + digit before .json
	base := name[:len(name)-len(".json")]
	if len(base) < 3 {
		return false
	}
	// look for ".v<digits>" suffix
	for i := 0; i < len(base); i++ {
		if base[i] == '.' && i+1 < len(base) && base[i+1] == 'v' {
			rest := base[i+2:]
			if rest != "" && allDigits(rest) {
				return true
			}
		}
	}
	return false
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
