package core_test

// Events seam 3d: the deadline scheduler is pure machinery this slice — no
// real condition type is registered here (that's seam 3e). These tests
// drive core.MonitorScheduler directly against a synthetic condition type to
// prove the heap/clock/marker/refcounting mechanics independent of any
// user-visible behavior.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/clocktest"
	"github.com/somebox/cards/internal/core/eventlogtest"
	"github.com/somebox/cards/internal/sqlite"
)

const syntheticCond core.EventType = "test_synthetic_condition"

// newMonitorTestService builds a service (for its bus/emitter/store) plus a
// fake clock, all wired together as a real Service would be.
func newMonitorTestService(t *testing.T) (*core.Service, *core.InProcBus, *sqlite.Store, *clocktest.Fake) {
	t.Helper()
	ws, types, boards := testConfig()
	st, err := sqlite.Open(":memory:", ws)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.InsertUser(context.Background(), core.User{ID: "u", Kind: "human"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	fake := clocktest.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	svc := core.NewService(ws, types, boards, st, core.WithClock(fake))
	bus, ok := svc.Bus().(*core.InProcBus)
	if !ok {
		t.Fatal("Service.Bus() is not the concrete *InProcBus")
	}
	return svc, bus, st, fake
}

// syntheticEvent is the event a synthetic verify function returns.
func syntheticEvent(cardID, key string) *core.Event {
	return core.CardEvent(cardID, syntheticCond, map[string]any{"key": key})
}

func alwaysTrueVerify() core.MonitorVerify {
	return func(ctx context.Context, cardID, key string) (*core.Event, error) {
		return syntheticEvent(cardID, key), nil
	}
}

func fixedRebuild(deadlines []core.MonitorDeadline) core.MonitorRebuild {
	return func(ctx context.Context) ([]core.MonitorDeadline, error) {
		return deadlines, nil
	}
}

// waitForRecorded polls (real wall-clock, bounded) for the recorder to reach
// at least n events — synchronizing the test with the scheduler's own
// background goroutine, which real production code never sleeps in.
func waitForRecorded(t *testing.T, rec *eventlogtest.Recorder, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rec.Len() >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d recorded events, got %d: %+v", n, rec.Len(), rec.Events())
}

// assertNoneRecordedSoon gives any (incorrect) background activity a brief
// window to manifest, then asserts the recorder is still at exactly want.
func assertNoneRecordedSoon(t *testing.T, rec *eventlogtest.Recorder, want int) {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	if got := rec.Len(); got != want {
		t.Fatalf("recorded %d events, want %d: %+v", got, want, rec.Events())
	}
}

// A registered type with no subscriber and no persist:true never arms: the
// heap stays empty, so the scheduler goroutine never calls clock.After at
// all — zero wakeups, proven by a counting Clock.
func TestMonitorScheduler_EmptyHeapZeroWakeups(t *testing.T) {
	svc, bus, st, fake := newMonitorTestService(t)
	counting := &countingClock{Fake: fake}
	sched := core.NewMonitorScheduler(counting, bus, svc.Emitter(), st)

	sched.Register(syntheticCond, alwaysTrueVerify(), fixedRebuild([]core.MonitorDeadline{
		{At: fake.Now().Add(time.Hour), CardID: "c1", Key: "k1"},
	}))
	sched.Start()
	defer sched.Stop()

	fake.Advance(2 * time.Hour)
	fake.Advance(2 * time.Hour)
	time.Sleep(20 * time.Millisecond) // let the (idle) goroutine settle

	if counting.calls() != 0 {
		t.Errorf("clock.After called %d times with no armed deadlines, want 0", counting.calls())
	}
}

// Registering a type that IS armed (a live subscriber present) schedules its
// rebuilt deadline, which fires on the fake clock advancing past it — and
// clock.After is called (heap non-empty), contrasting with the zero-wakeup
// case above.
func TestMonitorScheduler_ArmedDeadlineFires(t *testing.T) {
	svc, bus, st, fake := newMonitorTestService(t)
	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)

	sub := bus.Subscribe(core.EventFilter{Types: []string{string(syntheticCond)}}, 8)
	defer bus.Unsubscribe(sub.ID)

	sched := core.NewMonitorScheduler(fake, bus, svc.Emitter(), st)
	sched.Register(syntheticCond, alwaysTrueVerify(), fixedRebuild([]core.MonitorDeadline{
		{At: fake.Now().Add(time.Hour), CardID: "c1", Key: "k1"},
	}))
	sched.Start()
	defer sched.Stop()

	assertNoneRecordedSoon(t, rec, 0)
	fake.Advance(time.Hour)
	waitForRecorded(t, rec, 1)

	if rec.Events()[0].Type != syntheticCond || rec.Events()[0].CardID != "c1" {
		t.Errorf("fired event = %+v, want synthetic condition for c1", rec.Events()[0])
	}

	// No duplicate on a further advance — the deadline already fired and was
	// popped from the heap.
	fake.Advance(time.Hour)
	assertNoneRecordedSoon(t, rec, 1)
}

// A verify that reports the condition no longer true (card moved on before
// the deadline) fires nothing and marks nothing.
func TestMonitorScheduler_VerifyFalseFiresNothing(t *testing.T) {
	svc, bus, st, fake := newMonitorTestService(t)
	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)
	sub := bus.Subscribe(core.EventFilter{Types: []string{string(syntheticCond)}}, 8)
	defer bus.Unsubscribe(sub.ID)

	noLongerTrue := func(ctx context.Context, cardID, key string) (*core.Event, error) { return nil, nil }
	sched := core.NewMonitorScheduler(fake, bus, svc.Emitter(), st)
	sched.Register(syntheticCond, noLongerTrue, fixedRebuild([]core.MonitorDeadline{
		{At: fake.Now().Add(time.Hour), CardID: "c1", Key: "k1"},
	}))
	sched.Start()
	defer sched.Stop()

	fake.Advance(time.Hour)
	assertNoneRecordedSoon(t, rec, 0)

	if fired, err := st.ConditionFired(context.Background(), "c1", syntheticCond, "k1"); err != nil || fired {
		t.Errorf("ConditionFired = %v, %v, want false (never fired)", fired, err)
	}
}

