// Filter DSL (SPEC §9 subset) → SQL compilation. This lives in the storage
// layer — core hands the raw Filter map through CardQuery and never sees SQL.
package sqlite

import (
	"fmt"
	"sort"
	"strings"

	"github.com/somebox/cards/internal/core"
)

// compileFilter compiles a filter DSL node into a WHERE fragment + args.
// Invalid DSL yields a core validation error so callers surface it as a
// client error (HTTP 400), not an internal failure.
func compileFilter(node map[string]any) (string, []any, error) {
	frag, args, err := compileFilterNode(node)
	if err != nil {
		return "", nil, core.NewValidationError("filter", err.Error())
	}
	return frag, args, nil
}

func compileFilterNode(node map[string]any) (string, []any, error) {
	var parts []string
	var args []any
	keys := sortedKeys(node)
	for _, k := range keys {
		v := node[k]
		switch k {
		case "$and", "$or":
			arr, ok := v.([]any)
			if !ok {
				return "", nil, fmt.Errorf("%s expects an array", k)
			}
			sub := []string{}
			for _, e := range arr {
				em, ok := e.(map[string]any)
				if !ok {
					return "", nil, fmt.Errorf("%s element must be an object", k)
				}
				p, a, err := compileFilterNode(em)
				if err != nil {
					return "", nil, err
				}
				if p != "" {
					sub = append(sub, "("+p+")")
					args = append(args, a...)
				}
			}
			if len(sub) > 0 {
				joiner := " AND "
				if k == "$or" {
					joiner = " OR "
				}
				parts = append(parts, "("+strings.Join(sub, joiner)+")")
			}
		default:
			opMap, ok := v.(map[string]any)
			if !ok {
				return "", nil, fmt.Errorf("field %q must be an operator object", k)
			}
			p, a, err := compileFieldOp(k, opMap)
			if err != nil {
				return "", nil, err
			}
			parts = append(parts, p)
			args = append(args, a...)
		}
	}
	return strings.Join(parts, " AND "), args, nil
}

func compileFieldOp(key string, opMap map[string]any) (string, []any, error) {
	expr, tagMode, err := columnExpr(key)
	if err != nil {
		return "", nil, err
	}
	var parts []string
	var args []any
	for op, val := range opMap {
		p, a, err := compileOp(expr, tagMode, op, val)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, p)
		args = append(args, a...)
	}
	return strings.Join(parts, " AND "), args, nil
}

// columnExpr returns the SQL expression for a filter key and whether it's the
// tags array (which needs json_each).
func columnExpr(key string) (expr string, tagMode bool, err error) {
	switch key {
	case "status", "owner", "type_id", "created_by":
		return key, false, nil
	case "updated_at", "created_at":
		return key, false, nil
	case "tag", "tags":
		return "tags", true, nil
	default:
		if strings.HasPrefix(key, "fields.") {
			return "json_extract(fields, '$." + strings.TrimPrefix(key, "fields.") + "')", false, nil
		}
		// Unknown top-level key → treat as a typed field path.
		return "json_extract(fields, '$." + key + "')", false, nil
	}
}

func compileOp(expr string, tagMode bool, op string, val any) (string, []any, error) {
	if tagMode {
		return compileTagOp(op, val)
	}
	switch op {
	case "$eq":
		if val == nil {
			return expr + " IS NULL", nil, nil
		}
		return expr + " = ?", []any{val}, nil
	case "$ne":
		if val == nil {
			return expr + " IS NOT NULL", nil, nil
		}
		return expr + " != ?", []any{val}, nil
	case "$in":
		arr := toAnySlice(val)
		if len(arr) == 0 {
			return "0", nil, nil // empty IN matches nothing
		}
		return expr + " IN (" + placeholders(len(arr)) + ")", arr, nil
	case "$nin":
		arr := toAnySlice(val)
		if len(arr) == 0 {
			return "1", nil, nil
		}
		return expr + " NOT IN (" + placeholders(len(arr)) + ")", arr, nil
	case "$gt":
		return expr + " > ?", []any{val}, nil
	case "$gte":
		return expr + " >= ?", []any{val}, nil
	case "$lt":
		return expr + " < ?", []any{val}, nil
	case "$lte":
		return expr + " <= ?", []any{val}, nil
	case "$contains":
		return expr + " LIKE ?", []any{"%" + fmt.Sprint(val) + "%"}, nil
	default:
		return "", nil, fmt.Errorf("unsupported operator %q", op)
	}
}

func compileTagOp(op string, val any) (string, []any, error) {
	switch op {
	case "$eq", "$contains":
		return "EXISTS (SELECT 1 FROM json_each(tags) WHERE value = ?)", []any{val}, nil
	case "$in":
		arr := toAnySlice(val)
		if len(arr) == 0 {
			return "0", nil, nil
		}
		return "EXISTS (SELECT 1 FROM json_each(tags) WHERE value IN (" + placeholders(len(arr)) + "))", arr, nil
	case "$nin":
		arr := toAnySlice(val)
		if len(arr) == 0 {
			return "1", nil, nil
		}
		return "NOT EXISTS (SELECT 1 FROM json_each(tags) WHERE value IN (" + placeholders(len(arr)) + "))", arr, nil
	default:
		return "", nil, fmt.Errorf("unsupported tag operator %q", op)
	}
}

// toAnySlice normalizes a JSON-decoded array ([]any) or []string into []any
// for placeholder expansion; anything else yields nil.
func toAnySlice(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		if s, ok := v.([]string); ok {
			arr = make([]any, len(s))
			for i, x := range s {
				arr[i] = x
			}
			return arr
		}
		return nil
	}
	return arr
}

// sortedKeys keeps compiled SQL deterministic for a given filter map.
func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
