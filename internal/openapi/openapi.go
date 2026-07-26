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
		"Card":           cardEnvelopeSchema(),
		"Error":          errorSchema(),
		"CardList":       refArraySchema("Card"),
		"CreateCardReq":  createReqSchema(),
		"PatchCardReq":   patchReqSchema(),
		"Comment":        commentSchema(),
		"Link":           linkSchema(),
		"User":           userSchema(),
		"Event":          eventSchema(),
		"EventList":      refArraySchema("Event"),
		"EventItems":     itemsSchema(refOf("Event")),
		"HistoryItems":   itemsSchema(historyEntrySchema()),
		"TakeNextResult": takeNextResultSchema(),
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

// paths enumerates every /v1 operation the router serves. Keep this in step
// with the route table in internal/httpapi/server.go (plus the two reload-seam
// routes in cmd/cards/reload.go) — TestPathsCoverEveryRoute pins the pairing so
// a new endpoint cannot land undocumented.
func paths(types map[string]*core.CardType) map[string]any {
	p := map[string]any{}

	// --- cards ---
	p["/cards"] = map[string]any{
		"get":  op("List or search cards", listResp("Matching cards", "CardList"), listParams()),
		"post": write("Create a card", jsonBody("CreateCardReq"), created("Created card", "Card"), nil),
	}
	p["/cards/{id}"] = map[string]any{
		"get":    op("Get one card (with links + comments)", cardResp("Card"), idParam()),
		"patch":  write("Patch a card (optimistic concurrency via version)", jsonBody("PatchCardReq"), cardResp("Updated card"), idParam()),
		"delete": write("Delete a card (tombstone event; optional ?version= guard)", nil, cardResp("Deleted card"), params(idParam(), versionQuery(false))),
	}
	p["/cards/{id}/upgrade-schema"] = map[string]any{
		"post": write("Re-pin a card to a newer schema version (applies migration field_defaults, drops removed fields)",
			inlineBody(false, obj(props{
				"target_version": {"type": "integer", "description": "defaults to the type's current version; downgrades are refused"},
				"dry_run":        {"type": "boolean", "description": "validate and return the would-be card; sets the Dry-Run response header"},
			})),
			cardResp("Upgraded card"), idParam()),
	}

	// --- coordination atomics (in core because they need atomicity) ---
	p["/cards/take-next"] = map[string]any{
		"post": write("Atomically claim the oldest matching unowned card (updated_at ASC, id ASC). Returns {card: null} when nothing matches — a racing loser cannot distinguish that from an empty queue.",
			inlineBody(false, obj(props{
				"assign_to": {"type": "string", "description": "owner to assign; defaults to the resolved actor"},
				"status":    {"type": "string", "description": "optional status to move the claimed card to"},
				"type_id":   {"type": "string"},
				"board_id":  {"type": "string"},
				"filter":    {"type": "object", "description": "filter DSL ($and/$or/$eq/$ne/$in/$nin/$gt/$gte/$lt/$lte/$contains/$has)"},
			})),
			jsonResp("200", "Claimed card, or {card: null}", refOf("TakeNextResult")), nil),
	}
	p["/cards/{id}/claim"] = map[string]any{
		"post": write("Claim a specific card (compare-and-set on version; 409 if owned by another actor)",
			inlineBody(true, obj(props{
				"version": {"type": "integer", "description": "current version (required)"},
				"status":  {"type": "string", "description": "optional status to move to"},
			})),
			cardResp("Claimed card"), idParam()),
	}
	p["/cards/{id}/release"] = map[string]any{
		"post": write("Release ownership (owner → \"\"), optionally moving status; force bypasses enforced transitions for off-graph recovery",
			inlineBody(true, obj(props{
				"version": {"type": "integer", "description": "current version (required)"},
				"status":  {"type": "string"},
				"force":   {"type": "boolean", "description": "bypass the enforced-transition check"},
			})),
			cardResp("Released card"), idParam()),
	}

	// --- repeating fields (addressed by entry_id) ---
	p["/cards/{id}/fields/{field}/append"] = map[string]any{
		"post": write("Append a repeating-field entry (returns the card; the new entry carries a stable entry_id)",
			inlineBody(true, entryBodySchema()), cardResp("Updated card"), params(idParam(), fieldParam())),
	}
	p["/cards/{id}/fields/{field}/{entryID}"] = map[string]any{
		"patch": write("Update one repeating-field entry by entry_id",
			inlineBody(true, entryBodySchema()), cardResp("Updated card"), params(idParam(), fieldParam(), entryIDParam())),
		"delete": write("Remove one repeating-field entry by entry_id. version travels as ?version= (DELETE has no body) and is REQUIRED.",
			nil, cardResp("Updated card"), params(idParam(), fieldParam(), entryIDParam(), versionQuery(true))),
	}

	// --- links ---
	p["/cards/{id}/links"] = map[string]any{
		"post": write("Add a typed link. Links are stored on the SOURCE card: for depends-on/blocked-by the waiting card holds the link. Adding the same (type_id, target) twice is idempotent.",
			inlineBody(true, obj(props{
				"type_id": {"type": "string", "description": "a workspace link type, e.g. depends-on / blocked-by / related"},
				"target":  {"type": "string", "description": "target card id"},
				"note":    {"type": "string"},
			})),
			created("Updated card", "Card"), idParam()),
	}
	p["/cards/{id}/links/{typeID}/{target}"] = map[string]any{
		"delete": op("Remove a typed link", cardResp("Updated card"),
			params(idParam(), pathParam("typeID", "link type id"), pathParam("target", "target card id"))),
	}

	// --- comments ---
	p["/cards/{id}/comments"] = map[string]any{
		"post": write("Add a markdown comment (returns the updated card; bumps version and emits comment_added)",
			inlineBody(true, obj(props{"body": {"type": "string"}})), created("Updated card", "Card"), idParam()),
	}
	p["/cards/{id}/comments/{commentID}"] = map[string]any{
		"patch": write("Edit an existing comment body",
			inlineBody(true, obj(props{"body": {"type": "string"}})), cardResp("Updated card"),
			params(idParam(), pathParam("commentID", "comment id"))),
	}

	// --- artifacts ---
	p["/cards/{id}/artifacts/{field}"] = map[string]any{
		"post": write("Store bytes for an artifact field (raw body; content-addressed + path-confined). Optional ?version= applies the optimistic-concurrency guard before any bytes are published.",
			map[string]any{
				"required": true,
				"content": map[string]any{
					"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}},
				},
			},
			mergeResp(created("Updated card", "Card"), map[string]any{
				"413": map[string]any{"description": "artifact_too_large — body exceeded the upload cap",
					"content": map[string]any{"application/json": map[string]any{"schema": refOf("Error")}}},
			}),
			params(idParam(), fieldParam(), versionQuery(false))),
	}
	p["/artifacts/{uri}"] = map[string]any{
		"get": op("Serve stored artifact bytes by content-addressed uri (confined to the artifacts root; traversal/missing → 404)", map[string]any{
			"200": map[string]any{"description": "Artifact bytes",
				"content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}},
			"404": map[string]any{"description": "Not found or refused"},
		}, pathParam("uri", "content-addressed artifact uri")),
	}

	// --- events: three ways to consume ---
	p["/events"] = map[string]any{
		"get": op("Durable catch-up feed — append-only and gap-free, replayable from any event id. This is the path for durable recovery; the SSE stream is best-effort. Ordered by id ASC.",
			listResp("Page of events", "EventList"), []any{
				query("since", "string", "event-id floor (events with id > value)"),
				query("cursor", "string", "pagination continuation; overrides since"),
				query("actor", "string", "filter by the acting identity"),
				query("owner", "string", "filter by the card's current owner"),
				query("type", "string", "single event type"),
				query("types", "string", "comma-separated event types"),
				query("board_id", "string", "scope to a board's card types"),
				query("limit", "integer", "default 100, max 500"),
			}),
	}
	p["/events/stream"] = map[string]any{
		"get": op("Live SSE stream (text/event-stream). Bounded replay via Last-Event-ID header or ?since= (max 500 events), then subscribe — the feed-to-live handoff is NOT atomic, so strict consumers reconcile /events once more from their last durable id. Durable events carry an SSE id: cursor; ephemeral condition signals deliberately omit it so a reconnect keeps the last durable cursor. A slow consumer is dropped with a ': dropped, reconnect' comment.",
			map[string]any{"200": map[string]any{"description": "Event stream",
				"content": map[string]any{"text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}}}}},
			[]any{
				query("card_id", "string", "scope to one card"),
				query("board_id", "string", "scope to a board's card types"),
				query("types", "string", "comma-separated event types"),
				query("actor", "string", "filter by the acting identity"),
				query("owner", "string", "filter by the card's current owner"),
				query("since", "string", "event-id floor for replay (Last-Event-ID header takes precedence)"),
			}),
	}
	p["/cards/{id}/events"] = map[string]any{
		"get": op("One card's events. Returns {items} with no next_cursor — there is currently no way to page past the first page.",
			jsonResp("200", "Events for the card", refOf("EventItems")),
			params(idParam(), []any{
				query("types", "string", "comma-separated event types"),
				query("limit", "integer", ""),
			})),
	}
	p["/cards/{id}/history"] = map[string]any{
		"get": op("Resumption-ready timeline projected from the card's events. Returns {items} with no next_cursor.",
			jsonResp("200", "History timeline", refOf("HistoryItems")), idParam()),
	}
	p["/breaches"] = map[string]any{
		"get": op("Current-conditions catch-up: which conditions are true right now (wip_exceeded, lane_drained, card_blocked, and past-due status_timeout / card_idle). Item scans cap at 500 — check truncated/limit before trusting an empty result.",
			map[string]any{"200": map[string]any{"description": "Breach report {as_of, items, limit?, truncated?}"}},
			[]any{
				query("board_id", "string", "scope to one board"),
				query("type", "string", "comma-separated condition event types to filter to"),
			}),
	}

	// --- workspace, boards, users ---
	p["/health"] = map[string]any{
		"get": op("Liveness + identity of the served workspace", map[string]any{
			"200": map[string]any{"description": "Health",
				"content": map[string]any{"application/json": map[string]any{"schema": obj(props{
					"version":      {"type": "string"},
					"workspace_id": {"type": "string"},
				})}}},
		}, nil),
	}
	p["/workspace"] = map[string]any{
		"get": op("Workspace introspection: columns, card types (current version only), boards, link types, tags, users, settings. The one call an agent makes before acting.",
			map[string]any{"200": map[string]any{"description": "Workspace snapshot {workspace, card_types, boards, current_schema_versions}"}}, nil),
	}
	p["/boards/{id}"] = map[string]any{
		"get": op("One board's definition (columns, card types, default filter, transitions, wip limits, presentation)",
			map[string]any{
				"200": map[string]any{"description": "Board"},
				"404": errorResp("Unknown board id"),
			}, idParam()),
	}
	p["/users"] = map[string]any{
		"post": op("Register a user. Open — no auth. A registered id is required to own a card (PATCH owner / claim).",
			mergeResp(jsonResp("201", "Registered user", refOf("User")), map[string]any{
				"422": errorResp("validation_failed — id missing or registration refused"),
			}),
			nil),
	}

	// --- reload seam: served by cmd/cards/reload.go, so these exist under
	// `cards serve` but not in an embedded/serverless router. ---
	p["/workspace/reload"] = map[string]any{
		"post": op("Re-run the definitions loader and swap the live composition (cards serve only). Never mutates cards. On loader failure returns 422, emits definition_reload_failed, and keeps the previous generation serving.",
			map[string]any{
				"200": map[string]any{"description": "Reloaded",
					"content": map[string]any{"application/json": map[string]any{"schema": obj(props{
						"reloaded": {"type": "boolean"},
						"types":    {"type": "integer"},
						"boards":   {"type": "integer"},
						"themes":   {"type": "integer"},
						"warnings": {"type": "array", "items": map[string]any{"type": "string"}},
					})}}},
				"422": errorResp("Load failed — the previous definitions are still being served"),
			}, nil),
	}
	p["/boards"] = map[string]any{
		"post": write("Write and validate a new board definition file, then reload (cards serve only). A board that fails validation is rolled back off disk.",
			inlineBody(true, obj(props{
				"id":            {"type": "string", "description": "lowercase letters/digits/dashes; defaults to a slug of name"},
				"name":          {"type": "string"},
				"columns":       {"type": "array", "items": map[string]any{"type": "string"}},
				"card_type_ids": {"type": "array", "items": map[string]any{"type": "string"}},
				"wip_limits":    {"type": "object", "description": "column id → max"},
			})),
			map[string]any{
				"201": map[string]any{"description": "Created",
					"content": map[string]any{"application/json": map[string]any{"schema": obj(props{
						"id":   {"type": "string"},
						"path": {"type": "string"},
					})}}},
				"409": errorResp("A board with that id already exists"),
				"422": errorResp("validation_failed — the board file was rolled back"),
			}, nil),
	}

	return p
}

func listParams() []any {
	return []any{
		query("board_id", "string", "scope to a board (applies its default_filter + type/column scope)"),
		query("type_id", "string", "card type id; comma-separated matches ANY (IN)"),
		query("status", "string", "column id; comma-separated matches ANY (IN)"),
		query("owner", "string", "user id, exact match. NOTE: 'me' is NOT resolved here — the board UI substitutes the viewing actor before calling; ?owner=me matches the literal owner 'me'."),
		query("q", "string", "full-text search (FTS5); also matches a full or 8-char short card id"),
		query("blocked", "boolean", "=true returns cards with a blocked-by/depends-on link whose target is not yet done"),
		query("has_link", "string", "link type id present on the card"),
		query("link_target", "string", "card id the card links to"),
		query("include", "string", "comma-separated related collections to eager-load: links, comments"),
		query("sort", "string", "one key — created_at, updated_at, title, or fields.<id> — with optional leading '-' for descending. Missing-field cards sort last; an unknown key is a 422. Mutually exclusive with cursor, and returns no next_cursor."),
		query("cursor", "string", "opaque keyset cursor, welded to the default updated_at DESC order"),
		query("limit", "integer", "default 50, max 500"),
	}
}

// --- operation builders ---

func op(summary string, responses map[string]any, params []any) map[string]any {
	o := map[string]any{"summary": summary, "responses": responses}
	if len(params) > 0 {
		o["parameters"] = params
	}
	return o
}

// write builds a mutating operation: adds the Idempotency-Key header and the
// shared 4xx envelope responses that every write can return (SPEC §10).
func write(summary string, body, responses map[string]any, params []any) map[string]any {
	o := op(summary, mergeResp(responses, writeErrors()), append(params, idempotencyHeader()))
	if body != nil {
		o["requestBody"] = body
	}
	return o
}

func writeErrors() map[string]any {
	return map[string]any{
		"403": errorResp("actor_required — no actor could be resolved"),
		"409": errorResp("version_conflict — stale version; the current card is attached to the error"),
		"422": errorResp("validation_failed / unknown_enum / unknown_tag / unknown_user / unknown_field / transition_illegal / target_card_missing — the envelope names the field, the value, and valid_options"),
	}
}

func idempotencyHeader() any {
	return map[string]any{
		"name": "Idempotency-Key", "in": "header", "required": false,
		"description": "Replays the original response (with Idempotent-Replay: true) for a repeat of the same key by the same actor.",
		"schema":      map[string]any{"type": "string"},
	}
}

// --- parameter + response helpers ---

type props map[string]map[string]any

func obj(p props) map[string]any {
	out := map[string]any{}
	for k, v := range p {
		out[k] = v
	}
	return map[string]any{"type": "object", "properties": out}
}

func params(groups ...[]any) []any {
	var out []any
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func pathParam(name, desc string) []any {
	p := map[string]any{"name": name, "in": "path", "required": true, "schema": map[string]any{"type": "string"}}
	if desc != "" {
		p["description"] = desc
	}
	return []any{p}
}

func idParam() []any { return pathParam("id", "card id (a full id or an 8-char short id)") }

func fieldParam() []any { return pathParam("field", "repeating or artifact field id") }

func entryIDParam() []any { return pathParam("entryID", "repeating-entry id") }

func versionQuery(required bool) []any {
	p := map[string]any{"name": "version", "in": "query", "required": required,
		"schema": map[string]any{"type": "integer"}}
	if required {
		p["description"] = "current version — REQUIRED (DELETE carries no body)"
	} else {
		p["description"] = "optional optimistic-concurrency guard; omit to act unconditionally"
	}
	return []any{p}
}

func query(name, typ, desc string) any {
	p := map[string]any{"name": name, "in": "query", "schema": map[string]any{"type": typ}}
	if desc != "" {
		p["description"] = desc
	}
	return p
}

func jsonBody(ref string) map[string]any {
	return map[string]any{
		"required": true,
		"content":  map[string]any{"application/json": map[string]any{"schema": refOf(ref)}},
	}
}

func inlineBody(required bool, schema map[string]any) map[string]any {
	return map[string]any{
		"required": required,
		"content":  map[string]any{"application/json": map[string]any{"schema": schema}},
	}
}

func entryBodySchema() map[string]any {
	return obj(props{
		"entry":   {"type": "object", "description": "the entry's sub-field values"},
		"version": {"type": "integer", "description": "current version (optimistic concurrency)"},
	})
}

func jsonResp(code, desc string, schema map[string]any) map[string]any {
	return map[string]any{code: map[string]any{"description": desc,
		"content": map[string]any{"application/json": map[string]any{"schema": schema}}}}
}

func cardResp(desc string) map[string]any     { return jsonResp("200", desc, refOf("Card")) }
func created(desc, ref string) map[string]any { return jsonResp("201", desc, refOf(ref)) }

func listResp(desc, ref string) map[string]any { return jsonResp("200", desc, refOf(ref)) }

func errorResp(desc string) map[string]any {
	return map[string]any{"description": desc,
		"content": map[string]any{"application/json": map[string]any{"schema": refOf("Error")}}}
}

// mergeResp overlays b onto a without mutating either (a's codes win, so an
// operation's own 422 description beats the generic one).
func mergeResp(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range b {
		out[k] = v
	}
	for k, v := range a {
		out[k] = v
	}
	return out
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

func commentSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         map[string]any{"type": "string"},
			"author":     map[string]any{"type": "string"},
			"body":       map[string]any{"type": "string", "description": "markdown"},
			"created_at": map[string]any{"type": "string", "format": "date-time"},
			"edited_at":  map[string]any{"type": "string", "format": "date-time"},
		},
	}
}

func linkSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "stored on the SOURCE card; for depends-on/blocked-by the waiting card holds the link",
		"properties": map[string]any{
			"type_id":    map[string]any{"type": "string"},
			"target":     map[string]any{"type": "string", "description": "target card id"},
			"note":       map[string]any{"type": "string"},
			"created_by": map[string]any{"type": "string"},
			"created_at": map[string]any{"type": "string", "format": "date-time"},
		},
	}
}

func userSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":           map[string]any{"type": "string"},
			"display_name": map[string]any{"type": "string"},
			"kind":         map[string]any{"type": "string", "enum": []any{"human", "agent"}},
			"created_at":   map[string]any{"type": "string", "format": "date-time"},
		},
		"required": []any{"id"},
	}
}

