// Package mcp exposes the core service as a stdio MCP server. Tools are
// generated from workspace introspection: one create_<T>/update_<T> per card
// type (typed input from the field schema), plus generic coordination tools.
//
// Mutations delegate to the same service layer as HTTP/CLI, so validation,
// events, and concurrency are identical. See docs/MCP.md and docs/SPEC.md §13.
//
// The server speaks JSON-RPC 2.0 over stdio (newline-delimited) and implements
// the MCP subset: initialize, notifications/initialized, tools/list, tools/call.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/somebox/cards/internal/core"
)

// Server is the stdio MCP server.
type Server struct {
	svc    *core.Service
	ws     *core.Workspace
	types  map[string]*core.CardType
	boards map[string]*core.Board
	actor  string // session-bound actor (CARDS_USER)
	tools  []Tool
	in     io.Reader
	out    io.Writer
}

// Tool is one MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
	// run executes the tool; returns a result already shaped for MCP.
	run func(ctx context.Context, args map[string]any) (any, error)
}

// New constructs the Server and generates tools from the workspace.
func New(svc *core.Service, ws *core.Workspace, types map[string]*core.CardType, boards map[string]*core.Board, actor string) *Server {
	s := &Server{svc: svc, ws: ws, types: types, boards: boards, actor: actor, in: os.Stdin, out: os.Stdout}
	s.tools = s.buildTools()
	return s
}

// ServeOn runs the JSON-RPC loop over the given reader/writer (for testing).
func (s *Server) ServeOn(in io.Reader, out io.Writer) error {
	s.in = in
	s.out = out
	return s.Serve()
}

// Serve runs the stdio JSON-RPC loop until stdin closes.
func (s *Server) Serve() error {
	// MCP uses Content-Length headers OR newline-delimited JSON. Many clients
	// send newline-delimited JSON-RPC over stdio; we support that (one request
	// per line). Log to stderr so stdout stays clean for protocol output.
	scanner := bufio.NewScanner(s.in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := bufio.NewWriter(s.out)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.writeErr(out, nil, -32700, "parse error")
			continue
		}
		resp := s.handle(req)
		// Notifications (no ID) get no response.
		if req.ID == nil && req.Method != "" {
			continue
		}
		if resp != nil {
			b, _ := json.Marshal(resp)
			out.Write(b)
			out.WriteByte('\n')
			// bufio errors are sticky; Flush reports them. stdout is the
			// protocol channel, so failures can only go to stderr.
			if err := out.Flush(); err != nil {
				log.Printf("ERROR: mcp: write response: %v", err)
			}
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCErr     `json:"error,omitempty"`
}

type jsonRPCErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (s *Server) writeErr(out *bufio.Writer, id json.RawMessage, code int, msg string) {
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &jsonRPCErr{Code: code, Message: msg}}
	b, _ := json.Marshal(resp)
	out.Write(b)
	out.WriteByte('\n')
	if err := out.Flush(); err != nil {
		log.Printf("ERROR: mcp: write error response: %v", err)
	}
}

func (s *Server) handle(req jsonRPCRequest) *jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		return nil
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &jsonRPCErr{Code: -32601, Message: "method not found: " + req.Method}}
	}
}

func (s *Server) handleInitialize(req jsonRPCRequest) *jsonRPCResponse {
	result := map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo":      map[string]any{"name": "work-cards", "version": "poc"},
		"capabilities":    map[string]any{"tools": map[string]any{}},
	}
	return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func (s *Server) handleToolsList(req jsonRPCRequest) *jsonRPCResponse {
	tools := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		tools = append(tools, map[string]any{
			"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema,
		})
	}
	return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}}
}

func (s *Server) handleToolsCall(req jsonRPCRequest) *jsonRPCResponse {
	var params struct {
		Name string         `json:"name"`
		Args map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &jsonRPCErr{Code: -32602, Message: "invalid params"}}
	}
	for _, t := range s.tools {
		if t.Name == params.Name {
			ctx := core.WithActor(context.Background(), s.actor)
			result, err := t.run(ctx, params.Args)
			if err != nil {
				return toolError(req.ID, err)
			}
			return toolResult(req.ID, result)
		}
	}
	return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &jsonRPCErr{Code: -32601, Message: "unknown tool: " + params.Name}}
}

// toolResult wraps a Go value as an MCP tool-call result (text content).
func toolResult(id json.RawMessage, v any) *jsonRPCResponse {
	b, _ := json.MarshalIndent(v, "", "  ")
	return &jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}}
}

// toolError maps a core.Error (or plain error) to an MCP error result.
func toolError(id json.RawMessage, err error) *jsonRPCResponse {
	if ce := core.AsError(err); ce != nil {
		// Surface the structured error as text content with isError=true so the
		// agent can read valid_options and self-correct.
		b, _ := json.MarshalIndent(ce, "", "  ")
		return &jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(b)}},
			"isError": true,
		}}
	}
	return &jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &jsonRPCErr{Code: -32603, Message: err.Error()}}
}

// --- tool builders ---

