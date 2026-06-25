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
	FieldString   FieldType = "string"
	FieldText     FieldType = "text"
	FieldNumber   FieldType = "number"
	FieldDate     FieldType = "date"
	FieldEnum     FieldType = "enum"
	FieldTags     FieldType = "tags"
	FieldUser     FieldType = "user"
	FieldCardLink FieldType = "card_link"
	FieldRepeating FieldType = "repeating"
	FieldArtifact FieldType = "artifact"
)

// FieldDef is a single field in a card-type schema.
type FieldDef struct {
	ID           string     `json:"id"`
	Label        string     `json:"label"`
	Type         FieldType  `json:"type"`
	Required     bool       `json:"required"`
	Default      any        `json:"default,omitempty"`
	Description  string     `json:"description,omitempty"`
	Options      []string   `json:"options,omitempty"`        // enum
	Min          *float64   `json:"min,omitempty"`             // number/date
	Max          *float64   `json:"max,omitempty"`             // number/date
	TargetType   string     `json:"target_type,omitempty"`     // card_link
	LinkType     string     `json:"link_type,omitempty"`       // card_link
	ItemFields   []FieldDef `json:"item_fields,omitempty"`      // repeating
	ArtifactPolicy string   `json:"artifact_policy,omitempty"` // artifact: "local"|"uri"
	Deprecated   bool       `json:"deprecated,omitempty"`
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
