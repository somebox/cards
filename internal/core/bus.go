// Package core — bus.go
//
// In-process event bus: subscribers register a filter + callback; the service
// publishes events on commit. Used by SSE streaming (§3/§11) and the hook
// supervisor (EXTENSIONS.md). Slow clients are dropped, never block a write.
package core

import (
	"sync"
)

// EventFilter selects which events a subscriber receives. Empty fields match
// all. Types is an OR over event type strings.
type EventFilter struct {
	CardID  string
	BoardID string
	Types   []string
	Actor   string // events caused by this actor
}

// Matches reports whether the filter accepts the event. BoardID matching
// requires the event to carry board_ids (the service attaches them on
// publish for board-scoped events).
func (f EventFilter) Matches(e *Event) bool {
	if f.CardID != "" && e.CardID != f.CardID {
		return false
	}
	if f.BoardID != "" && e.BoardID != f.BoardID {
		return false
	}
	if f.Actor != "" && e.Actor != f.Actor {
		return false
	}
	if len(f.Types) > 0 {
		ok := false
		for _, t := range f.Types {
			if string(e.Type) == t {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Subscriber is a registered bus listener.
type Subscriber struct {
	ID     int64
	Filter EventFilter
	Ch     chan *Event
}

// Bus is the live fan-out surface: best-effort delivery to current subscribers.
// It is an interface so tests can substitute a recording/fake bus; the
// production implementation is InProcBus.
type Bus interface {
	// Subscribe registers a filter + buffered channel and returns the Subscriber.
	Subscribe(filter EventFilter, buf int) *Subscriber
	// Unsubscribe removes and closes a subscriber's channel.
	Unsubscribe(id int64)
	// Publish fans out an event to all matching subscribers, non-blocking.
	Publish(e *Event)
}

// InProcBus fans out published events to filtered subscribers. It is safe for
// concurrent use. The bus is bounded: each subscriber has a buffered channel;
// a full buffer means the subscriber is dropped (its channel is closed and
// removed) so a slow client never blocks a writer.
type InProcBus struct {
	mu          sync.RWMutex
	nextID      int64
	subscribers map[int64]*Subscriber
}

// NewBus constructs an in-process event bus.
func NewBus() *InProcBus {
	return &InProcBus{subscribers: map[int64]*Subscriber{}}
}

// Subscribe registers a filter + buffered channel of the given size and
// returns the Subscriber. Cancel via Unsubscribe(id).
func (b *InProcBus) Subscribe(filter EventFilter, buf int) *Subscriber {
	if buf <= 0 {
		buf = 16
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	sub := &Subscriber{ID: b.nextID, Filter: filter, Ch: make(chan *Event, buf)}
	b.subscribers[sub.ID] = sub
	return sub
}

// Unsubscribe removes and closes a subscriber's channel.
func (b *InProcBus) Unsubscribe(id int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sub, ok := b.subscribers[id]; ok {
		delete(b.subscribers, id)
		close(sub.Ch)
	}
}

// Publish fans out an event to all matching subscribers. Non-blocking: a full
// subscriber channel causes that subscriber to be dropped (closed + removed).
func (b *InProcBus) Publish(e *Event) {
	b.mu.RLock()
	subs := make([]*Subscriber, 0, len(b.subscribers))
	for _, s := range b.subscribers {
		subs = append(subs, s)
	}
	b.mu.RUnlock()

	for _, s := range subs {
		if !s.Filter.Matches(e) {
			continue
		}
		select {
		case s.Ch <- e:
		default:
			// Drop slow subscriber so the writer isn't blocked.
			b.mu.Lock()
			if _, ok := b.subscribers[s.ID]; ok {
				delete(b.subscribers, s.ID)
				close(s.Ch)
			}
			b.mu.Unlock()
		}
	}
}

// SubscriberCount returns the current number of subscribers (for diagnostics).
func (b *InProcBus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}
