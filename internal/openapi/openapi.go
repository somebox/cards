// Package openapi builds an OpenAPI 3.1 document for the /v1 card API from the
// live workspace definitions. Because card field shapes are defined per card
// type, the components schemas are generated from those types — the spec
// reflects the actual workspace, matching the "schema is the contract" model.
package openapi

import (
	"fmt"
	"sort"

	"github.com/somebox/cards/internal/core"
)

// Build returns the OpenAPI document as a generic map ready to JSON-encode.
// It covers the core card endpoints; the per-type field schemas are derived
// from the workspace's card types.
func Build(ws *core.Workspace, types map[string]*core.CardType) map[string]any {
	schemas := map[string]any{
		"Card":          cardEnvelopeSchema(),
		"Error":         errorSchema(),
		"CardList":      refArraySchema("Card"),
		"CreateCardReq": createReqSchema(),
		"PatchCardReq":  patchReqSchema(),
	}
	// One fields-schema per card type, plus a create/patch oneOf hint.
	for _, id := range sortedTypeIDs(types) {
		schemas[fieldsSchemaName(id)] = fieldsSchema(types[id])
	}

	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       ws.Name + " — Cards API",
			"version":     "1",
			"description": "Typed-card coordination API. Field schemas are generated from this workspace's card types.",
		},
		"servers":    []any{map[string]any{"url": "/v1"}},
		"paths":      paths(types),
		"components": map[string]any{"schemas": schemas},
	}
}

func paths(types map[string]*core.CardType) map[string]any {
	idParam := []any{map[string]any{
		"name": "id", "in": "path", "required": true,
		"schema": map[string]any{"type": "string"},
	}}
	jsonBody := func(ref string) map[string]any {
		return map[string]any{
			"required": true,
			"content": map[string]any{
				"application/json": map[string]any{"schema": refOf(ref)},
			},
		}
	}
	cardResp := map[string]any{
		"200": map[string]any{"description": "Card",
			"content": map[string]any{"application/json": map[string]any{"schema": refOf("Card")}}},
	}
	return map[string]any{
		"/cards": map[string]any{
			"get": op("List or search cards", map[string]any{
				"200": map[string]any{"description": "Matching cards",
					"content": map[string]any{"application/json": map[string]any{"schema": refOf("CardList")}}},
			}, listParams()),
			"post": opBody("Create a card", jsonBody("CreateCardReq"), map[string]any{
				"201": map[string]any{"description": "Created card",
					"content": map[string]any{"application/json": map[string]any{"schema": refOf("Card")}}},
			}, nil),
		},
		"/cards/{id}": map[string]any{
			"get":   op("Get one card", cardResp, idParam),
			"patch": opBody("Patch a card (optimistic concurrency via version)", jsonBody("PatchCardReq"), cardResp, idParam),
		},
		"/cards/{id}/upgrade-schema": map[string]any{
			"post": opBody("Upgrade a card to a newer schema version", map[string]any{
				"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"target_version": map[string]any{"type": "integer"},
						"dry_run":        map[string]any{"type": "boolean"},
					},
				}}},
			}, cardResp, idParam),
		},
		"/cards/take-next": map[string]any{
			"post": opBody("Atomically claim the next matching unowned card", map[string]any{
				"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}},
			}, cardResp, nil),
		},
		"/workspace": map[string]any{
			"get": op("Workspace introspection (columns, types, boards, users)", map[string]any{
				"200": map[string]any{"description": "Workspace"},
			}, nil),
		},
		"/boards/{id}": map[string]any{
			"get": op("Get one board definition", map[string]any{
				"200": map[string]any{"description": "Board"},
			}, idParam),
		},
		"/events/stream": map[string]any{
			"get": op("Server-sent event stream (text/event-stream); supports Last-Event-ID replay", map[string]any{
				"200": map[string]any{"description": "Event stream"},
			}, nil),
		},
	}
}

func listParams() []any {
	q := func(name, desc string) map[string]any {
		return map[string]any{"name": name, "in": "query", "description": desc,
			"schema": map[string]any{"type": "string"}}
	}
	return []any{
		q("board_id", "scope to a board"), q("type_id", "filter by card type"),
		q("status", "filter by status"), q("owner", "filter by owner (or 'me')"),
		q("q", "full-text query"), q("cursor", "pagination cursor"),
		map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer"}},
	}
}

