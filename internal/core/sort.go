package core

import (
	"regexp"
	"strings"
)

// Sort is a parsed, validated ORDER BY directive: one key plus a direction.
// The zero value (Field == "") means "no sort specified" — the store applies
// its default order (updated_at DESC, id DESC).
type Sort struct {
	Field string // canonical key: "created_at" | "updated_at" | "title" | "fields.<id>"
	Desc  bool   // true = descending
}

// IsZero reports whether no sort was specified (default order applies).
func (s Sort) IsZero() bool { return s.Field == "" }

// sortFieldRE matches a typed-field sort key: "fields." + a safe identifier.
// The strict identifier keeps the value safe to splice into a json_extract
// path in the store (no quoting escape hatch needed).
var sortFieldRE = regexp.MustCompile(`^fields\.[A-Za-z0-9_]+$`)

// ParseSort parses a flat sort directive like "-fields.priority" — a leading
// "-" means descending. The empty string parses to the zero Sort (default
// order). Any key outside the whitelist (created_at, updated_at, title, or
// fields.<id>) is a validation error, so a typo 422s instead of silently
// ordering by nothing. Shared by board-config validation and the store.
func ParseSort(s string) (Sort, error) {
	if s == "" {
		return Sort{}, nil
	}
	desc := false
	if strings.HasPrefix(s, "-") {
		desc = true
		s = strings.TrimPrefix(s, "-")
	}
	switch {
	case s == "created_at", s == "updated_at", s == "title":
	case sortFieldRE.MatchString(s):
	default:
		return Sort{}, NewValidationError("sort", "unsupported sort key: "+s+" (allowed: created_at, updated_at, title, fields.<id>)")
	}
	return Sort{Field: s, Desc: desc}, nil
}
