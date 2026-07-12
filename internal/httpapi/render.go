package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
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
	TypeIcon      string          // 1a — precomputed badge glyph
	TypeAccent    string          // 1a — precomputed accent (overrides [data-type])
	TypeMuted     string          // 1a — precomputed muted shade
	TypeLabel     string          // 1a — precomputed type display name (== CardType.Name)
	BoardID       string          // board this render belongs to; card links carry it as ?board= so the modal keeps the same theming context
	StatusLabel   string          // board card: resolved column/status display name
	Artifacts     []*ArtifactView // board card: stored artifacts (thumbnails / download chips), live via artifact_added SSE
	CommentCount  int             // board card: number of comments
	OutCount      int             // board card: number of outbound links
	InCount       int             // board card: number of inbound links (others → this)
	Blocked       bool            // board card: has an unresolved blocked-by link (condition engine)
}

// BreachRow is one resolved condition for the /ui/breaches page: the raw
// BreachItem with card/board ids replaced by human titles for display.
type BreachRow struct {
	Label     string     // human condition name ("WIP exceeded")
	RawType   string     // event slug, for [data-condition] styling
	Scope     string     // "board" | "card"
	BoardID   string     // links to the board page (board-scoped rows)
	BoardName string     // board display name
	Column    string     // column label (WIP/lane rows)
	Count     int        // current column count (WIP/lane rows)
	Limit     int        // configured cap (WIP rows)
	CardID    string     // links to the card modal (card-scoped rows)
	CardTitle string     // card display title
	Blockers  []LinkView // resolved blocker titles (card_blocked rows)
}

// conditionLabel maps a condition event slug to its human page label.
func conditionLabel(t core.EventType) string {
	switch t {
	case core.EventWIPExceeded:
		return "WIP exceeded"
	case core.EventLaneDrained:
		return "Lane empty"
	case core.EventCardBlocked:
		return "Card blocked"
	default:
		return string(t)
	}
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
	Kind  string // item-field type (user|date|...) — lets entry layout place chips/timestamps without knowing field ids
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
	Entries       []EntryView
	Users         []core.User
	ValueRendered string
	Display       string        // UI hint from FieldDef.Display (feed|badge|hidden|link|monospace)
	Artifact      *ArtifactView // set for artifact fields with stored bytes
}

// EntryView is one repeating-field entry in the modal/detail view: rendered
// rows plus what the in-modal entry editor needs — the entry id (for
// update/remove) and the raw values (for prefill; rendered values may be
// formatted for display).
type EntryView struct {
	ID     string
	Fields []PreviewField
	Raw    map[string]string // item-field id → raw value string, editor prefill
}

// ArtifactView is the rendered metadata of an artifact field, so card_detail can
// show a real thumbnail/download instead of the raw {uri,mime,size} JSON.
type ArtifactView struct {
	URI     string
	MIME    string
	Size    int64
	IsImage bool
	Href    string // GET /v1/artifacts/<uri>
}

