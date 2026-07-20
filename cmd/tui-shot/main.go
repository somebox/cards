// Command tui-shot — dev tool behind scripts/tui-screenshots.sh; not part of
// the shipped product (that is ./cmd/cards). It renders one frame of the
// terminal UI against a workspace directory — no TTY, no server — and prints
// it either as raw ANSI text or as a standalone dark-terminal HTML page that
// headless Chrome turns into a PNG.
package main

import (
	"flag"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
	"github.com/somebox/cards/internal/tui"
)

func main() {
	var (
		workspace = flag.String("workspace", "", "workspace directory (holds definitions/ + work-cards.db)")
		actor     = flag.String("actor", "local-dev", "actor the TUI runs as")
		width     = flag.Int("width", 140, "terminal columns")
		height    = flag.Int("height", 40, "terminal rows")
		keys      = flag.String("keys", "", "keys to press before capture (printable runes, e.g. \"f\")")
		format    = flag.String("format", "html", "output format: html or ansi")
		title     = flag.String("title", "cards", "window title for the html chrome")
	)
	flag.Parse()
	if *workspace == "" {
		log.Fatal("tui-shot: -workspace is required")
	}

	result, err := config.New(*workspace).Load()
	if err != nil {
		log.Fatalf("load workspace: %v", err)
	}
	st, err := sqlite.Open(filepath.Join(*workspace, "work-cards.db"), result.Workspace)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, st)
	if am, err := artifacts.New(filepath.Join(*workspace, "artifacts")); err == nil {
		svc.SetArtifacts(am)
	}

	frame := tui.Snapshot(svc, result, *actor, *width, *height, *keys)
	switch *format {
	case "ansi":
		fmt.Print(frame)
	case "html":
		fmt.Print(page(*title, ansiToHTML(frame)))
	default:
		log.Fatalf("unknown -format %q (want html or ansi)", *format)
	}
	_ = os.Stdout.Sync()
}

// page wraps the converted frame in a minimal dark terminal window.
func page(title, body string) string {
	return `<!doctype html><meta charset="utf-8"><style>
  html { background: #e8eaed; }
  body { margin: 0; padding: 28px; display: inline-block; }
  .win { background: #15171c; border-radius: 10px; overflow: hidden;
         box-shadow: 0 12px 40px rgba(0,0,0,.35); display: inline-block; }
  .bar { display: flex; align-items: center; gap: 8px; padding: 10px 14px;
         background: #1e2128; font: 500 12px -apple-system, sans-serif; color: #8a919c; }
  .dot { width: 12px; height: 12px; border-radius: 50%; }
  pre  { margin: 0; padding: 14px 18px 18px;
         font: 13px/1.3 "SF Mono", Menlo, Consolas, monospace;
         color: #d0d6de; }
</style>
<div class="win">
  <div class="bar">
    <span class="dot" style="background:#ff5f57"></span>
    <span class="dot" style="background:#febc2e"></span>
    <span class="dot" style="background:#28c840"></span>
    <span style="margin-left:8px">` + html.EscapeString(title) + `</span>
  </div>
  <pre>` + body + `</pre>
</div>
`
}

// --- ANSI (SGR) → HTML ------------------------------------------------------

// state is the live SGR attribute set while scanning a frame.
type state struct {
	fg, bg                        string // css color, "" = default
	bold, faint, italic, ul, rev  bool
}

func (s state) span() (string, bool) {
	fg, bg := s.fg, s.bg
	if s.rev {
		if fg == "" {
			fg = "#d0d6de"
		}
		if bg == "" {
			bg = "#15171c"
		}
		fg, bg = bg, fg
	}
	var css []string
	if fg != "" {
		css = append(css, "color:"+fg)
	}
	if bg != "" {
		css = append(css, "background:"+bg)
	}
	if s.bold {
		css = append(css, "font-weight:700")
	}
	if s.faint {
		css = append(css, "opacity:.6")
	}
	if s.italic {
		css = append(css, "font-style:italic")
	}
	if s.ul {
		css = append(css, "text-decoration:underline")
	}
	if len(css) == 0 {
		return "", false
	}
	return `<span style="` + strings.Join(css, ";") + `">`, true
}

// palette maps the 16 base SGR colors to a dark-terminal scheme.
var palette = [16]string{
	"#282c34", "#e06c75", "#98c379", "#e5c07b", "#61afef", "#c678dd", "#56b6c2", "#c8ccd4",
	"#5c6370", "#ff7a85", "#a9d47f", "#f0ca85", "#74baf7", "#d48ae8", "#6cc8d4", "#ffffff",
}

