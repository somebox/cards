package core_test

import (
	"testing"

	"github.com/foz/work-cards/internal/core"
)

func TestEventBus_FilteredFanOut(t *testing.T) {
	bus := core.NewBus()
	subAll := bus.Subscribe(core.EventFilter{}, 4)
	subStatus := bus.Subscribe(core.EventFilter{Types: []string{"status_changed"}}, 4)
	subCardA := bus.Subscribe(core.EventFilter{CardID: "A"}, 4)

	bus.Publish(&core.Event{CardID: "A", Type: core.EventCardCreated})
	bus.Publish(&core.Event{CardID: "A", Type: core.EventStatusChanged})
	bus.Publish(&core.Event{CardID: "B", Type: core.EventStatusChanged})

	gotAll := drain(subAll.Ch)
	if len(gotAll) != 3 {
		t.Errorf("all subscriber got %d, want 3", len(gotAll))
	}
	gotStatus := drain(subStatus.Ch)
	if len(gotStatus) != 2 {
		t.Errorf("status subscriber got %d, want 2", len(gotStatus))
	}
	gotA := drain(subCardA.Ch)
	if len(gotA) != 2 {
		t.Errorf("card-A subscriber got %d, want 2", len(gotA))
	}
}

func TestEventBus_DropsSlowSubscriber(t *testing.T) {
	bus := core.NewBus()
	// Buffer 1; we'll publish several without reading.
	sub := bus.Subscribe(core.EventFilter{}, 1)
	for i := 0; i < 5; i++ {
		bus.Publish(&core.Event{CardID: "A", Type: core.EventCardCreated})
	}
	// The slow subscriber should have been dropped (channel closed after
	// draining the buffered event).
	drain(sub.Ch)
	if _, ok := <-sub.Ch; ok {
		t.Error("slow subscriber channel should be closed after drop")
	}
	if bus.SubscriberCount() != 0 {
		t.Errorf("dropped subscriber should be removed, count=%d", bus.SubscriberCount())
	}
}

func drain(ch <-chan *core.Event) []*core.Event {
	out := []*core.Event{}
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		default:
			return out
		}
	}
}
