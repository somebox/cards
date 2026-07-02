package main

import "testing"

// DEBT-36: global flags are peeled from anywhere in the argument slice, so
// `cards list --json` works the same as `cards --json list`.
func TestPeelGlobalsAnyPosition(t *testing.T) {
	cases := []struct {
		name string
		args []string
		rest []string
	}{
		{"leading", []string{"--json", "list"}, []string{"list"}},
		{"trailing", []string{"list", "--json"}, []string{"list"}},
		{"interleaved", []string{"list", "--json", "--status", "todo"}, []string{"list", "--status", "todo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, rest := peelGlobals(tc.args)
			if !cfg.JSON {
				t.Errorf("args %v: JSON not peeled", tc.args)
			}
			if len(rest) != len(tc.rest) {
				t.Fatalf("rest = %v, want %v", rest, tc.rest)
			}
			for i := range rest {
				if rest[i] != tc.rest[i] {
					t.Errorf("rest = %v, want %v", rest, tc.rest)
				}
			}
		})
	}
}

func TestPeelGlobalsValueFlags(t *testing.T) {
	cfg, rest := peelGlobals([]string{"get", "--as", "alice", "card_x", "--url=http://h/v1"})
	if cfg.As != "alice" || cfg.URL != "http://h/v1" {
		t.Errorf("cfg = %+v", cfg)
	}
	if len(rest) != 2 || rest[0] != "get" || rest[1] != "card_x" {
		t.Errorf("rest = %v, want [get card_x]", rest)
	}
}
