// Package config loads and validates workspace definitions from JSON files
// in definitions/ (only definitions/extensions.{yaml,json} accepts YAML).
// This package does not watch files; `cards serve --watch` and
// POST /v1/workspace/reload re-invoke Load (see docs/architecture/reload.md).
//
// See docs/architecture/index.md (Workspace Loading).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	// Themes are workspace-loaded UI themes (definitions/themes/<name>.{css,json}),
	// validated at load time; a rejected theme is absent here and reported in
	// Warnings. Built-in themes are embedded in style.css, not here.
	Themes map[string]*core.Theme
	// Warnings are non-fatal load-time findings (e.g. an unrecognized
	// settings.persist_conditions entry) — surfaced by the caller (cmd/cards
	// logs them to stderr), never blocking Load. See EVENTS.md §12 Step 3
	// cross-cutting hardening.
	Warnings []string
}

// Load reads definitions/{workspace.json,card-types/*.json,boards/*.json}
// and returns a normalized Result. Validates cross-references at load time.
func (l *Loader) Load() (*Result, error) {
	ws, err := l.loadWorkspace()
	if err != nil {
		return nil, err
	}
	types, typeWarnings, err := l.loadCardTypes(ws)
	if err != nil {
		return nil, err
	}
	boards, boardWarnings, err := l.loadBoards(ws, types)
	if err != nil {
		return nil, err
	}
	exts, err := LoadExtensions(l.workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("load extensions: %w", err)
	}
	themes, themeWarnings, err := loadThemes(l.workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("load themes: %w", err)
	}
	// Seed users from settings.default_user if users list is empty.
	if len(ws.Users) == 0 && ws.Settings.DefaultUser != "" {
		ws.Users = []core.User{
			{ID: ws.Settings.DefaultUser, Kind: "human", CreatedAt: nowUTC()},
		}
	}
	warnings := validatePersistConditions(ws)
	warnings = append(warnings, typeWarnings...)
	warnings = append(warnings, boardWarnings...)
	warnings = append(warnings, themeWarnings...)
	return &Result{
		Workspace: ws, CardTypes: types, Boards: boards, Extensions: exts,
		Themes:   themes,
		Warnings: warnings,
	}, nil
}

// validatePersistConditions warns (does not fail Load) on any
// settings.persist_conditions entry that isn't a known condition type — a
// typo like "wip_exceded" would otherwise silently no-op (EVENTS.md §12
// Step 3 cross-cutting hardening).
func validatePersistConditions(ws *core.Workspace) []string {
	known := map[core.EventType]bool{}
	for _, t := range core.ConditionTypes() {
		known[t] = true
	}
	var warnings []string
	for _, raw := range ws.Settings.PersistConditions {
		if !known[core.EventType(raw)] {
			warnings = append(warnings, fmt.Sprintf(
				"settings.persist_conditions: unknown condition type %q (typo? see core.ConditionTypes for the known catalog)", raw))
		}
	}
	return warnings
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

func (l *Loader) loadCardTypes(ws *core.Workspace) (map[string]*core.CardType, []string, error) {
	dir := filepath.Join(l.workspaceDir, "definitions", "card-types")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read card-types dir: %w", err)
	}
	types := map[string]*core.CardType{}
	warnings := []string{}
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
			return nil, nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var ct core.CardType
		if err := json.Unmarshal(data, &ct); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if ct.ID == "" {
			return nil, nil, fmt.Errorf("%s: missing id", e.Name())
		}
		if _, dup := types[ct.ID]; dup {
			return nil, nil, fmt.Errorf("duplicate card type id: %s", ct.ID)
		}
		if err := validateCardType(&ct, ws); err != nil {
			return nil, nil, fmt.Errorf("card type %s: %w", ct.ID, err)
		}
		warnings = append(warnings, optionThemeInkWarnings(&ct)...)
		types[ct.ID] = &ct
	}
	return types, warnings, nil
}

// optionThemeInkWarnings flags option accents too light for the fixed light
// icon ink some themes paint ON the accent surface (labels: #fffdf5 on
// --card-stock). Warning tier, not rejection: the hard 4.5:1 floor covers
// accent-on-muted; icon-on-accent is a 3:1 graphics-scale concern and a light
// accent may be deliberate on themes that never do this.
func optionThemeInkWarnings(ct *core.CardType) []string {
	const themeInk = "#fffdf5"
	var out []string
	for _, f := range ct.Fields {
		for opt, th := range f.OptionThemes {
			c, err := core.ContrastRatio(themeInk, th.Accent)
			if err == nil && c < 3.0 {
				out = append(out, fmt.Sprintf(
					"card type %s: field %q: option_themes[%q]: accent %s gives %.1f:1 against the light icon ink themes paint on accent surfaces (3:1 graphics floor) — consider a darker accent",
					ct.ID, f.ID, opt, th.Accent, c))
			}
		}
	}
	return out
}

