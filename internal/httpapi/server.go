// Package httpapi exposes the core service over REST (/v1) and SSE.
// Handlers are thin; all rules live in internal/core. The same Server also
// serves a lightweight server-rendered htmx UI under /ui.
//
// See docs/SPEC.md (§11 API surface) and docs/ARCHITECTURE.md (Core Service
// Boundary). UI is a reference consumer, not part of the kernel.
package httpapi

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/somebox/cards/internal/core"
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
	base    *template.Template            // layout + FuncMap, cloned per render
	pages   map[string]*template.Template // pre-parsed page sets (layout+page+partials)
	envUser string
}

// New constructs the Server, parsing embedded templates into per-page sets.
func New(svc *core.Service, ws *core.Workspace, types map[string]*core.CardType, boards map[string]*core.Board, st core.Store) (*Server, error) {
	funcMap := template.FuncMap{
		"join": strings.Join,
		// shortID returns the last-8-hex suffix of a card id for compact display;
		// the full id is kept canonical in store/API JSON and in title="". (1e)
		"shortID": shortID,
		// iso formats a time.Time as RFC3339 for the client-side `data-ago`
		// relative-time helper — never emit time.Time.String() (non-standard,
		// Safari/Firefox parse it as Invalid Date → "NaN ago").
		"iso": func(t time.Time) string { return t.Format(time.RFC3339) },
		// columnName resolves a status/column id to its display name, so the
		// modal/detail header shows the same label the status <select> does
		// (WYSIWYG: the view must not change voice when it becomes editable).
		"columnName": func(id string) string {
			for _, c := range ws.Columns {
				if c.ID == id {
					return c.Name
				}
			}
			return id
		},
		// boardStyle renders a board's Theme as a safe inline custom-property
		// string for the board wrapper. Only whitelisted hue tokens with simple
		// color values are emitted (prevents CSS injection / breaking dark mode).
		"boardStyle": boardStyle,
		// dict turns key/value pairs into a map for partials that need a small
		// argument struct (e.g. {{template "x" (dict "Action" ... "Query" ...)}}). (1e/1d)
		"dict": func(kv ...any) map[string]any {
			m := make(map[string]any, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				key, ok := kv[i].(string)
				if !ok {
					continue
				}
				m[key] = kv[i+1]
			}
			return m
		},
		// typeTheme returns the effective TypeTheme for a type id from the
		// ViewData.TypeThemes map (built in boardData/uiCardModal/uiCardDetail/
		// uiIndex). Fallback for non-loop call-sites where CardView fields are
		// not precomputed. (1a)
		"typeTheme": func(themes map[string]core.TypeTheme, id string) core.TypeTheme {
			if t, ok := themes[id]; ok {
				return t
			}
			return core.TypeTheme{Icon: "card"}
		},
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
		"board.html":          {"templates/board.html", "templates/card_partial.html", "templates/search_form.html"},
		"card_ambiguous.html": {"templates/card_ambiguous.html"},
		"card_detail.html":    {"templates/card_detail.html", "templates/card_modal.html"},
		"card_form.html":      {"templates/card_form.html"},
		"card_modal.html":     {"templates/card_modal.html"},
		"home.html":           {"templates/home.html", "templates/search_form.html"},
		"search_results.html": {"templates/search_results.html", "templates/card_partial.html", "templates/search_form.html"},
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
	r.Get("/ui/search", s.uiSearch)
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
