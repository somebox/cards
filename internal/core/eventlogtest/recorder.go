package eventlogtest

import (
	"sync"

	"github.com/somebox/cards/internal/core"
)

// Recorder captures dispatched events for assertions (Events seam 1f). Register
// it on the emitter with svc.Emitter().Observe(rec.Record); every committed
// event then lands in the recorder. Concurrency-safe, so it also survives the
// per-observer panic isolation in dispatchCommitted alongside other observers.
//
// It replaces ad hoc bus-subscriber scaffolding in tests that only need to
// assert "what did this mutation emit" — no channels, no draining, no filter.
type Recorder struct {
	mu  sync.Mutex
	evs []core.Event
}

// Record is an EventObserver: pass it to Emitter.Observe.
func (r *Recorder) Record(e *core.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evs = append(r.evs, *e)
}

// Events returns a copy of the events recorded so far, in dispatch order.
func (r *Recorder) Events() []core.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.Event(nil), r.evs...)
}

// Types returns the recorded event types in dispatch order — the common
// assertion shape.
func (r *Recorder) Types() []core.EventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	ts := make([]core.EventType, len(r.evs))
	for i, e := range r.evs {
		ts[i] = e.Type
	}
	return ts
}

// Len reports how many events have been recorded.
func (r *Recorder) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.evs)
}
