// Package uioptions holds the small presentation-only helpers shared by the
// two board surfaces — the web UI (internal/httpapi) and the TUI
// (internal/tui) — so option lists and filter conveniences cannot drift
// between them. It is deliberately NOT internal/core: "Recently updated" is a
// presentation label and the core grows reluctantly (philosophy §1). Compile-
// time parity: both surfaces call these helpers, so the TUI's sort presets
// and `me` substitution are the web UI's by construction.
package uioptions

import "github.com/somebox/cards/internal/core"

// Option is a select option. It is the single option type for both UIs —
// internal/httpapi aliases it (`type Option = uioptions.Option`) so existing
// template view models keep compiling unchanged.
type Option struct {
	Value    string
	Label    string
	Selected bool
	Disabled bool
}

// SortOptions builds the board sort selector shared by the web board header
// and the TUI sort-cycle. The presets cover the common orders; a
// board-declared lane_sort that isn't one of them is surfaced as a leading
// "Board default" option so the configured order is selectable by name.
// `active` is the resolved sort directive ("" marks nothing selected — the
// caller maps the empty/default order to "-updated_at" for display, matching
// the store's default updated_at DESC). A nil board behaves as "no
// presentation".
func SortOptions(active string, b *core.Board) []Option {
	presets := []Option{
		{Value: "-updated_at", Label: "Recently updated"},
		{Value: "-created_at", Label: "Newest"},
		{Value: "created_at", Label: "Oldest"},
		{Value: "title", Label: "Title (A–Z)"},
	}
	known := map[string]bool{}
	for _, o := range presets {
		known[o.Value] = true
	}
	opts := []Option{}
	if b != nil && b.Presentation != nil && b.Presentation.LaneSort != "" && !known[b.Presentation.LaneSort] {
		opts = append(opts, Option{Value: b.Presentation.LaneSort, Label: "Board default"})
	}
	opts = append(opts, presets...)
	for i := range opts {
		opts[i].Selected = opts[i].Value == active
	}
	return opts
}

// ResolveMeFilter deep-copies a saved-filter map, replacing the literal "me"
// token with the viewing actor under identity keys (owner, created_by). The
// substitution is a UI-only convenience: the filter DSL compiler and the API
// stay presentation-free (they never know what "me" means), so this happens
// at the surface — the web board handler and the TUI's refresh — against the
// resolved actor. An empty actor leaves "me" untouched (nothing to resolve
// to).
func ResolveMeFilter(f map[string]any, actor string) map[string]any {
	if f == nil {
		return nil
	}
	out := make(map[string]any, len(f))
	for k, v := range f {
		if actor != "" && (k == "owner" || k == "created_by") {
			out[k] = replaceMe(v, actor)
		} else {
			out[k] = v
		}
	}
	return out
}

// replaceMe recursively replaces the string "me" with actor inside a filter
// value — handling a bare string, an op object ({"$eq":"me"}), and a list
// ({"$in":["me","alice"]}).
func replaceMe(v any, actor string) any {
	switch t := v.(type) {
	case string:
		if t == "me" {
			return actor
		}
		return t
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, vv := range t {
			m[k] = replaceMe(vv, actor)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, vv := range t {
			s[i] = replaceMe(vv, actor)
		}
		return s
	default:
		return v
	}
}