// ViewData is the template payload.
type ViewData struct {
	Title         string
	Theme         string // active UI theme name for html[data-theme] (empty = default)
	Actor         string // resolved UI actor (CARDS_USER → workspace default); sent as X-Work-Cards-Actor by the board JS
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
	// TypeThemes is the id→TypeTheme map for non-card call-sites (home recent
	// rows, type pickers) that look up by type id via the typeTheme template
	// func. Per-card surfaces use CardTheme / CardView instead. (1a / P4a)
	TypeThemes map[string]core.TypeTheme `json:"-"`
	// CardTheme is the precomputed effective theme for the modal/detail card
	// (resolveCardTheme). card_head reads this — not a live typeTheme lookup —
	// so board cards and modal/detail share one resolution path. (P4a)
	CardTheme core.TypeTheme `json:"-"`
	// Candidates is the disambiguation list shown by card_ambiguous.html when a
	// short id matches >1 card. (1e)
	Candidates []core.CardCandidate
	// Query is the current ?q= search string, repopulating the search box (1d).
	Query string
	// SSETypes is the comma-joined event-type filter the board's live stream
	// subscribes to (board mutations + condition events).
	SSETypes string
	// SortOptions / ActiveSort drive the board header's sort selector.
	SortOptions []Option
	ActiveSort  string
	// Board filter header: saved-filter chips + owner/type dropdowns.
	SavedFilters []Option
	ActiveFilter string
	OwnerOptions []Option
	ActiveOwner  string
	TypeOptions  []Option
	ActiveType   string
	// Home page
	Workspace   *core.Workspace
	CardCount   int
	RecentCards []RecentCard
	// Card detail/modal relationships (resolved titles, in/outbound).
	OutLinks []LinkView
	InLinks  []LinkView
	// Breaches page (/ui/breaches): resolved current conditions + snapshot time.
	Breaches   []BreachRow
	BreachAsOf string
	// MaxArtifactBytes is the server's per-upload cap, surfaced so the card
	// modal's file input can reject an oversize file client-side before POST.
	MaxArtifactBytes int64
	// CreateStatus is the lane a "+ in this column" creation pre-selects (P2).
	CreateStatus string
	// TagPolicy is workspace settings.tag_policy — the tags chip editor allows
	// free-add under open/propose and restricts to tag_set otherwise (P6).
	TagPolicy string
	// AllColumnIDs / AllTypeIDs pre-project every workspace column / offered
	// type id into a slice so the board-create form can seed its Alpine
	// checkbox arrays (x-model on a checkbox array is authoritative — it
	// unchecks anything the array does not contain at init).
	AllColumnIDs []string
	AllTypeIDs   []string
}

func (s *Server) baseData(title string) ViewData {
	return ViewData{Title: title, Boards: s.boards, Actor: s.uiActor(), MaxArtifactBytes: maxArtifactBytes,
		TagPolicy: s.ws.Settings.TagPolicy}
}