func (s *Server) buildTools() []Tool {
	tools := []Tool{}
	// Introspection.
	tools = append(tools, Tool{
		Name: "workspace", Description: "Introspect the workspace: columns, card types, boards, link types, tags, users.",
		InputSchema: objSchema(map[string]any{}),
		run: func(ctx context.Context, args map[string]any) (any, error) {
			return s.svc.Workspace(ctx)
		},
	})
	// Per-type create/update (the anti-hallucination headline feature).
	for _, ct := range s.types {
		tools = append(tools, s.makeCreateTool(ct))
		tools = append(tools, s.makeUpdateTool(ct))
	}
	// Generic coordination tools.
	tools = append(tools,
		Tool{Name: "get_card", Description: "Get one card by id (with links + comments).",
			InputSchema: objSchema(map[string]any{"card_id": str()}),
			run: func(ctx context.Context, a map[string]any) (any, error) {
				return s.svc.GetCard(ctx, strArg(a, "card_id"))
			}},
		Tool{Name: "list_cards", Description: "List/search cards. Pass filters; returns a page.",
			InputSchema: objSchema(map[string]any{
				"type_id": str(), "status": str(), "owner": str(), "board_id": str(),
				"q": str(), "blocked": boolSchema(), "limit": intSchema(), "cursor": str(),
			}),
			run: func(ctx context.Context, a map[string]any) (any, error) {
				return s.svc.ListCards(ctx, core.CardQuery{
					TypeID: strArg(a, "type_id"), Status: strArg(a, "status"),
					Owner: strArg(a, "owner"), BoardID: strArg(a, "board_id"),
					Q: strArg(a, "q"), Blocked: boolArg(a, "blocked"),
					Limit: intArg(a, "limit"), Cursor: strArg(a, "cursor"),
				})
			}},
		Tool{Name: "search_cards", Description: "Full-text search cards.",
			InputSchema: objSchema(map[string]any{"q": str(), "limit": intSchema()}),
			run: func(ctx context.Context, a map[string]any) (any, error) {
				return s.svc.ListCards(ctx, core.CardQuery{Q: strArg(a, "q"), Limit: intArg(a, "limit")})
			}},
		Tool{Name: "claim", Description: "Atomically claim a card (sets owner; 409 if owned by another).",
			InputSchema: objSchema(map[string]any{"card_id": str(), "version": intSchema(), "status": str()}),
			run: func(ctx context.Context, a map[string]any) (any, error) {
				return s.svc.Claim(ctx, strArg(a, "card_id"), core.ClaimRequest{
					Version: intArg(a, "version"), Status: strArg(a, "status"), Actor: s.actor,
				})
			}},
		Tool{Name: "take_next", Description: "Pick + atomically claim the oldest matching unowned card. Returns {card: null} when nothing matches.",
			InputSchema: objSchema(map[string]any{"type_id": str(), "board_id": str(), "assign_to": str(), "status": str(), "filter": objSchema(map[string]any{})}),
			run: func(ctx context.Context, a map[string]any) (any, error) {
				c, err := s.svc.TakeNext(ctx, core.TakeNextRequest{
					TypeID: strArg(a, "type_id"), BoardID: strArg(a, "board_id"),
					AssignTo: strArg(a, "assign_to"), Status: strArg(a, "status"),
					Filter: mapArg(a, "filter"), Actor: s.actor,
				})
				if err != nil {
					return nil, err
				}
				return map[string]any{"card": c}, nil
			}},
		Tool{Name: "append_entry", Description: "Append a repeating-field entry (stable entry_id returned).",
			InputSchema: objSchema(map[string]any{"card_id": str(), "field": str(), "version": intSchema(), "entry": objSchema(map[string]any{})}),
			run: func(ctx context.Context, a map[string]any) (any, error) {
				return s.svc.AppendEntry(ctx, strArg(a, "card_id"), strArg(a, "field"), mapArg(a, "entry"), intArg(a, "version"))
			}},
		Tool{Name: "add_link", Description: "Add a typed link to another card.",
			InputSchema: objSchema(map[string]any{"card_id": str(), "type_id": str(), "target": str(), "note": str()}),
			run: func(ctx context.Context, a map[string]any) (any, error) {
				return s.svc.AddLink(ctx, strArg(a, "card_id"), core.LinkInput{
					TypeID: strArg(a, "type_id"), Target: strArg(a, "target"),
					Note: strArg(a, "note"), Actor: s.actor,
				})
			}},
		Tool{Name: "add_comment", Description: "Add a markdown comment to a card.",
			InputSchema: objSchema(map[string]any{"card_id": str(), "body": str()}),
			run: func(ctx context.Context, a map[string]any) (any, error) {
				return s.svc.AddComment(ctx, strArg(a, "card_id"), strArg(a, "body"))
			}},
		Tool{Name: "history", Description: "Resumption-ready event timeline for a card.",
			InputSchema: objSchema(map[string]any{"card_id": str()}),
			run: func(ctx context.Context, a map[string]any) (any, error) {
				return s.svc.History(ctx, strArg(a, "card_id"))
			}},
	)
	return tools
}

