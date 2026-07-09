package themecss

import (
	"strings"
	"testing"
)

// hasMsg reports whether any violation's message contains sub.
func hasMsg(vs []Violation, sub string) bool {
	for _, v := range vs {
		if strings.Contains(v.Message, sub) {
			return true
		}
	}
	return false
}

func TestValidThemePasses(t *testing.T) {
	css := `
html[data-theme="mine"] { --accent: #c33; }
html[data-theme="mine"] .card { color: var(--accent); }
html[data-theme="mine"] .card, html[data-theme="mine"] .chip { border: 1px solid; }
@media (min-width: 600px) {
  html[data-theme="mine"] .card { padding: 1rem; }
}
/* a comment mentioning body{} should not trip anything */
html[data-theme="mine"] .bg { background: url(./local.png); }
html[data-theme="mine"] .avatar { background: url("data:image/png;base64,AAAA"); }
`
	if vs := Validate("mine", "mine.css", css); len(vs) != 0 {
		t.Fatalf("valid theme flagged: %v", vs)
	}
}

func TestScopeEscapeBalanced(t *testing.T) {
	// Balanced braces, but the second rule escapes the theme scope entirely.
	css := "html[data-theme=\"mine\"] { color: red }\nbody { color: blue }"
	vs := Validate("mine", "mine.css", css)
	if !hasMsg(vs, "not scoped under") {
		t.Fatalf("scope escape not caught: %v", vs)
	}
	// The offending `body` rule is on line 2.
	found := false
	for _, v := range vs {
		if strings.HasPrefix(v.Rule, "body") {
			found = true
			if v.Line != 2 {
				t.Errorf("body violation line = %d, want 2", v.Line)
			}
		}
	}
	if !found {
		t.Errorf("expected a violation naming the body rule: %v", vs)
	}
}

func TestUnbalancedBraces(t *testing.T) {
	css := `html[data-theme="mine"] { color: red` // missing }
	vs := Validate("mine", "mine.css", css)
	if !hasMsg(vs, "unbalanced braces") {
		t.Fatalf("unbalanced braces not caught: %v", vs)
	}
}

func TestUnscopedTopLevelRule(t *testing.T) {
	css := `.card { color: red }`
	if vs := Validate("mine", "mine.css", css); !hasMsg(vs, "not scoped under") {
		t.Fatalf("unscoped rule not caught: %v", vs)
	}
}

func TestWrongThemeNameIsUnscoped(t *testing.T) {
	// Scoped, but under the WRONG theme name — still leaks relative to "mine".
	css := `html[data-theme="other"] .card { color: red }`
	if vs := Validate("mine", "mine.css", css); !hasMsg(vs, "not scoped under") {
		t.Fatalf("wrong-theme scope not caught: %v", vs)
	}
}

func TestMediaWrapperInnerMustBeScoped(t *testing.T) {
	css := `@media (min-width: 600px) { .card { color: red } }`
	if vs := Validate("mine", "mine.css", css); !hasMsg(vs, "not scoped under") {
		t.Fatalf("unscoped rule inside @media not caught: %v", vs)
	}
}

func TestMediaWrapperItselfNotFlagged(t *testing.T) {
	css := `@media (min-width: 600px) { html[data-theme="mine"] .card { color: red } }`
	if vs := Validate("mine", "mine.css", css); len(vs) != 0 {
		t.Fatalf("properly-scoped @media flagged: %v", vs)
	}
}

func TestImportRejected(t *testing.T) {
	css := "@import url(\"https://evil.example/x.css\");\nhtml[data-theme=\"mine\"] { color: red }"
	vs := Validate("mine", "mine.css", css)
	if !hasMsg(vs, "@import is not allowed") {
		t.Fatalf("@import not caught: %v", vs)
	}
}

func TestRemoteURLRejected(t *testing.T) {
	for _, u := range []string{
		`url(https://evil.example/bg.png)`,
		`url(http://evil.example/bg.png)`,
		`url(//evil.example/bg.png)`,
		`url("https://evil.example/bg.png")`,
	} {
		css := `html[data-theme="mine"] .bg { background: ` + u + ` }`
		if vs := Validate("mine", "mine.css", css); !hasMsg(vs, "remote url()") {
			t.Errorf("remote url not caught for %s: %v", u, vs)
		}
	}
}

func TestLocalAndDataURLsAllowed(t *testing.T) {
	for _, u := range []string{
		`url(./bg.png)`,
		`url(../assets/bg.png)`,
		`url(data:image/svg+xml;base64,AAAA)`,
	} {
		css := `html[data-theme="mine"] .bg { background: ` + u + ` }`
		if vs := Validate("mine", "mine.css", css); len(vs) != 0 {
			t.Errorf("allowed url flagged for %s: %v", u, vs)
		}
	}
}

func TestCommentedOutViolationsIgnored(t *testing.T) {
	// The nasties are inside comments — must not be flagged, and line numbers of
	// the real rule must survive the comment blanking.
	css := "/* body { color: red }\n   @import 'x';\n   url(https://evil/x) */\nhtml[data-theme=\"mine\"] { color: red }"
	if vs := Validate("mine", "mine.css", css); len(vs) != 0 {
		t.Fatalf("commented-out content flagged: %v", vs)
	}
}

func TestPartialCommaSelectorFlagged(t *testing.T) {
	css := `html[data-theme="mine"] .a, body .b { color: red }`
	vs := Validate("mine", "mine.css", css)
	if !hasMsg(vs, "not scoped under") {
		t.Fatalf("unscoped comma-part not caught: %v", vs)
	}
	for _, v := range vs {
		if strings.HasPrefix(v.Rule, "html[data-theme") {
			t.Errorf("scoped comma-part wrongly flagged: %q", v.Rule)
		}
	}
}

func TestViolationStringNamesThemeFileLine(t *testing.T) {
	css := "\n\nbody { color: red }" // rule on line 3
	vs := Validate("mine", "themes/mine.css", css)
	if len(vs) == 0 {
		t.Fatal("expected a violation")
	}
	s := vs[0].String()
	for _, want := range []string{`"mine"`, "themes/mine.css", ":3"} {
		if !strings.Contains(s, want) {
			t.Errorf("violation string %q missing %q", s, want)
		}
	}
}

func TestThemeMayStyleComboboxHooks(t *testing.T) {
	// The combobox hooks (DESIGN.md stable list, rebuild P5) are themable like
	// any component: a scoped block over them must pass the contract check.
	css := `
html[data-theme="mine"] .combobox__menu { background: #101418; border-color: #333; }
html[data-theme="mine"] .combobox__option.is-active { background: #1c2530; }
html[data-theme="mine"] .combobox__filter { color: #e0e6ec; }
`
	if vs := Validate("mine", "mine.css", css); len(vs) != 0 {
		t.Fatalf("theme styling combobox hooks flagged: %v", vs)
	}
}
