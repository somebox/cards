package docaudit

// Frontend-contract guards (frontend-rebuild Phase 0). These pin the
// division-of-labor architecture the UI 2.0 plan is built on:
//
//   - Go templates render ALL server data and the first paint; Alpine (and
//     any JS) adds ephemeral interactivity only — never `x-for` over server
//     JSON (that would fork the API contract and break no-JS rendering).
//   - Server-rendered HTML enters the live DOM through ONE seam, swapHTML()
//     in ui.js (innerHTML + Alpine.initTree + refreshAgo). Any other
//     .innerHTML assignment risks Alpine components silently not booting —
//     the bug class these guards exist to prevent.
//   - Behavior lives in embedded assets (unit-testable via `node --test`);
//     template inline <script> blocks only hand over server parameters.
//   - CSS stays on the token system (DESIGN.md): type never sized in px/vw/vh,
//     and raw color literals don't proliferate outside the token blocks.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// vendored files are third-party and exempt from our JS style guards.
var vendoredJS = map[string]bool{"alpine.min.js": true}

// jsAssets returns name→content of our own (non-vendored) JS assets.
func jsAssets(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("../../internal/httpapi/templates/assets/*.js")
	if err != nil || len(files) == 0 {
		t.Fatalf("no JS assets found: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		name := filepath.Base(f)
		if vendoredJS[name] {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(b)
	}
	return out
}

// templateFiles returns name→content of the HTML templates.
func templateFiles(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("../../internal/httpapi/templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates found: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = string(b)
	}
	return out
}

var innerHTMLAssign = regexp.MustCompile(`\.innerHTML\s*=`)

// TestSwapSeamIsTheOnlyInnerHTMLWrite: exactly one .innerHTML assignment
// exists in our JS — inside swapHTML — and none in template inline scripts.
// Everything else must call swapHTML so Alpine.initTree runs on fresh HTML.
func TestSwapSeamIsTheOnlyInnerHTMLWrite(t *testing.T) {
	total := 0
	for name, src := range jsAssets(t) {
		for _, m := range innerHTMLAssign.FindAllStringIndex(src, -1) {
			line := 1 + strings.Count(src[:m[0]], "\n")
			total++
			if name != "ui.js" {
				t.Errorf("%s:%d assigns .innerHTML — route server HTML through swapHTML (ui.js) instead", name, line)
			}
		}
	}
	if total != 1 {
		t.Errorf(".innerHTML assignments in our JS = %d, want exactly 1 (inside swapHTML). "+
			"New DOM swaps must call swapHTML so Alpine.initTree runs on the fresh subtree.", total)
	}
	for name, src := range templateFiles(t) {
		if loc := innerHTMLAssign.FindStringIndex(src); loc != nil {
			line := 1 + strings.Count(src[:loc[0]], "\n")
			t.Errorf("%s:%d assigns .innerHTML in an inline script — use swapHTML from ui.js", name, line)
		}
	}
}

// TestTemplatesServerDataStaysServerRendered: the division-of-labor rule.
// Server data is rendered by Go templates; Alpine renders no server lists.
// x-for is allowed only for genuinely local, ephemeral UI state — add a file
// to the allowlist ONLY after review confirms the loop's data never comes
// from the API.
func TestTemplatesServerDataStaysServerRendered(t *testing.T) {
	xForAllowlist := map[string]bool{
		// (empty — nothing legitimately x-fors yet)
	}
	for name, src := range templateFiles(t) {
		if strings.Contains(src, "x-for") && !xForAllowlist[name] {
			t.Errorf("%s uses x-for — server data must be rendered by the Go template (division of labor); "+
				"if this loop is genuinely local ephemeral state, add the file to the allowlist with a justification", name)
		}
	}
}

// TestInlineScriptBudget: template inline <script> blocks are parameter
// handoffs, not behavior. The budget is a ratchet — if you need more lines,
// the code belongs in templates/assets/ (unit-testable, cacheable).
func TestInlineScriptBudget(t *testing.T) {
	const budget = 25 // baseline 19 at Phase 0 + slack for a handoff or two
	total := 0
	for name, src := range templateFiles(t) {
		inScript := false
		for _, line := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "<script>") {
				inScript = true
				continue
			}
			if strings.Contains(trimmed, "</script>") {
				inScript = false
				continue
			}
			if inScript && trimmed != "" {
				total++
			}
		}
		_ = name
	}
	if total > budget {
		t.Errorf("inline <script> lines across templates = %d, budget %d — move behavior into "+
			"internal/httpapi/templates/assets/*.js (tested by node --test) and keep only parameter handoffs inline", total, budget)
	}
}

// TestFieldControlIsTheOnlySwitch (Phase 1): the scalar type→control mapping
// lives in field_control.html and nowhere else. Structural branches
// (repeating / artifact / card_link / tags sections) are fine; branching on a
// SCALAR field type to emit an input outside the partial recreates the
// three-way drift this consolidation removed.
func TestFieldControlIsTheOnlySwitch(t *testing.T) {
	scalarBranch := regexp.MustCompile(`eq \.(Def\.)?Type "(text|enum|number|date|user|string)"`)
	for name, src := range templateFiles(t) {
		if name == "field_control.html" {
			continue
		}
		for _, m := range scalarBranch.FindAllStringIndex(src, -1) {
			line := 1 + strings.Count(src[:m[0]], "\n")
			t.Errorf("%s:%d branches on a scalar field type — render the control via "+
				`{{template "field_control" (dict ...)}} instead (the one canonical switch)`, name, line)
		}
	}
}

