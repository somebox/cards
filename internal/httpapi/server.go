// Package httpapi exposes the core service over REST (/v1) and SSE.
// Handlers are thin; all rules live in internal/core. The same Server also
// serves a lightweight server-rendered web UI (Go templates + Alpine) under /ui.
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
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/somebox/cards/internal/core"
)

//go:embed templates/*.html templates/*.css templates/assets/*
var templateFS embed.FS

// builtinThemeFonts is the EMBEDDED themes' font manifest: theme name → Google
// Fonts stylesheet href. Immutable data for the themes compiled into style.css.
// Workspace-loaded themes carry their own font href in their manifest and are
// merged over this per-Server in New() — the resolved lookup is instance state,
// not a package var, so a reload picks up a new theme's fonts.
var builtinThemeFonts = map[string]string{
	"journal": "https://fonts.googleapis.com/css2?family=Caveat:wght@500;600;700&family=Kalam:wght@400;700&display=swap",
	"labels":  "https://fonts.googleapis.com/css2?family=Sono:wght@200;400;600&display=swap",
}

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
	// themes are the workspace-loaded UI themes (name → validated CSS + font
	// manifest), served concatenated after the base stylesheet and offered in
	// the theme picker. Built-in themes live in style.css, not here.
	themes map[string]*core.Theme
	// assetStamp busts the browser's stylesheet cache. It is per-Server (per
	// composition generation), NOT per-process: each New() — including every
	// workspace reload, which builds a fresh Server — mints a new stamp, so the
	// /ui/style.css?v=<stamp> URL rotates on reload. That rotation is what makes
	// file-loaded workspace themes (THEMES.md step 2) safe to cache for 24h: a
	// reload that changes the served CSS also changes the URL, so returning tabs
	// refetch instead of holding stale CSS. UnixNano (not Unix) guarantees two
	// reloads within the same second still produce distinct stamps.
	assetStamp int64
}

