// Package httpapi exposes the core service over REST (/v1) and SSE.
// Handlers are thin; all rules live in internal/core. The same Server also
// serves a lightweight server-rendered htmx UI under /ui.
//
// See docs/SPEC.md (§11 API surface) and docs/ARCHITECTURE.md (Core Service
// Boundary). UI is a reference consumer, not part of the kernel.
package httpapi

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/openapi"
)

//go:embed templates/*.html templates/*.css
var templateFS embed.FS

// Server is the HTTP/SSE server. Routes mirror SPEC.md §11 plus /ui.
type Server struct {
	svc     *core.Service
	boards  map[string]*core.Board
	types   map[string]*core.CardType
	ws      *core.Workspace
	store   core.Store
	base    *template.Template // layout + FuncMap, cloned per render
	pages   map[string]*template.Template // pre-parsed page sets (layout+page+partials)
	envUser string
}

// New constructs the Server, parsing embedded templates into per-page sets.
func New(svc *core.Service, ws *core.Workspace, types map[string]*core.CardType, boards map[string]*core.Board, st core.Store) (*Server, error) {
	funcMap := template.FuncMap{
		"join": strings.Join,
	}
	base := template.New("base").Funcs(funcMap)
	// Parse layout first so clones carry it.
	base, err := base.ParseFS(templateFS, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}
	// Per-page sets: each clones the base (layout + funcs) then parses its own
	// page + partials so "content"/"card_partial"/"field_input" never collide
	// across pages.
	pageSets := map[string][]string{
		"board.html":        {"templates/board.html", "templates/card_partial.html"},
		"card_detail.html":  {"templates/card_detail.html"},
		"card_form.html":    {"templates/card_form.html"},
		"card_modal.html":   {"templates/card_modal.html"},
		"home.html":         {"templates/home.html"},
	}
	pages := map[string]*template.Template{}
	for name, files := range pageSets {
		clone, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone for %s: %w", name, err)
		}
		parsed, err := clone.ParseFS(templateFS, files...)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		pages[name] = parsed
	}
	return &Server{
		svc: svc, ws: ws, types: types, boards: boards, store: st,
		base: base, pages: pages, envUser: os.Getenv("CARDS_USER"),
	}, nil
}

// Router builds the chi router with /v1 API and /ui HTML routes.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	// --- API ---
	r.Get("/v1/health", s.apiHealth)
	r.Get("/v1/workspace", s.apiWorkspace)
	r.Get("/v1/cards", s.apiListCards)
	r.Post("/v1/cards", s.withActor(s.idempotent(s.apiCreateCard)))
	r.Get("/v1/cards/{id}", s.apiGetCard)
	r.Patch("/v1/cards/{id}", s.withActor(s.idempotent(s.apiPatchCard)))
	r.Post("/v1/cards/{id}/upgrade-schema", s.withActor(s.idempotent(s.apiUpgradeSchema)))
	r.Post("/v1/cards/take-next", s.withActor(s.idempotent(s.apiTakeNext)))
	r.Post("/v1/cards/{id}/claim", s.withActor(s.idempotent(s.apiClaim)))
	r.Post("/v1/cards/{id}/release", s.withActor(s.idempotent(s.apiRelease)))
	r.Post("/v1/cards/{id}/links", s.withActor(s.idempotent(s.apiAddLink)))
	r.Delete("/v1/cards/{id}/links/{typeID}/{target}", s.withActor(s.apiRemoveLink))
	r.Post("/v1/cards/{id}/comments", s.withActor(s.idempotent(s.apiAddComment)))
	r.Patch("/v1/cards/{id}/comments/{commentID}", s.withActor(s.idempotent(s.apiEditComment)))
	r.Post("/v1/cards/{id}/fields/{field}/append", s.withActor(s.idempotent(s.apiAppendEntry)))
	r.Patch("/v1/cards/{id}/fields/{field}/{entryID}", s.withActor(s.idempotent(s.apiUpdateEntry)))
	r.Delete("/v1/cards/{id}/fields/{field}/{entryID}", s.withActor(s.apiRemoveEntry))
	r.Get("/v1/cards/{id}/events", s.apiCardEvents)
	r.Get("/v1/cards/{id}/history", s.apiCardHistory)
	r.Get("/v1/events", s.apiEventFeed)
	r.Get("/v1/events/stream", s.apiEventStream)
	r.Get("/v1/openapi.json", s.apiOpenAPI)
	r.Post("/v1/users", s.apiRegisterUser)

	// --- UI ---
	r.Get("/", s.uiIndex)
	r.Get("/ui/style.css", s.uiStylesheet)
	r.Get("/ui/boards/{id}", s.uiBoard)
	r.Get("/ui/cards/new", s.uiNewCardForm)
	r.Post("/ui/cards", s.uiCreateCard)
	r.Get("/ui/cards/{id}", s.uiCardDetail)
	r.Get("/ui/cards/{id}/modal", s.uiCardModal)
	r.Post("/ui/cards/{id}/move", s.uiMoveCard)
	r.Post("/ui/cards/{id}/field", s.uiEditField)
	r.Post("/ui/cards/{id}/save", s.uiSaveCard)

	return r
}

