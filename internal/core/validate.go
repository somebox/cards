// Package core — validate.go
//
// Field/tag/column/user/link validation and board-policy lookups, split from
// service.go. All methods hang off Service; no state of their own.
package core

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// --- validation helpers ---

func (s *Service) findField(ct *CardType, id string) (*FieldDef, error) {
	for i := range ct.Fields {
		if ct.Fields[i].ID == id {
			return &ct.Fields[i], nil
		}
	}
	return nil, newUnknownField(id, fieldIDs(ct))
}

// validateFields validates a full fields map against the type's schema.
func (s *Service) validateFields(ctx context.Context, ct *CardType, in map[string]any, checkRequired bool) (map[string]any, error) {
	strict := s.ws.Settings.StrictFields
	defs := map[string]FieldDef{}
	for _, f := range ct.Fields {
		defs[f.ID] = f
	}
	if strict {
		for k := range in {
			if _, ok := defs[k]; !ok {
				return nil, newUnknownField(k, fieldIDs(ct))
			}
		}
	}
	out := map[string]any{}
	for _, f := range ct.Fields {
		v, present := in[f.ID]
		if !present || v == nil {
			if f.Default != nil {
				out[f.ID] = f.Default
				continue
			}
			if checkRequired && f.Required {
				return nil, NewValidationError(f.ID, fmt.Sprintf("field %q is required", f.ID))
			}
			continue
		}
		if err := s.validateFieldValue(ctx, &f, v); err != nil {
			return nil, err
		}
		out[f.ID] = v
	}
	if !strict {
		for k, v := range in {
			if _, ok := defs[k]; !ok {
				out[k] = v
			}
		}
	}
	return out, nil
}

func (s *Service) validateOneField(ctx context.Context, ct *CardType, id string, v any) error {
	for i := range ct.Fields {
		f := ct.Fields[i]
		if f.ID == id {
			if f.Type == FieldRepeating {
				return NewValidationError(id, "repeating fields use the append/update/remove API")
			}
			return s.validateFieldValue(ctx, &f, v)
		}
	}
	if s.ws.Settings.StrictFields {
		return newUnknownField(id, fieldIDs(ct))
	}
	return nil
}

// validateFieldValue checks one value; card_link targets are resolved against
// the store (existence + optional target_type).
func (s *Service) validateFieldValue(ctx context.Context, f *FieldDef, v any) error {
	switch f.Type {
	case FieldCardLink:
		id, ok := v.(string)
		if !ok {
			return NewValidationError(f.ID, "card_link expects a card id string")
		}
		if id == "" {
			return nil
		}
		target, err := s.getCard(ctx, id)
		if err != nil {
			if ce := AsError(err); ce != nil && ce.Code == "not_found" {
				return newTargetCardMissing(id, f.TargetType)
			}
			return err
		}
		if f.TargetType != "" && target.TypeID != f.TargetType {
			return newTargetCardTypeMismatch(id, []string{f.TargetType})
		}
		return nil
	case FieldRepeating:
		arr, ok := v.([]any)
		if !ok {
			return NewValidationError(f.ID, "repeating field expects an array")
		}
		for i, e := range arr {
			em, ok := e.(map[string]any)
			if !ok {
				return NewValidationError(f.ID, fmt.Sprintf("repeating entry %d is not an object", i))
			}
			if err := validateEntry(f, em); err != nil {
				return err
			}
		}
		return nil
	default:
		return validateValue(f, v)
	}
}

// validateValue checks a scalar value (no store needed). See SPEC §6.
func validateValue(f *FieldDef, v any) error {
	switch f.Type {
	case FieldString, FieldText:
		if _, ok := v.(string); !ok {
			return NewValidationError(f.ID, fmt.Sprintf("field %q expects a string", f.ID))
		}
	case FieldNumber:
		n, ok := toFloat(v)
		if !ok {
			return NewValidationError(f.ID, fmt.Sprintf("field %q expects a number", f.ID))
		}
		if f.Min != nil && n < *f.Min {
			return NewValidationError(f.ID, fmt.Sprintf("field %q below min %v", f.ID, *f.Min))
		}
		if f.Max != nil && n > *f.Max {
			return NewValidationError(f.ID, fmt.Sprintf("field %q above max %v", f.ID, *f.Max))
		}
	case FieldDate:
		str, ok := v.(string)
		if !ok {
			return NewValidationError(f.ID, fmt.Sprintf("field %q expects an ISO date string", f.ID))
		}
		if _, err := time.Parse(time.RFC3339, str); err != nil {
			if _, err := time.Parse("2006-01-02", str); err != nil {
				return NewValidationError(f.ID, fmt.Sprintf("field %q is not a parseable date", f.ID))
			}
		}
	case FieldEnum:
		str, ok := v.(string)
		if !ok {
			return NewValidationError(f.ID, fmt.Sprintf("field %q expects a string", f.ID))
		}
		if !contains(f.Options, str) {
			return newUnknownEnum(f.ID, str, f.Options)
		}
	case FieldUser:
		str, ok := v.(string)
		if !ok {
			return NewValidationError(f.ID, fmt.Sprintf("field %q expects a user id string", f.ID))
		}
		_ = str // existence checked at card level
	case FieldTags, FieldArtifact:
		// tags validated at card level against tag_set; artifact stored as-is.
	}
	return nil
}