// uiActor is the identity the server-rendered UI acts as: CARDS_USER if set,
// else the workspace default_user. There is no per-request browser header
// (the UI is the trusted-local surface), so this is the single injected
// actor the board JS sends as X-Work-Cards-Actor — replacing the old
// hardcoded 'foz'.
func (s *Server) uiActor() string {
	if s.envUser != "" {
		return s.envUser
	}
	return s.ws.Settings.DefaultUser
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
	b := s.boardFromRequest(r, c)
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
	data.CardTheme = s.resolveCardTheme(c, b)
	data.OutLinks, data.InLinks = s.cardRelations(r.Context(), c)
	data.Theme = s.resolveTheme(w, r, b)
	// Honest HTTP status on the save-error path (rebuild P8): a stale save
	// or a validation failure re-renders the detail page WITH the alert,
	// but returns the real 4xx so the client (cardsAPI) can tell success
	// from failure without inspecting the body.
	if err != nil {
		status := err.HTTPStatus
		if status == 0 {
			status = http.StatusUnprocessableEntity
		}
		w.WriteHeader(status)
	}
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
	b := s.boardFromRequest(r, c)
	users := s.listUsersBestEffort(r)
	data := s.baseData(c.Title)
	data.Card = c
	data.CardType = ct
	data.Board = b
	data.Fields = fieldViews(ct, c.Fields, users)
	data.MoveOptions = s.moveOptions(b, c.Status)
	data.Users = users
	data.TagSet = s.ws.TagSet
	data.CardTheme = s.resolveCardTheme(c, b)
	data.OutLinks, data.InLinks = s.cardRelations(r.Context(), c)
	data.Theme = s.resolveTheme(w, r, b)
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

// resolveCardTheme is the single per-card theme resolution path for board
// cards (cardView) and modal/detail (ViewData.CardTheme). Precedence is
// option-theme → type-theme → CSS-default (docs/design/STYLE-FIELD.md).
func (s *Server) resolveCardTheme(c *core.Card, b *core.Board) core.TypeTheme {
	if c == nil {
		return core.TypeTheme{}
	}
	th := typeTheme(s.types[c.TypeID])
	if b == nil || b.Presentation == nil {
		return th
	}
	sf := b.Presentation.StyleField
	if sf == "" {
		return th
	}
	opt := optionThemeFor(s.types[c.TypeID], c, sf)
	return mergeTypeTheme(th, opt)
}

// optionThemeFor looks up OptionThemes[value] for the board's style_field on
// this card's type. Empty when the field is absent, unset, or unthemed.
func optionThemeFor(ct *core.CardType, c *core.Card, styleField string) core.TypeTheme {
	if ct == nil || c == nil || styleField == "" {
		return core.TypeTheme{}
	}
	var fd *core.FieldDef
	for i := range ct.Fields {
		if ct.Fields[i].ID == styleField {
			fd = &ct.Fields[i]
			break
		}
	}
	if fd == nil || len(fd.OptionThemes) == 0 {
		return core.TypeTheme{}
	}
	fm, ok := c.Fields.(map[string]any)
	if !ok {
		return core.TypeTheme{}
	}
	raw, ok := fm[styleField]
	if !ok || raw == nil {
		return core.TypeTheme{}
	}
	val, ok := raw.(string)
	if !ok || val == "" {
		return core.TypeTheme{}
	}
	return fd.OptionThemes[val]
}

// mergeTypeTheme overlays non-empty fields from over onto base.
func mergeTypeTheme(base, over core.TypeTheme) core.TypeTheme {
	if over.Icon != "" {
		base.Icon = over.Icon
	}
	if over.Accent != "" {
		base.Accent = over.Accent
	}
	if over.Muted != "" {
		base.Muted = over.Muted
	}
	return base
}

// buildTypeThemes assembles the id→TypeTheme map used by ViewData.TypeThemes
// for type-id call-sites (home, create/board type pickers). (1a)
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
	th := s.resolveCardTheme(c, b)
	label := ""
	if ct != nil {
		label = ct.Name
	}
	boardID := ""
	if b != nil {
		boardID = b.ID
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
		BoardID:       boardID,
		StatusLabel:   s.columnName(c.Status),
		Artifacts:     cardArtifacts(ct, c),
	}
}