// xterm256 converts a 256-color index to css.
func xterm256(n int) string {
	if n < 16 {
		return palette[n]
	}
	if n < 232 { // 6×6×6 cube
		n -= 16
		steps := []int{0, 95, 135, 175, 215, 255}
		r, g, b := steps[n/36], steps[(n/6)%6], steps[n%6]
		return fmt.Sprintf("#%02x%02x%02x", r, g, b)
	}
	v := 8 + (n-232)*10 // greyscale ramp
	return fmt.Sprintf("#%02x%02x%02x", v, v, v)
}

// applySGR folds one ESC[...m parameter list into st.
func applySGR(st *state, params []int) {
	for i := 0; i < len(params); i++ {
		p := params[i]
		switch {
		case p == 0:
			*st = state{}
		case p == 1:
			st.bold = true
		case p == 2:
			st.faint = true
		case p == 3:
			st.italic = true
		case p == 4:
			st.ul = true
		case p == 7:
			st.rev = true
		case p == 22:
			st.bold, st.faint = false, false
		case p == 23:
			st.italic = false
		case p == 24:
			st.ul = false
		case p == 27:
			st.rev = false
		case p >= 30 && p <= 37:
			st.fg = palette[p-30]
		case p == 39:
			st.fg = ""
		case p >= 40 && p <= 47:
			st.bg = palette[p-40]
		case p == 49:
			st.bg = ""
		case p >= 90 && p <= 97:
			st.fg = palette[p-90+8]
		case p >= 100 && p <= 107:
			st.bg = palette[p-100+8]
		case p == 38 || p == 48:
			color, skip := extColor(params[i+1:])
			if color == "" {
				return // malformed; drop the rest of this sequence
			}
			if p == 38 {
				st.fg = color
			} else {
				st.bg = color
			}
			i += skip
		}
	}
}

// extColor parses the tail of a 38/48 extended-color sequence:
// "5;n" (256-color) or "2;r;g;b" (truecolor). Returns css + params consumed.
func extColor(rest []int) (string, int) {
	if len(rest) >= 2 && rest[0] == 5 {
		return xterm256(rest[1]), 2
	}
	if len(rest) >= 4 && rest[0] == 2 {
		return fmt.Sprintf("#%02x%02x%02x", rest[1]&0xff, rest[2]&0xff, rest[3]&0xff), 4
	}
	return "", 0
}

// ansiToHTML converts SGR-styled text to span-styled HTML. Non-SGR escape
// sequences (cursor movement, OSC) are stripped; renderView emits none, but
// stripping keeps the converter safe against future styling changes.
func ansiToHTML(s string) string {
	var out strings.Builder
	var st state
	emit := func(text string) {
		if text == "" {
			return
		}
		tag, styled := st.span()
		if styled {
			out.WriteString(tag)
		}
		out.WriteString(html.EscapeString(text))
		if styled {
			out.WriteString("</span>")
		}
	}

	for len(s) > 0 {
		esc := strings.IndexByte(s, 0x1b)
		if esc < 0 {
			emit(s)
			break
		}
		emit(s[:esc])
		s = s[esc:]
		switch {
		case strings.HasPrefix(s, "\x1b["): // CSI
			end := strings.IndexFunc(s[2:], func(r rune) bool { return r >= 0x40 && r <= 0x7e })
			if end < 0 {
				return out.String() // truncated sequence at EOF
			}
			body, final := s[2:2+end], s[2+end]
			if final == 'm' {
				var params []int
				for _, part := range strings.Split(body, ";") {
					n, err := strconv.Atoi(part)
					if err != nil {
						n = 0
					}
					params = append(params, n)
				}
				if body == "" {
					params = []int{0}
				}
				applySGR(&st, params)
			}
			s = s[2+end+1:]
		case strings.HasPrefix(s, "\x1b]"): // OSC ... BEL or ST
			if i := strings.IndexAny(s, "\a"); i >= 0 {
				s = s[i+1:]
			} else if i := strings.Index(s, "\x1b\\"); i >= 0 {
				s = s[i+2:]
			} else {
				return out.String()
			}
		default: // lone ESC + one byte
			if len(s) > 1 {
				s = s[2:]
			} else {
				s = ""
			}
		}
	}
	return out.String()
}
