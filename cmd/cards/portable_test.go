package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
	"github.com/somebox/cards/internal/sqlite/sqlitetest"
	"github.com/somebox/cards/internal/starter"
)

// testWorkspace + newStore mirror internal/sqlite's test setup: an in-memory DB
// with a minimal workspace so we can exercise export/import without disk or a
// definitions/ directory.
func newStore(t *testing.T) (*sqlite.Store, *core.Workspace) {
	t.Helper()
	ws := &core.Workspace{
		ID:   "demo",
		Name: "Demo workspace",
		Columns: []core.Column{
			{ID: "todo", Name: "To Do"}, {ID: "in_progress", Name: "In Progress"}, {ID: "done", Name: "Done"},
		},
		Settings: core.WorkspaceSettings{StrictFields: true, TagPolicy: "propose", DefaultUser: "u"},
	}
	st := sqlitetest.Open(t, ws, 1)
	return st, ws
}

// seed populates a store with a representative slice of state: two users, two
// cards (one with a repeating field, a comment, and an outgoing link), plus a
// couple of events. Returned so tests can assert against the originals.
func seedStore(t *testing.T, st *sqlite.Store) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)

	for _, u := range []core.User{
		{ID: "pi", DisplayName: "Pi", Kind: "agent", CreatedAt: at},
		{ID: "foz", DisplayName: "Foz", Kind: "human", CreatedAt: at},
	} {
		if err := st.InsertUser(ctx, u); err != nil {
			t.Fatalf("seed user %s: %v", u.ID, err)
		}
	}

	c1 := &core.Card{
		ID: "card_a", WorkspaceID: "demo", TypeID: "programming-task", SchemaVersion: 1,
		Title: "Finish import command", Status: "in_progress", Owner: "pi",
		Tags: []string{"feature"},
		Fields: map[string]any{
			"branch": "feat/import",
			"work_log": []any{
				map[string]any{"entry_id": "ent_1", "author": "pi", "notes": "wrote importJSONL"},
			},
		},
		Version: 3, CreatedAt: at, UpdatedAt: at.Add(time.Hour), CreatedBy: "foz",
	}
	if err := st.InsertCard(ctx, c1, &core.Event{CardID: "card_a", Type: core.EventCardCreated, Actor: "foz", At: at}); err != nil {
		t.Fatalf("seed card_a: %v", err)
	}
	if err := st.InsertComment(ctx, "card_a", core.Comment{ID: "cm_1", Author: "pi", Body: "import is the remaining half", CreatedAt: at.Add(30 * time.Minute), EditedAt: at.Add(45 * time.Minute)}); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	c2 := &core.Card{
		ID: "card_b", WorkspaceID: "demo", TypeID: "programming-task", SchemaVersion: 1,
		Title: "Export JSONL", Status: "done", Fields: map[string]any{"branch": "feat/export"},
		Version: 1, CreatedAt: at, UpdatedAt: at, CreatedBy: "pi",
	}
	if err := st.InsertCard(ctx, c2, nil); err != nil {
		t.Fatalf("seed card_b: %v", err)
	}
	// card_a depends-on card_b.
	if err := st.InsertLink(ctx, "card_a", core.Link{TypeID: "depends-on", Target: "card_b", CreatedBy: "pi", CreatedAt: at}); err != nil {
		t.Fatalf("seed link: %v", err)
	}
	if err := st.InsertEventRaw(ctx, &core.Event{CardID: "card_a", Type: core.EventStatusChanged, Actor: "pi", At: at.Add(time.Hour), Diff: map[string]any{"before": "todo", "after": "in_progress"}}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

// TestExportIsCompleteBeyondPageCap guards the export data-loss bug: a
// workspace with more cards than ListCards' page ceiling must export ALL of
// them (export cursor-paginates), not silently truncate to one page.
func TestExportIsCompleteBeyondPageCap(t *testing.T) {
	ctx := context.Background()
	src, ws := newStore(t)
	const total = 510 // just past the 500 page ceiling → two pages
	base := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	for i := range total {
		id := "card_" + strconv.Itoa(i)
		if err := src.InsertCard(ctx, &core.Card{
			ID: id, WorkspaceID: "demo", TypeID: "task", SchemaVersion: 1,
			Title: id, Status: "todo", Fields: map[string]any{}, Version: 1,
			CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Millisecond), CreatedBy: "u",
		}, nil); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	var buf bytes.Buffer
	exp, err := exportJSONL(ctx, src, &buf, ws, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exp.Cards != total {
		t.Fatalf("export tallied %d cards, want %d", exp.Cards, total)
	}
	if got := strings.Count(buf.String(), `"type":"card"`); got != total {
		t.Errorf("export wrote %d card lines, want %d", got, total)
	}

	// Round-trips completely into a fresh store.
	dst, _ := newStore(t)
	imp, err := importJSONL(ctx, dst, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imp.Cards != total {
		t.Errorf("import restored %d cards, want %d", imp.Cards, total)
	}
}

// A state-only export omits the mutation log (keeping only card_deleted
// tombstones, of which the seed has none) but still round-trips card state;
// importing it reconstructs no mutation history.
func TestExportStateOnly(t *testing.T) {
	ctx := context.Background()
	src, ws := newStore(t)
	seedStore(t, src)

	var buf bytes.Buffer
	exp, err := exportJSONL(ctx, src, &buf, ws, true)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exp.Events != 0 {
		t.Errorf("state-only export tallied %d events, want 0", exp.Events)
	}
	if strings.Contains(buf.String(), `"type":"event"`) {
		t.Errorf("state-only export wrote event lines")
	}
	if exp.Cards != 2 {
		t.Errorf("state-only export tallied %d cards, want 2", exp.Cards)
	}

	dst, _ := newStore(t)
	imp, err := importJSONL(ctx, dst, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imp.Cards != 2 || imp.Events != 0 {
		t.Errorf("import restored %+v, want 2 cards / 0 events", imp)
	}
}

// TestExportImportRoundTrip is the core fidelity guarantee: export a populated
// store, import the bytes into a fresh store, and assert every entity matches —
// ids, versions, timestamps, fields, comments, links, and the event log.
func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src, ws := newStore(t)
	seedStore(t, src)

	var buf bytes.Buffer
	exp, err := exportJSONL(ctx, src, &buf, ws, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	dst, _ := newStore(t)
	imp, err := importJSONL(ctx, dst, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if exp != imp {
		t.Fatalf("stats differ: export=%+v import=%+v", exp, imp)
	}
	if exp.Cards != 2 || exp.Users != 2 || exp.Comments != 1 || exp.Links != 1 || exp.Events != 2 {
		t.Fatalf("unexpected counts: %+v", exp)
	}

	// Card identity, version, timestamps, owner, tags preserved verbatim.
	got, err := dst.GetCard(ctx, "card_a")
	if err != nil {
		t.Fatalf("get card_a: %v", err)
	}
	if got.Title != "Finish import command" || got.Status != "in_progress" || got.Owner != "pi" {
		t.Errorf("card_a envelope wrong: %+v", got)
	}
	if got.Version != 3 {
		t.Errorf("version not preserved: got %d want 3", got.Version)
	}
	if !got.UpdatedAt.Equal(time.Date(2026, 6, 26, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("updated_at not preserved: %v", got.UpdatedAt)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "feature" {
		t.Errorf("tags not preserved: %v", got.Tags)
	}

	// Repeating field entry survived (including its stable entry_id).
	fields, _ := got.Fields.(map[string]any)
	wl, ok := fields["work_log"].([]any)
	if !ok || len(wl) != 1 {
		t.Fatalf("work_log not preserved: %#v", fields["work_log"])
	}
	if entry := wl[0].(map[string]any); entry["entry_id"] != "ent_1" {
		t.Errorf("entry_id not preserved: %v", entry["entry_id"])
	}

	// Comments and links restored — including edited_at (regression: import
	// used to hardcode edited_at to NULL, dropping comment edit timestamps).
	if len(got.Comments) != 1 || got.Comments[0].ID != "cm_1" {
		t.Errorf("comment not restored: %+v", got.Comments)
	}
	if len(got.Comments) == 1 && !got.Comments[0].EditedAt.Equal(time.Date(2026, 6, 26, 12, 45, 0, 0, time.UTC)) {
		t.Errorf("comment edited_at not preserved through round-trip: %v", got.Comments[0].EditedAt)
	}
	if len(got.Links) != 1 || got.Links[0].Target != "card_b" || got.Links[0].TypeID != "depends-on" {
		t.Errorf("link not restored: %+v", got.Links)
	}

	// Event log restored in order.
	evs, err := dst.List(ctx, core.EventQuery{CardID: "card_a", Limit: 100})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(evs) != 2 || evs[0].Type != core.EventCardCreated || evs[1].Type != core.EventStatusChanged {
		t.Fatalf("events not restored in order: %+v", evs)
	}
}

// TestImportHeaderOnly verifies the header line alone is a valid (empty) import.
func TestImportHeaderOnly(t *testing.T) {
	ctx := context.Background()
	st, _ := newStore(t)
	in := `{"type":"export","version":1,"workspace_id":"demo","workspace":"Demo workspace"}` + "\n"
	stats, err := importJSONL(ctx, st, strings.NewReader(in))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if (stats != portStats{}) {
		t.Errorf("expected zero stats, got %+v", stats)
	}
}

// TestImportRejectsUnknownType ensures a malformed/foreign record fails loudly
// rather than being silently skipped.
func TestImportRejectsUnknownType(t *testing.T) {
	ctx := context.Background()
	st, _ := newStore(t)
	in := `{"type":"widget","data":{}}` + "\n"
	if _, err := importJSONL(ctx, st, strings.NewReader(in)); err == nil {
		t.Fatal("expected error for unknown record type, got nil")
	}
}

// TestImportDuplicateCardFailsLoudly is the no-silent-overwrite guarantee: a
// second import of the same card id must error, not clobber.
func TestImportDuplicateCardFailsLoudly(t *testing.T) {
	ctx := context.Background()
	st, _ := newStore(t)
	line := `{"type":"card","data":{"id":"card_a","workspace_id":"demo","type_id":"programming-task","schema_version":1,"title":"X","status":"todo","fields":{},"version":1,"created_at":"2026-06-26T12:00:00Z","updated_at":"2026-06-26T12:00:00Z","created_by":"u"}}` + "\n"
	if _, err := importJSONL(ctx, st, strings.NewReader(line)); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := importJSONL(ctx, st, strings.NewReader(line)); err == nil {
		t.Fatal("expected duplicate card id to error, got nil")
	}
}

// TestExportImportMultiValueField pins the portable-snapshot contract for
// multiple fields (frontend-rebuild Phase 3): a JSON-array field value
// survives export → import byte-for-byte (as []any), and a card with the
// field unset stays unset (key absent) — never null, never [].
func TestExportImportMultiValueField(t *testing.T) {
	ctx := context.Background()
	src, ws := newStore(t)
	now := time.Now().UTC()
	withMulti := &core.Card{
		ID: "card_mv1", WorkspaceID: "demo", TypeID: "task", SchemaVersion: 1,
		Title: "multi", Status: "todo", Version: 1, CreatedBy: "u",
		CreatedAt: now, UpdatedAt: now,
		Fields: map[string]any{"platforms": []any{"desktop", "mobile"}},
	}
	without := &core.Card{
		ID: "card_mv2", WorkspaceID: "demo", TypeID: "task", SchemaVersion: 1,
		Title: "plain", Status: "todo", Version: 1, CreatedBy: "u",
		CreatedAt: now, UpdatedAt: now, Fields: map[string]any{},
	}
	for _, c := range []*core.Card{withMulti, without} {
		if err := src.InsertCard(ctx, c, nil); err != nil {
			t.Fatalf("insert %s: %v", c.ID, err)
		}
	}

	var buf bytes.Buffer
	if _, err := exportJSONL(ctx, src, &buf, ws, false); err != nil {
		t.Fatalf("export: %v", err)
	}
	dst, _ := newStore(t)
	if _, err := importJSONL(ctx, dst, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("import: %v", err)
	}

	got, err := dst.GetCard(ctx, "card_mv1")
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := got.Fields.(map[string]any)["platforms"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "desktop" || arr[1] != "mobile" {
		t.Fatalf("platforms after round-trip = %#v, want [desktop mobile]", got.Fields)
	}
	plain, err := dst.GetCard(ctx, "card_mv2")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := plain.Fields.(map[string]any)["platforms"]; present {
		t.Fatal("unset multiple field appeared after round-trip — must stay absent")
	}
}

// --- Sprint 2026-07-19 Phase 2: residual portable-contract gaps ------------
//
// The headline round-trip is pinned above; the tests below close the four
// residual gaps against a frozen, hash-pinned fixture (never the live board):
// CLI wrapper flag IO + fresh-DB refusal, re-export byte-stability (with the
// ordering guarantee named), the frozen fixture itself, and a board.sh smoke.

// frozenBacklogSHA256 pins cmd/cards/testdata/backlog.frozen.jsonl — a copy of
// .cards/backlog.jsonl taken at sprint start (190 cards / 34 tombstone events
// / 7 users). The live board is NOT a test input: it moves, so a snapshot test
// against it would be a change detector, not a contract pin.
const frozenBacklogSHA256 = "ceca56762b8ef754da7100f59aff1add0728b61c7a5d05823928c451195ac726"

// TestExportStateOnlyByteStable pins the ordering guarantee behind
// `cards export --state-only`: the output is a canonical, id-sorted snapshot
// (users, then cards, then card_deleted tombstone events), so
// export → import → re-export is BYTE-IDENTICAL. The guarantee under test is
// the id-sorted canonical ordering applied by portable.go's INLINE
// slices.SortFunc calls — users (portable.go:58), cards (portable.go:93),
// events (portable.go:118); there are no named sortCards/sortUsers/sortEvents
// functions. This test is a regression pin: writing it found NO
// non-determinism to fix — encoding/json already emits map keys (card fields,
// event diffs) sorted, and timestamps round-trip as RFC3339Nano strings.
//
// One named boundary, not a bug: InsertEventRaw re-mints autoincrement event
// ids on import, so byte-stability of the event section requires exported
// tombstone ids to be contiguous from 1 in export order (arranged here by
// seeding tombstones first; the live board's non-contiguous case is covered,
// with the re-mint named, by TestImportFrozenBacklogSnapshot).
func TestExportStateOnlyByteStable(t *testing.T) {
	ctx := context.Background()
	src, ws := newStore(t)

	// Two tombstones FIRST so their ids (1, 2) survive the import re-mint —
	// see the named boundary above. Seeding them also exercises the event
	// id-sort (portable.go:118), which the seed's card_created/status_changed
	// events never reach (state-only carries card_deleted only).
	at := time.Date(2026, 6, 26, 11, 0, 0, 0, time.UTC)
	for _, id := range []string{"card_gone_a", "card_gone_b"} {
		if err := src.InsertEventRaw(ctx, &core.Event{
			CardID: id, Type: core.EventCardDeleted, Actor: "pi", At: at,
			Diff: map[string]any{"card": map[string]any{"id": id, "title": "deleted " + id}},
		}); err != nil {
			t.Fatalf("seed tombstone %s: %v", id, err)
		}
	}
	seedStore(t, src)

	var first bytes.Buffer
	if _, err := exportJSONL(ctx, src, &first, ws, true); err != nil {
		t.Fatalf("export 1: %v", err)
	}
	// The ordering guarantee itself: every section is id-sorted. Byte-stability
	// below is the cross-round-trip consequence of this canonical order.
	assertJSONLIdSorted(t, first.Bytes())

	dst, _ := newStore(t)
	if _, err := importJSONL(ctx, dst, bytes.NewReader(first.Bytes())); err != nil {
		t.Fatalf("import: %v", err)
	}
	var second bytes.Buffer
	if _, err := exportJSONL(ctx, dst, &second, ws, true); err != nil {
		t.Fatalf("export 2: %v", err)
	}
	assertJSONLIdSorted(t, second.Bytes())

	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("state-only re-export not byte-stable (guarantee: id-sorted canonical ordering via portable.go inline slices.SortFunc — users :58 / cards :93 / events :118).\nLine-set diff (-first +second):\n%s",
			diffSortedLineSets(first.Bytes(), second.Bytes()))
	}
}

// TestImportFrozenBacklogSnapshot pins the portable-state contract against the
// frozen, hash-pinned board snapshot: it must import cleanly into a fresh
// store and re-export byte-for-byte identical — EXCEPT event "id" fields, for
// one named reason: InsertEventRaw re-mints fresh autoincrement ids on import,
// and the frozen tombstones carry non-contiguous ids (1..31, 45..47 — the live
// DB's full journal had non-tombstone events interleaved, which a state-only
// export never carries). That is a deterministic id re-mint, NOT a
// non-determinism, so no portable.go fix is required; event lines compare
// modulo the id field, everything else byte-for-byte.
func TestImportFrozenBacklogSnapshot(t *testing.T) {
	const path = "testdata/backlog.frozen.jsonl"
	frozen, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if sum := fmt.Sprintf("%x", sha256.Sum256(frozen)); sum != frozenBacklogSHA256 {
		t.Fatalf("frozen fixture drifted: sha256=%s, want pinned %s — regenerate + re-pin with: cp .cards/backlog.jsonl %s && sha256sum %s",
			sum, frozenBacklogSHA256, path, path)
	}

	ctx := context.Background()
	st, ws := newStore(t) // workspace id/name ("demo") match the fixture header
	stats, err := importJSONL(ctx, st, bytes.NewReader(frozen))
	if err != nil {
		t.Fatalf("import frozen fixture: %v", err)
	}
	// The fixture is hash-pinned, so its composition is stable — assert it.
	if want := (portStats{Cards: 190, Events: 34, Comments: 264, Links: 190, Users: 7}); stats != want {
		t.Fatalf("import tallied %+v, want %+v", stats, want)
	}

	// Byte-stability of the export path itself: two exports of the SAME store
	// are byte-identical (id-sorted canonical ordering, portable.go :58/:93/:118).
	var reexport, again bytes.Buffer
	if _, err := exportJSONL(ctx, st, &reexport, ws, true); err != nil {
		t.Fatalf("re-export: %v", err)
	}
	if _, err := exportJSONL(ctx, st, &again, ws, true); err != nil {
		t.Fatalf("re-export 2: %v", err)
	}
	if !bytes.Equal(reexport.Bytes(), again.Bytes()) {
		t.Fatalf("two exports of the imported store differ.\nLine-set diff (-first +second):\n%s",
			diffSortedLineSets(reexport.Bytes(), again.Bytes()))
	}

	// Against the frozen bytes: same line count and, modulo the named event-id
	// re-mint, identical content in identical (canonical id-sorted) order.
	wantLines := strings.Split(strings.TrimRight(string(frozen), "\n"), "\n")
	gotLines := strings.Split(strings.TrimRight(reexport.String(), "\n"), "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("re-export wrote %d lines, frozen fixture has %d", len(gotLines), len(wantLines))
	}
	for i := range wantLines {
		if normalizeEventID(gotLines[i]) != normalizeEventID(wantLines[i]) {
			t.Fatalf("line %d differs beyond the named event-id re-mint:\n frozen:   %.200s\n reexport: %.200s", i+1, wantLines[i], gotLines[i])
		}
	}
}

// eventIDLine matches the leading autoincrement id of an event envelope's
// data object (Event marshals id first): {"data":{"id":45,"card_id":...
var eventIDLine = regexp.MustCompile(`(\{"data":\{)"id":[0-9]+,`)

// normalizeEventID blanks the autoincrement id on event lines so a re-export
// compares equal to a snapshot taken before import re-minted ids (named in
// TestImportFrozenBacklogSnapshot). Non-event lines return unchanged.
func normalizeEventID(line string) string {
	if !strings.HasSuffix(line, `"type":"event"}`) {
		return line
	}
	return eventIDLine.ReplaceAllString(line, "${1}")
}

// assertJSONLIdSorted pins the canonical ordering guarantee of a state-only
// export: user lines, card lines, and event lines each appear in id order.
func assertJSONLIdSorted(t *testing.T, data []byte) {
	t.Helper()
	var users, cards []string
	var events []int64
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		var env struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("parse export line: %v", err)
		}
		switch env.Type {
		case "user":
			var u core.User
			_ = json.Unmarshal(env.Data, &u)
			users = append(users, u.ID)
		case "card":
			var c core.Card
			_ = json.Unmarshal(env.Data, &c)
			cards = append(cards, c.ID)
		case "event":
			var e core.Event
			_ = json.Unmarshal(env.Data, &e)
			events = append(events, e.ID)
		}
	}
	if !slices.IsSorted(users) {
		t.Errorf("user lines not id-sorted: %v", users)
	}
	if !slices.IsSorted(cards) {
		t.Errorf("card lines not id-sorted: %v", cards)
	}
	if !slices.IsSorted(events) {
		t.Errorf("event lines not id-sorted: %v", events)
	}
}

// diffSortedLineSets diffs two JSONL payloads as SORTED line sets, so a
// byte-stability failure names the differing records instead of an opaque
// byte offset. Long lines are truncated for readability.
func diffSortedLineSets(a, b []byte) string {
	tally := func(data []byte) map[string]int {
		m := map[string]int{}
		for _, ln := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			m[ln]++
		}
		return m
	}
	shorten := func(s string) string {
		if len(s) > 200 {
			return s[:200] + "…"
		}
		return s
	}
	ma, mb := tally(a), tally(b)
	var out []string
	for ln, n := range ma {
		if n > mb[ln] {
			out = append(out, "- "+shorten(ln))
		}
	}
	for ln, n := range mb {
		if n > ma[ln] {
			out = append(out, "+ "+shorten(ln))
		}
	}
	slices.Sort(out)
	return strings.Join(out, "\n")
}

// scaffoldWorkspace returns a temp workspace with starter definitions and NO
// database — the state importCmd's fresh-DB pre-flight expects.
func scaffoldWorkspace(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ws")
	if _, err := starter.Scaffold(dir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	return dir
}

// TestExportImportCmdFlagIO covers the command wrappers around
// exportJSONL/importJSONL: flag parsing, --out/--in file IO, the export no-DB
// pre-flight, and import's fresh-DB refusal (cmd/cards/import.go:52). The
// wrappers only open the store (via openWorkspace/dbPath from cmd/cards/open.go)
// and pick the reader/writer — the format itself is pinned by the round-trip
// tests above.
func TestExportImportCmdFlagIO(t *testing.T) {
	// Flag parsing: --workspace is required; jsonl is the only format.
	if err := exportCmd([]string{"--out", "x.jsonl"}); err == nil {
		t.Error("exportCmd without --workspace: expected error, got nil")
	}
	if err := importCmd([]string{"--in", "x.jsonl"}); err == nil {
		t.Error("importCmd without --workspace: expected error, got nil")
	}
	dir := scaffoldWorkspace(t)
	if err := exportCmd([]string{"--workspace", dir, "--format", "csv"}); err == nil {
		t.Error("exportCmd --format=csv: expected error, got nil")
	}
	if err := importCmd([]string{"--workspace", dir, "--format", "yaml"}); err == nil {
		t.Error("importCmd --format=yaml: expected error, got nil")
	}

	// Export pre-flight: no work-cards.db yet → loud refusal (export reads an
	// existing DB; opening would silently create an empty one).
	if err := exportCmd([]string{"--workspace", dir}); err == nil || !strings.Contains(err.Error(), "no work-cards.db") {
		t.Errorf("exportCmd with no DB: expected 'no work-cards.db' refusal, got %v", err)
	}

	// Seed one card directly (the wrapper under test is file IO, not the
	// service layer), then export it to --out.
	ctx := context.Background()
	st, _, result, err := openWorkspace(dir)
	if err != nil {
		t.Fatalf("openWorkspace: %v", err)
	}
	at := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if err := st.InsertCard(ctx, &core.Card{
		ID: "card_wrap", WorkspaceID: result.Workspace.ID, TypeID: "task", SchemaVersion: 1,
		Title: "wrapper round-trip", Status: "todo", Fields: map[string]any{}, Version: 1,
		CreatedAt: at, UpdatedAt: at, CreatedBy: "u",
	}, nil); err != nil {
		t.Fatalf("seed card: %v", err)
	}
	st.Close()

	out := filepath.Join(t.TempDir(), "snap.jsonl")
	if err := exportCmd([]string{"--workspace", dir, "--state-only", "--out", out}); err != nil {
		t.Fatalf("exportCmd --out: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read --out file: %v", err)
	}
	if !strings.Contains(string(data), `"type":"export"`) || !strings.Contains(string(data), `"id":"card_wrap"`) {
		t.Fatalf("--out file missing header or seeded card:\n%s", data)
	}

	// Import that file into a SECOND fresh workspace via --in.
	dir2 := scaffoldWorkspace(t)
	if err := importCmd([]string{"--workspace", dir2, "--in", out}); err != nil {
		t.Fatalf("importCmd --in: %v", err)
	}
	st2, _, _, err := openWorkspace(dir2)
	if err != nil {
		t.Fatalf("openWorkspace 2: %v", err)
	}
	got, err := st2.GetCard(ctx, "card_wrap")
	if err != nil || got.Title != "wrapper round-trip" {
		t.Fatalf("imported card: got %+v, err=%v", got, err)
	}
	st2.Close()

	// Fresh-DB refusal (import.go:52): a second import into the now-non-empty
	// workspace refuses loudly — never a silent merge/overwrite.
	if err := importCmd([]string{"--workspace", dir2, "--in", out}); err == nil || !strings.Contains(err.Error(), "workspace already contains cards") {
		t.Fatalf("expected fresh-DB refusal on non-empty workspace, got %v", err)
	}
}

// TestBoardScriptSmoke smokes scripts/board.sh — the git-sync wrapper around
// export/import — under `go test`, not as a forgotten CI step. It runs in a
// TEMP repo (script + workspace are copies) so install-hook writes its
// pre-commit hook into the temp repo's .git, never the real one. Covers:
// import --force into a fresh clone, the no-silent-overwrite refusal on a
// second import without --force, and install-hook.
func TestBoardScriptSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the cards binary and runs board.sh subprocesses")
	}
	for _, tool := range []string{"bash", "git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}

	// The script drives a real cards binary via CARDS_BIN.
	bin := filepath.Join(t.TempDir(), "cards")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build cards binary: %v\n%s", err, out)
	}

	// Temp repo: board.sh resolves repo_root from its own location, so it must
	// live at <repo>/scripts/board.sh. The snapshot is the frozen fixture —
	// the live .cards/backlog.jsonl is not a test input.
	repo := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	copyFile := func(src, dst string) error {
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	}
	must(os.MkdirAll(filepath.Join(repo, "scripts"), 0o755))
	must(copyFile(filepath.Join("..", "..", "scripts", "board.sh"), filepath.Join(repo, "scripts", "board.sh")))
	must(os.MkdirAll(filepath.Join(repo, ".cards"), 0o755))
	must(os.CopyFS(filepath.Join(repo, ".cards", "definitions"), os.DirFS(filepath.Join("..", "..", ".cards", "definitions"))))
	must(copyFile(filepath.Join("testdata", "backlog.frozen.jsonl"), filepath.Join(repo, ".cards", "backlog.jsonl")))
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	boardSh := func(args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{"scripts/board.sh"}, args...)...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "CARDS_WS=.cards", "CARDS_BIN="+bin)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// import --force into the fresh clone restores the snapshot.
	out, err := boardSh("import", "--force")
	if err != nil {
		t.Fatalf("board.sh import --force: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".cards", "work-cards.db")); err != nil {
		t.Fatalf("import --force left no DB: %v\n%s", err, out)
	}

	// A second import WITHOUT --force fails loudly — never a silent overwrite
	// (the script surfaces importCmd's fresh-DB refusal verbatim).
	out, err = boardSh("import")
	if err == nil || !strings.Contains(out, "workspace already contains cards") {
		t.Fatalf("second import should refuse a non-empty workspace: err=%v\n%s", err, out)
	}

	// --force again wipes and re-restores (the re-sync-a-machine path).
	if out, err := boardSh("import", "--force"); err != nil {
		t.Fatalf("board.sh import --force (re-sync): %v\n%s", err, out)
	}

	// install-hook writes an executable pre-commit into the TEMP repo's .git.
	if out, err := boardSh("install-hook"); err != nil {
		t.Fatalf("board.sh install-hook: %v\n%s", err, out)
	}
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	fi, err := os.Stat(hook)
	if err != nil {
		t.Fatalf("hook not installed: %v", err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Error("installed hook is not executable")
	}
	body, _ := os.ReadFile(hook)
	if !strings.Contains(string(body), "board.sh export") || !strings.Contains(string(body), "git add") {
		t.Errorf("installed hook missing the auto-export dance:\n%s", body)
	}
}

// --- artifact bundles (--with-artifacts) -------------------------------------

// bundleTypes is the minimal type map for artifact-ref discovery: one type
// with a top-level artifact field and a repeating field with an artifact item.
func bundleTypes() map[string]*core.CardType {
	return map[string]*core.CardType{
		"programming-task": {
			ID: "programming-task", SchemaVersion: 2,
			Fields: []core.FieldDef{
				{ID: "branch", Type: core.FieldString},
				{ID: "screenshot", Type: core.FieldArtifact},
				{ID: "work_log", Type: core.FieldRepeating, ItemFields: []core.FieldDef{
					{ID: "notes", Type: core.FieldText},
					{ID: "capture", Type: core.FieldArtifact},
				}},
			},
		},
	}
}

// seedArtifactCard stores blob bytes in root and inserts a card whose
// screenshot field points at it. Returns the pointer meta.
func seedArtifactCard(t *testing.T, st *sqlite.Store, root string, content string) artifacts.Meta {
	t.Helper()
	am, err := artifacts.New(root)
	if err != nil {
		t.Fatalf("artifacts root: %v", err)
	}
	meta, err := am.Put(strings.NewReader(content))
	if err != nil {
		t.Fatalf("put blob: %v", err)
	}
	at := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	c := &core.Card{
		ID: "card_shot", WorkspaceID: "demo", TypeID: "programming-task", SchemaVersion: 2,
		Title: "Card with a screenshot", Status: "done",
		Fields: map[string]any{
			"branch": "main",
			"screenshot": map[string]any{
				"uri": meta.URI, "mime": meta.MIME, "size": meta.Size, "sha256": meta.SHA256,
			},
		},
		Version: 1, CreatedAt: at, UpdatedAt: at, CreatedBy: "foz",
	}
	if err := st.InsertCard(context.Background(), c, nil); err != nil {
		t.Fatalf("seed artifact card: %v", err)
	}
	return meta
}

// TestExportImportWithArtifacts is the card's DONE-WHEN: a bundle export's
// artifact bytes round-trip through import into a fresh workspace, and the
// restored blob resolves sha256-verified.
func TestExportImportWithArtifacts(t *testing.T) {
	ctx := context.Background()
	srcSt, ws := newStore(t)
	defer srcSt.Close()
	srcRoot := filepath.Join(t.TempDir(), "artifacts")
	meta := seedArtifactCard(t, srcSt, srcRoot, "png bytes, allegedly")
	types := bundleTypes()

	// Export: JSONL + blobs into a bundle dir.
	bundleDir := t.TempDir()
	var buf bytes.Buffer
	if _, err := exportJSONL(ctx, srcSt, &buf, ws, true); err != nil {
		t.Fatalf("export jsonl: %v", err)
	}
	am, err := artifacts.New(srcRoot)
	if err != nil {
		t.Fatalf("artifacts: %v", err)
	}
	n, err := exportArtifacts(ctx, srcSt, types, am, filepath.Join(bundleDir, "artifacts"))
	if err != nil {
		t.Fatalf("export artifacts: %v", err)
	}
	if n != 1 {
		t.Fatalf("exported %d blobs, want 1", n)
	}
	blobPath := filepath.Join(bundleDir, "artifacts", filepath.FromSlash(meta.URI))
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("bundle blob missing: %v", err)
	}

	// Import into a fresh workspace: blobs first, then state.
	dstSt, _ := newStore(t)
	defer dstSt.Close()
	dstRoot := filepath.Join(t.TempDir(), "artifacts")
	dstAM, err := artifacts.New(dstRoot)
	if err != nil {
		t.Fatalf("artifacts: %v", err)
	}
	n, err = restoreArtifacts(bytes.NewReader(buf.Bytes()), types, filepath.Join(bundleDir, "artifacts"), dstAM)
	if err != nil {
		t.Fatalf("restore artifacts: %v", err)
	}
	if n != 1 {
		t.Fatalf("restored %d blobs, want 1", n)
	}
	if _, err := importJSONL(ctx, dstSt, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("import jsonl: %v", err)
	}

	// The restored card's pointer resolves in the destination workspace and
	// the bytes hash to the pointer's sha256.
	got, err := dstSt.GetCard(ctx, "card_shot")
	if err != nil {
		t.Fatalf("get imported card: %v", err)
	}
	refs := artifactRefs(got, types)
	if len(refs) != 1 || refs[0].SHA256 != meta.SHA256 {
		t.Fatalf("imported refs = %+v, want sha %s", refs, meta.SHA256)
	}
	p, err := dstAM.Resolve(refs[0].URI)
	if err != nil {
		t.Fatalf("restored blob does not resolve: %v", err)
	}
	sum, err := fileSHA256(p)
	if err != nil {
		t.Fatalf("hash restored blob: %v", err)
	}
	if sum != meta.SHA256 {
		t.Fatalf("restored blob sha %s, want %s", sum, meta.SHA256)
	}
}

// TestRestoreArtifactsTamperedFailsLoudly is the card's negative proof: a
// bundle blob whose bytes do not hash to the card's pointer must fail the
// import before any state lands.
func TestRestoreArtifactsTamperedFailsLoudly(t *testing.T) {
	ctx := context.Background()
	srcSt, ws := newStore(t)
	defer srcSt.Close()
	srcRoot := filepath.Join(t.TempDir(), "artifacts")
	meta := seedArtifactCard(t, srcSt, srcRoot, "original bytes")
	types := bundleTypes()

	bundleDir := t.TempDir()
	var buf bytes.Buffer
	if _, err := exportJSONL(ctx, srcSt, &buf, ws, true); err != nil {
		t.Fatalf("export jsonl: %v", err)
	}
	am, _ := artifacts.New(srcRoot)
	if _, err := exportArtifacts(ctx, srcSt, types, am, filepath.Join(bundleDir, "artifacts")); err != nil {
		t.Fatalf("export artifacts: %v", err)
	}

	// Tamper with the bundle blob.
	blobPath := filepath.Join(bundleDir, "artifacts", filepath.FromSlash(meta.URI))
	if err := os.WriteFile(blobPath, []byte("tampered bytes"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	dstAM, _ := artifacts.New(filepath.Join(t.TempDir(), "artifacts"))
	_, err := restoreArtifacts(bytes.NewReader(buf.Bytes()), types, filepath.Join(bundleDir, "artifacts"), dstAM)
	if err == nil {
		t.Fatal("tampered bundle imported without error")
	}
	if !strings.Contains(err.Error(), meta.SHA256) {
		t.Fatalf("error should name the expected sha256; got: %v", err)
	}
}

// TestExportArtifactsSelfSafe covers the dogfooding layout where the bundle
// artifacts dir IS the workspace artifacts root (.cards/backlog.jsonl beside
// .cards/artifacts/): export must verify in place, never copy a blob onto
// itself, and leave the bytes intact.
func TestExportArtifactsSelfSafe(t *testing.T) {
	ctx := context.Background()
	st, _ := newStore(t)
	defer st.Close()
	root := filepath.Join(t.TempDir(), "artifacts")
	meta := seedArtifactCard(t, st, root, "self-hosted bytes")
	types := bundleTypes()

	am, _ := artifacts.New(root)
	n, err := exportArtifacts(ctx, st, types, am, root) // destRoot == workspace root
	if err != nil {
		t.Fatalf("self export: %v", err)
	}
	if n != 1 {
		t.Fatalf("verified %d blobs, want 1", n)
	}
	p, err := am.Resolve(meta.URI)
	if err != nil {
		t.Fatalf("blob vanished: %v", err)
	}
	sum, _ := fileSHA256(p)
	if sum != meta.SHA256 {
		t.Fatalf("blob corrupted in place: sha %s, want %s", sum, meta.SHA256)
	}
}

// TestExportArtifactsMissingBlobFailsLoudly: a pointer whose bytes are absent
// from the workspace artifacts root must fail the bundle export — silently
// dropping bytes would produce a bundle that lies about its completeness.
func TestExportArtifactsMissingBlobFailsLoudly(t *testing.T) {
	ctx := context.Background()
	st, _ := newStore(t)
	defer st.Close()
	root := filepath.Join(t.TempDir(), "artifacts")
	meta := seedArtifactCard(t, st, root, "soon to vanish")
	types := bundleTypes()

	am, _ := artifacts.New(root)
	p, err := am.Resolve(meta.URI)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatalf("remove blob: %v", err)
	}

	_, err = exportArtifacts(ctx, st, types, am, filepath.Join(t.TempDir(), "artifacts"))
	if err == nil {
		t.Fatal("export succeeded with missing blob")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error should say the bytes are missing; got: %v", err)
	}
}