func (l *Loader) loadBoards(ws *core.Workspace, types map[string]*core.CardType) (map[string]*core.Board, []string, error) {
	dir := filepath.Join(l.workspaceDir, "definitions", "boards")
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No boards dir is not fatal; return empty.
		return map[string]*core.Board{}, nil, nil
	}
	boards := map[string]*core.Board{}
	var warnings []string
	for _, e := range entries {
		if e.IsDir() || !hasExt(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var b core.Board
		if err := json.Unmarshal(data, &b); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if b.ID == "" {
			return nil, nil, fmt.Errorf("%s: missing id", e.Name())
		}
		if _, dup := boards[b.ID]; dup {
			return nil, nil, fmt.Errorf("duplicate board id: %s", b.ID)
		}
		w, err := validateBoard(&b, ws, types)
		if err != nil {
			return nil, nil, fmt.Errorf("board %s: %w", b.ID, err)
		}
		warnings = append(warnings, w...)
		boards[b.ID] = &b
	}
	return boards, warnings, nil
}

// validateCardType checks allowed_columns reference workspace columns,
// field defs, and icon aliases.
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
	if ct.TypeTheme.Icon != "" && !core.IsKnownIconAlias(ct.TypeTheme.Icon) {
		return fmt.Errorf("type_theme.icon %q is not a known icon alias (allowed: %s)",
			ct.TypeTheme.Icon, core.KnownIconAliasList())
	}
	if ct.Icon != "" && !core.IsKnownIconAlias(ct.Icon) {
		return fmt.Errorf("icon %q is not a known icon alias (allowed: %s)",
			ct.Icon, core.KnownIconAliasList())
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
			if sf.Multiple {
				return fmt.Errorf("item_field %q: multiple is not supported inside repeating fields (v1)", sf.ID)
			}
			seen[sf.ID] = true
		}
	case core.FieldString, core.FieldText, core.FieldNumber, core.FieldDate,
		core.FieldTags, core.FieldUser, core.FieldCardLink, core.FieldArtifact:
		// ok
	default:
		return fmt.Errorf("unknown field type %q", f.Type)
	}
	if f.Type == core.FieldNumber || f.Type == core.FieldDate {
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			return fmt.Errorf("min (%v) must be <= max (%v)", *f.Min, *f.Max)
		}
	}
	if err := validateOptionThemes(f); err != nil {
		return err
	}
	if f.Multiple {
		// v1 scope: enum + user only. card_link multiple is a documented
		// fast-follow (SPEC-DATA-MODEL "Multi-value fields"), not shipped.
		if f.Type != core.FieldEnum && f.Type != core.FieldUser {
			return fmt.Errorf("field %q: multiple is only supported on enum and user fields (v1), not %q", f.ID, f.Type)
		}
		// A default for a multiple field must itself be a non-empty array of
		// strings (each checked against options for enums) — a scalar default
		// would violate the always-an-array wire contract.
		if f.Default != nil {
			arr, ok := f.Default.([]any)
			if !ok || len(arr) == 0 {
				return fmt.Errorf("field %q: default for a multiple field must be a non-empty array", f.ID)
			}
			for _, e := range arr {
				s, ok := e.(string)
				if !ok {
					return fmt.Errorf("field %q: default array elements must be strings", f.ID)
				}
				if f.Type == core.FieldEnum {
					found := false
					for _, o := range f.Options {
						if o == s {
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("field %q: default value %q is not in options", f.ID, s)
					}
				}
			}
		}
	}
	return nil
}

// validateOptionThemes enforces STYLE-FIELD define-side rules: enum-only,
// keys ⊆ options, known icons, icon+accent+muted together, and a 4.5:1
// accent-on-muted contrast floor (decision C — hard reject for option_themes).
func validateOptionThemes(f *core.FieldDef) error {
	if len(f.OptionThemes) == 0 {
		return nil
	}
	if f.Type != core.FieldEnum {
		return fmt.Errorf("option_themes is only allowed on enum fields, not %q", f.Type)
	}
	if f.Multiple {
		return fmt.Errorf("option_themes is not supported on multiple enum fields (style_field needs a single value)")
	}
	optSet := map[string]bool{}
	for _, o := range f.Options {
		optSet[o] = true
	}
	for val, th := range f.OptionThemes {
		if !optSet[val] {
			return fmt.Errorf("option_themes key %q is not in options", val)
		}
		if th.Icon == "" || th.Accent == "" || th.Muted == "" {
			return fmt.Errorf("option_themes[%q] requires icon, accent, and muted (color alone must not carry meaning)", val)
		}
		if !core.IsKnownIconAlias(th.Icon) {
			return fmt.Errorf("option_themes[%q].icon %q is not a known icon alias (allowed: %s)",
				val, th.Icon, core.KnownIconAliasList())
		}
		if _, err := core.MeetsContrastFloor(th.Accent, th.Muted); err != nil {
			return fmt.Errorf("option_themes[%q]: %w", val, err)
		}
	}
	return nil
}

