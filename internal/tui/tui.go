// Package tui is an interactive terminal UI for a cards workspace, built on
// bubbletea v2 + lipgloss v2. It talks directly to the core service layer
// (the same surface the HTTP router and CLI use), so it works without a
// running server; live updates come from the in-process event bus.
//
// Interaction model: a board's columns are TABS (h/l switches lanes). The
// card detail is a markdown document (glamour-rendered) with three
// visibility states — hidden (list only), split (list + detail), fullscreen
// (detail only):
//
//	enter on a card  → split, focus detail
//	enter again      → detail fullscreen
//	esc              → back to split
//	esc again        → detail hidden, list only
//
// Columns come entirely from the board definition — no per-lane colors or
// icons are assumed; the active lane is highlighted instead.
package tui

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
)

// ── palette ────────────────────────────────────────────────────────────────

var (
	cPurple color.Color = lipgloss.Color("#e135ff")
	cCyan   color.Color = lipgloss.Color("#80ffea")
	cYellow color.Color = lipgloss.Color("#f1fa8c")
	cGreen  color.Color = lipgloss.Color("#50fa7b")
	cRed    color.Color = lipgloss.Color("#ff6363")
	cMuted  color.Color = lipgloss.Color("#6e7795")
	cFg     color.Color = lipgloss.Color("#e6e9f5")
	cBgAct  color.Color = lipgloss.Color("#2c2f46")
)

func sty(c color.Color, s string) string { return lipgloss.NewStyle().Foreground(c).Render(s) }
func bold(s string) string               { return lipgloss.NewStyle().Bold(true).Render(s) }
func dim(s string) string                { return sty(cMuted, s) }
func boldC(c color.Color, s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(c).Render(s)
}

// ── small ANSI-safe helpers (lipgloss.Width is grapheme/ANSI aware) ────────

func trunc(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	out := strings.Builder{}
	cur := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if cur+rw > w-1 {
			break
		}
		out.WriteRune(r)
		cur += rw
	}
	return out.String() + "…"
}

func padR(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return trunc(s, w)
}

func padL(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return trunc(s, w)
}

// paintRow joins fg-styled segments under a persistent background: the bg
// SGR is re-emitted after every segment (each lipgloss-rendered segment
// ends with a full reset that would otherwise clear the row background).
func paintRow(selected bool, w int, segs ...string) string {
	bg := ""
	if selected {
		bg = "\x1b[48;2;44;47;70m"
	}
	content := bg + strings.Join(segs, bg) + bg
	if vw := lipgloss.Width(content); vw < w {
		content += strings.Repeat(" ", w-vw)
	}
	return content + "\x1b[0m"
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// ageStyle colors age by meaning: stale only stands out in active lanes —
// the first and last board columns are treated as parking (backlog/done).
func ageStyle(t time.Time, status string, cols []string) string {
	a := ago(t)
	if len(cols) > 0 && (status == cols[0] || status == cols[len(cols)-1]) {
		return dim(a)
	}
	d := time.Since(t)
	switch {
	case d >= 72*time.Hour:
		return sty(cRed, a)
	case d >= 24*time.Hour:
		return sty(cYellow, a)
	}
	return dim(a)
}

// ── markdown rendering (glamour) ──────────────────────────────────────────

// mdRender renders markdown with glamour. The renderer is rebuilt when the
// width changes; the last render is memoized by (width, input) since the
// detail is re-rendered on every frame. Package-level state is guarded so
// parallel tests can't race it.
var (
	mdMu   sync.Mutex
	mdR    *glamour.TermRenderer
	mdRW   int
	mdKey  string
	mdLast string
)

func mdRender(md string, w int) string {
	if w < 8 {
		w = 8
	}
	key := fmt.Sprintf("%d\x00%s", w, md)
	mdMu.Lock()
	defer mdMu.Unlock()
	if key == mdKey {
		return mdLast
	}
	if mdR == nil || mdRW != w {
		opts := []glamour.TermRendererOption{glamour.WithWordWrap(w)}
		if s := styleOverride(); s != "" {
			opts = append(opts, glamour.WithStandardStyle(s))
		} else {
			opts = append(opts, glamour.WithAutoStyle())
		}
		r, err := glamour.NewTermRenderer(opts...)
		if err != nil {
			return md
		}
		mdR, mdRW = r, w
	}
	out, err := mdR.Render(md)
	if err != nil {
		return md
	}
	mdKey, mdLast = key, out
	return out
}

// styleOverride lets dumps/screenshots force a glamour style (dark/light/
// notty) instead of terminal autodetection. Set CARDS_TUI_STYLE=dark.
var styleOverride = func() string { return os.Getenv("CARDS_TUI_STYLE") }

// mdEsc escapes markdown-significant characters in inline card text
// (titles, comment bodies) so they render literally.
func mdEsc(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"]", `\]`,
		"`", "\\`",
	)
	return r.Replace(s)
}

// ── keymap ─────────────────────────────────────────────────────────────────

type keyMap struct {
	Up, Down, Left, Right, Open, Board, Search, Status, Owner, Edit, Comment, Claim, New, Help, Quit key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:      key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
		Down:    key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
		Left:    key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("h/←", "lane ←")),
		Right:   key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l/→", "lane →")),
		Open:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Board:   key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "board")),
		Search:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "find")),
		Status:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "status")),
		Owner:   key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "owner")),
		Edit:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
		Comment: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "comment")),
		Claim:   key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mine")),
		New:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "keys")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Left, k.Right, k.Down, k.Open, k.Search, k.Help, k.Quit}
}
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Left, k.Right, k.Board},
		{k.Open, k.Search, k.Status, k.Owner, k.Edit, k.Comment},
		{k.Claim, k.New, k.Help, k.Quit},
	}
}