// --- actor middleware ---

func (s *Server) resolveActor(r *http.Request) string {
	if h := r.Header.Get("X-Work-Cards-Actor"); h != "" {
		return h
	}
	if s.envUser != "" {
		return s.envUser
	}
	return s.ws.Settings.DefaultUser
}

// withActor wraps write handlers that need an actor (API only; UI always has default).
func (s *Server) withActor(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := s.resolveActor(r)
		if actor == "" {
			writeAPIError(w, core.ActorRequired())
			return
		}
		r = r.WithContext(core.WithActor(r.Context(), actor))
		h(w, r)
	}
}

type actorKey struct{}

var _ = actorKey{} // retained for reference; actor now flows via core.WithActor.

func (s *Server) actorFromCtx(r *http.Request) string {
	if a := core.ActorFromCtx(r.Context()); a != "" {
		return a
	}
	return s.ws.Settings.DefaultUser
}

// --- API handlers ---

func (s *Server) apiHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"version":      "poc",
		"workspace_id": s.ws.ID,
	})
}

func (s *Server) apiWorkspace(w http.ResponseWriter, r *http.Request) {
	snap, err := s.svc.Workspace(r.Context())
	if err != nil {
		writeAPIError(w, core.NotFound("workspace"))
		return
	}
	writeJSON(w, 200, snap)
}

func (s *Server) apiListCards(w http.ResponseWriter, r *http.Request) {
	q := core.CardQuery{
		BoardID:    r.URL.Query().Get("board_id"),
		TypeID:     r.URL.Query().Get("type_id"),
		Status:     r.URL.Query().Get("status"),
		Owner:      r.URL.Query().Get("owner"),
		Q:          r.URL.Query().Get("q"),
		Blocked:    r.URL.Query().Get("blocked") == "true",
		HasLink:    r.URL.Query().Get("has_link"),
		LinkTarget: r.URL.Query().Get("link_target"),
		Cursor:     r.URL.Query().Get("cursor"),
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			q.Limit = n
		}
	}
	page, err := s.svc.ListCards(r.Context(), q)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) apiCreateCard(w http.ResponseWriter, r *http.Request) {
	var req core.CreateCardRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	req.Actor = s.actorFromCtx(r)
	c, err := s.svc.CreateCard(r.Context(), req)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	status := 201
	if req.DryRun {
		status = 200
		w.Header().Set("Dry-Run", "true")
	}
	writeJSON(w, status, c)
}

