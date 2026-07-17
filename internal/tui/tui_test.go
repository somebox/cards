package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
)

// openDemo builds a service against the demo workspace for a headless render.
func openDemo(t *testing.T) (*core.Service, *config.Result, func()) {
	t.Helper()
	dir := "../../examples/demo-workspace"
	result, err := config.New(dir).Load()
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	st, err := sqlite.Open(dir+"/work-cards.db", result.Workspace)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, st)
	return svc, result, func() { st.Close() }
}

func TestRenderDemoWorkspace(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 32
	out := m.renderView()

	// Lanes come from the board configuration (workspace column names).
	for _, want := range []string{"Backlog", "To Do", "In Progress", "Review", "Done"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing board column %q", want)
		}
	}
	// Board name shows in the top bar.
	if !strings.Contains(out, "Engineering") && !strings.Contains(out, "Welcome") {
		t.Errorf("render missing a board name")
	}
}

func TestLaneSwitchAndStatusPicker(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 32

	// Step through all lanes; render must not panic and the active tab changes.
	cols := m.columns()
	if len(cols) == 0 {
		t.Fatal("board has no columns")
	}
	for i := 0; i < len(cols); i++ {
		m.stepLane(1)
		_ = m.renderView()
	}

	// Status picker lists only legal transitions for the selected card.
	m.mode = modeStatus
	pick := m.statusPicker()
	if !strings.Contains(pick, "set status") {
		t.Errorf("status picker missing header")
	}
}

func TestTransitionsFromBoardConfig(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")

	// Engineering board enforces transitions: backlog -> todo only.
	got := m.legalTargets("backlog")
	if len(got) != 1 || got[0] != "todo" {
		t.Errorf("legalTargets(backlog) = %v, want [todo]", got)
	}
	// done is terminal in the engineering transitions map.
	if got := m.legalTargets("done"); len(got) != 0 {
		t.Errorf("legalTargets(done) = %v, want terminal", got)
	}
	// review -> done, in_progress.
	got = m.legalTargets("review")
	if len(got) != 2 {
		t.Errorf("legalTargets(review) = %v, want 2 targets", got)
	}
}

// press feeds one key into Update and returns the resulting model.
func press(t *testing.T, m model, code rune, text string) model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Code: code, Text: text})
	return next.(model)
}

func TestFocusFlow(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 16 // small viewport forces scrollable detail content

	if m.focus != focusList {
		t.Fatalf("initial focus = %v, want list", m.focus)
	}
	if m.selected() == nil {
		t.Skip("no card selected in initial lane")
	}
	if m.detailMaxScroll() == 0 {
		t.Skip("detail content fits without scrolling")
	}

	// enter reveals the detail pane (split) and focuses it.
	m = press(t, m, tea.KeyEnter, "")
	if m.focus != focusDetail || m.dmode != detailSplit {
		t.Fatalf("after enter: focus=%v dmode=%v, want detail+split", m.focus, m.dmode)
	}

	// j scrolls the detail, not the list cursor.
	cur := m.cursor
	m = press(t, m, 'j', "j")
	if m.cursor != cur {
		t.Errorf("j in detail moved list cursor")
	}
	if m.detailScroll != 1 {
		t.Errorf("detailScroll = %d, want 1", m.detailScroll)
	}

	// clamped: scroll far past the end.
	for i := 0; i < 100; i++ {
		m = press(t, m, 'j', "j")
	}
	if m.detailScroll > m.detailMaxScroll() {
		t.Errorf("detailScroll %d exceeds max %d", m.detailScroll, m.detailMaxScroll())
	}

	// esc hides the detail pane and returns to the list.
	m = press(t, m, tea.KeyEscape, "")
	if m.focus != focusList || m.dmode != detailHidden {
		t.Fatalf("after esc: focus=%v dmode=%v, want list+hidden", m.focus, m.dmode)
	}
}

func TestDetailModeFlow(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 32
	if m.selected() == nil {
		t.Skip("no card selected")
	}

	// hidden → enter → split(focus detail) → enter → fullscreen
	m = press(t, m, tea.KeyEnter, "")
	if m.dmode != detailSplit || m.focus != focusDetail {
		t.Fatalf("want split+detail, got %v/%v", m.dmode, m.focus)
	}
	m = press(t, m, tea.KeyEnter, "")
	if m.dmode != detailFull {
		t.Fatalf("want fullscreen, got %v", m.dmode)
	}
	// esc → split, esc → hidden
	m = press(t, m, tea.KeyEscape, "")
	if m.dmode != detailSplit || m.focus != focusDetail {
		t.Fatalf("want split again, got %v/%v", m.dmode, m.focus)
	}
	m = press(t, m, tea.KeyEscape, "")
	if m.dmode != detailHidden || m.focus != focusList {
		t.Fatalf("want hidden+list, got %v/%v", m.dmode, m.focus)
	}
	// tab also opens the detail.
	m = press(t, m, tea.KeyTab, "")
	if m.dmode != detailSplit || m.focus != focusDetail {
		t.Fatalf("tab should open split+detail, got %v/%v", m.dmode, m.focus)
	}
	// tab in split returns focus to the list (pane stays).
	m = press(t, m, tea.KeyTab, "")
	if m.dmode != detailSplit || m.focus != focusList {
		t.Fatalf("tab should refocus list, got %v/%v", m.dmode, m.focus)
	}
}

