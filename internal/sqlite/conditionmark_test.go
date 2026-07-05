package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

// A persist:true condition's fired-marker and its durable event must commit
// together: after a first fire, the mark is recorded AND the event is in the
// log; a duplicate fire appends nothing.
func TestMarkConditionFiredAndAppendAtomic(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	insertUnowned(t, st, "c1")

	ev := core.StatusTimeout("c1", "todo", time.Now().UTC(), "1h")
	ev.At = time.Now().UTC()
	first, err := st.MarkConditionFiredAndAppend(ctx, "c1", core.EventStatusTimeout, "k1", ev.At, ev)
	if err != nil || !first {
		t.Fatalf("first mark+append: first=%v err=%v", first, err)
	}
	if ev.ID == 0 {
		t.Error("event not persisted (ID 0) on first fire")
	}

	fired, err := st.ConditionFired(ctx, "c1", core.EventStatusTimeout, "k1")
	if err != nil || !fired {
		t.Fatalf("condition not marked fired: fired=%v err=%v", fired, err)
	}
	evs, err := st.List(ctx, core.EventQuery{CardID: "c1", Types: []string{string(core.EventStatusTimeout)}, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d status_timeout events, want 1", len(evs))
	}

	// Second call, same key: not first, and no duplicate event appended.
	dup := core.StatusTimeout("c1", "todo", time.Now().UTC(), "1h")
	dup.At = time.Now().UTC()
	second, err := st.MarkConditionFiredAndAppend(ctx, "c1", core.EventStatusTimeout, "k1", dup.At, dup)
	if err != nil || second {
		t.Fatalf("duplicate fire: second=%v err=%v, want (false, nil)", second, err)
	}
	evs2, _ := st.List(ctx, core.EventQuery{CardID: "c1", Types: []string{string(core.EventStatusTimeout)}, Limit: 10})
	if len(evs2) != 1 {
		t.Errorf("duplicate fire appended an event: got %d, want 1", len(evs2))
	}
}

// The atomicity guarantee: if the durable append fails inside the transaction,
// the whole thing rolls back — the mark is NOT recorded, so the condition can
// re-fire. This is the crash window the old two-transaction path left open.
func TestMarkConditionFiredAndAppendRollsBackOnAppendFailure(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	insertUnowned(t, st, "c1")

	// Force the event INSERT to fail mid-transaction by removing the table.
	if _, err := st.db.Exec(`ALTER TABLE events RENAME TO events_broken`); err != nil {
		t.Fatalf("rename events: %v", err)
	}
	ev := core.StatusTimeout("c1", "todo", time.Now().UTC(), "1h")
	ev.At = time.Now().UTC()
	first, err := st.MarkConditionFiredAndAppend(ctx, "c1", core.EventStatusTimeout, "k1", ev.At, ev)
	if err == nil {
		t.Fatalf("expected append failure, got first=%v with nil error", first)
	}

	// The mark must have rolled back with the failed append (condition_marks is
	// a separate table, unaffected by the rename), so the condition can re-fire.
	fired, ferr := st.ConditionFired(ctx, "c1", core.EventStatusTimeout, "k1")
	if ferr != nil {
		t.Fatalf("condition fired: %v", ferr)
	}
	if fired {
		t.Error("mark persisted despite append failure — the mark-then-append crash window is still open")
	}
}