func (s *Server) apiGetCard(w http.ResponseWriter, r *http.Request) {
	c, err := s.svc.GetCard(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) apiPatchCard(w http.ResponseWriter, r *http.Request) {
	var req core.PatchCardRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	req.Actor = s.actorFromCtx(r)
	c, err := s.svc.PatchCard(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	if req.DryRun {
		w.Header().Set("Dry-Run", "true")
	}
	writeJSON(w, 200, c)
}

func (s *Server) apiOpenAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, openapi.Build(s.ws, s.types))
}

func (s *Server) apiUpgradeSchema(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetVersion int  `json:"target_version"`
		DryRun        bool `json:"dry_run"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	c, err := s.svc.UpgradeSchema(r.Context(), chi.URLParam(r, "id"), core.UpgradeSchemaRequest{
		TargetVersion: body.TargetVersion, DryRun: body.DryRun, Actor: s.actorFromCtx(r),
	})
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	if body.DryRun {
		w.Header().Set("Dry-Run", "true")
	}
	writeJSON(w, 200, c)
}

func (s *Server) apiRegisterUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Kind        string `json:"kind"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON"))
		return
	}
	if body.ID == "" {
		writeAPIError(w, core.NewValidationError("id", "id is required"))
		return
	}
	if body.Kind == "" {
		body.Kind = "human"
	}
	u := core.User{ID: body.ID, DisplayName: body.DisplayName, Kind: body.Kind, CreatedAt: time.Now().UTC()}
	if err := s.store.InsertUser(r.Context(), u); err != nil {
		writeAPIError(w, core.NewValidationError("id", "could not register: "+err.Error()))
		return
	}
	writeJSON(w, 201, u)
}

// --- coordination loop handlers ---

func (s *Server) apiClaim(w http.ResponseWriter, r *http.Request) {
	var req core.ClaimRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	req.Actor = s.actorFromCtx(r)
	c, err := s.svc.Claim(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) apiRelease(w http.ResponseWriter, r *http.Request) {
	var req core.ReleaseRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	req.Actor = s.actorFromCtx(r)
	c, err := s.svc.Release(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) apiTakeNext(w http.ResponseWriter, r *http.Request) {
	var req core.TakeNextRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	req.Actor = s.actorFromCtx(r)
	c, err := s.svc.TakeNext(r.Context(), req)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	if c == nil {
		writeJSON(w, 200, map[string]any{"card": nil})
		return
	}
	writeJSON(w, 200, map[string]any{"card": c})
}

func (s *Server) apiAppendEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Entry   map[string]any `json:"entry"`
		Version int            `json:"version"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	c, err := s.svc.AppendEntry(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "field"), body.Entry, body.Version)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) apiUpdateEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Entry   map[string]any `json:"entry"`
		Version int            `json:"version"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	c, err := s.svc.UpdateEntry(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "field"), chi.URLParam(r, "entryID"), body.Entry, body.Version)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) apiRemoveEntry(w http.ResponseWriter, r *http.Request) {
	// Version is required for CAS (lost-update protection). Accept it via
	// query param (?version=N) since DELETE requests typically have no body.
	versionStr := r.URL.Query().Get("version")
	if versionStr == "" {
		writeAPIError(w, core.NewValidationError("version", "version is required for entry deletion (use ?version=N)"))
		return
	}
	version := 0
	for _, c := range versionStr {
		if c < '0' || c > '9' {
			writeAPIError(w, core.NewValidationError("version", "version must be a positive integer"))
			return
		}
		version = version*10 + int(c-'0')
	}
	c, err := s.svc.RemoveEntry(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "field"), chi.URLParam(r, "entryID"), version)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) apiAddLink(w http.ResponseWriter, r *http.Request) {
	var req core.LinkInput
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	req.Actor = s.actorFromCtx(r)
	c, err := s.svc.AddLink(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 201, c)
}

func (s *Server) apiRemoveLink(w http.ResponseWriter, r *http.Request) {
	c, err := s.svc.RemoveLink(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "typeID"), chi.URLParam(r, "target"))
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) apiAddComment(w http.ResponseWriter, r *http.Request) {
	var req core.CommentInput
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	c, err := s.svc.AddComment(r.Context(), chi.URLParam(r, "id"), req.Body)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 201, c)
}

