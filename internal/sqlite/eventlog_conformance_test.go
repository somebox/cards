package sqlite

import (
	"testing"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/eventlogtest"
)

// The SQLite store satisfies the EventLog seam boundary.
var _ core.EventLog = (*Store)(nil)

// The SQLite store honours the same EventLog contract as the fake (seam 1a):
// SQLite Store satisfies EventLog, and both pass the shared conformance suite.
func TestStore_EventLogConformance(t *testing.T) {
	eventlogtest.Conformance(t, func() core.EventLog {
		st, _ := testStore(t)
		return st
	})
}
