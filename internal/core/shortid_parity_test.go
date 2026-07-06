package core_test

// Short-id parity matrix (sprint 2026-07-06 Phase 1): every mutating verb
// accepts a full id OR an 8-char short id (the leading 8 chars of the hex
// part) with one shared error discipline:
//
//	short id, unique   → resolves (proven by version_conflict on version 999,
//	                     or by the verb succeeding)
//	short id, colliding→ code "ambiguous" (409) with both candidates
//	unknown reference  → code "not_found" (404); AddLink.Target keeps its
//	                     documented target_card_missing (422) rewrite
//	store failure      → code "internal_error" (500) — NEVER masked as 404
//
// Ambiguity is forced deterministically with the shared coretest fixture
// (crafted colliding ids at the store layer); the store-failure row runs
// against a closed store. Both are self-contained per subtest: the store is
// in-memory, so setup and teardown are automatic.

import (
	"context"
	"strings"
	"testing"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/core/coretest"
	"github.com/somebox/cards/internal/sqlite"
)

// parityVerb is one write verb under test. call routes its card reference
// through ref. prep (optional) seeds auxiliary cards and runs BEFORE the
// store_error scenario closes the store, so fixtures never race the failure
// under test.
type parityVerb struct {
	name string
	prep func(t *testing.T, svc *core.Service, st *sqlite.Store)
	call func(ctx context.Context, svc *core.Service, ref string) error
	// notFoundCode overrides the expected code for the unknown-reference row
	// (AddLink.Target documents target_card_missing).
	notFoundCode string
}

func parityVerbs() []parityVerb {
	entry := map[string]any{"commit_hash": "c0ffee"}
	linkTgt := coretest.CardID("LINKTGT1", "t")
	linkSubj := coretest.CardID("LINKSUB1", "s")
	return []parityVerb{
		{name: "PatchCard", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.PatchCard(ctx, ref, core.PatchCardRequest{Version: 999, Actor: "u"})
			return err
		}},
		{name: "DeleteCard", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.DeleteCard(ctx, ref, core.DeleteCardRequest{Version: 999, Actor: "u"})
			return err
		}},
		{name: "UpgradeSchema", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.UpgradeSchema(ctx, ref, core.UpgradeSchemaRequest{TargetVersion: 999, Actor: "u"})
			return err
		}},
		{name: "AppendEntry", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.AppendEntry(ctx, ref, "work_log", entry, 999)
			return err
		}},
		{name: "UpdateEntry", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.UpdateEntry(ctx, ref, "work_log", "e1", entry, 999)
			return err
		}},
		{name: "RemoveEntry", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.RemoveEntry(ctx, ref, "work_log", "e1", 999)
			return err
		}},
		{name: "AddLink.Subject",
			prep: func(t *testing.T, _ *core.Service, st *sqlite.Store) {
				coretest.SeedCard(t, st, "t", "task", linkTgt, nil)
			},
			call: func(ctx context.Context, svc *core.Service, ref string) error {
				_, err := svc.AddLink(ctx, ref, core.LinkInput{TypeID: "related", Target: linkTgt, Actor: "u"})
				return err
			}},
		{name: "AddLink.Target", notFoundCode: "target_card_missing",
			prep: func(t *testing.T, _ *core.Service, st *sqlite.Store) {
				coretest.SeedCard(t, st, "t", "task", linkSubj, nil)
			},
			call: func(ctx context.Context, svc *core.Service, ref string) error {
				_, err := svc.AddLink(ctx, linkSubj, core.LinkInput{TypeID: "related", Target: ref, Actor: "u"})
				return err
			}},
		{name: "RemoveLink", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.RemoveLink(ctx, ref, "related", "card_whatever")
			return err
		}},
		{name: "AddComment", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.AddComment(core.WithActor(ctx, "u"), ref, "hi")
			return err
		}},
		{name: "EditComment", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.EditComment(core.WithActor(ctx, "u"), ref, "cm_none", "hi")
			return err
		}},
		{name: "AddArtifact",
			prep: func(t *testing.T, svc *core.Service, _ *sqlite.Store) {
				am, err := artifacts.New(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				svc.SetArtifacts(am)
			},
			call: func(ctx context.Context, svc *core.Service, ref string) error {
				_, err := svc.AddArtifact(core.WithActor(ctx, "u"), ref, "description", strings.NewReader("x"), 0)
				return err
			}},
		{name: "Claim", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.Claim(ctx, ref, core.ClaimRequest{Version: 999, Actor: "u"})
			return err
		}},
		{name: "Release", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.Release(ctx, ref, core.ReleaseRequest{Version: 999, Actor: "u"})
			return err
		}},
		{name: "History", call: func(ctx context.Context, svc *core.Service, ref string) error {
			_, err := svc.History(ctx, ref)
			return err
		}},
	}
}

