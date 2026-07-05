// Package core — monitor.go
//
// The seam 3d deadline scheduler: a min-heap of upcoming temporal-condition
// deadlines, driven entirely by an injected Clock — no fixed tick, no real
// sleeps, and an empty heap parks the scheduler goroutine on its wake channel
// alone (zero wakeups). Deadlines are reconstructed from denormalized card
// state (e.g. status_since); nothing is persisted for the heap itself — only
// the fired-marker (condition_marks) is durable, so a temporal condition
// fires exactly once even across a restart.
//
// A monitor type is armed only while something is interested in it: a live
// bus subscriber matching the type, or the type being escalated via
// persist:true (a permanent consumer — its whole purpose is a durable
// record, so "lazy" doesn't apply to it). A serverless CLI process, with no
// subscribers and nothing persisted, never arms and the scheduler goroutine
// never wakes for real work.
//
// This file wires only the machinery; no condition type is registered here
// (see seam 3e, which wires status_timeout/card_idle in).
package core

import (
	"container/heap"
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ParseMonitorDuration parses a board monitor's duration string: Go's
// time.ParseDuration syntax, plus a "d" (days) suffix it lacks (e.g. "7d" ==
// 168h) — the natural unit for status_timeout/card_idle thresholds. Used by
// both config validation (at load) and the seam 3e rebuild callbacks.
func ParseMonitorDuration(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid days duration %q: %w", s, err)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(s)
}

// monitorDeadline is one pending temporal-condition check.
type monitorDeadline struct {
	at     time.Time
	cardID string
	cond   EventType
	key    string // fired-marker identity: (cardID, cond, key)
}

// monitorHeap is a container/heap min-heap ordered by monitorDeadline.at.
type monitorHeap []monitorDeadline

func (h monitorHeap) Len() int           { return len(h) }
func (h monitorHeap) Less(i, j int) bool { return h[i].at.Before(h[j].at) }
func (h monitorHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *monitorHeap) Push(x any)        { *h = append(*h, x.(monitorDeadline)) }
func (h *monitorHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// MonitorDeadline is one deadline a rebuild callback contributes: cardID
// should be checked at At, identified by key for the fired-marker.
type MonitorDeadline struct {
	At     time.Time
	CardID string
	Key    string
}

// MonitorVerify re-checks a deadline at fire time: if the condition it
// represents is still true, return the event to emit; if the card moved on
// (e.g. left the status before the deadline), return (nil, nil) — nothing
// fires, nothing is marked.
type MonitorVerify func(ctx context.Context, cardID, key string) (*Event, error)

// MonitorRebuild scans current card state for a condition type and returns
// every deadline that should be armed right now. Called when interest in the
// type is (re)gained; the scheduler filters out already-fired keys itself.
type MonitorRebuild func(ctx context.Context) ([]MonitorDeadline, error)

type monitorType struct {
	verify  MonitorVerify
	rebuild MonitorRebuild
}

// MonitorScheduler is the seam 3d tickless scheduler. Construct with
// NewMonitorScheduler, Register each temporal condition type, then Start.
// Stop releases the goroutine (tests call it; a production Service is
// process-lifetime and need not).
type MonitorScheduler struct {
	clock   Clock
	bus     *InProcBus
	emitter *Emitter
	store   Store

	mu    sync.Mutex
	heap  monitorHeap
	types map[EventType]monitorType
	armed map[EventType]bool

	wake chan struct{}
	stop chan struct{}
	done chan struct{}
}

// NewMonitorScheduler constructs a scheduler over the given clock, bus,
// emitter, and store. Call Register for each temporal condition type, then
// Start.
func NewMonitorScheduler(clock Clock, bus *InProcBus, emitter *Emitter, store Store) *MonitorScheduler {
	return &MonitorScheduler{
		clock: clock, bus: bus, emitter: emitter, store: store,
		types: map[EventType]monitorType{},
		armed: map[EventType]bool{},
		wake:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// Register wires a temporal condition type into the scheduler. Call before
// Start. Picks up any already-live interest immediately (e.g. persist:true
// configured before Start, or a subscriber that connected between
// construction and Register).
func (m *MonitorScheduler) Register(t EventType, verify MonitorVerify, rebuild MonitorRebuild) {
	m.mu.Lock()
	m.types[t] = monitorType{verify: verify, rebuild: rebuild}
	m.mu.Unlock()
	m.updateInterest(t)
}

// Start installs the bus subscription-change hook and launches the
// scheduler goroutine.
func (m *MonitorScheduler) Start() {
	m.bus.SetOnSubscriptionChange(m.onBusChange)
	go m.run()
}

// Stop signals the scheduler goroutine to exit and waits for it.
func (m *MonitorScheduler) Stop() {
	close(m.stop)
	<-m.done
}

// onBusChange re-derives interest for every registered type after a
// subscribe/unsubscribe/slow-consumer-drop.
func (m *MonitorScheduler) onBusChange() {
	m.mu.Lock()
	types := make([]EventType, 0, len(m.types))
	for t := range m.types {
		types = append(types, t)
	}
	m.mu.Unlock()
	for _, t := range types {
		m.updateInterest(t)
	}
}

// updateInterest arms (rebuilds deadlines) or disarms (drops them) a type on
// an interest crossing; a no-op if interest hasn't changed.
func (m *MonitorScheduler) updateInterest(t EventType) {
	interested := m.bus.HasSubscriberFor(t) || m.emitter.IsPersisted(t)
	m.mu.Lock()
	was, known := m.armed[t]
	if known && was == interested {
		m.mu.Unlock()
		return
	}
	m.armed[t] = interested
	m.mu.Unlock()
	if interested {
		m.rebuildType(context.Background(), t)
	} else {
		m.dropType(t)
	}
}

// rebuildType recomputes every currently-due deadline for t, skipping any
// key that has already fired (surviving a restart without re-firing).
func (m *MonitorScheduler) rebuildType(ctx context.Context, t EventType) {
	m.mu.Lock()
	mt, ok := m.types[t]
	m.mu.Unlock()
	if !ok {
		return
	}
	seeds, err := mt.rebuild(ctx)
	if err != nil {
		log.Printf("ERROR: monitor rebuild failed (type=%s): %v", t, err)
		return
	}
	fresh := make([]monitorDeadline, 0, len(seeds))
	for _, s := range seeds {
		fired, err := m.store.ConditionFired(ctx, s.CardID, t, s.Key)
		if err != nil {
			log.Printf("ERROR: monitor rebuild: check fired (type=%s card=%s): %v", t, s.CardID, err)
			continue
		}
		if fired {
			continue
		}
		fresh = append(fresh, monitorDeadline{at: s.At, cardID: s.CardID, cond: t, key: s.Key})
	}
	if len(fresh) == 0 {
		return
	}
	m.mu.Lock()
	for _, d := range fresh {
		heap.Push(&m.heap, d)
	}
	m.mu.Unlock()
	m.signalWake()
}

// dropType removes every pending deadline for t (interest lost).
func (m *MonitorScheduler) dropType(t EventType) {
	m.mu.Lock()
	kept := make(monitorHeap, 0, len(m.heap))
	for _, d := range m.heap {
		if d.cond != t {
			kept = append(kept, d)
		}
	}
	m.heap = kept
	heap.Init(&m.heap)
	m.mu.Unlock()
	m.signalWake()
}

// Arm schedules a single deadline for a live event — used when something
// just happened that should (re)arm a temporal condition (seam 3e's
// EventObserver), independent of a full rebuild. A no-op if t isn't armed
// (no interest): nothing is scheduled for a type nobody is watching.
//
// This can leave a stale entry in the heap when a card re-arms the same
// type again before the old deadline is due (e.g. every mutation re-arms
// card_idle) — harmless, since Verify re-checks identity at fire time and
// silently discards a deadline whose key no longer matches current state.
// Removing the superseded entry outright would need an indexed heap; for
// this scale, a silently-discarded stale fire is the simpler tradeoff.
func (m *MonitorScheduler) Arm(t EventType, cardID string, at time.Time, key string) {
	m.mu.Lock()
	armed := m.armed[t]
	if armed {
		heap.Push(&m.heap, monitorDeadline{at: at, cardID: cardID, cond: t, key: key})
	}
	m.mu.Unlock()
	if armed {
		m.signalWake()
	}
}

func (m *MonitorScheduler) signalWake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// run is the scheduler's one goroutine: waits until the earliest deadline
// (capped at 1h, per INTEGRATION.md's dropped-event safety net), or until
// woken by a heap/interest change, or until stopped. An empty heap waits on
// wake/stop alone — no clock.After call at all, so it costs zero wakeups.
func (m *MonitorScheduler) run() {
	defer close(m.done)
	for {
		m.mu.Lock()
		empty := len(m.heap) == 0
		var until time.Duration
		if !empty {
			until = m.heap[0].at.Sub(m.clock.Now())
			if until > time.Hour {
				until = time.Hour
			} else if until < 0 {
				until = 0
			}
		}
		m.mu.Unlock()

		if empty {
			select {
			case <-m.wake:
			case <-m.stop:
				return
			}
			continue
		}
		select {
		case now := <-m.clock.After(until):
			m.fireDue(now)
		case <-m.wake:
		case <-m.stop:
			return
		}
	}
}

// fireDue pops every deadline due at or before now and processes each.
func (m *MonitorScheduler) fireDue(now time.Time) {
	m.mu.Lock()
	var due []monitorDeadline
	for len(m.heap) > 0 && !m.heap[0].at.After(now) {
		due = append(due, heap.Pop(&m.heap).(monitorDeadline))
	}
	m.mu.Unlock()
	ctx := context.Background()
	for _, d := range due {
		m.fireOne(ctx, d)
	}
}

// fireOne re-verifies a due deadline and, if still true, records it fired
// (atomic check-and-set, exactly-once) and emits. The emission path splits on
// durability:
//
//   - persist:true — the fired-marker and the durable event append MUST commit
//     together, else a crash between them loses the event forever AND suppresses
//     re-fire (unrecoverable by any later cursor replay). The store commits both
//     in one transaction (MarkConditionFiredAndAppend); the Emitter stamps and,
//     on a first fire, dispatches post-commit. A failed atomic commit rolls both
//     back — the condition re-fires next evaluation — and is logged, never
//     propagated, so a best-effort escalation can't crash the scheduler loop.
//
//   - ephemeral — the fired-marker is the only durable write; the signal is
//     dispatch-only, so a mark-then-signal crash drops a signal that is, by
//     definition, for nobody. No atomicity needed.
//
// Crash re-fire semantics (persist:true): crash before commit -> nothing
// persisted, re-fires next evaluation; crash after commit -> mark + event both
// durable, no re-fire, live dispatch possibly missed but recoverable from the
// log (the outbox's job, EVENTS.md §8.5).
func (m *MonitorScheduler) fireOne(ctx context.Context, d monitorDeadline) {
	m.mu.Lock()
	mt, ok := m.types[d.cond]
	m.mu.Unlock()
	if !ok {
		return
	}
	ev, err := mt.verify(ctx, d.cardID, d.key)
	if err != nil {
		log.Printf("ERROR: monitor verify failed (type=%s card=%s): %v", d.cond, d.cardID, err)
		return
	}
	if ev == nil {
		return // no longer true — nothing fires, nothing is marked
	}

	if m.emitter.IsPersisted(d.cond) {
		firedAt := m.clock.Now()
		if _, err := m.emitter.EmitPersistedFire(ctx, ev, func(ev *Event) (bool, error) {
			return m.store.MarkConditionFiredAndAppend(ctx, d.cardID, d.cond, d.key, firedAt, ev)
		}); err != nil {
			log.Printf("ERROR: escalated condition mark+append failed (type=%s card=%s): %v", d.cond, d.cardID, err)
		}
		return
	}

	first, err := m.store.MarkConditionFired(ctx, d.cardID, d.cond, d.key, m.clock.Now())
	if err != nil {
		log.Printf("ERROR: monitor mark-fired failed (type=%s card=%s): %v", d.cond, d.cardID, err)
		return
	}
	if !first {
		return
	}
	m.emitter.Signal(ctx, ev)
}