// ── model ──────────────────────────────────────────────────────────────────

type modeKind int

const (
	modeBrowse modeKind = iota
	modeSearch
	modeStatus
	modeOwner
	modeTitle
	modeComment
	modeHelp
)

// focusKind is the pane that currently owns the keyboard.
type focusKind int

const (
	focusList focusKind = iota
	focusHeader
	focusDetail
)

// detailMode is the detail pane's visibility: hidden (list only), split
// (list + detail side by side / stacked), or fullscreen (detail only).
type detailMode int

const (
	detailHidden detailMode = iota
	detailSplit
	detailFull
)

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// eventMsg carries one event from the workspace bus.
type eventMsg struct{ ev *core.Event }

type model struct {
	svc     *core.Service
	ws      *core.Workspace
	types   map[string]*core.CardType
	boards  map[string]*core.Board
	actor   string
	sub     *core.Subscriber
	boards_ []string // ordered board ids

	boardIdx int
	laneIdx  int
	cursor   int
	cards    []core.Card // cards for the active board (all lanes)

	// extras for the selected card (refetched when the selection changes)
	extrasFor string
	inbound   []core.Card
	events    []core.Event

	mode         modeKind
	focus        focusKind
	dmode        detailMode
	listScroll   int
	detailScroll int
	in           textinput.Model
	help         help.Model
	keys         keyMap
	width        int
	height       int
	pulse        int
	flash        string
	flashAt      time.Time
	loadErr      string
}

// Run opens the interactive TUI against an already-open service. It blocks
// until the user quits. The caller owns closing the store.
func Run(ctx context.Context, svc *core.Service, result *config.Result, actor string) error {
	m := newModel(svc, result, actor)
	if m.sub != nil {
		defer svc.Bus().Unsubscribe(m.sub.ID) // releases any blocked waitEvent goroutine
	}
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}

func newModel(svc *core.Service, result *config.Result, actor string) model {
	in := textinput.New()
	in.Prompt = " › "
	in.CharLimit = 300

	boardIDs := make([]string, 0, len(result.Boards))
	for id := range result.Boards {
		boardIDs = append(boardIDs, id)
	}
	sort.Strings(boardIDs)

	m := model{
		svc:     svc,
		ws:      result.Workspace,
		types:   result.CardTypes,
		boards:  result.Boards,
		actor:   actor,
		boards_: boardIDs,
		in:      in,
		help:    help.New(),
		keys:    newKeyMap(),
	}
	if bus := svc.Bus(); bus != nil {
		m.sub = bus.Subscribe(core.EventFilter{}, 64)
	}
	m.refresh(m.ctx())
	// Start on the first lane that has cards (else first lane).
	if cols := m.columns(); len(cols) > 0 {
		for i, col := range cols {
			if len(m.laneCards(col)) > 0 {
				m.laneIdx = i
				break
			}
		}
	}
	return m
}

func (m *model) board() *core.Board {
	if len(m.boards_) == 0 {
		return nil
	}
	return m.boards[m.boards_[m.boardIdx]]
}

// columns returns the active board's lane ids, in board order.
func (m *model) columns() []string {
	if b := m.board(); b != nil {
		return b.Columns
	}
	return nil
}

func (m *model) lane() string {
	cols := m.columns()
	if len(cols) == 0 {
		return ""
	}
	if m.laneIdx >= len(cols) {
		m.laneIdx = 0
	}
	return cols[m.laneIdx]
}

func (m *model) columnName(id string) string {
	for _, c := range m.ws.Columns {
		if c.ID == id {
			return c.Name
		}
	}
	return id
}

