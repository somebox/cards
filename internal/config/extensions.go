// Package config — extensions.go
//
// Parses definitions/extensions.yaml (or .json): hook, service, and run
// declarations. Validated at load time. See docs/EXTENSIONS.md.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/somebox/cards/internal/core"
)

// Extension is one declared extension. Kind is hook|service|run.
type Extension struct {
	ID          string            `json:"id" yaml:"id"`
	Kind        string            `json:"kind" yaml:"kind"` // hook | service | run
	Description string            `json:"description" yaml:"description"`
	On          string            `json:"on" yaml:"on"`         // hook: event type
	Filter      HookFilter        `json:"filter" yaml:"filter"` // hook: event filter
	Run         []string          `json:"run" yaml:"run"`       // argv
	Cwd         string            `json:"cwd" yaml:"cwd"`
	Env         map[string]string `json:"env" yaml:"env"`
	Autostart   bool              `json:"autostart" yaml:"autostart"`
	Expose      *Expose           `json:"expose" yaml:"expose"`
}

// HookFilter selects which events trigger a hook.
type HookFilter struct {
	BoardID   string `json:"board_id" yaml:"board_id"`
	TypeID    string `json:"type_id" yaml:"type_id"`
	CardID    string `json:"card_id" yaml:"card_id"`
	ToStatus  string `json:"to_status" yaml:"to_status"`
	FromStatus string `json:"from_status" yaml:"from_status"`
}

// Expose is a service extension's exposed endpoint.
type Expose struct {
	Port     int    `json:"port" yaml:"port"`
	Protocol string `json:"protocol" yaml:"protocol"`
}

// LoadExtensions reads definitions/extensions.{yaml,json} if present. JSON is
// supported directly; YAML is parsed via a minimal inline parser (the POC only
// needs the subset used by EXTENSIONS.md examples). Returns nil if absent.
func LoadExtensions(workspaceDir string) ([]Extension, error) {
	for _, name := range []string{"extensions.yaml", "extensions.yml", "extensions.json"} {
		path := filepath.Join(workspaceDir, "definitions", name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var env struct {
			Extensions []Extension `json:"extensions"`
		}
		if strings.HasSuffix(name, ".json") {
			if err := json.Unmarshal(data, &env); err != nil {
				return nil, fmt.Errorf("parse %s: %w", name, err)
			}
		} else {
			exts, err := parseYAMLExtensions(data)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", name, err)
			}
			env.Extensions = exts
		}
		if err := validateExtensions(env.Extensions); err != nil {
			return nil, err
		}
		return env.Extensions, nil
	}
	return nil, nil
}

func validateExtensions(exts []Extension) error {
	seen := map[string]bool{}
	for _, e := range exts {
		if e.ID == "" {
			return fmt.Errorf("extension missing id")
		}
		if seen[e.ID] {
			return fmt.Errorf("duplicate extension id: %s", e.ID)
		}
		seen[e.ID] = true
		switch e.Kind {
		case "hook", "service", "run":
		default:
			return fmt.Errorf("extension %s: unknown kind %q", e.ID, e.Kind)
		}
		if len(e.Run) == 0 {
			return fmt.Errorf("extension %s: run is required", e.ID)
		}
		if e.Kind == "hook" && e.On == "" {
			return fmt.Errorf("extension %s: hook requires on", e.ID)
		}
	}
	return nil
}

// MatchesEvent reports whether a hook filter accepts an event (POC: type +
// card_id + board_id membership + to_status from a status_changed diff).
// cardBoardMembership returns the board id a card belongs to (or "").
// cardTypeID returns the card's type_id (or "").
func (e Extension) MatchesEvent(ev *core.Event, cardBoardMembership func(cardID string) string, cardTypeID func(cardID string) string) bool {
	if e.On != "" && string(ev.Type) != e.On {
		return false
	}
	if e.Filter.CardID != "" && ev.CardID != e.Filter.CardID {
		return false
	}
	if e.Filter.TypeID != "" {
		if tid := cardTypeID(ev.CardID); tid != e.Filter.TypeID {
			return false
		}
	}
	if e.Filter.BoardID != "" {
		bid := cardBoardMembership(ev.CardID)
		if bid != e.Filter.BoardID {
			return false
		}
	}
	if e.Filter.ToStatus != "" || e.Filter.FromStatus != "" {
		before, after := beforeAfterStrings(ev.Diff)
		if e.Filter.ToStatus != "" && after != e.Filter.ToStatus {
			return false
		}
		if e.Filter.FromStatus != "" && before != e.Filter.FromStatus {
			return false
		}
	}
	return true
}

// beforeAfterStrings extracts {before,after} string fields from an event's
// Diff regardless of its concrete Go type. Diff is `any` on the wire (docs/
// EVENTS.md §3/§4): built-in events carry typed structs (e.g.
// core.BeforeAfterDiff) in-process, but may also arrive as a decoded
// map[string]any (e.g. replayed from JSON). Round-tripping through JSON
// handles both without this package depending on internal/core's diff types.
func beforeAfterStrings(diff any) (before, after string) {
	if diff == nil {
		return "", ""
	}
	if m, ok := diff.(map[string]any); ok {
		b, _ := m["before"].(string)
		a, _ := m["after"].(string)
		return b, a
	}
	b, err := json.Marshal(diff)
	if err != nil {
		return "", ""
	}
	var ba struct {
		Before string `json:"before"`
		After  string `json:"after"`
	}
	if err := json.Unmarshal(b, &ba); err != nil {
		return "", ""
	}
	return ba.Before, ba.After
}

// parseYAMLExtensions is a minimal YAML parser for the EXTENSIONS.md subset
// (list of maps with scalar/array fields). Not a general YAML parser; the POC
// only needs this shape. For richer YAML, add gopkg.in/yaml.v3 later.
func parseYAMLExtensions(data []byte) ([]Extension, error) {
	lines := strings.Split(string(data), "\n")
	var exts []Extension
	var cur *Extension
	flush := func() {
		if cur != nil && cur.ID != "" {
			exts = append(exts, *cur)
		}
	}
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)
		if indent == 0 && strings.HasPrefix(trimmed, "- ") {
			flush()
			cur = &Extension{}
			trimmed = strings.TrimPrefix(trimmed, "- ")
		} else if indent == 0 && trimmed == "-" {
			flush()
			cur = &Extension{}
			continue
		} else if cur == nil {
			// top-level key like "extensions:"
			continue
		}
		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, "\"'")
		switch k {
		case "id":
			cur.ID = v
		case "kind":
			cur.Kind = v
		case "description":
			cur.Description = v
		case "on":
			cur.On = v
		case "autostart":
			cur.Autostart = v == "true"
		case "cwd":
			cur.Cwd = v
		case "run":
			// inline list [a, b] or single value
			cur.Run = parseYAMLList(v)
		case "board_id":
			cur.Filter.BoardID = v
		case "type_id":
			cur.Filter.TypeID = v
		case "card_id":
			cur.Filter.CardID = v
		case "to_status":
			cur.Filter.ToStatus = v
		case "from_status":
			cur.Filter.FromStatus = v
		}
	}
	flush()
	return exts, nil
}

func parseYAMLList(v string) []string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		inner := v[1 : len(v)-1]
		parts := strings.Split(inner, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.Trim(strings.TrimSpace(p), "\"'")
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	if v == "" {
		return nil
	}
	return []string{v}
}