func eventSchema() map[string]any {
	types := core.EventTypes()
	enum := make([]any, len(types))
	for i, t := range types {
		enum[i] = string(t)
	}
	return map[string]any{
		"type":        "object",
		"description": "Append-only event. diff is {before, after} for scalar changes, with richer shapes per type — it is diff.after, never diff.to.",
		"properties": map[string]any{
			"id":       map[string]any{"type": "integer", "description": "the replay cursor; ephemeral condition signals carry no durable id"},
			"card_id":  map[string]any{"type": "string"},
			"board_id": map[string]any{"type": "string", "description": "set on board-scoped events"},
			"scope":    map[string]any{"type": "string", "enum": []any{"card", "board"}, "description": "omitted for card events"},
			"version":  map[string]any{"type": "integer", "description": "event-contract version"},
			"type":     map[string]any{"type": "string", "enum": enum},
			"actor":    map[string]any{"type": "string"},
			"at":       map[string]any{"type": "string", "format": "date-time"},
			"diff":     map[string]any{"type": "object"},
		},
	}
}

func historyEntrySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"at":      map[string]any{"type": "string", "format": "date-time"},
			"actor":   map[string]any{"type": "string"},
			"type":    map[string]any{"type": "string"},
			"summary": map[string]any{"type": "string"},
		},
	}
}

func takeNextResultSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"card": map[string]any{"description": "the claimed card, or null when nothing matched",
				"oneOf": []any{refOf("Card"), map[string]any{"type": "null"}}},
		},
		"required": []any{"card"},
	}
}

// itemsSchema wraps a schema as {items: [...]} — the shape of the per-card
// event and history endpoints, which carry no next_cursor.
func itemsSchema(item map[string]any) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"items": map[string]any{"type": "array", "items": item}},
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
