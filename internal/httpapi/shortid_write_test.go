package httpapi_test

// HTTP-transport half of the sprint 2026-07-06 Phase 1 parity contract:
// write endpoints accept short ids, and an ambiguous reference renders the
// ONE taxonomy shape (code "ambiguous", 409, candidates) — never a 500 and
// never the old bespoke {error,query,candidates} body.

import (
	"fmt"
	"testing"

	"github.com/somebox/cards/internal/core/coretest"
)

func TestAPIPatchCard_ShortIDResolvesAndNormalizes(t *testing.T) {
	ts, _, st := newServerStore(t)
	full := coretest.CardID("PATCHSH1", "f")
	coretest.SeedCard(t, st, "demo", "programming-task", full,
		map[string]any{"description": "d", "branch": "b"})

	resp, out := do(t, ts, "PATCH", "/v1/cards/PATCHSH1",
		map[string]any{"version": 1, "title": "Renamed via short id", "actor": "local-dev"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("patch by short id: %d %v", resp.StatusCode, out)
	}
	// The response card carries the FULL id — the short reference was
	// normalized before any write.
	if out["id"] != full {
		t.Errorf("response id = %v, want full id %s", out["id"], full)
	}
	if out["title"] != "Renamed via short id" {
		t.Errorf("title = %v", out["title"])
	}
}

func TestAPIWriteVerbs_AmbiguousShortID409(t *testing.T) {
	ts, _, st := newServerStore(t)
	idA, idB := coretest.SeedCollidingCards(t, st, "demo", "programming-task", "WRAMBIG1")

	cases := []struct {
		name, method, path string
		body               any
	}{
		{"patch", "PATCH", "/v1/cards/WRAMBIG1", map[string]any{"version": 1, "title": "x", "actor": "local-dev"}},
		// DELETE pinned the live bug: it 500ed on ambiguous before Phase 1.
		{"delete", "DELETE", "/v1/cards/WRAMBIG1?version=1", nil},
		{"comment", "POST", "/v1/cards/WRAMBIG1/comments", map[string]any{"body": "x", "actor": "local-dev"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := do(t, ts, tc.method, tc.path, tc.body, nil)
			if resp.StatusCode != 409 {
				t.Fatalf("%s ambiguous: %d %v (want 409, never 500)", tc.name, resp.StatusCode, out)
			}
			if out["error"] != "ambiguous" || out["value"] != "WRAMBIG1" {
				t.Errorf("body = %v", out)
			}
			cands, _ := out["candidates"].([]any)
			if len(cands) != 2 {
				t.Fatalf("candidates = %d, want 2: %v", len(cands), out)
			}
			seen := map[string]bool{}
			for _, c := range cands {
				m, _ := c.(map[string]any)
				seen[fmt.Sprint(m["id"])] = true
			}
			if !seen[idA] || !seen[idB] {
				t.Errorf("candidates missing full ids: %v", cands)
			}
		})
	}
}