func (s *Server) apiEditComment(w http.ResponseWriter, r *http.Request) {
	var body core.CommentInput
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, core.NewValidationError("body", "invalid JSON: "+err.Error()))
		return
	}
	c, err := s.svc.EditComment(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "commentID"), body.Body)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) apiCardEvents(w http.ResponseWriter, r *http.Request) {
	q := core.EventQuery{CardID: chi.URLParam(r, "id")}
	if t := r.URL.Query().Get("types"); t != "" {
		q.Types = strings.Split(t, ",")
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			q.Limit = n
		}
	}
	evs, err := s.svc.ListEvents(r.Context(), q)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, map[string]any{"items": evs})
}

// apiEventFeed is the cursor-paged catch-up feed:
// GET /v1/events?since=&cursor=&actor=&owner=&type=&types=&board_id=&limit=
// since= and cursor= are both event-id floors (events with id > value); cursor=
// is the pagination continuation (the prior page's next_cursor) and overrides
// since= when present. This is the durable recovery path — replay missed facts,
// then resume the live SSE stream. SPEC §11, docs/INTEGRATION.md.
func (s *Server) apiEventFeed(w http.ResponseWriter, r *http.Request) {
	qv := r.URL.Query()
	q := core.EventQuery{
		Actor: qv.Get("actor"),
		Owner: qv.Get("owner"),
	}
	// type= (single) and types= (CSV) both populate the type filter.
	if t := qv.Get("types"); t != "" {
		q.Types = splitCSV(t)
	} else if t := qv.Get("type"); t != "" {
		q.Types = splitCSV(t)
	}
	// board_id scopes the feed to the board's card types.
	if boardID := qv.Get("board_id"); boardID != "" {
		b := s.boards[boardID]
		if b == nil {
			writeAPIError(w, core.NewValidationError("board_id", "unknown board_id"))
			return
		}
		q.CardTypeIn = b.CardTypeIDs
	}
	// cursor= overrides since=; both are event-id floors.
	floor := qv.Get("cursor")
	if floor == "" {
		floor = qv.Get("since")
	}
	if floor != "" {
		n, ok := parseEventID(floor)
		if !ok {
			writeAPIError(w, core.NewValidationError("cursor", "invalid cursor/since: must be a positive integer event id"))
			return
		}
		q.AfterID = n
	}
	if l := qv.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			q.Limit = n
		}
	}
	page, err := s.svc.ListEventsPage(r.Context(), q)
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) apiCardHistory(w http.ResponseWriter, r *http.Request) {
	h, err := s.svc.History(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAPIError(w, core.AsError(err))
		return
	}
	writeJSON(w, 200, map[string]any{"items": h})
}

