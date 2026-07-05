package httpapi

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/somebox/cards/internal/core"
)

// --- UI handlers ---

// loadCardUI fetches a card for a UI handler, distinguishing an unknown id
// (404) from a store failure (500). It writes the response itself; a nil
// return means the handler should stop.
func (s *Server) loadCardUI(w http.ResponseWriter, r *http.Request, id string) *core.Card {
	c, err := s.svc.GetCard(r.Context(), id)
	if err != nil {
		if ce := core.AsError(err); ce != nil && ce.Code == "not_found" {
			http.NotFound(w, r)
		} else {
			http.Error(w, "failed to load card", http.StatusInternalServerError)
		}
		return nil
	}
	return c
}

// listUsersBestEffort returns workspace users for name display and owner
// dropdowns. User names are decoration on every UI surface — on store failure
// the page degrades to raw ids rather than failing, but the failure is logged.
func (s *Server) listUsersBestEffort(r *http.Request) []core.User {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		log.Printf("WARN: list users for UI render: %v", err)
	}
	return users
}

func (s *Server) uiIndex(w http.ResponseWriter, r *http.Request) {
	// Home page: board list + recent activity — the page's primary content, so
	// a failing store is a 500, not an empty page.
	page, err := s.svc.ListCards(r.Context(), core.CardQuery{Limit: 10})
	if err != nil {
		http.Error(w, "failed to load cards", http.StatusInternalServerError)
		return
	}
	recent := []RecentCard{}
	for _, c := range page.Items {
		label := c.TypeID
		if ct := s.types[c.TypeID]; ct != nil {
			label = ct.Name
		}
		recent = append(recent, RecentCard{
			ID: c.ID, Title: c.Title, TypeID: c.TypeID, TypeLabel: label,
			Status: c.Status, UpdatedAt: c.UpdatedAt.Format(time.RFC3339Nano),
		})
	}
	totalCount := 0
	if all, err := s.svc.ListCards(r.Context(), core.CardQuery{Limit: 200}); err == nil {
		totalCount = len(all.Items)
	}
	// totalCount is a decorative stat; the first query above already proved the
	// store is reachable, so a miss here just shows 0.
	data := s.baseData(s.ws.Name)
	data.Workspace = s.ws
	data.CardCount = totalCount
	data.RecentCards = recent
	data.TypeThemes = s.buildTypeThemes()
	data.Query = r.URL.Query().Get("q")
	s.renderPage(w, r, "home.html", data)
}

// uiSearch renders global search results for GET /ui/search?q= (1d). q is
// shareable/bookmarkable. Results link to /ui/cards/{short}.
func (s *Server) uiSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	// Search results are the page's primary content: a store failure must not
	// render as "no results".
	page, err := s.svc.ListCards(r.Context(), core.CardQuery{Q: q, IDLike: q, Limit: 50})
	if err != nil {
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}
	views := []CardView{}
	users := s.listUsersBestEffort(r)
	for i := range page.Items {
		views = append(views, s.cardView(&page.Items[i], nil, users))
	}
	data := s.baseData("Search")
	data.Query = q
	data.FormTitle = q
	data.CardsByColumn = map[string][]CardView{"results": views}
	data.TypeThemes = s.buildTypeThemes()
	if wantsPartial(r) {
		s.renderPartial(w, "search_results.html", data)
		return
	}
	s.renderPage(w, r, "search_results.html", data)
}

// uiStylesheet serves the embedded design-system CSS.
func (s *Server) uiStylesheet(w http.ResponseWriter, r *http.Request) {
	data, err := templateFS.ReadFile("templates/style.css")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Write(data)
}

