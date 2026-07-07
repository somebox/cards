package docaudit

// UI-contract checks (UI sprint P3, cards e71a64fa + 72ebfbee): ordinary Go
// tests in the docaudit pattern, because two real incidents motivated them —
// a single dropped `}` silently swallowed the entire labels theme (the CSS
// parser eats everything to the next matching brace, and nothing validates
// CSS), and the labels theme once branched shared templates, which THEMES.md
// establishes as the one thing a theme must never do.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStyleCSSBalanced pins the brace balance of the embedded stylesheet.
// An imbalance means some rule is swallowing everything after it — the
// symptom is "my theme/section silently disappeared", not an error.
func TestStyleCSSBalanced(t *testing.T) {
	css := readRepoFile(t, "internal/httpapi/templates/style.css")
	open, close := strings.Count(css, "{"), strings.Count(css, "}")
	if open != close {
		t.Errorf("style.css braces unbalanced: %d open vs %d close — a dropped brace silently "+
			"nests every later rule inside the unterminated one (this ate the labels theme once)", open, close)
	}
	// Named themes must still be present as top-level blocks.
	for _, theme := range []string{"journal", "labels"} {
		if !strings.Contains(css, `html[data-theme="`+theme+`"]`) {
			t.Errorf("style.css lost the %q theme block", theme)
		}
	}
}

// TestTemplatesAreThemeBlind pins THEMES.md's contract: a theme is CSS (plus
// a font manifest), never a template branch. No template may switch markup on
// the active theme name.
func TestTemplatesAreThemeBlind(t *testing.T) {
	files, err := filepath.Glob("../../internal/httpapi/templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates found: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		for _, forbidden := range []string{`eq .Theme "`, `eq $.Theme "`} {
			if strings.Contains(src, forbidden) {
				t.Errorf("%s branches markup on the theme name (%s…) — themes are CSS-only "+
					"(docs/design/THEMES.md); add a stable hook and style it per theme instead",
					filepath.Base(f), forbidden)
			}
		}
	}
}
