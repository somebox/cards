package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/somebox/cards/internal/core"
)

// --- view building ---

// CardView is data for card_partial.html.
type CardView struct {
	Card          *core.Card
	CardType      *core.CardType
	PreviewFields []PreviewField
	MoveOptions   []Option
	TypeIcon      string // 1a — precomputed badge glyph
	TypeAccent    string // 1a — precomputed accent (overrides [data-type])
	TypeMuted     string // 1a — precomputed muted shade
	TypeLabel     string // 1a — precomputed type display name (== CardType.Name)
	CommentCount  int    // board card: number of comments
	OutCount      int    // board card: number of outbound links
	InCount       int    // board card: number of inbound links (others → this)
}

// LinkView is a resolved relationship to another card, shown with the target's
// title (not its id) and coloured by link type. Dir is "out" or "in".
type LinkView struct {
	TypeID string
	CardID string
	Title  string
	Dir    string
}

type PreviewField struct {
	Label string
	Value string
}

// Option is a select option.
type Option struct {
	Value    string
	Label    string
	Selected bool
	Disabled bool
}

// RecentCard is a card summary for the home page's recent-activity list.
type RecentCard struct {
	ID        string
	Title     string
	TypeID    string
	TypeLabel string
	Status    string
	UpdatedAt string
}

// FieldView is a rendered field in card_detail / card_form.
type FieldView struct {
	Def           *core.FieldDef
	Value         any
	ValueStr      string
	Entries       [][]PreviewField
	Users         []core.User
	ValueRendered string
	Display       string // UI hint from FieldDef.Display (feed|badge|hidden|link|monospace)
}

// ViewData is the template payload.
type ViewData struct {
	Title         string
	Theme         string // active UI theme name for html[data-theme] (empty = default)
	Boards        map[string]*core.Board
	Board         *core.Board
	Columns       []core.Column
	CardsByColumn map[string][]CardView
	Card          *core.Card
	CardType      *core.CardType
	Fields        []FieldView
	MoveOptions   []Option
	PreviewFields []PreviewField
	Error         *core.Error
	FormTitle     string
	FormTags      string
	StatusOptions []Option
	Users         []core.User
	TagSet        []string
	// TypeThemes is the id→TypeTheme map for non-loop call-sites (modal,
	// detail, home) that read via the typeTheme template func. (1a)
	TypeThemes map[string]core.TypeTheme `json:"-"`
	// Candidates is the disambiguation list shown by card_ambiguous.html when a
	// short id matches >1 card. (1e)
	Candidates []core.CardCandidate
	// Query is the current ?q= search string, repopulating the search box (1d).
	Query string
	// Home page
	Workspace   *core.Workspace
	CardCount   int
	RecentCards []RecentCard
	// Card detail/modal relationships (resolved titles, in/outbound).
	OutLinks []LinkView
	InLinks  []LinkView
}

func (s *Server) baseData(title string) ViewData {
	return ViewData{Title: title, Boards: s.boards}
}

// shortID = first 8 hex after the "card_" prefix (card_fca1f3d5… -> fca1f3d5).
func shortID(id string) string {
	if len(id) >= 13 {
		return id[5:13]
	}
	return id
}

// boardThemeTokens is the whitelist of design-system tokens a board may
// override, in a fixed order (deterministic output, no sort import). Only
// non-inverting HUE tokens are allowed — neutral/ink/surface/flat-neutral
// tokens stay theme-owned so the dark-mode remap keeps working.
var boardThemeTokens = []string{
	"--c-accent", "--c-accent-2", "--c-accent-soft",
	"--c-flat", "--c-flat-dot", "--c-label-bg", "--c-label-fg",
	"--type-feature", "--type-bug", "--type-task", "--type-experiment",
	"--type-research-goal", "--type-programming-task",
	"--link-depends-on", "--link-blocked-by", "--link-related", "--link-sent-to",
	"--rel-out", "--rel-in",
}