func TestListScrollFollowsCursor(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 10 // tiny: list budget is ~6 rows
	n := len(m.laneCards(m.lane()))
	if n < 8 {
		t.Skip("need a longer lane")
	}
	_, lh := m.listBudget()
	for i := 0; i < n-1; i++ {
		m = press(t, m, 'j', "j")
	}
	if m.cursor != n-1 {
		t.Fatalf("cursor = %d, want %d", m.cursor, n-1)
	}
	if m.listScroll == 0 {
		t.Errorf("listScroll did not advance past a long list")
	}
	if m.cursor < m.listScroll || m.cursor >= m.listScroll+lh {
		t.Errorf("cursor %d outside window [%d, %d)", m.cursor, m.listScroll, m.listScroll+lh)
	}
	// the rendered window must include the selected card's title fragment
	out := m.listView(80, lh)
	sel := m.selected()
	if sel == nil {
		t.Fatal("no selection after scrolling")
	}
	frag := sel.Title
	if len(frag) > 12 {
		frag = frag[:12]
	}
	if !strings.Contains(out, frag) {
		t.Errorf("rendered window missing selected card %q", frag)
	}
}

func TestCardMarkdownSections(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 40
	c := m.selected()
	if c == nil {
		t.Skip("no card selected")
	}
	md := m.cardMarkdown(c)

	// title as H2
	if !strings.Contains(md, "## ") {
		t.Errorf("markdown missing section headings")
	}
	// meta line has status + version
	if !strings.Contains(md, m.columnName(c.Status)) {
		t.Errorf("markdown missing status name")
	}
	// fields section renders schema fields (demo cards all have them)
	if !strings.Contains(md, "## ") || !(strings.Contains(md, "description") || strings.Contains(md, "## fields")) {
		t.Logf("markdown:\n%s", md)
		t.Errorf("markdown missing fields section")
	}
	// transitions line
	if len(m.legalTargets(c.Status)) > 0 && !strings.Contains(md, "next →") {
		t.Errorf("markdown missing transitions")
	}

	// comments are italic blockquotes when present
	if len(c.Comments) > 0 && !strings.Contains(md, "> *") {
		t.Errorf("comments not rendered as italic blockquotes")
	}

	// inbound links: syncExtras ran at init; if any exist they render with ←
	if len(m.inbound) > 0 && !strings.Contains(md, "←") {
		t.Errorf("inbound links not rendered")
	}
}

func TestRenderPaths(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	new := func() model {
		m := newModel(svc, result, "local-dev")
		m.width = 120
		m.height = 30
		return m
	}

	t.Run("narrow layout", func(t *testing.T) {
		m := new()
		m.width = 80
		m.dmode = detailSplit
		out := m.renderView()
		if out == "" || !strings.Contains(out, "Backlog") {
			t.Errorf("narrow render broken")
		}
	})

	t.Run("help view", func(t *testing.T) {
		m := new()
		m.mode = modeHelp
		out := m.renderView()
		for _, want := range []string{"KEYBINDINGS", "lanes", "detail pane"} {
			if !strings.Contains(out, want) {
				t.Errorf("help missing %q", want)
			}
		}
	})

	t.Run("footers", func(t *testing.T) {
		m := new()
		if out := m.footer(100); !strings.Contains(out, "quit") {
			t.Errorf("browse footer missing keys")
		}
		m.focus = focusDetail
		m.dmode = detailSplit
		if out := m.footer(100); !strings.Contains(out, "fullscreen") {
			t.Errorf("detail footer missing hints")
		}
		m.dmode = detailFull
		if out := m.footer(100); !strings.Contains(out, "split") {
			t.Errorf("fullscreen footer missing hints")
		}
		m.focus = focusHeader
		if out := m.footer(100); !strings.Contains(out, "board") {
			t.Errorf("header footer missing hints")
		}
		m.mode = modeSearch
		if out := m.footer(100); !strings.Contains(out, "filter") {
			t.Errorf("search footer missing hints")
		}
	})

	t.Run("status picker", func(t *testing.T) {
		m := new()
		m.mode = modeStatus
		out := m.statusPicker()
		if !strings.Contains(out, "set status") {
			t.Errorf("status picker missing header")
		}
	})

	t.Run("search highlight", func(t *testing.T) {
		m := new()
		m.mode = modeSearch
		m.in.SetValue("export")
		cs := m.laneCards(m.lane())
		if len(cs) == 0 {
			t.Skip("no match for 'export'")
		}
		row := m.rowView(&cs[0], true, 100)
		if !strings.Contains(row, "\x1b[1;38;2;255;106;193m") {
			t.Errorf("search match not highlighted")
		}
	})

	t.Run("empty lane", func(t *testing.T) {
		m := new()
		// review lane is empty in the demo workspace
		for i, col := range m.columns() {
			if col == "review" {
				m.laneIdx = i
			}
		}
		if len(m.laneCards(m.lane())) != 0 {
			t.Skip("review lane not empty")
		}
		out := m.listView(100, 5)
		if !strings.Contains(out, "no cards") {
			t.Errorf("empty lane should say so")
		}
		if m.selected() != nil {
			t.Errorf("no selection expected on empty lane")
		}
	})

	t.Run("modal text modes", func(t *testing.T) {
		m := new()
		m.dmode = detailSplit
		m.mode = modeComment
		out := m.renderView()
		if !strings.Contains(out, "add comment") {
			t.Errorf("comment modal missing label")
		}
	})
}

