package clocktest

import (
	"testing"
	"time"
)

func TestFake_AfterFiresOnlyOnceDeadlinePasses(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := New(start)
	ch := f.After(time.Hour)

	select {
	case <-ch:
		t.Fatal("After fired before the clock advanced")
	default:
	}

	f.Advance(30 * time.Minute)
	select {
	case <-ch:
		t.Fatal("After fired before its full deadline")
	default:
	}

	f.Advance(30 * time.Minute)
	select {
	case got := <-ch:
		want := start.Add(time.Hour)
		if !got.Equal(want) {
			t.Errorf("fired with %v, want %v", got, want)
		}
	default:
		t.Fatal("After did not fire once the deadline passed")
	}
}

func TestFake_NonPositiveDurationFiresImmediately(t *testing.T) {
	f := New(time.Now())
	select {
	case <-f.After(0):
	default:
		t.Fatal("After(0) should fire immediately, matching time.After")
	}
}

func TestFake_MultipleWaitersFireIndependently(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := New(start)
	soon := f.After(time.Minute)
	later := f.After(time.Hour)

	f.Advance(time.Minute)
	select {
	case <-soon:
	default:
		t.Fatal("soon should have fired")
	}
	select {
	case <-later:
		t.Fatal("later should not have fired yet")
	default:
	}

	f.Advance(59 * time.Minute)
	select {
	case <-later:
	default:
		t.Fatal("later should have fired after the full hour")
	}
}
