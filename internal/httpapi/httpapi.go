// Package httpapi exposes the core service over REST (/v1) and SSE.
// Handlers are thin; all rules live in internal/core.
//
// See docs/SPEC.md (§11 API surface) and docs/ARCHITECTURE.md (Core Service
// Boundary).
package httpapi

// Server is the HTTP/SSE server. Routes mirror SPEC.md §11.
type Server struct {
	// TODO: chi router, /v1/* routes, SSE with Last-Event-ID replay,
	// X-Work-Cards-Actor resolution, Idempotency-Key handling.
}