func validateBoard(b *core.Board, ws *core.Workspace, types map[string]*core.CardType) ([]string, error) {
	colSet := colSet(ws)
	boardCols := map[string]bool{}
	for _, c := range b.Columns {
		if !colSet[c] {
			return nil, fmt.Errorf("columns references unknown column %q", c)
		}
		boardCols[c] = true
	}
	for _, tid := range b.CardTypeIDs {
		if _, ok := types[tid]; !ok {
			return nil, fmt.Errorf("card_type_ids references unknown type %q", tid)
		}
	}
	// Transitions must reference this board's columns (a board subset), not
	// arbitrary workspace column ids. Otherwise transition_illegal.valid_options
	// can echo statuses the board cannot move to.
	if len(b.Transitions) > 0 && len(b.Columns) == 0 {
		return nil, fmt.Errorf("transitions require board columns")
	}
	for from, nexts := range b.Transitions {
		if !boardCols[from] {
			return nil, fmt.Errorf("transitions: from-status %q is not a board column", from)
		}
		for _, n := range nexts {
			if !boardCols[n] {
				return nil, fmt.Errorf("transitions[%s]: to-status %q is not a board column", from, n)
			}
		}
	}
	if b.Monitors != nil {
		for _, c := range b.Monitors.AlertWhenEmpty {
			if !colSet[c] {
				return nil, fmt.Errorf("monitors.alert_when_empty references unknown column %q", c)
			}
		}
		for col, dur := range b.Monitors.MaxTimeInStatus {
			if !colSet[col] {
				return nil, fmt.Errorf("monitors.max_time_in_status references unknown column %q", col)
			}
			if _, err := core.ParseMonitorDuration(dur); err != nil {
				return nil, fmt.Errorf("monitors.max_time_in_status[%s]: %w", col, err)
			}
		}
		if b.Monitors.IdleAfter != "" {
			if _, err := core.ParseMonitorDuration(b.Monitors.IdleAfter); err != nil {
				return nil, fmt.Errorf("monitors.idle_after: %w", err)
			}
		}
	}

	fieldUnion := boardFieldUnion(b, types)
	boardTypes := map[string]bool{}
	for _, tid := range b.CardTypeIDs {
		boardTypes[tid] = true
	}
	var warnings []string

	if b.Presentation != nil {
		p := b.Presentation
		if p.LaneSort != "" {
			sort, err := core.ParseSort(p.LaneSort)
			if err != nil {
				return nil, fmt.Errorf("presentation.lane_sort: %w", err)
			}
			if strings.HasPrefix(sort.Field, "fields.") {
				fid := strings.TrimPrefix(sort.Field, "fields.")
				if !fieldUnion[fid] {
					return nil, fmt.Errorf("presentation.lane_sort references unknown field %q (not on board card types)", fid)
				}
			}
		}
		if g := p.LaneGroupBy; g != "" && g != "status" {
			if !fieldUnion[g] {
				return nil, fmt.Errorf("presentation.lane_group_by references unknown field %q", g)
			}
		}
		if t := p.CardTitleField; t != "" && t != "title" {
			if !fieldUnion[t] {
				return nil, fmt.Errorf("presentation.card_title_field references unknown field %q", t)
			}
		}
		if sf := p.StyleField; sf != "" {
			if err := validateStyleField(b, sf, types); err != nil {
				return nil, err
			}
		}
		// Unknown presentation keys (including legacy card_accent_field) stay ignored.
		for typeID, fields := range p.CardPreview {
			ct, ok := types[typeID]
			if !ok {
				return nil, fmt.Errorf("presentation.card_preview references unknown type %q", typeID)
			}
			if !boardTypes[typeID] {
				warnings = append(warnings, fmt.Sprintf(
					"board %s: presentation.card_preview key %q is not in card_type_ids (orphan preview; hard-reject next sprint?)",
					b.ID, typeID))
			}
			typeFields := fieldIDSet(ct)
			for _, fid := range fields {
				if !typeFields[fid] {
					return nil, fmt.Errorf("presentation.card_preview[%s] references unknown field %q", typeID, fid)
				}
			}
		}
		for i, sec := range p.DetailSections {
			for _, fid := range sec.Fields {
				if !fieldUnion[fid] {
					return nil, fmt.Errorf("presentation.detail_sections[%d] references unknown field %q", i, fid)
				}
			}
		}
		for _, f := range p.Filters {
			w, err := checkFilterFieldRefs(
				fmt.Sprintf("board %s: presentation.filters[%s]", b.ID, f.ID),
				f.Filter, fieldUnion)
			if err != nil {
				return nil, err
			}
			warnings = append(warnings, w...)
		}
	}

	if len(b.DefaultFilter) > 0 {
		w, err := checkFilterFieldRefs(
			fmt.Sprintf("board %s: default_filter", b.ID),
			b.DefaultFilter, fieldUnion)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, w...)
	}
	return warnings, nil
}

