package httpapi

import (
	"bytes"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/core"
)

func TestWriteSSEEvent_OmitsCursorForEphemeralEvent(t *testing.T) {
	var ephemeral bytes.Buffer
	writeSSEEvent(&ephemeral, &core.Event{Type: core.EventWIPExceeded})
	if strings.HasPrefix(ephemeral.String(), "id:") {
		t.Fatalf("ephemeral frame must not replace the durable replay cursor:\n%s", ephemeral.String())
	}
	if !strings.HasPrefix(ephemeral.String(), "event: wip_exceeded\n") {
		t.Fatalf("ephemeral frame missing event line:\n%s", ephemeral.String())
	}

	var durable bytes.Buffer
	writeSSEEvent(&durable, &core.Event{ID: 42, Type: core.EventStatusChanged})
	if !strings.HasPrefix(durable.String(), "id: 42\nevent: status_changed\n") {
		t.Fatalf("durable frame missing replay cursor:\n%s", durable.String())
	}
}