// safeCSSColor rejects anything that isn't a plain color/token value, so a
// board Theme can't inject arbitrary CSS via the inline style attribute.
func safeCSSColor(v string) bool {
	if v == "" || len(v) > 32 {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '#' || r == '(' || r == ')' || r == ',' || r == '.' || r == '%' || r == ' ' || r == '-':
		default:
			return false
		}
	}
	return true
}

// boardStyle emits a board's whitelisted Theme overrides as an inline
// custom-property string (template.CSS = pre-sanitised for a style attr).
func boardStyle(b *core.Board) template.CSS {
	if b == nil || len(b.Theme) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, k := range boardThemeTokens {
		if v, ok := b.Theme[k]; ok && safeCSSColor(v) {
			sb.WriteString(k)
			sb.WriteByte(':')
			sb.WriteString(v)
			sb.WriteByte(';')
		}
	}
	return template.CSS(sb.String())
}

// linkGraphCounts loads the whole link graph + comment counts in two queries
// (not N+1) and returns per-card outbound/inbound/comment counts for the board.
func (s *Server) linkGraphCounts(ctx context.Context) (out, in, comments map[string]int) {
	out, in, comments = map[string]int{}, map[string]int{}, map[string]int{}
	// Counts are card-badge decoration; on store failure they render as 0.
	edges, err := s.store.AllLinks(ctx)
	if err != nil {
		log.Printf("WARN: link counts for UI render: %v", err)
	}
	for _, e := range edges {
		out[e.Source]++
		in[e.Target]++
	}
	if cc, err := s.store.CommentCounts(ctx); err == nil {
		comments = cc
	}
	return
}

// cardTitle resolves a card id to its title (cached per request), falling back
// to a short id when the target can't be loaded (deleted / cross-workspace).
func (s *Server) cardTitle(ctx context.Context, cache map[string]string, id string) string {
	if t, ok := cache[id]; ok {
		return t
	}
	t := shortID(id)
	if c, err := s.store.GetCard(ctx, id); err == nil && c.Title != "" {
		t = c.Title
	}
	cache[id] = t
	return t
}

// cardRelations builds the outbound + inbound relationship views for a card's
// detail/modal, resolving each linked card's title (not its id).
func (s *Server) cardRelations(ctx context.Context, c *core.Card) (outs, ins []LinkView) {
	cache := map[string]string{c.ID: c.Title}
	for _, l := range c.Links {
		outs = append(outs, LinkView{TypeID: l.TypeID, CardID: l.Target, Title: s.cardTitle(ctx, cache, l.Target), Dir: "out"})
	}
	// Inbound links are supplementary detail; on store failure the section is
	// empty (outbound links still render from the card itself).
	edges, err := s.store.AllLinks(ctx)
	if err != nil {
		log.Printf("WARN: inbound links for card %s: %v", c.ID, err)
	}
	for _, e := range edges {
		if e.Target == c.ID {
			ins = append(ins, LinkView{TypeID: e.TypeID, CardID: e.Source, Title: s.cardTitle(ctx, cache, e.Source), Dir: "in"})
		}
	}
	return
}

func (s *Server) renderCardDetail(w http.ResponseWriter, r *http.Request, c *core.Card, err *core.Error) {
	ct := s.types[c.TypeID]
	b := s.boardForCard(c)
	users := s.listUsersBestEffort(r)
	data := s.baseData(c.Title)
	data.Card = c
	data.CardType = ct
	data.Board = b
	data.Fields = fieldViews(ct, c.Fields, users)
	data.MoveOptions = s.moveOptions(b, c.Status)
	data.Users = users
	data.TagSet = s.ws.TagSet
	data.Error = err
	data.TypeThemes = s.buildTypeThemes()
	data.OutLinks, data.InLinks = s.cardRelations(r.Context(), c)
	if wantsPartial(r) {
		s.renderPartial(w, "card_detail.html", data)
	} else {
		s.renderPage(w, r, "card_detail.html", data)
	}
}