// apiEventStream is the SSE endpoint: GET /v1/events/stream?card_id=&types=&board_id=
// Supports Last-Event-ID (and since=) for resumable replay. SPEC §3/§11 D11.
func (s *Server) apiEventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	cardID := r.URL.Query().Get("card_id")
	types := splitCSV(r.URL.Query().Get("types"))
	boardID := r.URL.Query().Get("board_id")
	actor := r.URL.Query().Get("actor")
	owner := r.URL.Query().Get("owner")

	// Validate Last-Event-ID / since before committing the SSE response —
	// an invalid value should be a 400, not silently treated as 0.
	var afterID int64
	var leidRaw string
	if leid := r.Header.Get("Last-Event-ID"); leid != "" {
		leidRaw = leid
	} else if since := r.URL.Query().Get("since"); since != "" {
		leidRaw = since
	}
	if leidRaw != "" {
		n, ok := parseEventID(leidRaw)
		if !ok {
			writeAPIError(w, core.NewValidationError("last_event_id", "invalid Last-Event-ID (or since=): must be a positive integer"))
			return
		}
		afterID = n
	}

	// SSE headers.
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // disable proxy buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Replay: events after Last-Event-ID (or since=) matching the filter.
	if afterID > 0 {
		evs, _ := s.svc.ListEvents(r.Context(), core.EventQuery{CardID: cardID, Types: types, Actor: actor, Owner: owner, AfterID: afterID, Limit: 500})
		for _, e := range filterBoardEvents(s, evs, boardID) {
			writeSSEEvent(w, &e)
		}
		flusher.Flush()
	}

	// Live: subscribe to the bus.
	filter := core.EventFilter{CardID: cardID, Types: types, Actor: actor}
	sub := s.svc.Bus().Subscribe(filter, 64)
	defer s.svc.Bus().Unsubscribe(sub.ID)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.Ch:
			if !ok {
				// Dropped (slow consumer). Send a comment and resubscribe so the
			// client can decide to reconnect with Last-Event-ID.
				w.Write([]byte(": dropped, reconnect\n\n"))
				flusher.Flush()
				return
			}
			if boardID != "" && !s.cardInBoard(e.CardID, boardID) {
				continue
			}
			if owner != "" && !s.cardOwnedBy(e.CardID, owner) {
				continue
			}
			writeSSEEvent(w, e)
			flusher.Flush()
		}
	}
}

// writeSSEEvent writes one event in SSE wire format.
func writeSSEEvent(w io.Writer, e *core.Event) {
	payload, _ := json.Marshal(map[string]any{
		"id": e.ID, "type": e.Type, "card_id": e.CardID, "actor": e.Actor, "at": e.At, "diff": e.Diff,
	})
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, payload)
}

// filterBoardEvents keeps only events whose card belongs to the board (POC:
// board membership = card's type is in the board's card_type_ids).
func filterBoardEvents(s *Server, evs []core.Event, boardID string) []core.Event {
	if boardID == "" {
		return evs
	}
	out := make([]core.Event, 0, len(evs))
	for _, e := range evs {
		if s.cardInBoard(e.CardID, boardID) {
			out = append(out, e)
		}
	}
	return out
}

// cardInBoard reports whether the card's type is in the board's card_type_ids.
func (s *Server) cardInBoard(cardID, boardID string) bool {
	b := s.boards[boardID]
	if b == nil {
		return false
	}
	c, err := s.svc.GetCard(context.Background(), cardID)
	if err != nil {
		return false
	}
	for _, t := range b.CardTypeIDs {
		if t == c.TypeID {
			return true
		}
	}
	return false
}

// cardOwnedBy reports whether the card is currently owned by owner. Used to
// filter the live SSE stream by owner (the feed does this in SQL).
func (s *Server) cardOwnedBy(cardID, owner string) bool {
	c, err := s.svc.GetCard(context.Background(), cardID)
	if err != nil {
		return false
	}
	return c.Owner == owner
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseEventID validates and parses a positive-integer event id (used for
// Last-Event-ID / since=). Returns false on any non-numeric or empty input.
func parseEventID(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

// --- idempotency ---

// idempotent wraps a write handler so that an Idempotency-Key header replays
// the original response. SPEC §11. Key is scoped per actor.
func (s *Server) idempotent(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			h(w, r)
			return
		}
		actor := s.actorFromCtx(r)
		rec, err := s.store.GetIdempotency(r.Context(), key, actor)
		if err != nil {
			writeAPIError(w, core.NewValidationError("idempotency", err.Error()))
			return
		}
		if rec != nil {
			// Replay: SPEC §10 lists idempotency_replay as HTTP 200 carrying the
			// original response body.
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(200)
			w.Write(rec.Body)
			return
		}
		// Record the response.
		rw := &recordingWriter{header: http.Header{}, status: 200, buf: new(bytes.Buffer)}
		h(rw, r)
		_ = s.store.PutIdempotency(r.Context(), core.IdempotencyRecord{
			Key: key, Actor: actor, Status: rw.status, Body: rw.buf.Bytes(),
		})
		// Forward to the real response writer.
		for k, vs := range rw.header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rw.status)
		w.Write(rw.buf.Bytes())
	}
}