func (m *model) laneCards(col string) []core.Card {
	q := ""
	if m.mode == modeSearch {
		q = strings.ToLower(strings.TrimSpace(m.in.Value()))
	}
	var out []core.Card
	for _, c := range m.cards {
		if c.Status != col {
			continue
		}
		if q != "" {
			hay := strings.ToLower(c.ID + " " + c.Title + " " + c.Owner + " " + c.TypeID + " " + strings.Join(c.Tags, " "))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func (m *model) selected() *core.Card {
	cs := m.laneCards(m.lane())
	if len(cs) == 0 {
		return nil
	}
	c := cs[min(m.cursor, len(cs)-1)]
	for i := range m.cards {
		if m.cards[i].ID == c.ID {
			return &m.cards[i]
		}
	}
	return nil
}

// refresh reloads the active board's cards from the service.
func (m *model) refresh(ctx context.Context) {
	b := m.board()
	if b == nil {
		return
	}
	page, err := m.svc.ListCards(ctx, core.CardQuery{
		BoardID: b.ID,
		Limit:   500,
		Include: []string{"links", "comments"},
	})
	if err != nil {
		m.loadErr = err.Error()
		return
	}
	m.loadErr = ""
	m.cards = page.Items
	m.extrasFor = "" // force extras re-sync (inbound/activity may have changed)
	m.syncExtras(ctx)
}

// syncExtras fetches inbound links + recent activity for the selected card,
// but only when the selection changed (guarded, so it never runs per frame).
func (m *model) syncExtras(ctx context.Context) {
	c := m.selected()
	if c == nil {
		m.inbound, m.events, m.extrasFor = nil, nil, ""
		return
	}
	if m.extrasFor == c.ID {
		return
	}
	m.extrasFor = c.ID
	if page, err := m.svc.ListCards(ctx, core.CardQuery{LinkTarget: c.ID, Limit: 50}); err == nil {
		m.inbound = page.Items
	} else {
		m.inbound = nil
	}
	if evs, err := m.svc.ListEvents(ctx, core.EventQuery{CardID: c.ID, Limit: 6}); err == nil {
		m.events = evs
	} else {
		m.events = nil
	}
}

func (m *model) notify(s string) {
	m.flash = s
	m.flashAt = time.Now()
}

// ctx returns a context carrying the TUI's actor, so service writes
// (AddComment, PatchCard, Claim, ...) resolve the actor from context the same
// way the HTTP transport does via X-Work-Cards-Actor.
func (m *model) ctx() context.Context {
	return core.WithActor(context.Background(), m.actor)
}

func (m *model) notifyErr(err error) {
	if ce, ok := err.(*core.Error); ok {
		msg := ce.Message
		if len(ce.ValidOptions) > 0 {
			msg += " (" + strings.Join(ce.ValidOptions, ", ") + ")"
		}
		m.notify(msg)
		return
	}
	m.notify(err.Error())
}

// ── update ─────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), waitEvent(m.sub))
}

func waitEvent(sub *core.Subscriber) tea.Cmd {
	if sub == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-sub.Ch
		if !ok {
			return nil
		}
		return eventMsg{ev}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		m.pulse++
		return m, tickCmd()
	case eventMsg:
		m.refresh(m.ctx())
		return m, waitEvent(m.sub)
	case tea.KeyPressMsg:
		if m.mode != modeBrowse {
			return m.updateModal(msg)
		}
		switch m.focus {
		case focusHeader:
			return m.updateHeader(msg)
		case focusDetail:
			return m.updateDetail(msg)
		default:
			return m.updateBrowse(msg)
		}
	}
	return m, nil
}

// updateHeader handles keys while the lane tab bar has focus. h/l moves
// lanes, tab/shift+tab moves boards, j/esc/enter returns to the list.
func (m model) updateHeader(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.mode = modeHelp
		return m, nil
	case "tab":
		m.stepBoard(1)
		return m, nil
	case "shift+tab":
		m.stepBoard(-1)
		return m, nil
	case "j", "down", "enter", "esc":
		m.focus = focusList
		return m, nil
	case "h", "left":
		m.stepLane(-1)
		return m, nil
	case "l", "right":
		m.stepLane(1)
		return m, nil
	}
	return m, nil
}

// updateDetail handles keys while the card detail pane has focus.
func (m model) updateDetail(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	_, ih := m.detailInner()
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.mode = modeHelp
		return m, nil
	case "enter":
		// split → fullscreen; fullscreen → split (toggle)
		if m.dmode == detailSplit {
			m.dmode = detailFull
		} else if m.dmode == detailFull {
			m.dmode = detailSplit
		}
		return m, nil
	case "esc":
		// fullscreen → split; split → hidden (list only)
		if m.dmode == detailFull {
			m.dmode = detailSplit
		} else {
			m.dmode = detailHidden
			m.focus = focusList
		}
		return m, nil
	case "tab":
		if m.dmode == detailFull {
			m.dmode = detailSplit
		} else {
			m.focus = focusList
		}
		return m, nil
	case "j", "down":
		m.detailScroll = min(m.detailMaxScroll(), m.detailScroll+1)
		return m, nil
	case "k", "up":
		m.detailScroll = max(0, m.detailScroll-1)
		return m, nil
	case "ctrl+d":
		m.detailScroll = min(m.detailMaxScroll(), m.detailScroll+max(1, ih/2))
		return m, nil
	case "ctrl+u":
		m.detailScroll = max(0, m.detailScroll-max(1, ih/2))
		return m, nil
	case "g":
		m.detailScroll = 0
		return m, nil
	case "G":
		m.detailScroll = m.detailMaxScroll()
		return m, nil
	}
	return m, nil
}

func (m model) updateBrowse(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	ctx := m.ctx()
	n := len(m.laneCards(m.lane()))
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.mode = modeHelp
		return m, nil
	case msg.String() == "esc":
		// In split, esc hides the detail; otherwise clear the search.
		if m.dmode != detailHidden {
			m.dmode = detailHidden
			return m, nil
		}
		if m.in.Value() != "" {
			m.in.SetValue("")
			m.notify("search cleared")
		}
		return m, nil
	case key.Matches(msg, m.keys.Open), msg.String() == "tab":
		// enter/tab on a card reveals the detail pane and focuses it.
		if m.selected() != nil {
			m.dmode = detailSplit
			m.focus = focusDetail
			m.detailScroll = 0
		}
		return m, nil
	case msg.String() == "shift+tab":
		m.stepBoard(1)
		return m, nil
	case key.Matches(msg, m.keys.Left):
		m.stepLane(-1)
		return m, nil
	case key.Matches(msg, m.keys.Right):
		m.stepLane(1)
		return m, nil
	case key.Matches(msg, m.keys.Up):
		if m.cursor == 0 {
			m.focus = focusHeader
		} else {
			m.cursor--
			m.detailScroll = 0
		}
		m.ensureCursorVisible()
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if m.cursor < n-1 {
			m.cursor++
			m.detailScroll = 0
		}
		m.ensureCursorVisible()
		return m, nil
	case msg.String() == "g":
		m.cursor = 0
		m.detailScroll = 0
		m.ensureCursorVisible()
		return m, nil
	case msg.String() == "G":
		m.cursor = max(0, n-1)
		m.detailScroll = 0
		m.ensureCursorVisible()
		return m, nil
	case key.Matches(msg, m.keys.Search):
		m.mode = modeSearch
		m.in.Focus()
		return m, textinput.Blink
	case key.Matches(msg, m.keys.Status):
		if m.selected() != nil {
			m.openModal(modeStatus)
		}
		return m, nil
	case key.Matches(msg, m.keys.Owner):
		if c := m.selected(); c != nil {
			m.openModal(modeOwner)
			m.in.SetValue(c.Owner)
			return m, textinput.Blink
		}
		return m, nil
	case key.Matches(msg, m.keys.Edit):
		if c := m.selected(); c != nil {
			m.openModal(modeTitle)
			m.in.SetValue(c.Title)
			return m, textinput.Blink
		}
		return m, nil
	case key.Matches(msg, m.keys.Comment):
		if m.selected() != nil {
			m.openModal(modeComment)
			m.in.SetValue("")
			return m, textinput.Blink
		}
		return m, nil
	case key.Matches(msg, m.keys.Claim):
		if c := m.selected(); c != nil {
			if c.Owner == m.actor {
				if _, err := m.svc.Release(ctx, c.ID, core.ReleaseRequest{Version: c.Version, Actor: m.actor}); err != nil {
					m.notifyErr(err)
				} else {
					m.notify("released")
				}
			} else {
				if _, err := m.svc.Claim(ctx, c.ID, core.ClaimRequest{Version: c.Version, Actor: m.actor}); err != nil {
					m.notifyErr(err)
				} else {
					m.notify("claimed by " + m.actor)
				}
			}
			m.refresh(ctx)
		}
		return m, nil
	case key.Matches(msg, m.keys.New):
		return m.createCard()
	}
	return m, nil
}

