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

// TestThemeRulesAreScoped pins contract guarantee 2 (THEMES.md): every rule
// that mentions a named theme is scoped under html[data-theme="<name>"], and
// base CSS never references a theme name. This is what makes themes safe to
// add, remove, or change — an unscoped rule would leak a theme's styling
// into every other theme, including the default.
func TestThemeRulesAreScoped(t *testing.T) {
	css := readRepoFile(t, "internal/httpapi/templates/style.css")
	// Strip comments first — block comments precede most selectors and would
	// otherwise glue onto them.
	for {
		start := strings.Index(css, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(css[start:], "*/")
		if end < 0 {
			css = css[:start]
			break
		}
		css = css[:start] + css[start+end+2:]
	}
	// Walk top-level rules; recurse into @media/@supports bodies. Selectors
	// are everything between a rule boundary and its opening brace.
	var walk func(body string)
	walk = func(body string) {
		i := 0
		for i < len(body) {
			open := strings.IndexByte(body[i:], '{')
			if open < 0 {
				return
			}
			selector := strings.TrimSpace(body[i : i+open])
			// find the matching close brace
			depth, j := 1, i+open+1
			for j < len(body) && depth > 0 {
				switch body[j] {
				case '{':
					depth++
				case '}':
					depth--
				}
				j++
			}
			inner := body[i+open+1 : j-1]
			if strings.HasPrefix(selector, "@media") || strings.HasPrefix(selector, "@supports") {
				walk(inner)
			} else if strings.Contains(selector, "data-theme=") {
				for _, part := range strings.Split(selector, ",") {
					part = strings.TrimSpace(part)
					if part == "" {
						continue
					}
					if strings.Contains(part, "data-theme=") && !strings.HasPrefix(part, `html[data-theme="`) {
						t.Errorf("theme rule not scoped from the root: %q — every themed selector must start html[data-theme=\"<name>\"]", part)
					}
				}
			}
			i = j
		}
	}
	walk(css)
}