// validateEntry validates a repeating entry against item_fields. The reserved
// entry_id key is ignored. SPEC §6 (no nested repeating).
func validateEntry(f *FieldDef, entry map[string]any) error {
	defs := map[string]FieldDef{}
	for _, sf := range f.ItemFields {
		defs[sf.ID] = sf
	}
	// Unknown sub-fields rejected.
	for k := range entry {
		if k == "entry_id" {
			continue
		}
		if _, ok := defs[k]; !ok {
			return NewValidationError(f.ID+"."+k, "unknown item_field")
		}
	}
	for _, sf := range f.ItemFields {
		v, present := entry[sf.ID]
		if !present || v == nil {
			if sf.Required {
				return NewValidationError(f.ID+"."+sf.ID, fmt.Sprintf("item field %q is required", sf.ID))
			}
			continue
		}
		// Reuse the scalar validator; sub-fields can't be repeating in v1.
		if err := validateValue(&sf, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateTags(tags []string) error {
	set := map[string]bool{}
	for _, t := range s.ws.TagSet {
		set[t] = true
	}
	for _, t := range tags {
		if !set[t] {
			return newUnknownTag(t, s.ws.TagSet)
		}
	}
	return nil
}

func (s *Service) checkColumn(status string, ct *CardType) *Error {
	colSet := map[string]bool{}
	for _, c := range s.ws.Columns {
		colSet[c.ID] = true
	}
	if !colSet[status] {
		return newUnknownEnum("status", status, columnIDs(s.ws))
	}
	if len(ct.AllowedColumns) > 0 && !contains(ct.AllowedColumns, status) {
		return newUnknownEnum("status", status, ct.AllowedColumns)
	}
	return nil
}

func (s *Service) checkUserExists(ctx context.Context, userID string) error {
	users, _ := s.store.ListUsers(ctx)
	for _, u := range users {
		if u.ID == userID {
			return nil
		}
	}
	return newUnknownUser(userID)
}

func (s *Service) lookupLinkType(id string) *LinkType {
	for i := range s.ws.LinkTypes {
		if s.ws.LinkTypes[i].ID == id {
			return &s.ws.LinkTypes[i]
		}
	}
	return nil
}

func (s *Service) checkLinkTypeConstraints(lt *LinkType, sourceType, targetType string) *Error {
	if len(lt.SourceTypes) > 0 && !contains(lt.SourceTypes, sourceType) {
		return newTargetCardTypeMismatch(sourceType, lt.SourceTypes)
	}
	if len(lt.TargetTypes) > 0 && !contains(lt.TargetTypes, targetType) {
		return newTargetCardTypeMismatch(targetType, lt.TargetTypes)
	}
	return nil
}

func (s *Service) boardForCard(c *Card) *Board {
	return s.boardForTypeID(c.TypeID, "")
}

func (s *Service) boardForTypeID(typeID, boardID string) *Board {
	if boardID != "" {
		if b, ok := s.boards[boardID]; ok {
			return b
		}
	}
	// A card's type may belong to several boards (e.g. one board per sub-app).
	// This lookup only gates transition enforcement, so prefer a board that
	// enforces transitions; break ties by board id so the choice never depends
	// on Go's randomized map iteration order.
	ids := make([]string, 0, len(s.boards))
	for id := range s.boards {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var match *Board
	for _, id := range ids {
		b := s.boards[id]
		if !contains(b.CardTypeIDs, typeID) {
			continue
		}
		if b.Settings.EnforceTransitions {
			return b
		}
		if match == nil {
			match = b
		}
	}
	return match
}

// allowedFromStatuses returns statuses that may transition to `to` under b's graph.
func allowedFromStatuses(b *Board, to string) []string {
	out := []string{}
	for from, nexts := range b.Transitions {
		if contains(nexts, to) {
			out = append(out, from)
		}
	}
	if len(out) == 0 {
		// No explicit edge; allow same-status (no-op) so a card already at `to`
		// can still be claimed.
		out = append(out, to)
	}
	return out
}
