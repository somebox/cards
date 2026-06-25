// Package mcp exposes the core service as a stdio MCP server. Tools are
// generated from workspace introspection: one create/update tool per card
// type, plus generic coordination tools (claim, take_next, append_entry,
// history, subscribe).
//
// See docs/MCP.md for the tool surface and docs/SPEC.md §13 for the
// agent coordination loop.
package mcp

// Server is the stdio MCP server.
type Server struct {
	// TODO: generate typed tools from GET /workspace, dispatch mutations to
	// the service layer (never bypass validation), bind session actor.
}
