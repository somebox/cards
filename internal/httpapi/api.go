package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/openapi"
)

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
		IDLike:     r.URL.Query().Get("q"), // also match id/short-id when q is set (1d)
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
	c, err := s.svc.ResolveCard(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		var amb *core.AmbiguousIDError
		if errors.As(err, &amb) {
			writeJSON(w, 409, map[string]any{
				"error":      "ambiguous",
				"query":      amb.Short,
				"candidates": amb.Candidates,
			})
			return
		}
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