func TestWriteVerbs_ShortIDParity(t *testing.T) {
	ctx := context.Background()
	for _, v := range parityVerbs() {
		t.Run(v.name, func(t *testing.T) {
			t.Run("ambiguous", func(t *testing.T) {
				svc, st := newTestService(t)
				if v.prep != nil {
					v.prep(t, svc, st)
				}
				idA, idB := coretest.SeedCollidingCards(t, st, "t", "task", "AMBIG901")
				err := v.call(ctx, svc, "AMBIG901")
				ce := core.AsError(err)
				if ce == nil || ce.Code != "ambiguous" {
					t.Fatalf("expected ambiguous, got %v", err)
				}
				if ce.HTTPStatus != 409 {
					t.Errorf("ambiguous HTTPStatus = %d, want 409", ce.HTTPStatus)
				}
				if len(ce.Candidates) != 2 {
					t.Fatalf("candidates = %d, want 2", len(ce.Candidates))
				}
				got := map[string]bool{}
				for _, c := range ce.Candidates {
					got[c.ID] = true
				}
				if !got[idA] || !got[idB] {
					t.Errorf("candidates missing full ids: %v", ce.Candidates)
				}
			})
			t.Run("not_found", func(t *testing.T) {
				svc, st := newTestService(t)
				if v.prep != nil {
					v.prep(t, svc, st)
				}
				want := v.notFoundCode
				if want == "" {
					want = "not_found"
				}
				err := v.call(ctx, svc, "ZZZZ9999")
				if ce := core.AsError(err); ce == nil || ce.Code != want {
					t.Fatalf("expected %s, got %v", want, err)
				}
			})
			t.Run("store_error", func(t *testing.T) {
				// A store failure during resolution must surface as 500-class,
				// never masked as not-found (the fixed bug the old getCard
				// comment documents).
				svc, st := newTestService(t)
				if v.prep != nil {
					v.prep(t, svc, st)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				err := v.call(ctx, svc, "ZZZZ9999")
				ce := core.AsError(err)
				if ce == nil || ce.Code != "internal_error" {
					t.Fatalf("expected internal_error from closed store, got %v", err)
				}
			})
			t.Run("short_hit", func(t *testing.T) {
				if v.name == "AddLink.Target" || v.name == "RemoveLink" || v.name == "EditComment" {
					t.Skip("proven by TestShortID_NormalizesIntoEventsAndRows")
				}
				svc, st := newTestService(t)
				if v.prep != nil {
					v.prep(t, svc, st)
				}
				coretest.SeedCard(t, st, "t", "task", coretest.CardID("SHORT901", "h"), nil)
				err := v.call(ctx, svc, "SHORT901")
				// Resolution succeeded iff the outcome is anything but a
				// resolution failure: nil, version_conflict, or a later
				// validation error are all proof.
				if ce := core.AsError(err); ce != nil &&
					(ce.Code == "not_found" || ce.Code == "ambiguous" || ce.Code == "internal_error") {
					t.Fatalf("short id did not resolve: %v", err)
				}
			})
		})
	}
}

// TestShortID_NormalizesIntoEventsAndRows pins the normalization property:
// a mutation addressed by SHORT id must record the FULL id everywhere — the
// comment row, the event log, and link rows — never the raw reference.
func TestShortID_NormalizesIntoEventsAndRows(t *testing.T) {
	svc, st := newTestService(t)
	ctx := core.WithActor(context.Background(), "u")
	full := coretest.CardID("NORML901", "n")
	coretest.SeedCard(t, st, "t", "task", full, nil)

	// Comment via short id lands on the full id.
	if _, err := svc.AddComment(ctx, "NORML901", "via short id"); err != nil {
		t.Fatalf("AddComment by short id: %v", err)
	}
	c, err := svc.ResolveCard(ctx, full)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Comments) != 1 || c.Comments[0].Body != "via short id" {
		t.Fatalf("comment not on full-id card: %+v", c.Comments)
	}

	// Link via short SUBJECT and short TARGET normalizes both ends.
	tgtFull := coretest.CardID("NORMT901", "m")
	coretest.SeedCard(t, st, "t", "task", tgtFull, nil)
	if _, err := svc.AddLink(ctx, "NORML901", core.LinkInput{TypeID: "related", Target: "NORMT901", Actor: "u"}); err != nil {
		t.Fatalf("AddLink by short ids: %v", err)
	}
	c, _ = svc.ResolveCard(ctx, full)
	if len(c.Links) != 1 || c.Links[0].Target != tgtFull {
		t.Fatalf("link target not normalized to full id: %+v", c.Links)
	}
	// RemoveLink via short subject removes it (proves the row was stored
	// under the full subject id, and the remove path resolves too).
	if _, err := svc.RemoveLink(ctx, "NORML901", "related", tgtFull); err != nil {
		t.Fatalf("RemoveLink by short id: %v", err)
	}

	// EditComment via short id.
	c, _ = svc.ResolveCard(ctx, full)
	if _, err := svc.EditComment(ctx, "NORML901", c.Comments[0].ID, "edited"); err != nil {
		t.Fatalf("EditComment by short id: %v", err)
	}

	// Every event for this card is keyed by the FULL id: querying the event
	// log by full id must return all of the above; the raw short reference
	// must never appear as a card id.
	evs, err := svc.ListEventsPage(ctx, core.EventQuery{CardID: full, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs.Items) < 3 { // comment_added, link_added, comment_edited (+link_removed if durable)
		t.Fatalf("expected the short-id mutations in the full-id event stream, got %d events", len(evs.Items))
	}
	for _, e := range evs.Items {
		if e.CardID != full {
			t.Errorf("event %s has CardID %q, want full id", e.Type, e.CardID)
		}
	}
}
