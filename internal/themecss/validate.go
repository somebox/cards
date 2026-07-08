// Package themecss validates a workspace theme's CSS against the THEMES.md
// contract before it is concatenated after the base stylesheet. It is the
// shared guard used by the theme loader (internal/config, THEMES.md step 2) and
// pinned by a docaudit test against the built-in themes.
//
// # Threat model
//
// Workspace theme files live in the git-backed definitions/ directory — the
// SAME trust boundary as every other core definition, authored and reviewed by
// the workspace operator. This validator is therefore a CONTRACT check, not a
// security sandbox: it does not defend against a hostile operator (who can
// already edit any definition, run hooks, etc.), and "validated" must not be
// read as "sandboxed". What it DOES guarantee is that one theme cannot silently
// break OTHER themes or the base stylesheet — the property THEMES.md relies on
// to make themes safe to add, remove, and share:
//
//  1. Braces balance. An unterminated rule makes the CSS parser swallow every
//     following rule into the unclosed one — the "my theme silently vanished"
//     incident that motivated the original brace test.
//  2. Every rule is scoped under html[data-theme="<name>"]. A rule that escapes
//     that scope — including via a premature `}` that closes the theme scope so
//     a trailing `body{…}` lands at the top level — would leak into every other
//     theme and the default. @media/@supports wrappers are transparent: their
//     inner rules must also be scoped.
//  3. No @import. It would pull unbounded external CSS in at load time, outside
//     the contract and the validator's view.
//  4. No remote url() (http:, https:, or protocol-relative //). A theme must
//     not phone home for images/fonts; bundle assets or declare web fonts via
//     the theme's font manifest. Relative and data: URLs are allowed.
package themecss

import (
	"fmt"
	"strings"
)