// uiCardModal returns the card detail rendered into a <dialog> modal shell.
// Inline-edit fields share a single save button (dirty-tracked client-side).
func (s *Server) uiCardModal(w http.ResponseWriter, r *http.Request) {
	c, err := s.svc.GetCard(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := s.types[c.TypeID]
	b := s.boardForCard(c)
	users := s.listUsersBestEffort(r)
	data := s.baseData(c.Title)
	data.Card = c
	data.CardType = ct
	data.Board = b
	data.Fields = fieldViews(ct, c.Fields, users)
	data.MoveOptions = s.moveOptions(b, c.Status)
	data.Users = users
	data.TagSet = s.ws.TagSet
	data.TypeThemes = s.buildTypeThemes()
	data.OutLinks, data.InLinks = s.cardRelations(r.Context(), c)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if e := s.pages["card_modal.html"].ExecuteTemplate(w, "card_modal", data); e != nil {
		http.Error(w, "template error: "+e.Error(), http.StatusInternalServerError)
	}
}

// typeTheme returns the effective TypeTheme for ct, merging config over CSS
// [data-type] defaults. Empty Accent/Muted → CSS token defaults still apply.
// (1a) Callers never read ct.TypeTheme directly.
func typeTheme(ct *core.CardType) core.TypeTheme {
	if ct == nil {
		return core.TypeTheme{}
	}
	t := ct.TypeTheme
	if t.Icon == "" {
		t.Icon = ct.Icon // back-compat: legacy flat Icon field
	}
	// Accent/Muted left empty when unset → CSS [data-type] selectors render.
	return t
}

// buildTypeThemes assembles the id→TypeTheme map used by ViewData.TypeThemes
// for non-loop call-sites (modal, detail, home). (1a)
func (s *Server) buildTypeThemes() map[string]core.TypeTheme {
	themes := make(map[string]core.TypeTheme, len(s.types))
	for id, ct := range s.types {
		themes[id] = typeTheme(ct)
	}
	return themes
}

func (s *Server) cardView(c *core.Card, b *core.Board, users []core.User) CardView {
	ct := s.types[c.TypeID]
	previews := []PreviewField{}
	if b != nil && b.Presentation != nil {
		if fields, ok := b.Presentation.CardPreview[c.TypeID]; ok {
			fm, _ := c.Fields.(map[string]any)
			for _, fid := range fields {
				if v, ok := fm[fid]; ok && v != nil {
					previews = append(previews, PreviewField{Label: fid, Value: fmt.Sprintf("%v", v)})
				}
			}
		}
	}
	th := typeTheme(ct)
	label := ""
	if ct != nil {
		label = ct.Name
	}
	return CardView{
		Card:          c,
		CardType:      ct,
		PreviewFields: previews,
		MoveOptions:   s.moveOptions(b, c.Status),
		TypeIcon:      th.Icon,
		TypeAccent:    th.Accent,
		TypeMuted:     th.Muted,
		TypeLabel:     label,
	}
}

func (s *Server) moveOptions(b *core.Board, current string) []Option {
	if b == nil {
		return nil
	}
	// Build option list from board columns.
	enforce := b.Settings.EnforceTransitions
	var allowed []string
	if enforce {
		allowed = b.Transitions[current]
	}
	opts := []Option{}
	for _, cid := range b.Columns {
		disabled := false
		if enforce {
			// Only allow current + transitions[current]; others disabled.
			if cid != current && !containsStr(allowed, cid) {
				disabled = true
			}
		}
		opts = append(opts, Option{
			Value: cid, Label: s.columnName(cid),
			Selected: cid == current, Disabled: disabled,
		})
	}
	return opts
}

func (s *Server) statusOptions(ct *core.CardType, b *core.Board, selected string) []Option {
	cols := ct.AllowedColumns
	if len(cols) == 0 {
		cols = []string{}
		for _, c := range s.ws.Columns {
			cols = append(cols, c.ID)
		}
	}
	if b != nil {
		// restrict to board columns
		boardCols := map[string]bool{}
		for _, c := range b.Columns {
			boardCols[c] = true
		}
		filtered := []string{}
		for _, c := range cols {
			if boardCols[c] {
				filtered = append(filtered, c)
			}
		}
		cols = filtered
	}
	def := selected
	if def == "" && len(cols) > 0 {
		def = cols[0]
	}
	opts := []Option{}
	for _, cid := range cols {
		opts = append(opts, Option{Value: cid, Label: s.columnName(cid), Selected: cid == def})
	}
	return opts
}

func (s *Server) boardForCard(c *core.Card) *core.Board {
	for _, b := range s.boards {
		if containsStr(b.CardTypeIDs, c.TypeID) {
			return b
		}
	}
	return nil
}

func (s *Server) columnName(id string) string {
	for _, c := range s.ws.Columns {
		if c.ID == id {
			return c.Name
		}
	}
	return id
}

func fieldViews(ct *core.CardType, fields any, users []core.User) []FieldView {
	if ct == nil {
		return nil
	}
	fm, _ := fields.(map[string]any)
	out := []FieldView{}
	for i := range ct.Fields {
		f := ct.Fields[i] // copy to take addr
		if f.Label == "" {
			f.Label = humanizeID(f.ID)
		}
		fv := FieldView{Def: &f, Users: users, Display: f.Display}
		if v, ok := fm[f.ID]; ok {
			fv.Value = v
			fv.ValueStr = renderValue(v)
			fv.ValueRendered = renderValue(v)
		}
		if f.Type == core.FieldRepeating {
			fv.Entries = repeatingEntries(f.ID, fm, f.ItemFields)
		}
		out = append(out, fv)
	}
	return out
}

func repeatingEntries(fieldID string, fm map[string]any, itemFields []core.FieldDef) [][]PreviewField {
	if fm == nil {
		return nil
	}
	v, ok := fm[fieldID]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := [][]PreviewField{}
	for _, e := range arr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		row := []PreviewField{}
		for _, sf := range itemFields {
			val := em[sf.ID]
			row = append(row, PreviewField{Label: sf.ID, Value: renderValue(val)})
		}
		out = append(out, row)
	}
	return out
}

