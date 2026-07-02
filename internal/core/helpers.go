// Package core — helpers.go
//
// Id generation, cursor codec, actor context, and small pure utilities,
// split from service.go.
package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// --- id + cursor helpers ---

func newCardID() string    { return "card_" + strings.ReplaceAll(uuid.NewString(), "-", "") }
func newEntryID() string   { return "ent_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] }
func newCommentID() string { return "cm_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] }

// EncodeCursor / DecodeCursor: base64("updated_at|id").
func EncodeCursor(updatedAt time.Time, id string) string {
	s := updatedAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func DecodeCursor(c string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("bad cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return t, parts[1], nil
}

// --- small utilities ---

func ctxActor(ctx context.Context) string {
	if v, ok := ctx.Value(actorCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// ActorFromCtx is the exported accessor for transports.
func ActorFromCtx(ctx context.Context) string { return ctxActor(ctx) }

// WithActor returns a context carrying an actor (used by transports).
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorCtxKey{}, actor)
}

type actorCtxKey struct{}

func defaultStatus(ct *CardType, ws *Workspace) string {
	if len(ct.AllowedColumns) > 0 {
		return ct.AllowedColumns[0]
	}
	if len(ws.Columns) > 0 {
		return ws.Columns[0].ID
	}
	return ""
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func fieldIDs(ct *CardType) []string {
	out := make([]string, 0, len(ct.Fields))
	for _, f := range ct.Fields {
		out = append(out, f.ID)
	}
	sort.Strings(out)
	return out
}

func linkTypeIDs(ws *Workspace) []string {
	out := make([]string, 0, len(ws.LinkTypes))
	for _, l := range ws.LinkTypes {
		out = append(out, l.ID)
	}
	return out
}

func columnIDs(ws *Workspace) []string {
	out := make([]string, 0, len(ws.Columns))
	for _, c := range ws.Columns {
		out = append(out, c.ID)
	}
	return out
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func diffTags(old, new []string) (added, removed []string) {
	oset := map[string]bool{}
	for _, t := range old {
		oset[t] = true
	}
	nset := map[string]bool{}
	for _, t := range new {
		nset[t] = true
	}
	for _, t := range new {
		if !oset[t] {
			added = append(added, t)
		}
	}
	for _, t := range old {
		if !nset[t] {
			removed = append(removed, t)
		}
	}
	return
}

// getMapField fetches a key from a fields map (any-shaped).
func getMapField(fields any, key string) any {
	m, _ := fields.(map[string]any)
	if m == nil {
		return nil
	}
	return m[key]
}

func setField(fields any, key string, val any) map[string]any {
	m, _ := fields.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	out[key] = val
	return out
}

func appendEntry(fields any, field string, entry any) []any {
	m, _ := fields.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	arr, _ := m[field].([]any)
	out := make([]any, 0, len(arr)+1)
	out = append(out, arr...)
	out = append(out, entry)
	return out
}

func findEntry(arr []any, entryID string) (map[string]any, int, bool) {
	for i, e := range arr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := em["entry_id"].(string); id == entryID {
			return em, i, true
		}
	}
	return nil, -1, false
}

// summarizeEvent renders a human/agent-readable one-liner for the history
// timeline. SPEC §8.
func summarizeEvent(e Event) string {
	d, _ := e.Diff.(map[string]any)
	switch e.Type {
	case EventCardCreated:
		if c, ok := d["card"].(map[string]any); ok {
			return fmt.Sprintf("card created: %q (type=%v, status=%v)", c["title"], c["type_id"], c["status"])
		}
		return "card created"
	case EventFieldUpdated:
		return fmt.Sprintf("field %v: %v → %v", d["field"], d["before"], d["after"])
	case EventStatusChanged:
		return fmt.Sprintf("status: %v → %v", d["before"], d["after"])
	case EventOwnerChanged:
		return fmt.Sprintf("owner: %v → %v", d["before"], d["after"])
	case EventTagsChanged:
		return fmt.Sprintf("tags + %v / - %v", d["added"], d["removed"])
	case EventItemAppended:
		return fmt.Sprintf("appended entry to %v (entry_id=%v)", d["field"], d["entry_id"])
	case EventItemUpdated:
		return fmt.Sprintf("updated entry in %v (entry_id=%v)", d["field"], d["entry_id"])
	case EventItemRemoved:
		return fmt.Sprintf("removed entry from %v (entry_id=%v)", d["field"], d["entry_id"])
	case EventLinkAdded:
		return fmt.Sprintf("link %v → %v", d["type_id"], d["target"])
	case EventLinkRemoved:
		return fmt.Sprintf("removed link %v → %v", d["type_id"], d["target"])
	case EventCommentAdded:
		return fmt.Sprintf("comment added (%v)", d["comment_id"])
	case EventCommentEdited:
		return fmt.Sprintf("comment edited (%v)", d["comment_id"])
	case EventSchemaUpgraded:
		return fmt.Sprintf("schema upgraded: %v → %v", d["from"], d["to"])
	default:
		return string(e.Type)
	}
}
