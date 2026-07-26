// Package config — inert.go
//
// A declared-but-unread setting is the inverse of "schemas, not magic"
// (philosophy §3): the knob is visible in the definitions schema, validated at
// load, and then read by nothing. The author sets it, sees no error, and gets
// no behavior change. This file warns about that class so it stops growing
// quietly, and so each entry has to be deleted deliberately when the feature
// behind it lands.
package config

import (
	"fmt"

	"github.com/somebox/cards/internal/core"
)

// inertDeclaration is one knob the loader accepts but nothing consumes.
//
// The list lives here, next to the check, on purpose: adding an inert field
// means adding a row here, which is the conscious act. Removing a row is how
// you record that the feature landed.
type inertDeclaration struct {
	// name is what the author wrote in their JSON.
	name string
	// tracking points at where the work is recorded, so the warning is
	// actionable rather than merely discouraging.
	tracking string
	// set reports whether this workspace actually declares it — the warning
	// only fires for authors who wrote the key expecting behavior.
	set func(*Result) bool
}

// inertDeclarations is the current catalog.
//
// Retired entries (kept as a record of what closing one looks like):
//   - settings.tag_policy      — honored since the two-value dial landed
//   - settings.default_board   — honored across TUI / UI / take-next
//   - card type searchable_fields — honored by the FTS indexer
var inertDeclarations = []inertDeclaration{
	{
		name:     "settings.event_retention_days",
		tracking: "no trimming job reads it; the events table is append-only and never trimmed (docs/roadmap.md §7)",
		set:      func(r *Result) bool { return r.Workspace.Settings.EventRetentionDays != 0 },
	},
	{
		name:     "extensions[].expose",
		tracking: "parsed but unconsumed; the supervisor never binds or advertises a port (docs/architecture/index.md)",
		set: func(r *Result) bool {
			for _, e := range r.Extensions {
				if e.Expose != nil {
					return true
				}
			}
			return false
		},
	},
}

// inertWarnings returns one warning per inert knob this workspace declares.
//
// Warnings rather than load errors, deliberately: a hard error would break
// workspaces that set event_retention_days in good faith years before any
// trimming job exists, and the setting is inert — it cannot corrupt anything.
// The rule is "fail loudly" (philosophy §9), not "refuse to start over a knob
// that does nothing".
func inertWarnings(r *Result) []string {
	var out []string
	for _, d := range inertDeclarations {
		if d.set(r) {
			out = append(out, fmt.Sprintf(
				"%s is declared but currently has no effect — %s", d.name, d.tracking))
		}
	}
	return out
}

// assertKnobsStillInert is a compile-time reminder that this list is about
// core declarations; it exists so the file fails to build if the settings
// struct loses a field the catalog names.
var _ = core.WorkspaceSettings{}.EventRetentionDays