// humanizeID turns a field id like "pull_request_url" into "Pull request url".
func humanizeID(id string) string {
	id = strings.ReplaceAll(id, "_", " ")
	id = strings.ReplaceAll(id, "-", " ")
	if id == "" {
		return id
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

func renderValue(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// trim trailing zeros for integers
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		parts := []string{}
		for _, x := range t {
			parts = append(parts, renderValue(x))
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		b, _ := json.Marshal(t)
		return string(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// --- render helpers ---

func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, name string, data ViewData) {
	data.Theme = s.resolveTheme(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages[name].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// resolveTheme picks the active UI theme for html[data-theme]. Precedence:
// an explicit ?theme= (persisted in a cookie so it sticks across navigation;
// ?theme=default clears it), else the cookie, else the workspace default.
// Empty string = the built-in default theme.
func (s *Server) resolveTheme(w http.ResponseWriter, r *http.Request) string {
	if t := r.URL.Query().Get("theme"); t != "" {
		if t == "default" {
			t = ""
		}
		http.SetCookie(w, &http.Cookie{Name: "wc_theme", Value: t, Path: "/", MaxAge: 31536000})
		return t
	}
	if c, err := r.Cookie("wc_theme"); err == nil && c.Value != "" {
		return c.Value
	}
	return s.ws.Settings.Theme
}

func (s *Server) renderPartial(w http.ResponseWriter, name string, data ViewData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages[name].ExecuteTemplate(w, "content", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

func wantsPartial(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}
