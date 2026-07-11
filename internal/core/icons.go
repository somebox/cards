package core

import "strings"

// KnownIconAliases is the closed set of monochrome glyph names accepted by
// type_theme.icon, legacy CardType.icon, and FieldDef.option_themes. Keep in
// sync with the [data-icon] aliases in internal/httpapi/templates/style.css.
var KnownIconAliases = map[string]struct{}{
	"card":   {},
	"star":   {},
	"bug":    {},
	"check":  {},
	"flask":  {},
	"target": {},
	"code":   {},
	"pen":    {},
	"wrench": {},
}

// knownIconAliasOrder is the stable, human-facing order for error messages.
var knownIconAliasOrder = []string{
	"card", "star", "bug", "check", "flask", "target", "code", "pen", "wrench",
}

// IsKnownIconAlias reports whether name is one of the CSS [data-icon] aliases.
// Empty string is not known (callers treat omitempty as "no icon set").
func IsKnownIconAlias(name string) bool {
	_, ok := KnownIconAliases[name]
	return ok
}

// KnownIconAliasList returns the allowed aliases as a comma-separated list
// for structured rejection messages.
func KnownIconAliasList() string {
	return strings.Join(knownIconAliasOrder, ", ")
}
