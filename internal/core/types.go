// Package core implements the Work Cards service layer: card and schema
// types, validation, transition evaluation, optimistic concurrency,
// idempotency, and event production.
//
// All transports (HTTP, CLI, MCP, RPC) call into this package so that
// validation and storage behavior are identical across surfaces. See
// docs/SPEC.md (v0.4) for the normative contract.
package core

import "time"

// FieldType is the small, composable field-type catalog. See SPEC.md §6.
type FieldType string

const (
	FieldString    FieldType = "string"
	FieldText      FieldType = "text"
	FieldNumber    FieldType = "number"
	FieldDate      FieldType = "date"
	FieldEnum      FieldType = "enum"
	FieldTags      FieldType = "tags"
	FieldUser      FieldType = "user"
	FieldCardLink  FieldType = "card_link"
	FieldRepeating FieldType = "repeating"
	FieldArtifact  FieldType = "artifact"
)

// FieldDef is a single field in a card-type schema.
type FieldDef struct {
	ID             string     `json:"id"`
	Label          string     `json:"label"`
	Type           FieldType  `json:"type"`
	Required       bool       `json:"required"`
	Default        any        `json:"default,omitempty"`
	Description    string     `json:"description,omitempty"`
	Options        []string   `json:"options,omitempty"`         // enum
	Min            *float64   `json:"min,omitempty"`             // number/date
	Max            *float64   `json:"max,omitempty"`             // number/date
	TargetType     string     `json:"target_type,omitempty"`     // card_link
	LinkType       string     `json:"link_type,omitempty"`       // card_link
	ItemFields     []FieldDef `json:"item_fields,omitempty"`      // repeating
	ArtifactPolicy string     `json:"artifact_policy,omitempty"` // artifact: "local"|"uri"
	Deprecated     bool       `json:"deprecated,omitempty"`
}

// CardType is a versioned schema for a kind of card. Types are defined at
// the workspace level. See SPEC.md §4.
type CardType struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Description      string     `json:"description,omitempty"`
	SchemaVersion    int        `json:"schema_version"`
	Fields           []FieldDef `json:"fields"`
	AllowedColumns   []string   `json:"allowed_columns,omitempty"`
	Icon             string     `json:"icon,omitempty"`
	SearchableFields []string   `json:"searchable_fields,omitempty"`
}

// Column is a status lane. Status values on cards are column IDs.
type Column struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// LinkType is workspace-level vocabulary for card relationships.
type LinkType struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"` // "directional" | "bidirectional"
	SourceTypes []string `json:"source_types,omitempty"`
	TargetTypes []string `json:"target_types,omitempty"`
}

// Link is a typed edge between cards.
type Link struct {
	TypeID    string    `json:"type_id"`
	Target    string    `json:"target"`
	Note      string    `json:"note,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// Comment is a universal markdown discussion entry on a card.
type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	EditedAt  time.Time `json:"edited_at,omitempty"`
}

// User is an open identifier (no auth in v1). See SPEC.md §4.
type User struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name,omitempty"`
	Kind        string    `json:"kind,omitempty"` // "human" | "agent"
	CreatedAt   time.Time `json:"created_at"`
}

// WorkspaceSettings holds workspace-wide defaults. See SPEC.md §4.
type WorkspaceSettings struct {
	EnforceTransitions  bool   `json:"enforce_transitions"`
	StrictFields        bool   `json:"strict_fields"`
	TagPolicy           string `json:"tag_policy"`
	EventRetentionDays  int    `json:"event_retention_days,omitempty"`
	DefaultUser         string `json:"default_user,omitempty"`
}

// Workspace is the top-level scope. All cards belong to one workspace.
type Workspace struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Columns  []Column          `json:"columns"`
	TagSet   []string          `json:"tag_set"`
	LinkTypes []LinkType       `json:"link_types"`
	Users    []User            `json:"users"`
	Settings WorkspaceSettings `json:"settings"`
}

// BoardPresentation carries UI hints. See SPEC.md §4 + DEVELOPER-REFERENCE.md §7.
type BoardPresentation struct {
	LaneGroupBy string                       `json:"lane_group_by,omitempty"`
	CardPreview map[string][]string          `json:"card_preview,omitempty"`
	Filters     []BoardFilter                 `json:"filters,omitempty"`
}

// BoardFilter is a named saved filter on a board.
type BoardFilter struct {
	ID     string         `json:"id"`
	Label  string         `json:"label"`
	Filter map[string]any `json:"filter"`
}

// Board is a Kanban lens: a column subset, the card types shown, a default
// filter, optional transitions, and UI hints. It does not own cards.
type Board struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Columns      []string            `json:"columns"`
	CardTypeIDs  []string            `json:"card_type_ids,omitempty"`
	DefaultFilter map[string]any     `json:"default_filter,omitempty"`
	Transitions  map[string][]string `json:"transitions,omitempty"`
	Presentation *BoardPresentation  `json:"presentation,omitempty"`
	Settings     struct {
		EnforceTransitions bool `json:"enforce_transitions"`
	} `json:"settings"`
}

// View is a named filter plus optional URL binding. Read-only in v1.
type View struct {
	ID      string         `json:"id"`
	BoardID string         `json:"board_id,omitempty"`
	Path    string         `json:"path"`
	Bind    map[string]any `json:"bind,omitempty"`
	Filter  map[string]any `json:"filter"`
	Methods []string       `json:"methods,omitempty"`
}

// Card is an instance of a card type. The universal envelope is managed by
// the runtime; custom data lives in Fields. See SPEC.md §4.
type Card struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	TypeID        string    `json:"type_id"`
	SchemaVersion int       `json:"schema_version"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	Fields        any       `json:"fields"`
	Owner         string    `json:"owner,omitempty"`
	Tags          []string  `json:"tags,omitempty"`
	Links         []Link    `json:"links,omitempty"`
	Comments      []Comment `json:"comments,omitempty"`
	Version       int       `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CreatedBy     string    `json:"created_by"`
}

