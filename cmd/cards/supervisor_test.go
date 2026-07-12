package main

import "testing"

func TestCardsURLForChildrenLoopback(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string
	}{
		{"127.0.0.1", 8787, "http://127.0.0.1:8787/v1"},
		{"0.0.0.0", 8787, "http://127.0.0.1:8787/v1"},
		{"", 9000, "http://127.0.0.1:9000/v1"},
		{"localhost", 8787, "http://localhost:8787/v1"},
	}
	for _, tc := range cases {
		got := cardsURLForChildren(tc.host, tc.port)
		if got != tc.want {
			t.Errorf("cardsURLForChildren(%q,%d)=%q want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

func TestCountAutostartServices(t *testing.T) {
	// Smoke: shared helpers used by both entry points.
	if n := countAutostartServices(nil); n != 0 {
		t.Fatalf("nil → %d", n)
	}
}