// TestCSSTypeNeverSizedInPxOrViewport pins DESIGN.md's unit rule: rem for
// font-size; never px (breaks zoom) and never vw/vh (renders a different px
// size per window).
func TestCSSTypeNeverSizedInPxOrViewport(t *testing.T) {
	css := readRepoFile(t, "internal/httpapi/templates/style.css")
	re := regexp.MustCompile(`font-size\s*:[^;{}]*\b(\d+(?:\.\d+)?)(px|vw|vh)`)
	for _, m := range re.FindAllStringIndex(css, -1) {
		line := 1 + strings.Count(css[:m[0]], "\n")
		t.Errorf("style.css:%d sizes type in px/vw/vh — use rem via the --t-*/--role-* tokens (DESIGN.md)", line)
	}
}

// TestCSSHexLiteralRatchet: raw hex colors belong in token declarations
// (:root / theme blocks / the dark remap), not sprinkled through component
// rules. This is a ratchet, not a ban: the count may go DOWN freely; going up
// means a new literal skipped the token system — prefer var(--c-*)/var(--type-*),
// or if a new token is genuinely needed, declare it and raise the pin in the
// same commit with a comment saying why.
func TestCSSHexLiteralRatchet(t *testing.T) {
	const pin = 179 // Phase 0 baseline
	css := readRepoFile(t, "internal/httpapi/templates/style.css")
	n := len(regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(css, -1))
	if n > pin {
		t.Errorf("style.css hex color literals = %d, ratchet pin %d — new colors go through tokens; "+
			"if adding a token, raise the pin deliberately in the same commit", n, pin)
	}
}

// TestNoHTMXResidue (Phase 2): htmx was removed — it was loaded but issued
// zero requests (no hx-* attribute ever existed). Nothing may reference it,
// and the partial-fragment header is ours (X-Cards-Partial), not HX-Request.
// The direct toast() path must keep existing — it is the only error surface.
func TestNoHTMXResidue(t *testing.T) {
	for name, src := range templateFiles(t) {
		lower := strings.ToLower(src)
		if strings.Contains(lower, "htmx") || strings.Contains(lower, "hx-request") {
			t.Errorf("%s references htmx — the dependency was removed in rebuild Phase 2", name)
		}
	}
	for name, src := range jsAssets(t) {
		if strings.Contains(src, "htmx") || strings.Contains(src, "HX-Request") {
			t.Errorf("%s references htmx/HX-Request — use X-Cards-Partial and direct toast() calls", name)
		}
	}
	ui := readRepoFile(t, "internal/httpapi/templates/assets/ui.js")
	if !strings.Contains(ui, "function toast(") {
		t.Error("ui.js lost the direct toast() error path — it is the only error-toast surface")
	}
	if !strings.Contains(ui, "'X-Cards-Partial'") {
		t.Error("ui.js no longer sends X-Cards-Partial — partial rendering (wantsPartial) would silently break")
	}
}

// TestVendoredAlpinePresent: the self-hosted Alpine build is embedded and the
// layout loads it (with the cache-busting stamp) — a missing or renamed asset
// would otherwise fail only at runtime in the browser.
func TestVendoredAlpinePresent(t *testing.T) {
	js := readRepoFile(t, "internal/httpapi/templates/assets/alpine.min.js")
	if len(js) < 20_000 {
		t.Errorf("alpine.min.js is suspiciously small (%d bytes) — vendoring broken?", len(js))
	}
	layout := readRepoFile(t, "internal/httpapi/templates/layout.html")
	if !strings.Contains(layout, `/ui/assets/alpine.min.js?v={{assetStamp}}`) {
		t.Error("layout.html does not load the self-hosted Alpine with the assetStamp cache-buster")
	}
	for _, asset := range []string{"helpers.js", "api.js", "ui.js", "components.js"} {
		if !strings.Contains(layout, "/ui/assets/"+asset+"?v={{assetStamp}}") {
			t.Errorf("layout.html does not load %s with the assetStamp cache-buster", asset)
		}
	}
}

// TestComboboxBlockIsTokenOnly (rebuild P5): the combobox rules read neutral +
// role tokens only — never --type-* hues (board inline styles override those
// and would render un-dark-adapted inside a themed board) and no raw hex
// (the global ratchet also covers this, but the message here is targeted).
func TestComboboxBlockIsTokenOnly(t *testing.T) {
	css := readRepoFile(t, "internal/httpapi/templates/style.css")
	start := strings.Index(css, ".combobox {")
	if start < 0 {
		t.Fatal("combobox block not found in style.css")
	}
	end := strings.Index(css[start:], ".combobox__empty")
	if end < 0 {
		t.Fatal("combobox block end marker not found")
	}
	block := css[start : start+end]
	if strings.Contains(block, "--type-") {
		t.Error("combobox rules reference --type-* hues — use neutral/role tokens (DESIGN.md)")
	}
	if m := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindString(block); m != "" {
		t.Errorf("combobox rules contain a raw hex literal (%s) — use tokens", m)
	}
}