// EventType enumerates append-only event kinds. See SPEC.md §4/§8.
type EventType string

const (
	EventCardCreated      EventType = "card_created"
	EventFieldUpdated     EventType = "field_updated"
	EventStatusChanged    EventType = "status_changed"
	EventOwnerChanged     EventType = "owner_changed"
	EventTagsChanged      EventType = "tags_changed"
	EventItemAppended     EventType = "item_appended"
	EventItemUpdated      EventType = "item_updated"
	EventItemRemoved      EventType = "item_removed"
	EventLinkAdded        EventType = "link_added"
	EventLinkRemoved      EventType = "link_removed"
	EventCommentAdded     EventType = "comment_added"
	EventCommentEdited    EventType = "comment_edited"
	EventSchemaUpgraded   EventType = "schema_upgraded"
	EventArtifactAdded    EventType = "artifact_added"
	EventDefinitionReload EventType = "definition_reloaded"
)

// Event is an append-only mutation record with a normative diff (SPEC §8).
type Event struct {
	ID      int64     `json:"id"`
	CardID  string    `json:"card_id"`
	Type    EventType `json:"type"`
	Actor   string    `json:"actor"`
	At      time.Time `json:"at"`
	Diff    any       `json:"diff"`
}

// --- Request / result types ---

// CardQuery is the POC filter set for ListCards. See SPEC.md §9.
type CardQuery struct {
	BoardID    string
	TypeID     string
	TypeIDIn   []string
	Status     string
	StatusIn   []string
	Owner      string
	Q          string
	Blocked    bool
	HasLink    string // link type id present on the card
	LinkTarget string // card id linked to
	Unowned    bool
	Limit      int
	Cursor     string
	// Filter is the jq-like filter DSL (§9). Compiled by compileFilterInto.
	Filter     map[string]any
	FilterSQL  string
	FilterArgs []any
}

// Page is a paginated result.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// CreateCardRequest is the body of POST /cards. See SPEC.md §11.
type CreateCardRequest struct {
	TypeID        string         `json:"type_id"`
	Title         string         `json:"title"`
	Status        string         `json:"status,omitempty"`
	Fields        map[string]any `json:"fields,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	SchemaVersion int            `json:"schema_version,omitempty"`
	DryRun        bool           `json:"dry_run,omitempty"`
	Actor         string         `json:"actor,omitempty"`
}

// PatchCardRequest is the body of PATCH /cards/:id. See SPEC.md §11.
type PatchCardRequest struct {
	Version int            `json:"version"`
	Title   *string        `json:"title,omitempty"`
	Status  *string        `json:"status,omitempty"`
	Owner   *string        `json:"owner,omitempty"`
	Tags    *[]string      `json:"tags,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
	Force   bool           `json:"force,omitempty"` // bypass enforced-transition check (admin/force-move)
	DryRun  bool           `json:"dry_run,omitempty"`
	Actor   string         `json:"actor,omitempty"`
}

// LinkInput is the body of POST /cards/:id/links. See SPEC.md §11.
type LinkInput struct {
	TypeID string `json:"type_id"`
	Target string `json:"target"`
	Note   string `json:"note,omitempty"`
	Actor  string `json:"actor,omitempty"`
}

// CommentInput is the body of POST /cards/:id/comments.
type CommentInput struct {
	Body  string `json:"body"`
	Actor string `json:"actor,omitempty"`
}

// ClaimRequest is the body of POST /cards/:id/claim. See SPEC.md §11.
type ClaimRequest struct {
	Version int    `json:"version"`
	Status  string `json:"status,omitempty"`
	Actor   string `json:"actor,omitempty"`
}

// ReleaseRequest is the body of POST /cards/:id/release. See SPEC.md §11.
// Release clears the card's owner (the inverse of claim). Optionally moves
// status (e.g. back to backlog/todo) with force=true to bypass enforced
// transitions — the recovery path for mis-claimed or mis-triaged cards.
type ReleaseRequest struct {
	Version int    `json:"version"`
	Status  string `json:"status,omitempty"` // optional status to move to
	Force   bool   `json:"force,omitempty"`   // bypass enforced-transition check
	Actor   string `json:"actor,omitempty"`
}

// TakeNextRequest is the body of POST /cards/take-next. See SPEC.md §11/§9.
type TakeNextRequest struct {
	AssignTo string         `json:"assign_to"`
	Status   string         `json:"status,omitempty"`
	TypeID   string         `json:"type_id,omitempty"`
	BoardID  string         `json:"board_id,omitempty"`
	Filter   map[string]any `json:"filter,omitempty"`
	Actor    string         `json:"actor,omitempty"`
}

// EventQuery filters events for GET /cards/:id/events and SSE replay.
type EventQuery struct {
	CardID  string
	Types   []string
	Limit   int
	AfterID int64 // replay: events with id > this (SSE Last-Event-ID)
}

// HistoryEntry is one rendered line of the resumption timeline.
type HistoryEntry struct {
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`
	Type    EventType `json:"type"`
	Summary string    `json:"summary"`
}

// IdempotencyRecord is a stored mutation response for replay.
type IdempotencyRecord struct {
	Key      string
	Actor    string
	Status   int
	Body     []byte
}

// WorkspaceSnapshot is the introspection payload for GET /workspace.
type WorkspaceSnapshot struct {
	Workspace       *Workspace              `json:"workspace"`
	CardTypes       map[string]*CardType    `json:"card_types"`
	Boards          map[string]*Board       `json:"boards"`
	CurrentVersions map[string]int           `json:"current_schema_versions"`
}