// recordingWriter captures a handler's response for idempotency replay.
type recordingWriter struct {
	header http.Header
	status int
	buf    *bytes.Buffer
}

func (rw *recordingWriter) Header() http.Header {
	if rw.header == nil {
		rw.header = http.Header{}
	}
	return rw.header
}
func (rw *recordingWriter) WriteHeader(code int) { rw.status = code }
func (rw *recordingWriter) Write(b []byte) (int, error) { return rw.buf.Write(b) }


// --- UI handlers ---

func (s *Server) uiIndex(w http.ResponseWriter, r *http.Request) {
	// Home page: board list + recent activity.
	page, _ := s.svc.ListCards(r.Context(), core.CardQuery{Limit: 10})
	recent := []RecentCard{}
	if page != nil {
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
	}
	totalCount := 0
	if all, _ := s.svc.ListCards(r.Context(), core.CardQuery{Limit: 200}); all != nil {
		totalCount = len(all.Items)
	}
	data := s.baseData(s.ws.Name)
	data.Workspace = s.ws
	data.CardCount = totalCount
	data.RecentCards = recent
	s.renderPage(w, "home.html", data)
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
	s.renderPage(w, "board.html", s.boardData(r, b))
}

// boardData builds the ViewData for a board page (cards grouped by column).
// Reused by uiBoard and the htmx move handler so a move re-renders the whole
// board with the card in its new column.
func (s *Server) boardData(r *http.Request, b *core.Board) ViewData {
	all := []core.Card{}
	for _, t := range b.CardTypeIDs {
		page, err := s.svc.ListCards(r.Context(), core.CardQuery{TypeID: t, Limit: 200})
		if err != nil {
			continue
		}
		all = append(all, page.Items...)
	}
	byCol := map[string][]CardView{}
	colSet := map[string]bool{}
	for _, c := range b.Columns {
		colSet[c] = true
		byCol[c] = []CardView{}
	}
	users, _ := s.store.ListUsers(r.Context())
	for i := range all {
		c := &all[i]
		if !colSet[c.Status] {
			continue
		}
		byCol[c.Status] = append(byCol[c.Status], s.cardView(c, b, users))
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
	return data
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
			if containsBoard(s.boards[id].CardTypeIDs, typeID) {
				boardID = id
				break
			}
		}
	}
	b := s.boards[boardID]
	users, _ := s.store.ListUsers(r.Context())
	data := s.baseData("New " + ct.Name)
	data.CardType = ct
	data.Board = b
	data.Fields = fieldViews(ct, nil, users)
	data.StatusOptions = s.statusOptions(ct, b, "")
	data.Users = users
	data.TagSet = s.ws.TagSet
	data.FormTitle = ""
	data.FormTags = ""
	s.renderPage(w, "card_form.html", data)
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
		users, _ := s.store.ListUsers(r.Context())
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
		s.renderPage(w, "card_form.html", data)
		return
	}
	http.Redirect(w, r, "/ui/boards/"+boardID, http.StatusSeeOther)
}