// openModal enters a modal mode; the detail pane must be visible to host the
// modal content, so hidden upgrades to split.
func (m *model) openModal(mode modeKind) {
	m.mode = mode
	if m.dmode == detailHidden {
		m.dmode = detailSplit
		m.focus = focusList
	}
	m.in.Focus()
}

func (m *model) stepLane(d int) {
	cols := m.columns()
	if len(cols) == 0 {
		return
	}
	m.laneIdx = (m.laneIdx + d + len(cols)) % len(cols)
	m.cursor = 0
	m.listScroll = 0
	m.detailScroll = 0
	m.syncExtras(m.ctx())
}

func (m *model) stepBoard(d int) {
	if len(m.boards_) == 0 {
		return
	}
	m.boardIdx = (m.boardIdx + d + len(m.boards_)) % len(m.boards_)
	m.laneIdx = 0
	m.cursor = 0
	m.listScroll = 0
	m.detailScroll = 0
	m.refresh(m.ctx())
	if b := m.board(); b != nil {
		m.notify("board → " + b.Name)
	}
}

func (m model) createCard() (tea.Model, tea.Cmd) {
	ctx := m.ctx()
	b := m.board()
	if b == nil {
		return m, nil
	}
	// Pick a board type with no required fields, so `n` always succeeds.
	var typeID string
	for _, t := range b.CardTypeIDs {
		ct := m.types[t]
		if ct == nil {
			continue
		}
		required := false
		for _, f := range ct.Fields {
			if f.Required {
				required = true
				break
			}
		}
		if !required {
			typeID = t
			break
		}
	}
	if typeID == "" {
		m.notify("all board types have required fields — use `cards create`")
		return m, nil
	}
	lane := m.lane()
	c, err := m.svc.CreateCard(ctx, core.CreateCardRequest{
		TypeID: typeID,
		Title:  "new card",
		Status: lane,
		Actor:  m.actor,
	})
	if err != nil {
		m.notifyErr(err)
		return m, nil
	}
	m.refresh(ctx)
	for i, lc := range m.laneCards(lane) {
		if lc.ID == c.ID {
			m.cursor = i
			break
		}
	}
	m.openModal(modeTitle)
	m.in.SetValue(c.Title)
	return m, textinput.Blink
}

// legalTargets returns the columns a card in `from` may move to, honoring
// board transitions when enforced; otherwise any other column.
func (m *model) legalTargets(from string) []string {
	b := m.board()
	cols := m.columns()
	if b == nil {
		return nil
	}
	if b.Settings.EnforceTransitions && b.Transitions != nil {
		if next, ok := b.Transitions[from]; ok {
			return next
		}
		return nil
	}
	var out []string
	for _, c := range cols {
		if c != from {
			out = append(out, c)
		}
	}
	return out
}

func (m model) updateModal(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	ctx := m.ctx()
	switch m.mode {
	case modeHelp:
		switch msg.String() {
		case "esc", "?", "q":
			m.mode = modeBrowse
		}
		return m, nil
	case modeStatus:
		if msg.String() == "esc" {
			m.mode = modeBrowse
			return m, nil
		}
		c := m.selected()
		if c == nil {
			m.mode = modeBrowse
			return m, nil
		}
		targets := m.legalTargets(c.Status)
		if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
			i := int(s[0] - '1')
			if i < len(targets) {
				next := targets[i]
				if _, err := m.svc.PatchCard(ctx, c.ID, core.PatchCardRequest{
					Version: c.Version,
					Status:  &next,
					Actor:   m.actor,
				}); err != nil {
					m.notifyErr(err)
				} else {
					m.notify("→ " + m.columnName(next))
				}
				// Always refresh after a write — don't rely on the bus for our
				// own mutations (and there is no bus in headless tests).
				m.refresh(ctx)
				m.mode = modeBrowse
			}
		}
		return m, nil
	}

	// text-input modes
	switch msg.String() {
	case "esc":
		// esc cancels the mode; for search it also clears the query.
		m.mode = modeBrowse
		m.in.Blur()
		m.in.SetValue("")
		return m, nil
	case "enter":
		v := strings.TrimSpace(m.in.Value())
		switch m.mode {
		case modeSearch:
			m.mode = modeBrowse
			m.in.Blur()
			return m, nil
		case modeOwner:
			if c := m.selected(); c != nil {
				if v == "" {
					if _, err := m.svc.Release(ctx, c.ID, core.ReleaseRequest{Version: c.Version, Actor: m.actor}); err != nil {
						m.notifyErr(err)
					} else {
						m.notify("unassigned")
					}
				} else {
					if _, err := m.svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Owner: &v, Actor: m.actor}); err != nil {
						m.notifyErr(err)
					} else {
						m.notify("owner → " + v)
					}
				}
				m.refresh(ctx)
			}
		case modeTitle:
			if c := m.selected(); c != nil && v != "" {
				if _, err := m.svc.PatchCard(ctx, c.ID, core.PatchCardRequest{Version: c.Version, Title: &v, Actor: m.actor}); err != nil {
					m.notifyErr(err)
				} else {
					m.notify("title updated")
				}
				m.refresh(ctx)
			}
		case modeComment:
			if c := m.selected(); c != nil && v != "" {
				if _, err := m.svc.AddComment(ctx, c.ID, v); err != nil {
					m.notifyErr(err)
				} else {
					m.notify("comment posted")
				}
				m.refresh(ctx)
			}
		}
		m.mode = modeBrowse
		m.in.Blur()
		m.in.SetValue("")
		return m, nil
	}

	var cmd tea.Cmd
	m.in, cmd = m.in.Update(msg)
	if m.mode == modeSearch {
		m.cursor = 0
		m.listScroll = 0
	}
	return m, cmd
}