// cardArtifacts collects the stored artifacts on a card's artifact fields, in
// the type's field order, so the board card can show a thumbnail (images) or a
// download chip (everything else). Empty when nothing is attached.
func cardArtifacts(ct *core.CardType, c *core.Card) []*ArtifactView {
	if ct == nil {
		return nil
	}
	fm, ok := c.Fields.(map[string]any)
	if !ok {
		return nil
	}
	var out []*ArtifactView
	for i := range ct.Fields {
		if ct.Fields[i].Type != core.FieldArtifact {
			continue
		}
		if av := artifactView(fm[ct.Fields[i].ID]); av != nil {
			out = append(out, av)
		}
	}
	return out
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
			if cid != current && !core.Contains(allowed, cid) {
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

// statusOptions builds the create-modal's status select: every column of the
// board (or workspace, if boardless) in board order, with columns the type's
// allowed_columns forbids DISABLED rather than hidden — so the board's shape
// stays visible and the constraint is explainable, not mysterious. The
// selection is the preset lane when allowed, else the first allowed column.
func (s *Server) statusOptions(ct *core.CardType, b *core.Board, selected string) []Option {
	var cols []string
	if b != nil {
		cols = b.Columns
	} else {
		for _, c := range s.ws.Columns {
			cols = append(cols, c.ID)
		}
	}
	allowed := func(cid string) bool {
		return len(ct.AllowedColumns) == 0 || core.Contains(ct.AllowedColumns, cid)
	}
	def := selected
	if def == "" || !allowed(def) {
		def = ""
		for _, cid := range cols {
			if allowed(cid) {
				def = cid
				break
			}
		}
	}
	opts := []Option{}
	for _, cid := range cols {
		opts = append(opts, Option{
			Value: cid, Label: s.columnName(cid),
			Selected: cid == def,
			Disabled: !allowed(cid),
		})
	}
	return opts
}

// boardForCard picks the board a card is themed/moved against when the
// request carries no board context. Deterministic (sorted ids): with a type
// on several boards, Go map order must not flip the modal's option theme per
// request. Prefer boardFromRequest wherever an *http.Request is available.
func (s *Server) boardForCard(c *core.Card) *core.Board {
	ids := make([]string, 0, len(s.boards))
	for id := range s.boards {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if b := s.boards[id]; core.Contains(b.CardTypeIDs, c.TypeID) {
			return b
		}
	}
	return nil
}

// boardFromRequest resolves the board context for a card render: an explicit
// ?board= naming a board that actually hosts the card's type wins (the board
// page passes it, so "the same card renders differently on two boards" holds
// in the modal too); anything else falls back to the deterministic pick.
func (s *Server) boardFromRequest(r *http.Request, c *core.Card) *core.Board {
	if id := r.URL.Query().Get("board"); id != "" {
		if b, ok := s.boards[id]; ok && core.Contains(b.CardTypeIDs, c.TypeID) {
			return b
		}
	}
	return s.boardForCard(c)
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
		if f.Type == core.FieldArtifact {
			fv.Artifact = artifactView(fv.Value)
		}
		out = append(out, fv)
	}
	return out
}

// artifactView extracts an artifact field's stored metadata ({uri,mime,size})
// into a view with a serve href and an image hint; nil if no bytes are stored.
func artifactView(v any) *ArtifactView {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	uri, _ := m["uri"].(string)
	if uri == "" {
		return nil
	}
	mime, _ := m["mime"].(string)
	var size int64
	switch s := m["size"].(type) {
	case float64:
		size = int64(s)
	case int64:
		size = s
	}
	return &ArtifactView{
		URI:     uri,
		MIME:    mime,
		Size:    size,
		IsImage: strings.HasPrefix(mime, "image/"),
		Href:    "/v1/artifacts/" + uri,
	}
}

func repeatingEntries(fieldID string, fm map[string]any, itemFields []core.FieldDef) []EntryView {
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
	out := []EntryView{}
	for _, e := range arr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		ev := EntryView{Raw: map[string]string{}}
		if id, _ := em["entry_id"].(string); id != "" {
			ev.ID = id
		}
		for _, sf := range itemFields {
			val := em[sf.ID]
			ev.Fields = append(ev.Fields, PreviewField{Label: sf.ID, Value: renderValue(val), Kind: string(sf.Type)})
			if val != nil {
				ev.Raw[sf.ID] = fmt.Sprintf("%v", val)
			}
		}
		out = append(out, ev)
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
	data.Theme = s.resolveTheme(w, r, data.Board)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages[name].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// resolveTheme picks the active UI theme for html[data-theme]. Precedence,
// highest first: an explicit ?theme= (persisted in a cookie so it sticks across
// navigation; ?theme=default clears it), else the cookie, else the board's
// presentation.theme (board may be nil), else the workspace default. Empty
// string = the built-in default theme. See docs/design/THEMES.md.
func (s *Server) resolveTheme(w http.ResponseWriter, r *http.Request, board *core.Board) string {
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
	if board != nil && board.Presentation != nil && board.Presentation.Theme != "" {
		return board.Presentation.Theme
	}
	return s.ws.Settings.Theme
}

func (s *Server) renderPartial(w http.ResponseWriter, name string, data ViewData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages[name].ExecuteTemplate(w, "content", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// wantsPartial reports whether the client asked for an HTML fragment (no
// layout shell) — set by ui.js on modal loads, in-place saves, and the SSE
// board refetch. Renamed from htmx's HX-Request when the unused htmx
// dependency was removed (frontend-rebuild Phase 2); the semantics are ours,
// so the header is too.
func wantsPartial(r *http.Request) bool {
	return r.Header.Get("X-Cards-Partial") == "true"
}
