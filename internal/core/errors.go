// Package core — errors.go
//
// Structured error type matching SPEC.md §10. Every rejection carries a
// stable `error` code plus the fields an agent/UI needs to self-correct
// (valid_options, hint, current card for version_conflict, etc.).
package core

import (
	"errors"
	"fmt"
)

// Error is the structured JSON error returned by all writes. See SPEC.md §10.
type Error struct {
	Code         string   `json:"error"`
	Message      string   `json:"message"`
	Field        string   `json:"field,omitempty"`
	Value        any      `json:"value,omitempty"`
	ValidOptions []string `json:"valid_options,omitempty"`
	Hint         string   `json:"hint,omitempty"`
	HTTPStatus   int      `json:"-"`
	// CurrentCard is attached to version_conflict (409) responses.
	CurrentCard *Card `json:"card,omitempty"`
	// Candidates is attached to "ambiguous" (409) short-id responses: the
	// id+title of every card matching the short id, so callers can pick the
	// full id and retry. Never auto-resolved. (1e)
	Candidates []CardCandidate `json:"candidates,omitempty"`
}

func (e *Error) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s (field=%s)", e.Code, e.Message, e.Field)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewValidationError returns a generic validation_failed 422.
func NewValidationError(field, msg string) *Error {
	return &Error{Code: "validation_failed", Field: field, Message: msg, HTTPStatus: 422}
}

func newUnknownEnum(field string, value any, options []string) *Error {
	return &Error{
		Code: "unknown_enum", Field: field, Value: value,
		Message:      "Unknown enum value.",
		ValidOptions: options,
		Hint:         "See GET /v1/workspace",
		HTTPStatus:   422,
	}
}

// newUnknownTag reports the policy actually in force. The hint used to
// hardcode "propose" regardless of configuration, which named a mode the
// author had not chosen — the one diagnostic they got pointed at the wrong
// setting.
func newUnknownTag(value string, tagSet []string, policy string) *Error {
	if policy == "" {
		policy = TagPolicyLocked
	}
	return &Error{
		Code: "unknown_tag", Value: value,
		Message:      "Unknown tag.",
		ValidOptions: tagSet,
		Hint: "Tag policy is '" + policy + "'; add it to workspace.tag_set, " +
			"or set settings.tag_policy to 'open' to allow free tags.",
		HTTPStatus: 422,
	}
}

func newUnknownUser(value string) *Error {
	return &Error{
		Code: "unknown_user", Value: value,
		Message: "Unknown user.",
		Hint:    "Register first: POST /v1/users",
		HTTPStatus: 422,
	}
}

func newUnknownField(field string, fields []string) *Error {
	return &Error{
		Code: "unknown_field", Field: field,
		Message:      "Unknown field for this card type (strict mode).",
		ValidOptions: fields,
		HTTPStatus:   422,
	}
}

func newTransitionIllegal(from string, allowed []string) *Error {
	return &Error{
		Code: "transition_illegal", Field: "status", Value: from,
		Message:      "Status transition not allowed by board transitions.",
		ValidOptions: allowed,
		HTTPStatus:   422,
	}
}

func newSchemaVersionMismatch(current int) *Error {
	return &Error{
		Code: "schema_version_mismatch",
		Message:             "Card pinned to a different schema version.",
		ValidOptions:        []string{fmt.Sprintf("%d", current)},
		Hint:                "POST /v1/cards/:id/upgrade-schema",
		HTTPStatus:          422,
	}
}

func newTargetCardMissing(value string, targetType ...string) *Error {
	return &Error{
		Code: "target_card_missing", Field: "", Value: value,
		Message: "card_link target does not exist.",
		Hint:    "Create the target card first, or fix the id.",
		HTTPStatus: 422,
	}
}

func newTargetCardTypeMismatch(value string, valid []string) *Error {
	return &Error{
		Code: "target_card_type_mismatch", Value: value,
		Message:      "card_link target is not of an allowed card type.",
		ValidOptions: valid,
		HTTPStatus:   422,
	}
}

// VersionConflict returns a 409 carrying the current card.
func VersionConflict(current *Card) *Error {
	return &Error{
		Code: "version_conflict",
		Message:    "Stale version; another mutation has occurred.",
		HTTPStatus: 409,
		CurrentCard: current,
	}
}

// NotFound returns a 404.
func NotFound(resource string) *Error {
	return &Error{Code: "not_found", Message: "Resource not found: " + resource, HTTPStatus: 404}
}

// Internal returns a 500 for unexpected server-side failures.
func Internal(msg string) *Error {
	return &Error{Code: "internal_error", Message: msg, HTTPStatus: 500}
}

// ErrNotFound is a sentinel returned by the store when a row is absent.
// The service layer maps it to a 404 NotFound.
var ErrNotFound = errors.New("not found")

// ErrClaimRaced is a sentinel ClaimAtomic returns when its CAS lost a race
// with another claimant (as opposed to no candidate matching at all, which
// returns nil, nil, nil). TakeNext retries on it: a fresh transaction sees
// the racer's commit and naturally selects the next candidate.
var ErrClaimRaced = errors.New("claim raced")

// ActorRequired returns a 403.
func ActorRequired() *Error {
	return &Error{
		Code: "actor_required",
		Message: "No actor supplied. Set X-Work-Cards-Actor, CARDS_USER, or workspace.settings.default_user.",
		HTTPStatus: 403,
	}
}

// AsError unwraps *Error from error; nil if not a core.Error.
// *AmbiguousIDError is part of the taxonomy: it converts to code "ambiguous"
// (409) carrying the candidate list, so every renderer that goes through
// AsError (HTTP writeAPIError, MCP toolError, CLI) surfaces the same shape.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var amb *AmbiguousIDError
	if errors.As(err, &amb) {
		return &Error{
			Code:       "ambiguous",
			Message:    amb.Error(),
			Value:      amb.Short,
			Candidates: amb.Candidates,
			Hint:       "Use a full card id.",
			HTTPStatus: 409,
		}
	}
	var e *Error
	if ok := errors.As(err, &e); ok {
		return e
	}
	return nil
}