// ── layout math ────────────────────────────────────────────────────────────

// listBudget returns the width/height of the card list at the current
// terminal size and detail visibility.
func (m *model) listBudget() (int, int) {
	w := max(48, m.width)
	h := max(10, m.height)
	bodyH := h - 4
	switch m.dmode {
	case detailHidden:
		return w, bodyH
	case detailFull:
		return 0, 0
	default: // split
		if w >= 100 {
			return w * 58 / 100, bodyH
		}
		return w, bodyH * 55 / 100
	}
}

// detailBudget returns the width/height of the detail box, or 0s when hidden.
func (m *model) detailBudget() (int, int) {
	w := max(48, m.width)
	h := max(10, m.height)
	bodyH := h - 4
	switch m.dmode {
	case detailHidden:
		return 0, 0
	case detailFull:
		return w, bodyH
	default: // split
		if w >= 100 {
			return w - w*58/100, bodyH
		}
		return w, bodyH - bodyH*55/100
	}
}

// detailInner returns the content width/height inside the detail box
// (border + padding removed).
func (m *model) detailInner() (int, int) {
	dw, dh := m.detailBudget()
	return max(10, dw-5), max(1, dh-2)
}

// detailMaxScroll is how far the detail content can scroll, clamped ≥ 0.
func (m *model) detailMaxScroll() int {
	iw, ih := m.detailInner()
	lines := strings.Split(m.cardDetail(iw), "\n")
	return max(0, len(lines)-ih)
}

// ensureCursorVisible advances listScroll so the cursor is inside the
// visible window of the card list at the current layout.
func (m *model) ensureCursorVisible() {
	_, lh := m.listBudget()
	if lh <= 0 {
		return
	}
	if m.cursor < m.listScroll {
		m.listScroll = m.cursor
	}
	if m.cursor >= m.listScroll+lh {
		m.listScroll = m.cursor - lh + 1
	}
	n := len(m.laneCards(m.lane()))
	m.listScroll = max(0, min(m.listScroll, max(0, n-lh)))
}

// ── view ───────────────────────────────────────────────────────────────────

func (m model) View() tea.View {
	v := tea.NewView(m.renderView())
	v.AltScreen = true
	if m.ws != nil {
		v.WindowTitle = "cards · " + m.ws.Name
	}
	return v
}

func (m model) renderView() string {
	w := max(48, m.width)

	if m.mode == modeHelp {
		return m.helpView(w)
	}

	var b strings.Builder
	b.WriteString(m.topBar(w) + "\n")
	b.WriteString(m.tabBar(w) + "\n")
	b.WriteString(m.laneLine(w) + "\n")

	// A modal mode needs the detail pane to host its content.
	dmode := m.dmode
	if m.mode != modeBrowse && dmode == detailHidden {
		dmode = detailSplit
	}

	switch dmode {
	case detailHidden:
		lw, lh := m.listBudget()
		b.WriteString(m.listView(lw, lh))
	case detailFull:
		dw, dh := m.detailBudget()
		b.WriteString(m.detailView(dw, dh))
	default: // split
		if w >= 100 {
			lw, _ := m.listBudget()
			dw, dh := m.detailBudget()
			list := m.listView(lw, dh)
			detail := m.detailView(dw, dh)
			b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, list, detail))
		} else {
			lw, lh := m.listBudget()
			dw, dh := m.detailBudget()
			b.WriteString(m.listView(lw, lh) + m.detailView(dw, dh))
		}
	}
	b.WriteString("\n" + m.footer(w))
	return b.String()
}

func (m model) topBar(w int) string {
	if m.ws == nil {
		return ""
	}
	myCount := 0
	for _, c := range m.cards {
		if c.Owner == m.actor {
			myCount++
		}
	}
	pulse := []string{"●", "◉", "○", "◉"}[m.pulse%4]
	left := " " + boldC(cPurple, m.ws.Name)
	if b := m.board(); b != nil {
		left += dim(" · ") + sty(cFg, b.Name)
	}
	if myCount > 0 {
		left += dim(" · ") + sty(cGreen, fmt.Sprintf("my %d", myCount))
	}
	right := sty(cGreen, pulse) + dim(" live ")
	gap := max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	return lipgloss.NewStyle().Width(w).Render(left + strings.Repeat(" ", gap) + right)
}

