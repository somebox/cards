// Package eventlogtest provides an in-memory core.EventLog fake and a shared
// conformance suite, so every EventLog implementation (the SQLite store and the
// fake) is proven to honour the same Append/List/Page/Replay contract. This is
// the test seam extracted by Events seam 1a — call sites don't change; the
// interface boundary is pinned against both a real and a fake backend.
package eventlogtest

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/somebox/cards/internal/core"
)

// Mem is an append-only in-memory core.EventLog for tests: monotonic ids,
// insertion order preserved. It implements the log's own contract — filtering
// by card_id / type / actor / id, paging, and replay. The board-scope joins
// (EventQuery.Owner / CardTypeIn, which read the cards table) are store-only and
// intentionally unsupported here; the shared suite does not exercise them.
type Mem struct {
	mu   sync.Mutex
	evs  []core.Event
	next int64
}

// NewMem returns an empty in-memory EventLog.
func NewMem() *Mem { return &Mem{} }

var _ core.EventLog = (*Mem)(nil)

// Append assigns each event the next monotonic id (mutating e.ID, as the store
// does via LastInsertId) and stores a copy in insertion order.
func (m *Mem) Append(_ context.Context, evs ...*core.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range evs {
		if e == nil {
			continue
		}
		m.next++
		e.ID = m.next
		m.evs = append(m.evs, *e)
	}
	return nil
}

func matches(q core.EventQuery, e core.Event) bool {
	if q.CardID != "" && e.CardID != q.CardID {
		return false
	}
	if q.Actor != "" && e.Actor != q.Actor {
		return false
	}
	if e.ID <= q.AfterID {
		return false
	}
	if len(q.Types) > 0 && !slices.Contains(q.Types, string(e.Type)) {
		return false
	}
	return true
}