// Exactly-once: concurrent MarkConditionFired calls for the same
// (card, type, key) — the real-world concurrency risk (independent of the
// scheduler's single goroutine, which never races itself) — only one call
// reports first=true, via the DB's atomic INSERT OR IGNORE.
func TestMonitorScheduler_MarkConditionFiredExactlyOnce(t *testing.T) {
	_, _, st, _ := newMonitorTestService(t)
	ctx := context.Background()
	const n = 20
	firsts := make(chan bool, n)
	start := make(chan struct{})
	for range n {
		go func() {
			<-start
			first, err := st.MarkConditionFired(ctx, "c1", syntheticCond, "k1", time.Now().UTC())
			if err != nil {
				t.Errorf("mark: %v", err)
				firsts <- false
				return
			}
			firsts <- first
		}()
	}
	close(start)
	trueCount := 0
	for range n {
		if <-firsts {
			trueCount++
		}
	}
	if trueCount != 1 {
		t.Errorf("first=true count = %d, want exactly 1 (of %d concurrent marks)", trueCount, n)
	}
}

// A restarted scheduler (fresh instance, same store) rebuilds from current
// state but skips any key already marked fired — no duplicate event.
func TestMonitorScheduler_RestartDoesNotRefire(t *testing.T) {
	svc, bus, st, fake := newMonitorTestService(t)
	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)
	sub := bus.Subscribe(core.EventFilter{Types: []string{string(syntheticCond)}}, 8)
	defer bus.Unsubscribe(sub.ID)

	deadlines := []core.MonitorDeadline{{At: fake.Now().Add(time.Hour), CardID: "c1", Key: "k1"}}

	sched1 := core.NewMonitorScheduler(fake, bus, svc.Emitter(), st)
	sched1.Register(syntheticCond, alwaysTrueVerify(), fixedRebuild(deadlines))
	sched1.Start()
	fake.Advance(time.Hour)
	waitForRecorded(t, rec, 1)
	sched1.Stop()

	// "Restart": a brand-new scheduler instance over the same store/emitter/
	// bus, registering the identical (still-due, per rebuild) deadline.
	sched2 := core.NewMonitorScheduler(fake, bus, svc.Emitter(), st)
	sched2.Register(syntheticCond, alwaysTrueVerify(), fixedRebuild(deadlines))
	sched2.Start()
	defer sched2.Stop()

	fake.Advance(time.Hour)
	assertNoneRecordedSoon(t, rec, 1) // still 1, not 2 — rebuild skipped the fired key
}

