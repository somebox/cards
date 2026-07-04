package httpapi_test

// The seam 3e mandated integration test (EVENTS.md §12 Step 3): a real SSE
// client, an injected fake clock, no real sleeps for the business logic
// under test (only short bounded waits for the async pipeline — subscribe,
// mutation, scheduler goroutine — to settle, which is normal Go test
// synchronization, not the temporal logic itself).

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/clocktest"
	"github.com/somebox/cards/internal/httpapi"
	"github.com/somebox/cards/internal/sqlite"
)

// newTemporalServer builds a minimal workspace whose only board monitors
// max_time_in_status["review"], wired to a fake clock so the test drives
// time deterministically.
func newTemporalServer(t *testing.T) (*httptest.Server, *clocktest.Fake) {
	t.Helper()
	ws := &core.Workspace{
		ID:   "t",
		Name: "T",
		Columns: []core.Column{
			{ID: "todo", Name: "Todo"}, {ID: "review", Name: "Review"}, {ID: "done", Name: "Done"},
		},
		Settings: core.WorkspaceSettings{StrictFields: true, TagPolicy: "propose", DefaultUser: "u"},
	}
	types := map[string]*core.CardType{
		"task": {
			ID: "task", Name: "Task", SchemaVersion: 1,
			Fields: []core.FieldDef{{ID: "description", Type: core.FieldText, Required: true}},
		},
	}
	boards := map[string]*core.Board{
		"eng": {
			ID: "eng", Name: "Eng",
			Columns:     []string{"todo", "review", "done"},
			CardTypeIDs: []string{"task"},
			Monitors:    &core.BoardMonitors{MaxTimeInStatus: map[string]string{"review": "1h"}},
		},
	}
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
	t.Cleanup(svc.Close)
	srv, err := httpapi.New(svc, ws, types, boards, st)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	return ts, fake
}

// sseReader is a real SSE client read on a background goroutine, decoupled
// from the test's assertions by a channel so readOne can apply its own
// bounded timeout per call.
type sseReader struct {
	resp  *http.Response
	lines chan string
}

func newSSEReader(t *testing.T, url string) *sseReader {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("sse request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sse connect: %v", err)
	}
	r := &sseReader{resp: resp, lines: make(chan string, 16)}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				r.lines <- data
			}
		}
		close(r.lines)
	}()
	return r
}

// readOne waits up to timeout for the next data line; ok is false on
// timeout (nothing arrived) or stream close.
func (r *sseReader) readOne(timeout time.Duration) (line string, ok bool) {
	select {
	case line, ok = <-r.lines:
		return line, ok
	case <-time.After(timeout):
		return "", false
	}
}

func (r *sseReader) Close() { r.resp.Body.Close() }

// The full seam 3e contract, end to end: arms on a live SSE subscriber,
// fires exactly once at the deadline, no duplicate on a further advance,
// disarms on disconnect (a second card's breach never fires with nobody
// listening), and a fresh subscriber's rebuild fires that still-true breach
// exactly once on reconnect.
func TestTemporalIntegration_StatusTimeoutSSE(t *testing.T) {
	ts, fake := newTemporalServer(t)
	H := map[string]string{"X-Work-Cards-Actor": "u", "Content-Type": "application/json"}

	clientA := newSSEReader(t, ts.URL+"/v1/events/stream?types=status_timeout")
	time.Sleep(50 * time.Millisecond) // let the subscription land (arms the type)

	_, created := do(t, ts, "POST", "/v1/cards", map[string]any{
		"type_id": "task", "title": "A", "status": "todo",
		"fields": map[string]any{"description": "d"},
	}, H)
	idA := created["id"].(string)
	do(t, ts, "PATCH", "/v1/cards/"+idA, map[string]any{"version": 1, "status": "review"}, H)
	time.Sleep(50 * time.Millisecond) // let monitorObserver arm the deadline

	fake.Advance(59 * time.Minute)
	if _, ok := clientA.readOne(150 * time.Millisecond); ok {
		t.Fatal("status_timeout fired before its deadline")
	}
	fake.Advance(time.Minute)
	line, ok := clientA.readOne(2 * time.Second)
	if !ok || !strings.Contains(line, "status_timeout") || !strings.Contains(line, idA) {
		t.Fatalf("expected status_timeout for %s, got ok=%v line=%q", idA, ok, line)
	}

	// No duplicate on a further advance — the deadline already fired.
	fake.Advance(time.Hour)
	if l, ok := clientA.readOne(150 * time.Millisecond); ok {
		t.Fatalf("duplicate status_timeout fired: %q", l)
	}

	// Disconnect: the handler's ctx.Done() unwinds and unsubscribes, which
	// must disarm the type (no live consumer left).
	clientA.Close()
	time.Sleep(100 * time.Millisecond)

	_, created2 := do(t, ts, "POST", "/v1/cards", map[string]any{
		"type_id": "task", "title": "B", "status": "todo",
		"fields": map[string]any{"description": "d"},
	}, H)
	idB := created2["id"].(string)
	do(t, ts, "PATCH", "/v1/cards/"+idB, map[string]any{"version": 1, "status": "review"}, H)
	time.Sleep(50 * time.Millisecond)

	// Past B's deadline while nobody is subscribed — nothing to observe it
	// with, which is the point: disarmed means no live delivery is even
	// attempted. The real proof is the reconnect assertion below.
	fake.Advance(time.Hour)

	clientB := newSSEReader(t, ts.URL+"/v1/events/stream?types=status_timeout")
	defer clientB.Close()
	time.Sleep(100 * time.Millisecond) // let the new subscription arm + rebuild run

	lineB, okB := clientB.readOne(2 * time.Second)
	if !okB || !strings.Contains(lineB, "status_timeout") || !strings.Contains(lineB, idB) {
		t.Fatalf("expected reconnect rebuild to fire B's still-true breach, got ok=%v line=%q", okB, lineB)
	}
	// Exactly once: A already fired (and was marked) before the disconnect,
	// so the rebuild must not re-fire it — only B's fresh breach appears.
	if l, ok := clientB.readOne(150 * time.Millisecond); ok {
		t.Fatalf("unexpected extra frame after reconnect (A re-fired?): %q", l)
	}
}