func op(summary string, responses map[string]any, params []any) map[string]any {
	o := map[string]any{"summary": summary, "responses": responses}
	if len(params) > 0 {
		o["parameters"] = params
	}
	return o
}

func opBody(summary string, body, responses map[string]any, params []any) map[string]any {
	o := op(summary, responses, params)
	o["requestBody"] = body
	return o
}

// --- component schemas ---

func cardEnvelopeSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":             map[string]any{"type": "string"},
			"workspace_id":   map[string]any{"type": "string"},
			"type_id":        map[string]any{"type": "string"},
			"schema_version": map[string]any{"type": "integer"},
			"title":          map[string]any{"type": "string"},
			"status":         map[string]any{"type": "string"},
			"owner":          map[string]any{"type": "string"},
			"tags":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"fields":         map[string]any{"type": "object", "description": "type-specific; see <Type>Fields schemas"},
			"version":        map[string]any{"type": "integer"},
			"created_at":     map[string]any{"type": "string", "format": "date-time"},
			"updated_at":     map[string]any{"type": "string", "format": "date-time"},
			"created_by":     map[string]any{"type": "string"},
		},
		"required": []any{"id", "type_id", "title", "status", "version"},
	}
}

func createReqSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"type_id", "title"},
		"properties": map[string]any{
			"type_id": map[string]any{"type": "string"},
			"title":   map[string]any{"type": "string"},
			"status":  map[string]any{"type": "string"},
			"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"fields":  map[string]any{"type": "object"},
		},
	}
}

func patchReqSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"version"},
		"properties": map[string]any{
			"version": map[string]any{"type": "integer", "description": "current version (optimistic concurrency)"},
			"status":  map[string]any{"type": "string"},
			"owner":   map[string]any{"type": "string"},
			"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"fields":  map[string]any{"type": "object"},
			"dry_run": map[string]any{"type": "boolean"},
		},
	}
}

func errorSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"error":         map[string]any{"type": "string"},
			"message":       map[string]any{"type": "string"},
			"field":         map[string]any{"type": "string"},
			"valid_options": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}

// fieldsSchema turns a card type's fields into a JSON Schema object.
func fieldsSchema(ct *core.CardType) map[string]any {
	props := map[string]any{}
	required := []any{}
	for _, f := range ct.Fields {
		props[f.ID] = fieldSchema(f)
		if f.Required {
			required = append(required, f.ID)
		}
	}
	s := map[string]any{
		"type":        "object",
		"title":       fieldsSchemaName(ct.ID),
		"description": fmt.Sprintf("fields for card type %q (schema_version %d)", ct.ID, ct.SchemaVersion),
		"properties":  props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func fieldSchema(f core.FieldDef) map[string]any {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	var s map[string]any
	switch f.Type {
	case core.FieldString, core.FieldText, core.FieldUser, core.FieldCardLink:
		s = str()
	case core.FieldNumber:
		s = map[string]any{"type": "number"}
		if f.Min != nil {
			s["minimum"] = *f.Min
		}
		if f.Max != nil {
			s["maximum"] = *f.Max
		}
	case core.FieldDate:
		s = map[string]any{"type": "string", "format": "date-time"}
	case core.FieldEnum:
		opts := make([]any, len(f.Options))
		for i, o := range f.Options {
			opts[i] = o
		}
		s = map[string]any{"type": "string", "enum": opts}
	case core.FieldTags:
		s = map[string]any{"type": "array", "items": str()}
	case core.FieldArtifact:
		s = map[string]any{"type": "object", "description": "artifact metadata {uri, mime, size, sha256}"}
	case core.FieldRepeating:
		itemProps := map[string]any{}
		for _, it := range f.ItemFields {
			itemProps[it.ID] = fieldSchema(it)
		}
		s = map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":       "object",
				"properties": itemProps,
			},
		}
	default:
		s = map[string]any{}
	}
	if f.Description != "" {
		s["description"] = f.Description
	}
	if f.Deprecated {
		s["deprecated"] = true
	}
	return s
}

func fieldsSchemaName(typeID string) string { return typeID + ".fields" }

func refOf(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }

func refArraySchema(itemRef string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items":       map[string]any{"type": "array", "items": refOf(itemRef)},
			"next_cursor": map[string]any{"type": "string"},
		},
	}
}

func sortedTypeIDs(types map[string]*core.CardType) []string {
	ids := make([]string, 0, len(types))
	for id := range types {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