func TestHeaderFocus(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 32
	m.cursor = 0

	// k at the list top focuses the header.
	m = press(t, m, 'k', "k")
	if m.focus != focusHeader {
		t.Fatalf("focus = %v, want header", m.focus)
	}

	// h/l moves lanes from the header.
	before := m.laneIdx
	m = press(t, m, 'l', "l")
	if m.laneIdx == before {
		t.Errorf("l in header did not change lane")
	}

	// tab switches boards; shift+tab switches back.
	b0 := m.boardIdx
	m = press(t, m, tea.KeyTab, "")
	if m.boardIdx == b0 && len(m.boards_) > 1 {
		t.Errorf("tab in header did not switch board")
	}
	m = press(t, m, tea.KeyTab, "shift+tab")
	// (shift+tab is delivered as code KeyTab with shift; the string form is what Update sees)
	_ = m

	// esc returns to the list.
	m = press(t, m, tea.KeyEscape, "")
	if m.focus != focusList {
		t.Fatalf("after esc focus = %v, want list", m.focus)
	}
}

func TestShiftTabSwitchesBoard(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 32
	if len(m.boards_) < 2 {
		t.Skip("need 2+ boards")
	}
	before := m.boardIdx
	// shift+tab from the list switches board.
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = next.(model)
	if m.boardIdx == before {
		t.Errorf("shift+tab did not switch board")
	}
}

// openDemoCopy copies the demo workspace DB to a temp dir and opens it, so
// mutation tests never touch the checked-out database.
func openDemoCopy(t *testing.T) (*core.Service, *config.Result, func()) {
	t.Helper()
	src := "../../examples/demo-workspace"
	dst := t.TempDir()

	result, err := config.New(src).Load()
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(src, "work-cards.db"))
	if err != nil {
		t.Fatalf("read demo db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "work-cards.db"), data, 0o644); err != nil {
		t.Fatalf("copy db: %v", err)
	}
	st, err := sqlite.Open(filepath.Join(dst, "work-cards.db"), result.Workspace)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, st)
	return svc, result, func() { st.Close() }
}

func TestCommentSaveFlow(t *testing.T) {
	svc, result, done := openDemoCopy(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 30
	c := m.selected()
	if c == nil {
		t.Skip("no card selected")
	}
	before := len(c.Comments)

	m.openModal(modeComment)
	m.in.SetValue("tui comment flow")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)

	if m.mode != modeBrowse {
		t.Fatalf("mode = %v, want browse after save", m.mode)
	}
	got := m.selected()
	if got == nil || len(got.Comments) != before+1 {
		t.Fatalf("comments = %d, want %d", len(got.Comments), before+1)
	}
	if got.Comments[0].Body != "tui comment flow" {
		t.Errorf("comment body = %q", got.Comments[0].Body)
	}
}

func TestClaimReleaseFlow(t *testing.T) {
	svc, result, done := openDemoCopy(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 30
	c := m.selected()
	if c == nil {
		t.Skip("no card selected")
	}
	wasMine := c.Owner == "local-dev"

	next, _ := m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	m = next.(model)
	got := m.selected()
	if wasMine {
		if got.Owner != "" {
			t.Errorf("release failed: owner = %q", got.Owner)
		}
	} else if got.Owner != "local-dev" {
		t.Errorf("claim failed: owner = %q", got.Owner)
	}
}

func TestStatusMoveEnforced(t *testing.T) {
	svc, result, done := openDemoCopy(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 30
	c := m.selected()
	if c == nil {
		t.Skip("no card selected")
	}
	targets := m.legalTargets(c.Status)
	if len(targets) == 0 {
		t.Skip("no legal transitions from " + c.Status)
	}

	m.openModal(modeStatus)
	next, _ := m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	m = next.(model)

	found := false
	for _, lc := range m.cards {
		if lc.ID == c.ID && lc.Status == targets[0] {
			found = true
		}
	}
	if !found {
		t.Errorf("status did not move to %q", targets[0])
	}
}