func (m *Mem) filtered(q core.EventQuery) []core.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []core.Event{}
	for _, e := range m.evs {
		if matches(q, e) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// List returns matching events ascending by id, capped at q.Limit when > 0.
func (m *Mem) List(_ context.Context, q core.EventQuery) ([]core.Event, error) {
	out := m.filtered(q)
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// Page is the cursor-paged catch-up feed: it fetches one past the limit to
// detect a further page and sets NextCursor to the last event id (mirrors the
// store's Page).
func (m *Mem) Page(_ context.Context, q core.EventQuery) (*core.Page[core.Event], error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	out := m.filtered(q)
	next := ""
	if len(out) > q.Limit {
		out = out[:q.Limit]
		next = strconv.FormatInt(out[len(out)-1].ID, 10)
	}
	return &core.Page[core.Event]{Items: out, NextCursor: next}, nil
}

// Replay streams events with id > fromID ascending into fn, stopping on the
// first error fn returns.
func (m *Mem) Replay(_ context.Context, fromID int64, fn func(*core.Event) error) error {
	out := m.filtered(core.EventQuery{AfterID: fromID})
	// Index, not range-value: callbacks that retain the pointer must each get
	// a distinct event, not aliases of one loop variable.
	for i := range out {
		if err := fn(&out[i]); err != nil {
			return err
		}
	}
	return nil
}

// errStop lets the replay-stops-on-error case assert propagation.
var errStop = errors.New("stop")

// Conformance runs the shared EventLog contract suite against a fresh log
// produced by newLog. Both the SQLite store and Mem must pass it identically.
func Conformance(t *testing.T, newLog func() core.EventLog) {
	t.Helper()
	ctx := context.Background()

	t.Run("append assigns monotonic ids and preserves order", func(t *testing.T) {
		log := newLog()
		a := &core.Event{CardID: "A", Type: core.EventCardCreated, Actor: "u1"}
		b := &core.Event{CardID: "B", Type: core.EventStatusChanged, Actor: "u2"}
		c := &core.Event{CardID: "A", Type: core.EventStatusChanged, Actor: "u1"}
		if err := log.Append(ctx, a, b, c); err != nil {
			t.Fatalf("append: %v", err)
		}
		if !(a.ID < b.ID && b.ID < c.ID) {
			t.Fatalf("ids not strictly increasing: %d, %d, %d", a.ID, b.ID, c.ID)
		}
		got, err := log.List(ctx, core.EventQuery{Limit: 100})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 3 || got[0].ID != a.ID || got[1].ID != b.ID || got[2].ID != c.ID {
			t.Fatalf("list not in append order: %+v", got)
		}
	})

	t.Run("list filters by card, type and actor", func(t *testing.T) {
		log := newLog()
		if err := log.Append(ctx,
			&core.Event{CardID: "A", Type: core.EventCardCreated, Actor: "u1"},
			&core.Event{CardID: "A", Type: core.EventStatusChanged, Actor: "u2"},
			&core.Event{CardID: "B", Type: core.EventStatusChanged, Actor: "u1"},
		); err != nil {
			t.Fatal(err)
		}
		if got, _ := log.List(ctx, core.EventQuery{CardID: "A", Limit: 100}); len(got) != 2 {
			t.Errorf("filter card=A: got %d want 2", len(got))
		}
		if got, _ := log.List(ctx, core.EventQuery{Types: []string{"status_changed"}, Limit: 100}); len(got) != 2 {
			t.Errorf("filter type=status_changed: got %d want 2", len(got))
		}
		if got, _ := log.List(ctx, core.EventQuery{Actor: "u1", Limit: 100}); len(got) != 2 {
			t.Errorf("filter actor=u1: got %d want 2", len(got))
		}
	})

	t.Run("page walks the whole log by cursor", func(t *testing.T) {
		log := newLog()
		for range 5 {
			if err := log.Append(ctx, &core.Event{CardID: "A", Type: core.EventStatusChanged}); err != nil {
				t.Fatal(err)
			}
		}
		seen, pages := 0, 0
		q := core.EventQuery{Limit: 2}
		for {
			pg, err := log.Page(ctx, q)
			if err != nil {
				t.Fatalf("page: %v", err)
			}
			seen += len(pg.Items)
			pages++
			if pg.NextCursor == "" {
				break
			}
			cur, err := strconv.ParseInt(pg.NextCursor, 10, 64)
			if err != nil {
				t.Fatalf("bad cursor %q: %v", pg.NextCursor, err)
			}
			q.AfterID = cur
			if pages > 10 {
				t.Fatal("cursor did not terminate")
			}
		}
		if seen != 5 {
			t.Errorf("paged %d events, want 5", seen)
		}
	})

	t.Run("replay streams id>fromID ascending and stops on error", func(t *testing.T) {
		log := newLog()
		a := &core.Event{CardID: "A", Type: core.EventStatusChanged}
		b := &core.Event{CardID: "A", Type: core.EventStatusChanged}
		c := &core.Event{CardID: "A", Type: core.EventStatusChanged}
		if err := log.Append(ctx, a, b, c); err != nil {
			t.Fatal(err)
		}
		var got []int64
		if err := log.Replay(ctx, a.ID, func(e *core.Event) error { got = append(got, e.ID); return nil }); err != nil {
			t.Fatalf("replay: %v", err)
		}
		if len(got) != 2 || got[0] != b.ID || got[1] != c.ID {
			t.Errorf("replay from %d got %v, want [%d %d]", a.ID, got, b.ID, c.ID)
		}
		count := 0
		if err := log.Replay(ctx, 0, func(e *core.Event) error { count++; return errStop }); !errors.Is(err, errStop) || count != 1 {
			t.Errorf("replay stop-on-error: count=%d err=%v", count, err)
		}
	})

	t.Run("replay callbacks receive distinct events, not one aliased pointer", func(t *testing.T) {
		log := newLog()
		a := &core.Event{CardID: "A", Type: core.EventStatusChanged}
		b := &core.Event{CardID: "B", Type: core.EventStatusChanged}
		if err := log.Append(ctx, a, b); err != nil {
			t.Fatal(err)
		}
		// Retain the pointers across callbacks; an implementation that hands
		// out the address of a shared loop variable fails here.
		var kept []*core.Event
		if err := log.Replay(ctx, 0, func(e *core.Event) error { kept = append(kept, e); return nil }); err != nil {
			t.Fatalf("replay: %v", err)
		}
		if len(kept) != 2 {
			t.Fatalf("replayed %d events, want 2", len(kept))
		}
		if kept[0].CardID != "A" || kept[1].CardID != "B" || kept[0].ID == kept[1].ID {
			t.Errorf("retained events aliased: [0]=%+v [1]=%+v", kept[0], kept[1])
		}
	})
}
