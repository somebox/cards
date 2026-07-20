package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/seed"
	"github.com/somebox/cards/internal/sqlite"
	"github.com/somebox/cards/internal/uioptions"
)

// openDemo builds a service against a COPY of the demo workspace for a
// headless render. It snapshots like openDemoCopy because content assertions
// (e.g. the selected card's inbound links) break when the live board moves —
// the dogfooding server mutates work-cards.db between runs (found when a
// sprint's card updates flipped TestCardMarkdownSections red).
func openDemo(t *testing.T) (*core.Service, *config.Result, func()) {
	t.Helper()
	return openDemoCopy(t)
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

// claimCards assigns up to n seeded cards to local-dev (seed material ships
// every card unowned) so owner-filter tests have a deterministic slice to
// narrow to. Returns the owner id.
func claimCards(t *testing.T, svc *core.Service, n int) string {
	t.Helper()
	ctx := context.Background()
	page, err := svc.ListCards(ctx, core.CardQuery{Limit: n})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("list seeded cards: %v (%d found)", err, len(page.Items))
	}
	owner := "local-dev"
	for i := range page.Items {
		c := page.Items[i]
		if _, err := svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Owner: &owner, Actor: owner}); err != nil {
			t.Fatalf("claim %s: %v", c.ID, err)
		}
	}
	return owner
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
	// fields section renders schema fields: at least one of the selected
	// card's own field ids must appear as a section heading (seed cards may
	// lead with a research-goal, whose only field is hypothesis).
	if fields, ok := c.Fields.(map[string]any); ok && len(fields) > 0 {
		found := false
		for id := range fields {
			if strings.Contains(md, "## "+id) {
				found = true
				break
			}
		}
		if !found {
			t.Logf("markdown:\n%s", md)
			t.Errorf("markdown missing a schema-field section")
		}
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
	// Definitions come from the committed demo workspace; the DB is built
	// fresh from the seed material in a temp dir. The local
	// examples/demo-workspace/work-cards.db is gitignored and machine-local,
	// so copying it worked on a dev machine and always failed in CI.
	result, err := config.New("../../examples/demo-workspace").Load()
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "work-cards.db"), result.Workspace)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	svc := core.NewService(result.Workspace, result.CardTypes, result.Boards, st)
	if err := seed.IfEmpty(context.Background(), st, svc, result.Workspace); err != nil {
		st.Close()
		t.Fatalf("seed demo db: %v", err)
	}
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
	// Comments come back oldest-first (ORDER BY created_at); the new comment
	// is the last. (Do not assert [0] — the selected card may already carry
	// comments from the dogfood board.)
	if got.Comments[len(got.Comments)-1].Body != "tui comment flow" {
		t.Errorf("comment body = %q", got.Comments[len(got.Comments)-1].Body)
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

// ── sort / filter parity (sprint 2026-07-19 P4) ────────────────────────────

// TestSortDirectiveOrdersCards: m.sort composes into refresh's query and the
// store orders server-side.
func TestSortDirectiveOrdersCards(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.sort = "-created_at"
	m.refresh(m.ctx())
	if m.loadErr != "" {
		t.Fatalf("refresh: %s", m.loadErr)
	}
	if len(m.cards) < 2 {
		t.Skip("need multiple cards on the board")
	}
	for i := 1; i < len(m.cards); i++ {
		if m.cards[i-1].CreatedAt.Before(m.cards[i].CreatedAt) {
			t.Fatalf("-created_at: cards[%d] %v < cards[%d] %v", i-1, m.cards[i-1].CreatedAt, i, m.cards[i].CreatedAt)
		}
	}
	if m.cards[0].CreatedAt.Before(m.cards[len(m.cards)-1].CreatedAt) {
		t.Errorf("first %v < last %v", m.cards[0].CreatedAt, m.cards[len(m.cards)-1].CreatedAt)
	}

	m.sort = "created_at"
	m.refresh(m.ctx())
	for i := 1; i < len(m.cards); i++ {
		if m.cards[i].CreatedAt.Before(m.cards[i-1].CreatedAt) {
			t.Fatalf("created_at: cards[%d] %v < cards[%d] %v", i, m.cards[i].CreatedAt, i-1, m.cards[i-1].CreatedAt)
		}
	}

	m.sort = "title"
	m.refresh(m.ctx())
	// The store orders titles COLLATE NOCASE (sqlite.orderClause).
	for i := 1; i < len(m.cards); i++ {
		prev, cur := strings.ToLower(m.cards[i-1].Title), strings.ToLower(m.cards[i].Title)
		if cur < prev {
			t.Fatalf("title: cards[%d] %q < cards[%d] %q", i, m.cards[i].Title, i-1, m.cards[i-1].Title)
		}
	}
}

// TestMeSubstitutionFilter is the MAJOR regression for P4: `owner == me` in
// the filter modal must resolve `me` to the TUI actor (the same substitution
// the web UI applies to saved filters), returning only the actor's cards.
func TestMeSubstitutionFilter(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	// Seeded cards ship unowned, so give the actor a deterministic slice to
	// filter: claim two cards for local-dev (a seeded user). The filter must
	// then provably narrow (not vacuously empty, not everything).
	actor := claimCards(t, svc, 2)

	m := newModel(svc, result, actor)
	m.filter = "owner == me"
	m.refresh(m.ctx())
	if m.loadErr != "" {
		t.Fatalf("refresh: %s", m.loadErr)
	}
	if len(m.cards) == 0 {
		t.Fatal("owner == me returned no cards for the actor")
	}
	for _, c := range m.cards {
		if c.Owner != m.actor {
			t.Errorf("card %s owner = %q, want only %q", c.ID, c.Owner, m.actor)
		}
	}

	// And via the query's Owner field (the lifted term path).
	m2 := newModel(svc, result, actor)
	if err := m2.setFilter("owner:me"); err != nil {
		t.Fatalf("setFilter: %v", err)
	}
	if m2.owner != "me" || m2.filter != "" {
		t.Fatalf("setFilter lifted owner = %q filter = %q, want owner %q filter %q", m2.owner, m2.filter, "me", "")
	}
	m2.refresh(m2.ctx())
	if len(m2.cards) == 0 {
		t.Fatal("owner:me returned no cards for the actor")
	}
	for _, c := range m2.cards {
		if c.Owner != m2.actor {
			t.Errorf("card %s owner = %q, want only %q", c.ID, c.Owner, m2.actor)
		}
	}
}

// TestFilterSortDirectivesSurviveRefresh pins the invariant that active
// directives live on the model, so a refresh that rebuilds the query from
// scratch (every bus-driven live update goes through refresh) preserves
// them. Cross-referenced from docs/design/tui-bus-disposition.md.
func TestFilterSortDirectivesSurviveRefresh(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	actor := claimCards(t, svc, 2)
	m := newModel(svc, result, actor)
	m.sort = "-created_at"
	m.filter = "owner == me"
	m.refresh(m.ctx())
	if len(m.cards) == 0 {
		t.Skip("no cards for directive combo")
	}
	first := make([]string, len(m.cards))
	for i, c := range m.cards {
		first[i] = c.ID
	}

	// Simulate a burst of bus-driven re-fetches.
	for i := 0; i < 3; i++ {
		m.refresh(m.ctx())
	}
	if m.sort != "-created_at" || m.filter != "owner == me" {
		t.Fatalf("directives mutated: sort=%q filter=%q", m.sort, m.filter)
	}
	if len(m.cards) != len(first) {
		t.Fatalf("card count changed across refresh: %d → %d", len(first), len(m.cards))
	}
	for i, c := range m.cards {
		if c.ID != first[i] {
			t.Fatalf("order changed at %d: %s → %s (sort directive lost?)", i, first[i], c.ID)
		}
		if c.Owner != m.actor {
			t.Fatalf("card %s owner %q slipped past the surviving filter", c.ID, c.Owner)
		}
	}
}

// TestFilterModal exercises the modal behavior: `f` opens the filter modal
// while `/` still opens find; setFilter lifts owner terms and validates;
// enter applies and empty clears.
func TestFilterModal(t *testing.T) {
	svc, result, done := openDemoCopy(t)
	defer done()

	// Claim every seeded card so the bug-tagged one is owned by the actor —
	// the combined owner+tag narrowing below must not be vacuously empty.
	actor := claimCards(t, svc, 500)
	m := newModel(svc, result, actor)
	m.width = 120
	m.height = 30

	// `/` is find, `f` is the filter modal — separate bindings.
	m2 := press(t, m, '/', "/")
	if m2.mode != modeSearch {
		t.Errorf("/ mode = %v, want modeSearch", m2.mode)
	}
	m2 = press(t, m, 'f', "f")
	if m2.mode != modeFilter {
		t.Fatalf("f mode = %v, want modeFilter", m2.mode)
	}

	// setFilter: owner-eq lifts into m.owner; the rest stays as filter text.
	if err := m.setFilter("owner:me, tag:bug"); err != nil {
		t.Fatalf("setFilter: %v", err)
	}
	if m.owner != "me" || m.filter != "tag:bug" {
		t.Errorf("setFilter = owner %q filter %q, want owner:me + tag:bug", m.owner, m.filter)
	}
	if got := m.filterExpression(); got != "owner:me, tag:bug" {
		t.Errorf("filterExpression = %q", got)
	}

	// Bad terms are rejected without touching the committed directives.
	if err := m.setFilter("not a term at all"); err == nil {
		t.Error("setFilter(bad) = nil error, want validation failure")
	}
	if m.owner != "me" || m.filter != "tag:bug" {
		t.Error("rejected setFilter mutated the active directives")
	}

	// Enter applies the modal text and narrows server-side: every returned
	// card carries the bug tag and the actor as owner.
	m = press(t, m, 'f', "f")
	m.in.SetValue("owner:me, tag:bug")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeBrowse {
		t.Fatalf("mode = %v, want browse after apply", m.mode)
	}
	if m.loadErr != "" {
		t.Fatalf("refresh: %s", m.loadErr)
	}
	if len(m.cards) == 0 {
		t.Fatal("no bug-tagged owned cards after claiming the seeded board")
	}
	for _, c := range m.cards {
		if c.Owner != actor {
			t.Errorf("card %s owner = %q, want %q", c.ID, c.Owner, actor)
		}
		tagged := false
		for _, tg := range c.Tags {
			if tg == "bug" {
				tagged = true
			}
		}
		if !tagged {
			t.Errorf("card %s tags = %v, want bug", c.ID, c.Tags)
		}
	}

	// Empty text clears the directives.
	if err := m.setFilter(""); err != nil {
		t.Fatalf("setFilter(clear): %v", err)
	}
	if m.owner != "" || m.filter != "" {
		t.Errorf("clear left owner %q filter %q", m.owner, m.filter)
	}
}

// TestSortCycle: F cycles the shared preset list (uioptions.SortOptions — the
// same options the web board header renders) and refresh honors each one.
func TestSortCycle(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 30

	shared := uioptions.SortOptions("", m.board())
	if len(shared) == 0 {
		t.Fatal("no shared sort options")
	}
	// From the default (-updated_at), the first F lands on the next shared
	// preset ("Newest"); a full lap returns to the start.
	var seen []string
	for i := 0; i < len(shared); i++ {
		m = press(t, m, 'F', "F")
		if m.loadErr != "" {
			t.Fatalf("refresh after cycle: %s", m.loadErr)
		}
		seen = append(seen, m.sort)
	}
	if seen[0] != shared[1].Value {
		t.Errorf("first F sort = %q, want next shared preset %q", seen[0], shared[1].Value)
	}
	for i, v := range seen {
		want := shared[(i+1)%len(shared)].Value
		if v != want {
			t.Errorf("cycle[%d] = %q, want %q (shared preset order)", i, v, want)
		}
	}
	// The cycled set is exactly the shared preset set — parity by construction.
	for _, v := range seen {
		found := false
		for _, o := range shared {
			if o.Value == v {
				found = true
			}
		}
		if !found {
			t.Errorf("cycled to %q, not in uioptions.SortOptions", v)
		}
	}
}

// TestTypeCycle: T narrows to each of the board's card types in turn, then
// wraps back to all types; refresh honors the narrow.
func TestTypeCycle(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	m := newModel(svc, result, "local-dev")
	m.width = 120
	m.height = 30
	b := m.board()
	if len(b.CardTypeIDs) == 0 {
		t.Skip("board has no card types")
	}

	m = press(t, m, 'T', "T")
	if m.typeID != b.CardTypeIDs[0] {
		t.Fatalf("first T typeID = %q, want %q", m.typeID, b.CardTypeIDs[0])
	}
	if m.loadErr != "" {
		t.Fatalf("refresh: %s", m.loadErr)
	}
	for _, c := range m.cards {
		if c.TypeID != m.typeID {
			t.Errorf("card %s type = %q, want only %q", c.ID, c.TypeID, m.typeID)
		}
	}
	// Cycle the rest of the way around: back to all types.
	for i := 0; i < len(b.CardTypeIDs); i++ {
		m = press(t, m, 'T', "T")
	}
	if m.typeID != "" {
		t.Errorf("after full cycle typeID = %q, want all types", m.typeID)
	}
}

// TestBadDirectivesSurfaceErrors: the TUI is serverless — there is no HTTP
// 422. A bad sort key or filter DSL must come back as a *core.Error from
// ListCards (or filter compile) and surface via notifyErr (flash) plus the
// persistent loadErr line.
func TestBadDirectivesSurfaceErrors(t *testing.T) {
	svc, result, done := openDemo(t)
	defer done()

	t.Run("bad sort key", func(t *testing.T) {
		m := newModel(svc, result, "local-dev")
		m.sort = "bogus"
		m.refresh(m.ctx())
		if m.loadErr == "" {
			t.Error("bad sort: loadErr empty, want the ParseSort validation error")
		}
		if m.flash == "" {
			t.Error("bad sort: no notifyErr flash")
		}
	})

	t.Run("unparseable filter term", func(t *testing.T) {
		m := newModel(svc, result, "local-dev")
		m.filter = "utter garbage!!"
		m.refresh(m.ctx())
		if m.loadErr == "" || m.flash == "" {
			t.Errorf("bad filter term: loadErr %q flash %q", m.loadErr, m.flash)
		}
	})

	t.Run("invalid DSL reaches the store compiler", func(t *testing.T) {
		m := newModel(svc, result, "local-dev")
		// $has is only valid on fields.<id> paths; the store compiler rejects
		// it on a core column — proving DSL errors surface from ListCards
		// itself, not just the modal's pre-parse.
		m.filter = `{"status": {"$has": "x"}}`
		m.refresh(m.ctx())
		if m.loadErr == "" || m.flash == "" {
			t.Errorf("store-level DSL error: loadErr %q flash %q", m.loadErr, m.flash)
		}
	})
}

// TestCompileFilterText pins the filter-text grammar: terms, the type alias,
// numeric coercion, the JSON escape hatch, saved-filter ids, and the shared
// `me` substitution.
func TestCompileFilterText(t *testing.T) {
	board := &core.Board{
		Presentation: &core.BoardPresentation{
			Filters: []core.BoardFilter{{
				ID:     "mine-open",
				Label:  "My open",
				Filter: map[string]any{"owner": map[string]any{"$eq": "me"}, "status": map[string]any{"$nin": []any{"done"}}},
			}},
		},
	}

	t.Run("empty", func(t *testing.T) {
		f, err := compileFilterText("  ", board, "jeremy")
		if err != nil || f != nil {
			t.Errorf("empty = %v, %v", f, err)
		}
	})

	t.Run("terms AND together", func(t *testing.T) {
		f, err := compileFilterText("tag:bug, status != done", board, "jeremy")
		if err != nil {
			t.Fatalf("%v", err)
		}
		and, ok := f["$and"].([]any)
		if !ok || len(and) != 2 {
			t.Fatalf("want $and of 2, got %v", f)
		}
	})

	t.Run("type aliases to type_id", func(t *testing.T) {
		f, err := compileFilterText("type:task", board, "jeremy")
		if err != nil {
			t.Fatalf("%v", err)
		}
		if _, ok := f["type_id"]; !ok {
			t.Errorf("type: did not compile to type_id: %v", f)
		}
	})

	t.Run("range ops coerce numerics", func(t *testing.T) {
		f, err := compileFilterText("fields.priority >= 2", board, "jeremy")
		if err != nil {
			t.Fatalf("%v", err)
		}
		got := f["fields.priority"].(map[string]any)["$gte"]
		if got != float64(2) {
			t.Errorf("$gte = %#v, want float64(2)", got)
		}
	})

	t.Run("me substitution on identity keys", func(t *testing.T) {
		f, err := compileFilterText("owner == me", board, "jeremy")
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got := f["owner"].(map[string]any)["$eq"]; got != "jeremy" {
			t.Errorf("owner.$eq = %v, want jeremy", got)
		}
	})

	t.Run("saved filter id resolves with me substitution", func(t *testing.T) {
		f, err := compileFilterText("mine-open", board, "jeremy")
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got := f["owner"].(map[string]any)["$eq"]; got != "jeremy" {
			t.Errorf("saved owner.$eq = %v, want jeremy", got)
		}
		if _, ok := f["status"]; !ok {
			t.Errorf("saved filter lost its status term: %v", f)
		}
	})

	t.Run("JSON pass-through", func(t *testing.T) {
		f, err := compileFilterText(`{"owner": {"$in": ["me", "alice"]}}`, board, "jeremy")
		if err != nil {
			t.Fatalf("%v", err)
		}
		in := f["owner"].(map[string]any)["$in"].([]any)
		if in[0] != "jeremy" || in[1] != "alice" {
			t.Errorf("$in = %v, want [jeremy alice]", in)
		}
	})

	t.Run("bad JSON and bad terms are loud", func(t *testing.T) {
		if _, err := compileFilterText("{not json", board, "jeremy"); err == nil {
			t.Error("bad JSON accepted")
		}
		if _, err := compileFilterText("no operator here", board, "jeremy"); err == nil {
			t.Error("bad term accepted")
		}
	})
}