// tabBar renders the active board's columns as tabs. Per the design, lanes
// have no fixed colors or icons — the active tab is highlighted, nothing
// else is styled.
func (m model) tabBar(w int) string {
	cols := m.columns()
	counts := map[string]int{}
	for _, c := range m.cards {
		counts[c.Status]++
	}
	var tabs []string
	for i, col := range cols {
		label := fmt.Sprintf(" %s %d ", m.columnName(col), counts[col])
		switch {
		case i == m.laneIdx && m.focus == focusHeader:
			tabs = append(tabs, lipgloss.NewStyle().Bold(true).Foreground(cFg).Background(cBgAct).Render(label))
		case i == m.laneIdx:
			tabs = append(tabs, lipgloss.NewStyle().Bold(true).Foreground(cFg).Underline(true).Render(label))
		default:
			tabs = append(tabs, dim(label))
		}
	}
	row := " " + strings.Join(tabs, dim("│"))
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Render(row)
}

func (m model) laneLine(w int) string {
	if m.mode == modeSearch {
		m.in.SetWidth(w - 4)
		return lipgloss.NewStyle().Width(w).Foreground(cCyan).Render(" /" + m.in.View())
	}
	n := len(m.laneCards(m.lane()))
	pos := ""
	if n > 0 {
		pos = dim(fmt.Sprintf("%d/%d", min(m.cursor+1, n), n))
	}
	hint := ""
	if m.focus == focusDetail {
		hint = dim(" · ") + sty(cPurple, "reading detail")
	}
	left := dim(" ─ " + m.columnName(m.lane()) + " · " + fmt.Sprint(n) + " cards")
	right := pos + hint
	gap := max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

// ── list ───────────────────────────────────────────────────────────────────

func (m model) listView(w, h int) string {
	cs := m.laneCards(m.lane())
	var rows []string
	if m.loadErr != "" {
		rows = append(rows, lipgloss.NewStyle().Width(w).Render(sty(cRed, "  "+m.loadErr)))
	}
	if len(cs) == 0 && m.loadErr == "" {
		rows = append(rows, lipgloss.NewStyle().Width(w).Render(dim("  no cards in this lane")))
	}

	// window the list around the cursor (scrolling)
	scroll := m.listScroll
	if m.cursor < scroll {
		scroll = m.cursor
	}
	if m.cursor >= scroll+h {
		scroll = m.cursor - h + 1
	}
	scroll = max(0, min(scroll, max(0, len(cs)-h)))

	for i := scroll; i < min(len(cs), scroll+h); i++ {
		rows = append(rows, m.rowView(&cs[i], i == m.cursor, w))
	}
	for len(rows) < h {
		rows = append(rows, lipgloss.NewStyle().Width(w).Render(""))
	}
	return strings.Join(rows[:h], "\n") + "\n"
}

func (m model) rowView(c *core.Card, selected bool, w int) string {
	gut := " "
	if selected {
		gut = sty(cPurple, "▌")
	}

	typeName := c.TypeID
	if ct := m.types[c.TypeID]; ct != nil && ct.Name != "" {
		typeName = ct.Name
	}
	typeS := dim(padR(trunc(typeName, 12), 12))

	owner := c.Owner
	if owner == "" {
		owner = "·"
	}
	ownerS := padR(trunc(owner, 8), 8)
	if c.Owner == m.actor {
		ownerS = sty(cGreen, ownerS)
	} else if c.Owner == "" {
		ownerS = dim(ownerS)
	}

	ageS := padL(ageStyle(c.UpdatedAt, c.Status, m.columns()), 5)

	chipsS := m.chips(c)

	// Constant layout so owner/age columns align on every row.
	fixed := 1 + 1 + 12 + 1 + 8 + 1 + 5 + 1
	titleW := max(6, w-fixed-8)
	title := c.Title
	if m.mode == modeSearch && strings.TrimSpace(m.in.Value()) != "" {
		title = hlTitle(trunc(title, titleW), strings.TrimSpace(m.in.Value()), selected, titleW)
	} else if selected {
		title = selOpen + padR(title, titleW) + "\x1b[0m"
	} else {
		title = padR(title, titleW)
	}

	return paintRow(selected, w, gut, " ", typeS, " ", title, " ", ownerS, " ", ageS, " ", chipsS)
}

func (m model) chips(c *core.Card) string {
	var parts []string
	if len(c.Links) > 0 {
		parts = append(parts, dim(fmt.Sprintf("↪%d", len(c.Links))))
	}
	if len(c.Comments) > 0 {
		parts = append(parts, dim(fmt.Sprintf("▾%d", len(c.Comments))))
	}
	return strings.Join(parts, " ")
}

const selOpen = "\x1b[1;4;38;2;225;53;255m"
const hlOpen = "\x1b[1;38;2;255;106;193m"

// hlTitle composes the title column with coral+bold match highlights using
// raw SGR + partial resets (so the row background survives across segments).
func hlTitle(text, q string, selected bool, w int) string {
	padded := padR(trunc(text, w), w)
	base := ""
	if selected {
		base = selOpen
	}
	lc, lq := strings.ToLower(padded), strings.ToLower(q)
	var b strings.Builder
	i := 0
	for i < len(padded) {
		idx := strings.Index(lc[i:], lq)
		if idx < 0 {
			b.WriteString(base + padded[i:])
			break
		}
		j := i + idx
		if j > i {
			b.WriteString(base + padded[i:j])
		}
		end := j + len(lq)
		if end > len(padded) {
			end = len(padded)
		}
		b.WriteString(hlOpen + padded[j:end] + "\x1b[22;39m")
		i = end
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// ── detail ─────────────────────────────────────────────────────────────────

func (m model) detailView(w, h int) string {
	boxW := w - 1
	innerW := boxW - 4
	innerH := h - 2 // box fills h rows: border(2) + content(h-2)

	// Border accent signals focus/mode (modes need visual identity).
	accent := cMuted
	if m.mode != modeBrowse || m.focus == focusDetail {
		accent = cPurple
	}

	var content string
	switch m.mode {
	case modeStatus:
		content = m.statusPicker()
	case modeOwner, modeTitle, modeComment:
		var label string
		switch m.mode {
		case modeOwner:
			label = "assign owner (empty releases)"
		case modeTitle:
			label = "edit title"
		case modeComment:
			label = "add comment"
		}
		m.in.SetWidth(innerW - 3)
		content = bold(label) + "\n\n" + m.in.View() + "\n\n" + dim("enter save · esc cancel")
	default:
		// Scrollable read view: slice the rendered markdown by detailScroll,
		// then pad so the box renders a clean border every frame.
		lines := strings.Split(m.cardDetail(innerW), "\n")
		ds := min(m.detailScroll, max(0, len(lines)-innerH))
		end := min(len(lines), ds+max(1, innerH))
		window := append([]string{}, lines[ds:end]...)
		for len(window) < innerH {
			window = append(window, "")
		}
		content = strings.Join(window[:max(1, innerH)], "\n")
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1).
		Width(boxW)
	return box.Render(content)
}

func (m model) statusPicker() string {
	c := m.selected()
	var b strings.Builder
	b.WriteString(bold("set status") + "\n\n")
	if c != nil {
		b.WriteString(dim("current: ") + sty(cFg, m.columnName(c.Status)) + "\n\n")
		targets := m.legalTargets(c.Status)
		if len(targets) == 0 {
			b.WriteString(dim("  no legal transitions\n"))
		}
		for i, t := range targets {
			b.WriteString(fmt.Sprintf("  %s %s\n", sty(cYellow, fmt.Sprintf("(%d)", i+1)), sty(cFg, m.columnName(t))))
		}
	}
	b.WriteString("\n" + dim("press number · esc cancel"))
	return b.String()
}

// cardDetail renders the selected card as a markdown document (glamour).
func (m model) cardDetail(w int) string {
	c := m.selected()
	if c == nil {
		return dim("no card selected")
	}
	return mdRender(m.cardMarkdown(c), w)
}

// cardMarkdown builds the detail document for a card. This is the same
// shape an export verb could reuse (`cards show --md`).
func (m *model) cardMarkdown(c *core.Card) string {
	var b strings.Builder

	// Title as H2 (H1's full-width background band is too heavy inside a box).
	b.WriteString("## " + mdEsc(c.Title) + "\n\n")

	// meta line
	typeName := c.TypeID
	if ct := m.types[c.TypeID]; ct != nil && ct.Name != "" {
		typeName = ct.Name
	}
	owner := c.Owner
	if owner == "" {
		owner = "unassigned"
	}
	meta := []string{
		typeName,
		m.columnName(c.Status),
		owner,
		"v" + fmt.Sprint(c.Version),
		trunc(c.ID, 8),
		ago(c.UpdatedAt),
	}
	b.WriteString(strings.Join(meta, " · ") + "\n")

	// tags
	if len(c.Tags) > 0 {
		var ts []string
		for _, t := range c.Tags {
			ts = append(ts, "#"+t)
		}
		b.WriteString("\n" + strings.Join(ts, " ") + "\n")
	}

	// legal transitions
	if targets := m.legalTargets(c.Status); len(targets) > 0 {
		var names []string
		for _, t := range targets {
			names = append(names, m.columnName(t))
		}
		b.WriteString("\n**next →** " + strings.Join(names, " · ") + "\n")
	}

	// fields (schema-driven)
	if ct := m.types[c.TypeID]; ct != nil && len(ct.Fields) > 0 {
		fields, _ := c.Fields.(map[string]any)
		if fields == nil {
			fields = map[string]any{}
		}
		var scalars []string
		for _, fd := range ct.Fields {
			v, ok := fields[fd.ID]
			if !ok || v == nil {
				continue
			}
			switch fd.Type {
			case "text":
				if s, ok := v.(string); ok && s != "" {
					b.WriteString("\n## " + fd.ID + "\n\n" + mdEsc(s) + "\n")
				}
			case "repeating":
				if arr, ok := v.([]any); ok && len(arr) > 0 {
					b.WriteString("\n## " + fd.ID + "\n\n")
					for _, item := range arr {
						if im, ok := item.(map[string]any); ok {
							var kv []string
							for _, sub := range fd.ItemFields {
								if sv, ok := im[sub.ID]; ok && sv != nil && fmt.Sprint(sv) != "" {
									kv = append(kv, fmt.Sprint(sv))
								}
							}
							b.WriteString("- " + mdEsc(strings.Join(kv, " — ")) + "\n")
						}
					}
				}
			case "artifact":
				scalars = append(scalars, "- **"+fd.ID+"**: (artifact attached)")
			case "card_link":
				if s, ok := v.(string); ok && s != "" {
					scalars = append(scalars, "- **"+fd.ID+"**: → "+mdEsc(m.linkLabel(s)))
				}
			default: // string, enum, user, date, number
				s := fmt.Sprint(v)
				if fd.Multiple {
					if arr, ok := v.([]any); ok {
						var parts []string
						for _, x := range arr {
							parts = append(parts, fmt.Sprint(x))
						}
						s = strings.Join(parts, ", ")
					}
				}
				if s != "" && s != "<nil>" {
					scalars = append(scalars, "- **"+fd.ID+"**: "+mdEsc(s))
				}
			}
		}
		if len(scalars) > 0 {
			b.WriteString("\n## fields\n\n" + strings.Join(scalars, "\n") + "\n")
		}
	}

	// links: outbound (on the card) + inbound (queried)
	if len(c.Links) > 0 || len(m.inbound) > 0 {
		b.WriteString("\n## links\n\n")
		for _, l := range c.Links {
			b.WriteString("- → **" + l.TypeID + "** → " + mdEsc(m.linkLabel(l.Target)) + "\n")
		}
		for _, ic := range m.inbound {
			for _, l := range ic.Links {
				if l.Target == c.ID {
					b.WriteString("- ← **" + l.TypeID + "** ← " + mdEsc(trunc(ic.ID, 8)+" "+ic.Title) + "\n")
				}
			}
		}
	}

	// comments (italic in blockquotes)
	if len(c.Comments) > 0 {
		b.WriteString("\n## comments\n\n")
		for _, cm := range c.Comments {
			b.WriteString("**" + mdEsc(cm.Author) + "** · " + ago(cm.CreatedAt) + "\n\n")
			for _, line := range strings.Split(cm.Body, "\n") {
				b.WriteString("> *" + mdEsc(line) + "*\n")
			}
			b.WriteString("\n")
		}
	}

	// activity
	if len(m.events) > 0 {
		b.WriteString("\n## activity\n\n")
		for _, ev := range m.events {
			b.WriteString("- " + ago(ev.At) + " **" + mdEsc(ev.Actor) + "** " + mdEsc(eventText(ev)) + "\n")
		}
	}

	return b.String()
}

// linkLabel resolves a link target to "shortid title" when the target is
// among the loaded board cards; otherwise just the short id.
func (m *model) linkLabel(id string) string {
	short := trunc(id, 8)
	for _, c := range m.cards {
		if c.ID == id {
			return short + " " + c.Title
		}
	}
	return short
}

// eventText renders one activity line from an event's diff.
func eventText(ev core.Event) string {
	t := string(ev.Type)
	if d, ok := ev.Diff.(map[string]any); ok {
		switch ev.Type {
		case core.EventStatusChanged:
			from, _ := d["from"].(string)
			to, _ := d["to"].(string)
			return "status " + from + " → " + to
		case core.EventCommentAdded, core.EventCommentEdited:
			body, _ := d["body"].(string)
			if len(body) > 60 {
				body = body[:60] + "…"
			}
			return "comment: " + body
		}
	}
	return t
}

// ── footer / help ──────────────────────────────────────────────────────────

func (m model) footer(w int) string {
	if m.flash != "" && time.Since(m.flashAt) < 2*time.Second {
		return lipgloss.NewStyle().Width(w).Foreground(cYellow).Render(" ▸ " + m.flash)
	}
	if m.mode != modeBrowse {
		switch m.mode {
		case modeSearch:
			return lipgloss.NewStyle().Width(w).Render(dim(" type to filter · enter keep · esc cancel"))
		case modeStatus:
			return lipgloss.NewStyle().Width(w).Render(dim(" number to move · esc cancel"))
		default:
			return lipgloss.NewStyle().Width(w).Render(dim(" enter save · esc cancel"))
		}
	}
	switch m.focus {
	case focusHeader:
		return lipgloss.NewStyle().Width(w).Render(dim(" h/l lane · tab board · j/esc back"))
	case focusDetail:
		var bar string
		if m.dmode == detailFull {
			bar = " j/k scroll · enter/esc/tab split"
		} else {
			bar = " j/k scroll · enter fullscreen · esc hide · tab list"
		}
		if ms := m.detailMaxScroll(); ms > 0 {
			bar += dim(fmt.Sprintf(" · %d%%", m.detailScroll*100/ms))
		}
		return lipgloss.NewStyle().Width(w).Render(dim(bar))
	}
	m.help.SetWidth(w)
	return lipgloss.NewStyle().Width(w).Render(m.help.View(m.keys))
}

func (m model) helpView(w int) string {
	var b strings.Builder
	b.WriteString(boldC(cPurple, " KEYBINDINGS ") + dim("  esc / ? close") + "\n\n")
	sections := []struct {
		name string
		rows [][2]string
	}{
		{"panes", [][2]string{
			{"enter", "open card → split; again → fullscreen"},
			{"esc", "fullscreen → split → list only"},
			{"tab", "toggle list ↔ detail"},
			{"k (at list top)", "focus the lane tab bar"},
		}},
		{"lanes & boards", [][2]string{
			{"h / l  ← →", "switch lane (column tab)"},
			{"shift+tab", "switch board"},
			{"(in header) tab / shift+tab", "next / prev board"},
		}},
		{"cards", [][2]string{
			{"j / k  ↑ ↓", "move cursor"},
			{"g / G", "top / bottom"},
			{"s", "set status (numbered legal transitions)"},
			{"o", "assign owner (empty releases)"},
			{"e", "edit title"},
			{"c", "add comment"},
			{"m", "claim / release (me)"},
			{"n", "new card in this lane"},
		}},
		{"detail pane", [][2]string{
			{"j / k", "scroll"},
			{"ctrl+d / ctrl+u", "half page"},
			{"g / G", "top / bottom"},
		}},
		{"find", [][2]string{
			{"/", "search within lane (live filter)"},
			{"esc", "clear search / hide detail / cancel"},
		}},
		{"meta", [][2]string{
			{"?", "this help"},
			{"q / ctrl+c", "quit"},
		}},
	}
	for _, s := range sections {
		b.WriteString("  " + boldC(cCyan, s.name) + "\n")
		for _, r := range s.rows {
			b.WriteString("    " + sty(cYellow, padR(r[0], 22)) + dim(r[1]) + "\n")
		}
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(w).Render(b.String())
}