// Interest arms on Subscribe and disarms on Unsubscribe — a deadline
// scheduled while armed is dropped once the last matching subscriber
// disconnects, and does not fire even once its time comes.
func TestMonitorScheduler_ArmDisarmOnSubscribeUnsubscribe(t *testing.T) {
	svc, bus, st, fake := newMonitorTestService(t)
	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)

	sched := core.NewMonitorScheduler(fake, bus, svc.Emitter(), st)
	sched.Register(syntheticCond, alwaysTrueVerify(), fixedRebuild([]core.MonitorDeadline{
		{At: fake.Now().Add(time.Hour), CardID: "c1", Key: "k1"},
	}))
	sched.Start()
	defer sched.Stop()

	// Registered but nobody is listening yet — disarmed.
	assertNoneRecordedSoon(t, rec, 0)

	sub := bus.Subscribe(core.EventFilter{Types: []string{string(syntheticCond)}}, 8)
	// Give the subscription-change hook a moment to arm + rebuild.
	time.Sleep(20 * time.Millisecond)
	bus.Unsubscribe(sub.ID)
	time.Sleep(20 * time.Millisecond)

	fake.Advance(time.Hour)
	assertNoneRecordedSoon(t, rec, 0) // disarmed before it could fire
}

// A subscriber dropped for being slow (never called Unsubscribe) must still
// disarm the type — InProcBus's drop path notifies the scheduler exactly
// like an explicit Unsubscribe.
func TestMonitorScheduler_DisarmOnSlowConsumerDrop(t *testing.T) {
	svc, bus, st, fake := newMonitorTestService(t)
	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)

	sched := core.NewMonitorScheduler(fake, bus, svc.Emitter(), st)
	sched.Register(syntheticCond, alwaysTrueVerify(), fixedRebuild([]core.MonitorDeadline{
		{At: fake.Now().Add(time.Hour), CardID: "c1", Key: "k1"},
	}))
	sched.Start()
	defer sched.Stop()

	// Buffer size 1: the first Publish fills it, the second overflows and
	// drops the subscriber (InProcBus's slow-consumer path) without it ever
	// reading — never calling Unsubscribe itself.
	sub := bus.Subscribe(core.EventFilter{}, 1)
	time.Sleep(20 * time.Millisecond) // let Register's arm land
	bus.Publish(core.CardEvent("filler1", "filler", nil))
	bus.Publish(core.CardEvent("filler2", "filler", nil)) // overflow -> dropped
	time.Sleep(20 * time.Millisecond)
	if bus.SubscriberCount() != 0 {
		t.Fatalf("subscriber should have been dropped, count = %d", bus.SubscriberCount())
	}
	_ = sub

	fake.Advance(time.Hour)
	assertNoneRecordedSoon(t, rec, 0) // dropped subscriber => disarmed => no fire
}

// persist:true is a permanent consumer: a type escalated via
// PersistConditions arms and fires even with zero bus subscribers ever.
func TestMonitorScheduler_PersistTrueArmsWithNoSubscribers(t *testing.T) {
	svc, bus, st, fake := newMonitorTestService(t)
	rec := &eventlogtest.Recorder{}
	svc.Emitter().Observe(rec.Record)
	svc.Emitter().PersistConditions(syntheticCond)

	sched := core.NewMonitorScheduler(fake, bus, svc.Emitter(), st)
	sched.Register(syntheticCond, alwaysTrueVerify(), fixedRebuild([]core.MonitorDeadline{
		{At: fake.Now().Add(time.Hour), CardID: "c1", Key: "k1"},
	}))
	sched.Start()
	defer sched.Stop()

	if bus.SubscriberCount() != 0 {
		t.Fatalf("test setup: expected zero subscribers, got %d", bus.SubscriberCount())
	}
	fake.Advance(time.Hour)
	waitForRecorded(t, rec, 1)
}

// countingClock wraps clocktest.Fake, counting After() calls — the proof
// vehicle for "empty heap = zero wakeups" (production code never sleeps;
// this is purely a test instrument). n is accessed from both the scheduler's
// goroutine (After) and the test goroutine (calls), hence atomic.
type countingClock struct {
	*clocktest.Fake
	n atomic.Int64
}

func (c *countingClock) After(d time.Duration) <-chan time.Time {
	c.n.Add(1)
	return c.Fake.After(d)
}

func (c *countingClock) calls() int64 { return c.n.Load() }
