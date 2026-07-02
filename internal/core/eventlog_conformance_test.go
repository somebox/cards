package core_test

import (
	"testing"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/eventlogtest"
)

// The in-memory fake honours the shared EventLog contract (Events seam 1a).
func TestMemEventLog_Conformance(t *testing.T) {
	eventlogtest.Conformance(t, func() core.EventLog { return eventlogtest.NewMem() })
}