// makeCreateTool generates a create_<T> tool whose input schema is the card
// type's fields + universal title/status/tags. The tool signature IS the
// schema — an agent can't send a wrong-typed field. MCP.md.
func (s *Server) makeCreateTool(ct *core.CardType) Tool {
	props := map[string]any{"title": strReq()}
	props["status"] = str()
	props["tags"] = map[string]any{"type": "array", "items": str()}
	for _, f := range ct.Fields {
		props[f.ID] = fieldSchema(f, false)
	}
	req := []string{"title"}
	for _, f := range ct.Fields {
		if f.Required {
			req = append(req, f.ID)
		}
	}
	return Tool{
		Name:        "create_" + ct.ID,
		Description: fmt.Sprintf("Create a %s card. Field schema is enforced.", ct.Name),
		InputSchema: map[string]any{"type": "object", "properties": props, "required": req},
		run: func(ctx context.Context, a map[string]any) (any, error) {
			fields := map[string]any{}
			for _, f := range ct.Fields {
				if v, ok := a[f.ID]; ok {
					fields[f.ID] = v
				}
			}
			return s.svc.CreateCard(ctx, core.CreateCardRequest{
				TypeID: ct.ID, Title: strArg(a, "title"), Status: strArg(a, "status"),
				Fields: fields, Tags: sliceArg(a, "tags"), Actor: s.actor,
			})
		},
	}
}

func (s *Server) makeUpdateTool(ct *core.CardType) Tool {
	props := map[string]any{
		"card_id": strReq(), "version": intReq(),
		"status": str(), "owner": str(), "tags": map[string]any{"type": "array", "items": str()},
	}
	for _, f := range ct.Fields {
		props[f.ID] = fieldSchema(f, false)
	}
	req := []string{"card_id", "version"}
	return Tool{
		Name:        "update_" + ct.ID,
		Description: fmt.Sprintf("Update a %s card (optimistic concurrency: version required).", ct.Name),
		InputSchema: map[string]any{"type": "object", "properties": props, "required": req},
		run: func(ctx context.Context, a map[string]any) (any, error) {
			patch := core.PatchCardRequest{
				Version: intArg(a, "version"), Actor: s.actor,
				Fields: map[string]any{},
			}
			if v, ok := a["status"]; ok {
				sv := fmt.Sprint(v)
				patch.Status = &sv
			}
			if v, ok := a["owner"]; ok {
				sv := fmt.Sprint(v)
				patch.Owner = &sv
			}
			if v, ok := a["tags"]; ok {
				t := sliceAny(v)
				patch.Tags = &t
			}
			for _, f := range ct.Fields {
				if v, ok := a[f.ID]; ok {
					patch.Fields[f.ID] = v
				}
			}
			return s.svc.PatchCard(ctx, strArg(a, "card_id"), patch)
		},
	}
}

// --- JSON-schema helpers ---

func fieldSchema(f core.FieldDef, required bool) map[string]any {
	m := map[string]any{}
	switch f.Type {
	case core.FieldString, core.FieldText:
		m["type"] = "string"
	case core.FieldNumber:
		m["type"] = "number"
	case core.FieldDate:
		m["type"] = "string"
	case core.FieldEnum:
		m["type"] = "string"
		m["enum"] = f.Options
	case core.FieldTags:
		m["type"] = "array"
		m["items"] = map[string]any{"type": "string"}
	case core.FieldUser:
		m["type"] = "string"
	case core.FieldCardLink:
		m["type"] = "string"
	case core.FieldRepeating:
		m["type"] = "array"
		items := map[string]any{"type": "object", "properties": map[string]any{}}
		for _, sf := range f.ItemFields {
			items["properties"].(map[string]any)[sf.ID] = fieldSchema(sf, false)
		}
		m["items"] = items
	case core.FieldArtifact:
		m["type"] = "object"
	}
	if f.Description != "" {
		m["description"] = f.Description
	}
	if required {
		m["x-required"] = true
	}
	return m
}

func objSchema(props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props}
}
func str() map[string]any        { return map[string]any{"type": "string"} }
func strReq() map[string]any     { return map[string]any{"type": "string"} }
func intSchema() map[string]any  { return map[string]any{"type": "integer"} }
func intReq() map[string]any     { return map[string]any{"type": "integer"} }
func boolSchema() map[string]any { return map[string]any{"type": "boolean"} }

// --- arg extractors ---

func strArg(a map[string]any, k string) string {
	if v, ok := a[k]; ok {
		return fmt.Sprint(v)
	}
	return ""
}
func intArg(a map[string]any, k string) int {
	if v, ok := a[k]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}
func boolArg(a map[string]any, k string) bool {
	if v, ok := a[k].(bool); ok {
		return v
	}
	return false
}
func mapArg(a map[string]any, k string) map[string]any {
	if v, ok := a[k].(map[string]any); ok {
		return v
	}
	return nil
}
func sliceArg(a map[string]any, k string) []string {
	arr := sliceAny(a[k])
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		out = append(out, fmt.Sprint(v))
	}
	return out
}
func sliceAny(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		out = append(out, fmt.Sprint(x))
	}
	return out
}