// validateStyleField checks BoardPresentation.StyleField: the named field must
// be a single-value enum on every board type that declares it, and at least one
// board type must declare it. OptionThemes (when present) are already checked
// in validateField; cards without the field or an unthemed value fall through.
func validateStyleField(b *core.Board, styleField string, types map[string]*core.CardType) error {
	found := false
	for _, tid := range b.CardTypeIDs {
		ct := types[tid]
		if ct == nil {
			continue
		}
		for _, f := range ct.Fields {
			if f.ID != styleField {
				continue
			}
			found = true
			if f.Type != core.FieldEnum {
				return fmt.Errorf("presentation.style_field %q on type %q must be an enum field, got %q",
					styleField, tid, f.Type)
			}
			if f.Multiple {
				return fmt.Errorf("presentation.style_field %q on type %q must not be multiple",
					styleField, tid)
			}
		}
	}
	if !found {
		return fmt.Errorf("presentation.style_field %q is not a field on any card type on this board", styleField)
	}
	return nil
}

// boardFieldUnion is the set of field ids across all card types on the board.
func boardFieldUnion(b *core.Board, types map[string]*core.CardType) map[string]bool {
	m := map[string]bool{}
	for _, tid := range b.CardTypeIDs {
		ct := types[tid]
		if ct == nil {
			continue
		}
		for id := range fieldIDSet(ct) {
			m[id] = true
		}
	}
	return m
}

func fieldIDSet(ct *core.CardType) map[string]bool {
	m := map[string]bool{}
	for _, f := range ct.Fields {
		m[f.ID] = true
	}
	return m
}

// knownFilterKeys are first-class filter paths from SPEC-QUERY-DSL (not typed fields).
var knownFilterKeys = map[string]bool{
	"status": true, "owner": true, "type_id": true, "created_by": true,
	"updated_at": true, "created_at": true, "tag": true, "tags": true,
}

// checkFilterFieldRefs walks a filter DSL map. Unambiguous fields.<id> refs that
// are missing from fieldUnion are hard errors. Bare keys that are neither known
// builtins nor fields on the board warn (legacy/ambiguous — sqlite treats bare
// ids as json_extract paths). Nested $and/$or are walked recursively.
func checkFilterFieldRefs(path string, filter map[string]any, fieldUnion map[string]bool) ([]string, error) {
	if filter == nil {
		return nil, nil
	}
	var warnings []string
	for k, v := range filter {
		switch k {
		case "$and", "$or":
			arr, ok := v.([]any)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("%s: %s expects an array (left unchecked this sprint)", path, k))
				continue
			}
			for i, e := range arr {
				em, ok := e.(map[string]any)
				if !ok {
					warnings = append(warnings, fmt.Sprintf("%s: %s[%d] is not an object (left unchecked this sprint)", path, k, i))
					continue
				}
				w, err := checkFilterFieldRefs(fmt.Sprintf("%s.%s[%d]", path, k, i), em, fieldUnion)
				if err != nil {
					return nil, err
				}
				warnings = append(warnings, w...)
			}
		default:
			if strings.HasPrefix(k, "fields.") {
				fid := strings.TrimPrefix(k, "fields.")
				if fid == "" || !fieldUnion[fid] {
					return nil, fmt.Errorf("%s references unknown field %q", path, fid)
				}
				continue
			}
			if knownFilterKeys[k] {
				continue
			}
			if fieldUnion[k] {
				// Bare field id — sqlite accepts it; prefer fields.<id> later.
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"%s: unrecognized filter key %q (not a known path or board field; hard-reject next sprint?)",
				path, k))
		}
	}
	return warnings, nil
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