// Violation is one contract breach in a theme's CSS. Line is 1-based. Rule is
// the offending selector, at-rule, or snippet (for messages). The JSON shape is
// what a 422 reload response surfaces so an author can jump to the spot.
type Violation struct {
	Theme   string `json:"theme"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

func (v Violation) String() string {
	return fmt.Sprintf("theme %q (%s:%d): %s — %s", v.Theme, v.File, v.Line, v.Message, v.Rule)
}

// Validate returns every contract violation in css for a theme named theme,
// loaded from file (used only in messages). An empty slice means the CSS is
// safe to serve concatenated after the base stylesheet. It never returns an
// error — malformed CSS surfaces as violations, not as a Go error.
func Validate(theme, file, css string) []Violation {
	var vs []Violation
	add := func(line int, rule, msg string) {
		vs = append(vs, Violation{Theme: theme, File: file, Line: line, Rule: strings.TrimSpace(rule), Message: msg})
	}

	// Blank comments to spaces but KEEP newlines, so every offset in `clean`
	// maps to the same line as in the original source.
	clean := blankComments(css)

	// (1) Brace balance. Report before the structural walk, since an imbalance
	// makes scope analysis meaningless.
	if open, close := strings.Count(clean, "{"), strings.Count(clean, "}"); open != close {
		add(1, "", fmt.Sprintf("unbalanced braces: %d '{' vs %d '}' — an unterminated rule swallows every rule after it", open, close))
		return vs // structure is unreliable; stop here rather than emit noise
	}

	// (2)+(3) Structural walk: every top-level rule must be theme-scoped;
	// @media/@supports recurse; @import is rejected.
	walkRules(clean, func(selector string, offset int, inner string, isAtBlock bool) {
		line := lineAt(clean, offset)
		if isAtBlock {
			return // @media/@supports handled by recursion in walkRules
		}
		for _, part := range strings.Split(selector, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if !strings.HasPrefix(part, `html[data-theme="`+theme+`"]`) {
				add(line, part, `rule is not scoped under html[data-theme="`+theme+`"] — an unscoped rule leaks into other themes and the default`)
			}
		}
	}, func(atStatement string, offset int) {
		// top-level at-statement terminated by ';' (no block), e.g. @import.
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(atStatement)), "@import") {
			add(lineAt(clean, offset), atStatement, "@import is not allowed — it pulls external CSS in at load time, outside the contract")
		}
	})

	// (4) Remote url() anywhere.
	for _, u := range remoteURLs(clean) {
		add(u.line, u.raw, "remote url() is not allowed — bundle the asset or use the theme font manifest; only relative and data: URLs are permitted")
	}
	return vs
}

// blankComments replaces /* ... */ spans with spaces, preserving newlines so
// line numbers are unchanged. An unterminated comment runs to EOF.
func blankComments(s string) string {
	b := []byte(s)
	for i := 0; i < len(b)-1; i++ {
		if b[i] == '/' && b[i+1] == '*' {
			j := i + 2
			for j < len(b)-1 && !(b[j] == '*' && b[j+1] == '/') {
				if b[j] != '\n' {
					b[j] = ' '
				}
				j++
			}
			// blank the opening /* and closing */ (or run to EOF)
			b[i], b[i+1] = ' ', ' '
			if j < len(b)-1 {
				b[j], b[j+1] = ' ', ' '
				i = j + 1
			} else {
				for k := i; k < len(b); k++ {
					if b[k] != '\n' {
						b[k] = ' '
					}
				}
				break
			}
		}
	}
	return string(b)
}

// lineAt returns the 1-based line number of byte offset off in s.
func lineAt(s string, off int) int {
	if off > len(s) {
		off = len(s)
	}
	return 1 + strings.Count(s[:off], "\n")
}

// walkRules parses top-level rules of a (comment-blanked, brace-balanced) CSS
// body. For each block rule it calls onRule(selector, selectorOffset, inner,
// isAtBlock); @media/@supports blocks are reported (isAtBlock=true) AND their
// inner rules are walked recursively. Top-level at-statements ending in ';'
// (no block) are reported via onAtStatement. Offsets are into the original s so
// callers can map them to lines.
func walkRules(s string, onRule func(sel string, off int, inner string, isAtBlock bool), onAtStatement func(at string, off int)) {
	var walk func(body string, base int)
	walk = func(body string, base int) {
		i := 0
		for i < len(body) {
			// Skip leading whitespace to find the token start.
			for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r') {
				i++
			}
			if i >= len(body) {
				return
			}
			open := strings.IndexByte(body[i:], '{')
			semi := strings.IndexByte(body[i:], ';')
			// A ';' before the next '{' is an at-statement (or stray decl) with
			// no block — e.g. @import url(...);
			if semi >= 0 && (open < 0 || semi < open) {
				stmt := body[i : i+semi]
				if strings.HasPrefix(strings.TrimSpace(stmt), "@") {
					onAtStatement(stmt, base+i)
				}
				i = i + semi + 1
				continue
			}
			if open < 0 {
				return
			}
			selector := strings.TrimSpace(body[i : i+open])
			selOff := base + i
			// Match the closing brace of this rule.
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
			isAt := strings.HasPrefix(selector, "@media") || strings.HasPrefix(selector, "@supports")
			onRule(selector, selOff, inner, isAt)
			if isAt {
				walk(inner, base+i+open+1)
			}
			i = j
		}
	}
	walk(s, 0)
}

type urlRef struct {
	raw  string
	line int
}

// remoteURLs finds url(...) values whose target is remote (http:, https:, or
// protocol-relative //). Quotes are tolerated; relative and data: URLs pass.
func remoteURLs(s string) []urlRef {
	var out []urlRef
	low := strings.ToLower(s)
	for i := 0; ; {
		k := strings.Index(low[i:], "url(")
		if k < 0 {
			break
		}
		start := i + k
		end := strings.IndexByte(s[start:], ')')
		if end < 0 {
			break
		}
		raw := s[start : start+end+1]
		arg := strings.TrimSpace(s[start+4 : start+end])
		arg = strings.Trim(arg, `"'`)
		la := strings.ToLower(arg)
		if strings.HasPrefix(la, "http://") || strings.HasPrefix(la, "https://") || strings.HasPrefix(la, "//") {
			out = append(out, urlRef{raw: raw, line: lineAt(s, start)})
		}
		i = start + end + 1
	}
	return out
}