// sortedThemeNames returns the embedded theme names unioned with the loaded
// ones, deduped and sorted — the selectable set for the theme picker.
func sortedThemeNames(loaded map[string]*core.Theme) []string {
	set := map[string]bool{}
	for name := range builtinThemeFonts {
		set[name] = true
	}
	for name := range loaded {
		set[name] = true
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// New constructs the Server, parsing embedded templates into per-page sets.
// themes are the workspace-loaded themes (config.Result.Themes); nil is fine
// (only the embedded themes are then available).
func New(svc *core.Service, ws *core.Workspace, types map[string]*core.CardType, boards map[string]*core.Board, themes map[string]*core.Theme, st core.Store) (*Server, error) {
	// One stamp per composition generation; the funcMap closure below captures
	// it and the Server stores it. See the assetStamp field comment.
	assetStamp := time.Now().UnixNano()
	// Resolved font lookup is per-Server: embedded defaults overlaid with each
	// loaded theme's manifest font. Instance state, not a package var.
	themeFonts := map[string]string{}
	for name, href := range builtinThemeFonts {
		themeFonts[name] = href
	}
	for name, th := range themes {
		if th.Fonts != "" {
			themeFonts[name] = th.Fonts
		}
	}
	// themeNames is the sorted, deduped set offered in the theme picker: the
	// embedded themes plus every loaded one. Built once per generation.
	themeNames := sortedThemeNames(themes)
	funcMap := template.FuncMap{
		"join": strings.Join,
		// themeFonts resolves a theme's web-font stylesheet URL from the resolved
		// per-Server manifest (embedded defaults + loaded themes). Data, not
		// template branches: the layout emits ONE font link block driven by it.
		"themeFonts": func(name string) string { return themeFonts[name] },
		// allThemes lists the selectable theme names (embedded + loaded), sorted,
		// for the nav theme picker.
		"allThemes": func() []string { return themeNames },
		// assetStamp versions the stylesheet URL so a restarted server OR a
		// workspace reload (each a new Server generation) serves fresh CSS to
		// returning tabs. Captures this generation's stamp.
		"assetStamp": func() int64 { return assetStamp },
		// shortID returns the leading 8 hex chars of a card id (matches substr(id,6,8) resolution) for compact display;
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
		// inList reports whether s is an element of v (a []any or []string —
		// the runtime shapes of a multiple field's value or default). Used by
		// field_control to mark <option selected> in multi-selects; nil → false.
		"inList": func(v any, s string) bool {
			switch t := v.(type) {
			case []any:
				for _, e := range t {
					if str, ok := e.(string); ok && str == s {
						return true
					}
				}
			case []string:
				for _, e := range t {
					if e == s {
						return true
					}
				}
			}
			return false
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
		"breaches.html":       {"templates/breaches.html"},
		"card_ambiguous.html": {"templates/card_ambiguous.html"},
		"card_detail.html":    {"templates/card_detail.html", "templates/card_modal.html", "templates/field_control.html"},
		"card_create.html":    {"templates/card_create.html", "templates/field_control.html"},
		"board_create.html":   {"templates/board_create.html"},
		"card_modal.html":     {"templates/card_modal.html", "templates/field_control.html"},
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
		themes:     themes,
		assetStamp: assetStamp,
	}, nil
}

// Router builds the chi router with /v1 API and /ui HTML routes.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	// --- API ---
	r.Get("/v1/health", s.apiHealth)
	r.Get("/v1/workspace", s.apiWorkspace)
	r.Get("/v1/boards/{id}", s.apiGetBoard)
	r.Get("/v1/breaches", s.apiBreaches)
	r.Get("/v1/cards", s.apiListCards)
	r.Post("/v1/cards", s.withActor(s.idempotent(s.apiCreateCard)))
	r.Get("/v1/cards/{id}", s.apiGetCard)
	r.Patch("/v1/cards/{id}", s.withActor(s.idempotent(s.apiPatchCard)))
	r.Delete("/v1/cards/{id}", s.withActor(s.idempotent(s.apiDeleteCard)))
	r.Post("/v1/cards/{id}/upgrade-schema", s.withActor(s.idempotent(s.apiUpgradeSchema)))
	r.Post("/v1/cards/take-next", s.withActor(s.idempotent(s.apiTakeNext)))
	r.Post("/v1/cards/{id}/claim", s.withActor(s.idempotent(s.apiClaim)))
	r.Post("/v1/cards/{id}/release", s.withActor(s.idempotent(s.apiRelease)))
	r.Post("/v1/cards/{id}/links", s.withActor(s.idempotent(s.apiAddLink)))
	r.Delete("/v1/cards/{id}/links/{typeID}/{target}", s.withActor(s.apiRemoveLink))
	r.Post("/v1/cards/{id}/comments", s.withActor(s.idempotent(s.apiAddComment)))
	r.Patch("/v1/cards/{id}/comments/{commentID}", s.withActor(s.idempotent(s.apiEditComment)))
	r.Post("/v1/cards/{id}/artifacts/{field}", s.withActor(s.idempotent(s.apiAddArtifact)))
	r.Get("/v1/artifacts/*", s.apiGetArtifact)
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
	r.Get("/ui/assets/{name}", s.uiAsset)
	r.Get("/ui/boards/{id}", s.uiBoard)
	r.Get("/ui/breaches", s.uiBreaches)
	r.Get("/ui/cards/new", s.uiNewCardRedirect)
	r.Get("/ui/cards/new/modal", s.uiNewCardModal)
	r.Get("/ui/boards/new/modal", s.uiNewBoardModal)
	r.Get("/ui/cards/{id}", s.uiCardDetail)
	r.Get("/ui/cards/{id}/modal", s.uiCardModal)
	r.Post("/ui/cards/{id}/save", s.uiSaveCard)

	return r
}