func (s *Server) uiBoard(w http.ResponseWriter, r *http.Request) {
	boardID := chi.URLParam(r, "id")
	b, ok := s.boards[boardID]
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := s.boardData(r, b)
	if err != nil {
		http.Error(w, "failed to load board", http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, "board.html", data)
}

// uiBreaches renders the workspace-wide current-conditions page: the same
// report GET /v1/breaches returns, with card/board ids resolved to titles and
// rows linking back to the card modal / board.
func (s *Server) uiBreaches(w http.ResponseWriter, r *http.Request) {
	report, err := s.svc.Breaches(r.Context(), "", nil)
	if err != nil {
		http.Error(w, "failed to load breaches", http.StatusInternalServerError)
		return
	}
	cache := map[string]string{}
	rows := make([]BreachRow, 0, len(report.Items))
	for _, it := range report.Items {
		row := BreachRow{
			Label:   conditionLabel(it.Type),
			RawType: string(it.Type),
			Scope:   it.Scope,
			Column:  it.Column,
			Count:   it.Count,
			Limit:   it.Limit,
			CardID:  it.CardID,
		}
		if it.BoardID != "" {
			row.BoardID = it.BoardID
			if b, ok := s.boards[it.BoardID]; ok {
				row.BoardName = b.Name
			}
		}
		if it.CardID != "" {
			row.CardTitle = s.cardTitle(r.Context(), cache, it.CardID)
		}
		for _, bID := range it.Blockers {
			row.Blockers = append(row.Blockers, LinkView{CardID: bID, Title: s.cardTitle(r.Context(), cache, bID)})
		}
		rows = append(rows, row)
	}
	data := s.baseData("Breaches")
	data.Breaches = rows
	data.BreachAsOf = report.AsOf.Format("2006-01-02 15:04:05 MST")
	s.renderPage(w, r, "breaches.html", data)
}

// boardData builds the ViewData for a board page (cards grouped by column).
// Reused by uiBoard and the htmx move handler so a move re-renders the whole
// board with the card in its new column. Errors on the card queries — the
// board's primary content — so a failing store never renders as empty columns.
func (s *Server) boardData(r *http.Request, b *core.Board) (ViewData, error) {
	q := r.URL.Query().Get("q")
	// Sort precedence: ?sort= request param > board lane_sort > default order.
	sort := r.URL.Query().Get("sort")
	if sort == "" && b.Presentation != nil {
		sort = b.Presentation.LaneSort
	}
	// One query across all of the board's types (not a per-type loop that
	// concatenated separately-ordered pages), so each lane is globally ordered
	// by the resolved sort by construction.
	cq := core.CardQuery{TypeIDIn: b.CardTypeIDs, Sort: sort, Limit: 200}
	if q != "" {
		cq.Q = q
		cq.IDLike = q // scoped to this board's types (1d)
	}
	// Filters compose server-side into the one query, so ordering-under-filter
	// is consistent by construction. A saved filter (?filter=<id>) resolves
	// from the board's presentation.Filters, with "me" substituted to the UI
	// actor; ?owner= / ?type= narrow further.
	actor := s.uiActor()
	activeFilter := r.URL.Query().Get("filter")
	if activeFilter != "" && b.Presentation != nil {
		for _, bf := range b.Presentation.Filters {
			if bf.ID == activeFilter {
				cq.Filter = resolveMeFilter(bf.Filter, actor)
				break
			}
		}
	}
	owner := r.URL.Query().Get("owner")
	if owner == "me" {
		owner = actor
	}
	if owner != "" {
		cq.Owner = owner
	}
	typeID := r.URL.Query().Get("type")
	if typeID != "" {
		cq.TypeID = typeID // ANDs with TypeIDIn to a single board type
	}
	page, err := s.svc.ListCards(r.Context(), cq)
	if err != nil {
		return ViewData{}, fmt.Errorf("board %s: list cards: %w", b.ID, err)
	}
	all := page.Items
	byCol := map[string][]CardView{}
	colSet := map[string]bool{}
	for _, c := range b.Columns {
		colSet[c] = true
		byCol[c] = []CardView{}
	}
	users := s.listUsersBestEffort(r)
	outCount, inCount, commentCount := s.linkGraphCounts(r.Context())
	// One bulk query for every blocked card on this board's types (the same
	// predicate card_blocked/Breaches use) → set membership, so the badge is
	// O(1) per card rather than an N+1 blockers lookup.
	blocked := map[string]bool{}
	if bl, err := s.svc.ListCards(r.Context(), core.CardQuery{Blocked: true, TypeIDIn: b.CardTypeIDs, Limit: 500}); err == nil {
		for _, c := range bl.Items {
			blocked[c.ID] = true
		}
	}
	for i := range all {
		c := &all[i]
		if !colSet[c.Status] {
			continue
		}
		cv := s.cardView(c, b, users)
		cv.CommentCount = commentCount[c.ID]
		cv.OutCount = outCount[c.ID]
		cv.InCount = inCount[c.ID]
		cv.Blocked = blocked[c.ID]
		byCol[c.Status] = append(byCol[c.Status], cv)
	}
	cols := []core.Column{}
	for _, cid := range b.Columns {
		for _, wc := range s.ws.Columns {
			if wc.ID == cid {
				cols = append(cols, wc)
				break
			}
		}
	}
	data := s.baseData(b.Name)
	data.Board = b
	data.Columns = cols
	data.CardsByColumn = byCol
	data.TypeThemes = s.buildTypeThemes()
	data.Query = q
	data.SSETypes = boardSSETypes()
	// The resolved sort drives the header selector's selected state. An empty
	// resolved sort renders as the default "Recently updated" option (which
	// produces the same order the store applies for the zero Sort).
	active := sort
	if active == "" {
		active = "-updated_at"
	}
	data.ActiveSort = active
	data.SortOptions = boardSortOptions(active, b)
	// Filter header: saved-filter chips + owner/type dropdowns, each carrying
	// its active state so the UI reflects the current query.
	data.SavedFilters = boardSavedFilters(b, activeFilter)
	data.ActiveFilter = activeFilter
	data.OwnerOptions = ownerOptions(users, owner)
	data.ActiveOwner = owner
	data.TypeOptions = boardTypeOptions(s.types, b, typeID)
	data.ActiveType = typeID
	return data, nil
}

// boardSavedFilters lists the board's saved filters as selectable chips.
func boardSavedFilters(b *core.Board, active string) []Option {
	if b.Presentation == nil {
		return nil
	}
	opts := make([]Option, 0, len(b.Presentation.Filters))
	for _, bf := range b.Presentation.Filters {
		opts = append(opts, Option{Value: bf.ID, Label: bf.Label, Selected: bf.ID == active})
	}
	return opts
}

// ownerOptions builds the owner dropdown from the workspace users, marking the
// active owner selected. A leading empty "Anyone" option clears the filter.
func ownerOptions(users []core.User, active string) []Option {
	opts := []Option{{Value: "", Label: "Anyone", Selected: active == ""}}
	for _, u := range users {
		opts = append(opts, Option{Value: u.ID, Label: u.ID, Selected: u.ID == active})
	}
	return opts
}

// boardTypeOptions builds the type dropdown from the board's card types.
func boardTypeOptions(types map[string]*core.CardType, b *core.Board, active string) []Option {
	opts := []Option{{Value: "", Label: "All types", Selected: active == ""}}
	for _, tid := range b.CardTypeIDs {
		label := tid
		if ct := types[tid]; ct != nil {
			label = ct.Name
		}
		opts = append(opts, Option{Value: tid, Label: label, Selected: tid == active})
	}
	return opts
}

// boardSortOptions builds the board header's sort selector. The presets cover
// the common orders; a board-declared lane_sort that isn't one of them is
// surfaced as a leading "Board default" option so the configured order is
// selectable by name.
func boardSortOptions(active string, b *core.Board) []Option {
	presets := []Option{
		{Value: "-updated_at", Label: "Recently updated"},
		{Value: "-created_at", Label: "Newest"},
		{Value: "created_at", Label: "Oldest"},
		{Value: "title", Label: "Title (A–Z)"},
	}
	known := map[string]bool{}
	for _, o := range presets {
		known[o.Value] = true
	}
	opts := []Option{}
	if b.Presentation != nil && b.Presentation.LaneSort != "" && !known[b.Presentation.LaneSort] {
		opts = append(opts, Option{Value: b.Presentation.LaneSort, Label: "Board default"})
	}
	opts = append(opts, presets...)
	for i := range opts {
		opts[i].Selected = opts[i].Value == active
	}
	return opts
}

// boardMutationTypes are the durable card/board mutations that change what a
// board lane shows and so must trigger a live re-fetch.
var boardMutationTypes = []string{
	"status_changed", "card_created", "field_updated", "owner_changed",
	"tags_changed", "item_appended", "item_updated", "item_removed",
	"link_added", "comment_added", "artifact_added",
}

// boardSSETypes is the comma-joined event-type filter the board's live stream
// subscribes to: the durable mutations above plus every condition event
// (core.ConditionTypes) so a blocked/unblocked or WIP crossing re-renders the
// badges without a manual reload. Single source for both the stream URL and
// the client-side addEventListener loop.
func boardSSETypes() string {
	types := append([]string{}, boardMutationTypes...)
	for _, t := range core.ConditionTypes() {
		types = append(types, string(t))
	}
	return strings.Join(types, ",")
}

func (s *Server) uiNewCardForm(w http.ResponseWriter, r *http.Request) {
	typeID := r.URL.Query().Get("type")
	if typeID == "" {
		// pick first type
		for id := range s.types {
			typeID = id
			break
		}
	}
	ct, ok := s.types[typeID]
	if !ok {
		http.NotFound(w, r)
		return
	}
	boardID := r.URL.Query().Get("board")
	if boardID == "" {
		for id := range s.boards {
			if containsStr(s.boards[id].CardTypeIDs, typeID) {
				boardID = id
				break
			}
		}
	}
	b := s.boards[boardID]
	users := s.listUsersBestEffort(r)
	data := s.baseData("New " + ct.Name)
	data.CardType = ct
	data.Board = b
	data.Fields = fieldViews(ct, nil, users)
	data.StatusOptions = s.statusOptions(ct, b, "")
	data.Users = users
	data.TagSet = s.ws.TagSet
	data.FormTitle = ""
	data.FormTags = ""
	s.renderPage(w, r, "card_form.html", data)
}

func (s *Server) uiCreateCard(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	typeID := r.FormValue("type_id")
	boardID := r.FormValue("board_id")
	ct, ok := s.types[typeID]
	if !ok {
		http.Error(w, "unknown card type: "+typeID, http.StatusBadRequest)
		return
	}
	b := s.boards[boardID]

	fields := map[string]any{}
	for k := range r.Form {
		if strings.HasPrefix(k, "field:") {
			fid := strings.TrimPrefix(k, "field:")
			val := r.FormValue(k)
			if val == "" {
				continue
			}
			if ct != nil {
				for _, f := range ct.Fields {
					if f.ID == fid {
						if f.Type == core.FieldNumber {
							if n, err := strconv.ParseFloat(val, 64); err == nil {
								fields[fid] = n
							}
						} else {
							fields[fid] = val
						}
						break
					}
				}
			} else {
				fields[fid] = val
			}
		}
	}
	tags := parseTags(r.FormValue("tags"))
	req := core.CreateCardRequest{
		TypeID: typeID,
		Title:  r.FormValue("title"),
		Status: r.FormValue("status"),
		Fields: fields,
		Tags:   tags,
		Actor:  s.ws.Settings.DefaultUser,
	}
	_, err := s.svc.CreateCard(r.Context(), req)
	if err != nil {
		// Re-render form with error.
		users := s.listUsersBestEffort(r)
		data := s.baseData("New " + ct.Name)
		data.CardType = ct
		data.Board = b
		data.Fields = fieldViews(ct, fields, users)
		data.StatusOptions = s.statusOptions(ct, b, req.Status)
		data.Users = users
		data.TagSet = s.ws.TagSet
		data.FormTitle = req.Title
		data.FormTags = r.FormValue("tags")
		data.Error = core.AsError(err)
		s.renderPage(w, r, "card_form.html", data)
		return
	}
	http.Redirect(w, r, "/ui/boards/"+boardID, http.StatusSeeOther)
}

func (s *Server) uiCardDetail(w http.ResponseWriter, r *http.Request) {
	c, err := s.svc.ResolveCard(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		var amb *core.AmbiguousIDError
		if errors.As(err, &amb) {
			s.renderCardAmbiguous(w, r, amb)
			return
		}
		http.NotFound(w, r)
		return
	}
	s.renderCardDetail(w, r, c, nil)
}

// renderCardAmbiguous renders the "did you mean?" page listing candidate
// cards linking to /ui/cards/{full_id}. (1e)
func (s *Server) renderCardAmbiguous(w http.ResponseWriter, r *http.Request, amb *core.AmbiguousIDError) {
	data := s.baseData("Ambiguous id: " + amb.Short)
	data.Error = &core.Error{Code: "ambiguous", Message: amb.Short, HTTPStatus: 409}
	data.Candidates = amb.Candidates
	s.renderPage(w, r, "card_ambiguous.html", data)
}

// uiSaveCard is the modal's single save endpoint: gathers title/status/owner/
// tags/field:* from one form, applies them in one PATCH (respecting the current
// version), and returns the refreshed modal so the client swaps it in.
func (s *Server) uiSaveCard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	// The modal's Save button posts via fetch(FormData), which the browser
	// always encodes as multipart/form-data — ParseForm alone does not parse
	// multipart bodies (only application/x-www-form-urlencoded + query args).
	// ParseMultipartForm calls ParseForm internally and falls back cleanly
	// (ErrNotMultipart) for the urlencoded case used by curl/tests.
	if err := r.ParseMultipartForm(10 << 20); err != nil && err != http.ErrNotMultipart {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	c := s.loadCardUI(w, r, id)
	if c == nil {
		return
	}
	version := c.Version
	if v := r.FormValue("version"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			version = n
		}
	}
	req := core.PatchCardRequest{
		Version: version,
		Actor:   s.ws.Settings.DefaultUser,
		Fields:  map[string]any{},
	}
	if v := r.FormValue("title"); v != c.Title {
		t := v
		req.Title = &t
	}
	if v := r.FormValue("status"); v != "" && v != c.Status {
		st := v
		req.Status = &st
	}
	if owner := r.FormValue("owner"); owner != c.Owner {
		o := owner
		req.Owner = &o
	}
	if tagsVal, ok := getFormIfPresent(r, "tags"); ok {
		tags := parseTags(tagsVal)
		req.Tags = &tags
	}
	for k := range r.Form {
		if strings.HasPrefix(k, "field:") {
			fid := strings.TrimPrefix(k, "field:")
			val := r.FormValue(k)
			// Coerce numbers if the field is numeric.
			if ct := s.types[c.TypeID]; ct != nil {
				for _, f := range ct.Fields {
					if f.ID == fid && f.Type == core.FieldNumber {
						if n, err := strconv.ParseFloat(val, 64); err == nil {
							req.Fields[fid] = n
						} else {
							req.Fields[fid] = val
						}
						break
					}
				}
				if _, set := req.Fields[fid]; !set {
					req.Fields[fid] = val
				}
			} else {
				req.Fields[fid] = val
			}
		}
	}
	_, err := s.svc.PatchCard(r.Context(), id, req)
	if err != nil {
		// Re-render the originating surface (modal or detail) with the error +
		// the version the client had.
		c.Version = version // show the version the user posted with
		if r.FormValue("from") == "detail" {
			s.renderCardDetail(w, r, c, core.AsError(err))
			return
		}
		s.renderCardModalErr(w, r, c, core.AsError(err))
		return
	}
	// Return the refreshed surface: detail move/save re-renders the detail page,
	// modal save re-renders the modal.
	if r.FormValue("from") == "detail" {
		s.uiCardDetail(w, r)
		return
	}
	s.uiCardModal(w, r)
}

// renderCardModalErr re-renders the modal with an error attached.
func (s *Server) renderCardModalErr(w http.ResponseWriter, r *http.Request, c *core.Card, err *core.Error) {
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The response is already streaming; a mid-render template error can only
	// be logged, not turned into a different status.
	if terr := s.pages["card_modal.html"].ExecuteTemplate(w, "card_modal", data); terr != nil {
		log.Printf("ERROR: render card_modal for %s: %v", c.ID, terr)
	}
}
func parseTags(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := []string{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// getFormIfPresent returns the value and ok=false if field absent.
func getFormIfPresent(r *http.Request, key string) (string, bool) {
	if _, ok := r.Form[key]; !ok {
		return "", false
	}
	return r.FormValue(key), true
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
