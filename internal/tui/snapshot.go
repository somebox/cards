// Snapshot — headless single-frame rendering for the TUI.
//
// The web UI gets its docs screenshots from headless Chrome
// (scripts/site-screenshots.sh); the TUI equivalent needs no terminal at
// all: the model renders to a plain string, so a capture is just "build the
// model, feed it a size (and optionally keys), render once". This file is
// that seam. It is consumed by cmd/tui-shot, which converts the ANSI frame
// to HTML for image capture (scripts/tui-screenshots.sh).
package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
)

// Snapshot renders one frame of the TUI at the given size without a
// terminal and returns it as ANSI-styled text. keys are fed through the
// normal Update loop first (printable runes only), so a capture can show
// modals and pickers — e.g. keys="f" opens the filter prompt.
func Snapshot(svc *core.Service, result *config.Result, actor string, width, height int, keys string) string {
	m := newModel(svc, result, actor)
	if m.sub != nil {
		defer svc.Bus().Unsubscribe(m.sub.ID)
	}
	var next tea.Model
	next, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	for _, r := range keys {
		next, _ = next.(model).Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return next.(model).renderView()
}
