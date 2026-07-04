package core

// White-box (package core, not core_test) since claimWithRetry is
// unexported — tested here with an injected closure rather than a real
// store, isolating the retry loop from ClaimAtomic's actual concurrency.

import (
	"testing"
)

func TestClaimWithRetry_SucceedsAfterRaces(t *testing.T) {
	calls := 0
	c, evs, err := claimWithRetry(3, func() (*Card, []*Event, error) {
		calls++
		if calls < 3 {
			return nil, nil, ErrClaimRaced
		}
		return &Card{ID: "c1"}, []*Event{{CardID: "c1"}}, nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if c == nil || c.ID != "c1" {
		t.Fatalf("card = %+v, want c1", c)
	}
	if len(evs) != 1 {
		t.Fatalf("evs = %v, want 1 event", evs)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want exactly 3 (raced twice, succeeded on the third)", calls)
	}
}

func TestClaimWithRetry_ExhaustsAttemptsReturnsNilNotError(t *testing.T) {
	calls := 0
	c, evs, err := claimWithRetry(3, func() (*Card, []*Event, error) {
		calls++
		return nil, nil, ErrClaimRaced
	})
	if err != nil {
		t.Fatalf("err = %v, want nil (raced-out is not an error)", err)
	}
	if c != nil || evs != nil {
		t.Errorf("got (%+v, %v), want (nil, nil)", c, evs)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want exactly 3 (bounded attempts)", calls)
	}
}

func TestClaimWithRetry_EmptyPoolReturnsImmediately(t *testing.T) {
	calls := 0
	c, evs, err := claimWithRetry(3, func() (*Card, []*Event, error) {
		calls++
		return nil, nil, nil // nothing matched — not a race
	})
	if err != nil || c != nil || evs != nil {
		t.Fatalf("got (%+v, %v, %v), want (nil, nil, nil)", c, evs, err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on a genuinely empty pool)", calls)
	}
}

func TestClaimWithRetry_RealErrorPropagatesImmediately(t *testing.T) {
	wantErr := ErrNotFound
	calls := 0
	_, _, err := claimWithRetry(3, func() (*Card, []*Event, error) {
		calls++
		return nil, nil, wantErr
	})
	if err != wantErr {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (a real error must not be retried)", calls)
	}
}
