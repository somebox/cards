package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
)

// portStats is the per-run tally shared by export and import, reported to the
// user on stderr.
type portStats struct {
	Cards    int
	Events   int
	Comments int
	Links    int
	Users    int
}

// exportJSONL writes the workspace state (header, users, cards with embedded
// comments+links, and — unless stateOnly — the event log) to w as one JSON
// object per line. It is the testable core of `cards export`; the command
// wrapper only opens the store and picks the destination.
//
// stateOnly omits the event journal: definitions + current card state are the
// canonical, git-portable form, while the event journal (and condition_marks,
// and future delivery state) are SQLite-owned durable ground truth that an
// import must never reconstruct (docs/events/core.md). A state-only export is
// also sorted by id so a committed snapshot diffs cleanly across re-exports.
func exportJSONL(ctx context.Context, st *sqlite.Store, w io.Writer, ws *core.Workspace, stateOnly bool) (portStats, error) {
	var stats portStats
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	// Header — identifies the export format + workspace.
	if err := enc.Encode(map[string]any{
		"type":         "export",
		"version":      1,
		"workspace_id": ws.ID,
		"workspace":    ws.Name,
		"state_only":   stateOnly,
	}); err != nil {
		return stats, err
	}

	users, err := st.ListUsers(ctx)
	if err != nil {
		return stats, fmt.Errorf("export users: %w", err)
	}
	if stateOnly {
		slices.SortFunc(users, func(a, b core.User) int { return strings.Compare(a.ID, b.ID) })
	}
	for _, u := range users {
		if err := enc.Encode(map[string]any{"type": "user", "data": u}); err != nil {
			return stats, err
		}
	}
	stats.Users = len(users)

	// Cards (with links + comments loaded via GetCard). ListCards caps a single
	// page (see clampCardLimit), so a full backup must paginate by cursor
	// rather than ask for an arbitrarily large limit — otherwise the export
	// silently truncates to the cap. Collect first, then encode, so a state-only
	// export can be id-sorted for stable diffs.
	var cards []*core.Card
	cursor := ""
	for {
		page, err := st.ListCards(ctx, core.CardQuery{Limit: 500, Cursor: cursor})
		if err != nil {
			return stats, fmt.Errorf("export cards: %w", err)
		}
		for _, c := range page.Items {
			full, err := st.GetCard(ctx, c.ID)
			if err != nil {
				// A backup that silently drops cards is worse than one that fails.
				return stats, fmt.Errorf("export card %s: %w", c.ID, err)
			}
			cards = append(cards, full)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if stateOnly {
		slices.SortFunc(cards, func(a, b *core.Card) int { return strings.Compare(a.ID, b.ID) })
	}
	for _, full := range cards {
		stats.Comments += len(full.Comments)
		stats.Links += len(full.Links)
		if err := enc.Encode(map[string]any{"type": "card", "data": full}); err != nil {
			return stats, err
		}
	}
	stats.Cards = len(cards)

	// Events. A full export carries the whole audit log. A state-only export
	// carries ONLY card_deleted tombstones — the record of which cards were
	// removed — so references to retired cards (e.g. a roadmap item that came
	// from a since-deleted board card) stay resolvable, without committing the
	// churn of the mutation log (which is SQLite-owned durable state).
	q := core.EventQuery{Limit: 1000000}
	if stateOnly {
		q.Types = []string{string(core.EventCardDeleted)}
	}
	evs, err := st.List(ctx, q)
	if err != nil {
		return stats, fmt.Errorf("export events: %w", err)
	}
	if stateOnly {
		slices.SortFunc(evs, func(a, b core.Event) int { return cmp.Compare(a.ID, b.ID) })
	}
	for _, e := range evs {
		if err := enc.Encode(map[string]any{"type": "event", "data": e}); err != nil {
			return stats, err
		}
	}
	stats.Events = len(evs)
	return stats, nil
}

// portEnvelope is the on-disk line shape: {"type": "...", "data": {...}}.
type portEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// importJSONL loads a JSONL export (as produced by exportJSONL) into st. Card
// ids, versions, and timestamps are preserved verbatim so links and history
// stay intact (unlike re-creating through the API, which mints new ids).
//
// It is a full-snapshot restore, not a merge: the caller must ensure st is a
// fresh/empty workspace (the command wrapper enforces this). A duplicate card
// id is therefore a hard error — we fail loudly rather than silently overwrite
// existing state (SPEC §3 / D13: never a silent overwrite).
func importJSONL(ctx context.Context, st *sqlite.Store, r io.Reader) (portStats, error) {
	var stats portStats
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024) // allow large card lines
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var env portEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return stats, fmt.Errorf("line %d: parse: %w", line, err)
		}
		switch env.Type {
		case "export":
			continue // header line — nothing to load
		case "user":
			var u core.User
			if err := json.Unmarshal(env.Data, &u); err != nil {
				return stats, fmt.Errorf("line %d: user: %w", line, err)
			}
			if err := st.InsertUser(ctx, u); err != nil {
				return stats, fmt.Errorf("line %d: insert user %q: %w", line, u.ID, err)
			}
			stats.Users++
		case "card":
			var c core.Card
			if err := json.Unmarshal(env.Data, &c); err != nil {
				return stats, fmt.Errorf("line %d: card: %w", line, err)
			}
			if err := st.InsertCard(ctx, &c, nil); err != nil {
				return stats, fmt.Errorf("line %d: insert card %q: %w", line, c.ID, err)
			}
			stats.Cards++
			for _, cm := range c.Comments {
				if err := st.InsertComment(ctx, c.ID, cm); err != nil {
					return stats, fmt.Errorf("line %d: comment %q on %q: %w", line, cm.ID, c.ID, err)
				}
				stats.Comments++
			}
			for _, l := range c.Links {
				if err := st.InsertLink(ctx, c.ID, l); err != nil {
					return stats, fmt.Errorf("line %d: link on %q: %w", line, c.ID, err)
				}
				stats.Links++
			}
		case "event":
			var e core.Event
			if err := json.Unmarshal(env.Data, &e); err != nil {
				return stats, fmt.Errorf("line %d: event: %w", line, err)
			}
			if err := st.InsertEventRaw(ctx, &e); err != nil {
				return stats, fmt.Errorf("line %d: insert event: %w", line, err)
			}
			stats.Events++
		default:
			return stats, fmt.Errorf("line %d: unknown record type %q", line, env.Type)
		}
	}
	if err := sc.Err(); err != nil {
		return stats, fmt.Errorf("read input: %w", err)
	}
	return stats, nil
}
