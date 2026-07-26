package httpapi_test

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/openapi"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
)

// reloadSeamOps are served by cmd/cards/reload.go on the parent mux (they need
// the file-write + generation-swap seam), so they never appear on this router
// even though they are part of the published /v1 contract under `cards serve`.
var reloadSeamOps = map[string]bool{
	"post /workspace/reload": true,
	"post /boards":           true,
}

// undocumentedOps are deliberately absent from the OpenAPI document.
var undocumentedOps = map[string]bool{
	"get /openapi.json": true, // the document describing itself
}

// TestOpenAPICoversEveryRoute pins the /v1 route table against the generated
// OpenAPI document in both directions. Adding an endpoint without documenting
// it — or documenting one that does not exist — fails here rather than shipping
// a contract that lies. This is the drift the test exists to prevent: `claim`,
// `release`, and the durable `/events` feed went undocumented for months while
// the doc still advertised itself as the machine-readable contract.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	routed := routedOps(t)
	documented := documentedOps(t)

	var missing []string
	for op := range routed {
		if undocumentedOps[op] {
			continue
		}
		if !documented[op] {
			missing = append(missing, op)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("routes with no OpenAPI operation (add them to openapi.paths):\n  %s",
			strings.Join(missing, "\n  "))
	}

	var phantom []string
	for op := range documented {
		if routed[op] || reloadSeamOps[op] {
			continue
		}
		phantom = append(phantom, op)
	}
	sort.Strings(phantom)
	if len(phantom) > 0 {
		t.Errorf("OpenAPI operations with no matching route (stale documentation):\n  %s",
			strings.Join(phantom, "\n  "))
	}
}

// routedOps walks the chi router and returns the /v1 operations it serves, as
// "method path" keys with the /v1 prefix stripped to match the document's
// `servers: [{url: /v1}]` base.
func routedOps(t *testing.T) map[string]bool {
	t.Helper()
	r, err := config.New("../../examples/demo-workspace").Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	st := sqlitetest.Open(t, r.Workspace, 1)
	svc := core.NewService(r.Workspace, r.CardTypes, r.Boards, st)
	srv, err := httpapi.New(svc, r.Workspace, r.CardTypes, r.Boards, r.Themes, st)
	if err != nil {
		t.Fatalf("new http server: %v", err)
	}
	routes, ok := srv.Router().(chi.Routes)
	if !ok {
		t.Fatal("router is not a chi.Routes; cannot walk the route table")
	}

	ops := map[string]bool{}
	walk := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/v1/") {
			return nil // /ui is a reference consumer, not part of the contract
		}
		ops[strings.ToLower(method)+" "+normalizeRoute(route)] = true
		return nil
	}
	if err := chi.Walk(routes, walk); err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("walked zero /v1 routes — the walk is broken, not the coverage")
	}
	return ops
}

// normalizeRoute maps a chi route pattern onto the OpenAPI path it documents:
// strip the /v1 base, drop chi's trailing slash on wildcards, and name the
// artifact wildcard the way the document's path parameter does.
func normalizeRoute(route string) string {
	p := strings.TrimPrefix(route, "/v1")
	p = strings.TrimSuffix(p, "/")
	if p == "/artifacts/*" {
		return "/artifacts/{uri}"
	}
	return p
}

func documentedOps(t *testing.T) map[string]bool {
	t.Helper()
	r, err := config.New("../../examples/demo-workspace").Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	doc := openapi.Build(r.Workspace, r.CardTypes)
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("document has no paths object")
	}
	ops := map[string]bool{}
	for path, item := range paths {
		methods, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("path %s is not an object", path)
		}
		for method := range methods {
			ops[method+" "+path] = true
		}
	}
	return ops
}