func (s *Server) uiCardDetail(w http.ResponseWriter, r *http.Request) {
	c, err := s.svc.GetCard(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.renderCardDetail(w, r, c, nil)
}

func (s *Server) uiMoveCard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	c, _ := s.svc.GetCard(r.Context(), id)
	if c == nil {
		http.NotFound(w, r)
		return
	}
	b := s.boardForCard(c)
	newStatus := r.FormValue("status")
	v := newStatus
	req := core.PatchCardRequest{
		Version: c.Version,
		Status:  &v,
		Actor:   s.ws.Settings.DefaultUser,
	}
	_, err := s.svc.PatchCard(r.Context(), id, req)
	if err != nil {
		// On error, re-render whichever surface the move came from.
		if r.FormValue("from") == "detail" {
			s.renderCardDetail(w, r, c, core.AsError(err))
		} else if b != nil {
			s.renderPartial(w, "board.html", s.boardData(r, b))
		} else {
			s.renderCardDetail(w, r, c, core.AsError(err))
		}
		return
	}
	// Success: detail move → re-render detail partial; board move → re-render
	// the whole board so the card appears in its new column.
	if r.FormValue("from") == "detail" && wantsPartial(r) {
		updated, _ := s.svc.GetCard(r.Context(), id)
		s.renderCardDetail(w, r, updated, nil)
		return
	}
	if b != nil {
		if wantsPartial(r) {
			s.renderPartial(w, "board.html", s.boardData(r, b))
			return
		}
		http.Redirect(w, r, "/ui/boards/"+b.ID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) uiEditField(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	c, _ := s.svc.GetCard(r.Context(), id)
	if c == nil {
		http.NotFound(w, r)
		return
	}
	req := core.PatchCardRequest{
		Version: c.Version,
		Actor:   s.ws.Settings.DefaultUser,
		Fields:  map[string]any{},
	}
	if v := r.FormValue("title"); v != "" && v != c.Title {
		t := v
		req.Title = &t
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
			req.Fields[fid] = r.FormValue(k)
		}
	}
	updated, err := s.svc.PatchCard(r.Context(), id, req)
	if err != nil {
		s.renderCardDetail(w, r, c, core.AsError(err))
		return
	}
	s.renderCardDetail(w, r, updated, nil)
}

// uiSaveCard is the modal's single save endpoint: gathers title/status/owner/
// tags/field:* from one form, applies them in one PATCH (respecting the current
// version), and returns the refreshed modal so the client swaps it in.
func (s *Server) uiSaveCard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	c, _ := s.svc.GetCard(r.Context(), id)
	if c == nil {
		http.NotFound(w, r)
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
		// Re-render the modal with the error + the version the client had.
		c.Version = version // show the version the user posted with
		s.renderCardModalErr(w, r, c, core.AsError(err))
		return
	}
	// Return the refreshed modal.
	s.uiCardModal(w, r)
}

// renderCardModalErr re-renders the modal with an error attached.
func (s *Server) renderCardModalErr(w http.ResponseWriter, r *http.Request, c *core.Card, err *core.Error) {
	ct := s.types[c.TypeID]
	b := s.boardForCard(c)
	users, _ := s.store.ListUsers(r.Context())
	data := s.baseData(c.Title)
	data.Card = c
	data.CardType = ct
	data.Board = b
	data.Fields = fieldViews(ct, c.Fields, users)
	data.MoveOptions = s.moveOptions(b, c.Status)
	data.Users = users
	data.TagSet = s.ws.TagSet
	data.Error = err
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.pages["card_modal.html"].ExecuteTemplate(w, "card_modal", data)
}

// --- view building ---

// CardView is data for card_partial.html.
type CardView struct {
	Card          *core.Card
	CardType      *core.CardType
	PreviewFields []PreviewField
	MoveOptions   []Option
	LinkChips     []Chip
	RepeatingChips []Chip
}

type PreviewField struct {
	Label string
	Value string
}

// Chip is a small count badge on a card overview (links / repeating fields).
type Chip struct {
	Label string
	Count int
	TypeID string // for link chips (drives data-link-type theming)
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
	Def     *core.FieldDef
	Value   any
	ValueStr string
	Entries [][]PreviewField
	Users   []core.User
	ValueRendered string
	Display string // UI hint from FieldDef.Display (feed|badge|hidden|link|monospace)
}

// ViewData is the template payload.
type ViewData struct {
	Title          string
	Boards         map[string]*core.Board
	Board          *core.Board
	Columns        []core.Column
	CardsByColumn  map[string][]CardView
	Card           *core.Card
	CardType       *core.CardType
	Fields         []FieldView
	MoveOptions    []Option
	PreviewFields  []PreviewField
	Error          *core.Error
	FormTitle      string
	FormTags       string
	StatusOptions  []Option
	Users          []core.User
	TagSet         []string
	// Home page
	Workspace      *core.Workspace
	CardCount      int
	RecentCards    []RecentCard
}

func (s *Server) baseData(title string) ViewData {
	return ViewData{Title: title, Boards: s.boards}
}

func (s *Server) renderCardDetail(w http.ResponseWriter, r *http.Request, c *core.Card, err *core.Error) {
	ct := s.types[c.TypeID]
	b := s.boardForCard(c)
	users, _ := s.store.ListUsers(r.Context())
	data := s.baseData(c.Title)
	data.Card = c
	data.CardType = ct
	data.Board = b
	data.Fields = fieldViews(ct, c.Fields, users)
	data.MoveOptions = s.moveOptions(b, c.Status)
	data.Users = users
	data.TagSet = s.ws.TagSet
	data.Error = err
	if wantsPartial(r) {
		s.renderPartial(w, "card_detail.html", data)
	} else {
		s.renderPage(w, "card_detail.html", data)
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
	users, _ := s.store.ListUsers(r.Context())
	data := s.baseData(c.Title)
	data.Card = c
	data.CardType = ct
	data.Board = b
	data.Fields = fieldViews(ct, c.Fields, users)
	data.MoveOptions = s.moveOptions(b, c.Status)
	data.Users = users
	data.TagSet = s.ws.TagSet
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if e := s.pages["card_modal.html"].ExecuteTemplate(w, "card_modal", data); e != nil {
		http.Error(w, "template error: "+e.Error(), http.StatusInternalServerError)
	}
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
	return CardView{
		Card:           c,
		CardType:       ct,
		PreviewFields:  previews,
		MoveOptions:    s.moveOptions(b, c.Status),
		LinkChips:      linkChips(c, s.ws),
		RepeatingChips: repeatingChips(c, ct),
	}
}

// linkChips groups a card's links by type_id into count chips.
func linkChips(c *core.Card, ws *core.Workspace) []Chip {
	if len(c.Links) == 0 {
		return nil
	}
	counts := map[string]int{}
	order := []string{}
	for _, l := range c.Links {
		if _, ok := counts[l.TypeID]; !ok {
			order = append(order, l.TypeID)
		}
		counts[l.TypeID]++
	}
	out := []Chip{}
	for _, id := range order {
		label := id
		for _, lt := range ws.LinkTypes {
			if lt.ID == id {
				label = lt.Name
				break
			}
		}
		out = append(out, Chip{Label: label, Count: counts[id], TypeID: id})
	}
	return out
}

// repeatingChips builds chips for repeating fields that have entries.
func repeatingChips(c *core.Card, ct *core.CardType) []Chip {
	if ct == nil {
		return nil
	}
	fm, _ := c.Fields.(map[string]any)
	if fm == nil {
		return nil
	}
	out := []Chip{}
	for _, f := range ct.Fields {
		if f.Type != core.FieldRepeating {
			continue
		}
		arr, _ := fm[f.ID].([]any)
		if len(arr) == 0 {
			continue
		}
		label := f.Label
		if label == "" {
			label = humanizeID(f.ID)
		}
		out = append(out, Chip{Label: label, Count: len(arr)})
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
		if containsBoard(b.CardTypeIDs, c.TypeID) {
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

func (s *Server) renderPage(w http.ResponseWriter, name string, data ViewData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages[name].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
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

// --- json helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, e *core.Error) {
	if e == nil {
		e = &core.Error{Code: "internal_error", Message: "unknown error", HTTPStatus: 500}
	}
	status := e.HTTPStatus
	if status == 0 {
		status = 500
	}
	writeJSON(w, status, e)
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	return dec.Decode(v)
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

func containsBoard(s []string, v string) bool { return containsStr(s, v) }
