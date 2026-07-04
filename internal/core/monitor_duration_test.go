package core_test

import (
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

func TestParseMonitorDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"168h", 168 * time.Hour, false},
		{"7d", 168 * time.Hour, false},
		{"1.5d", 36 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"", 0, true},
		{"not-a-duration", 0, true},
		{"3x", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := core.ParseMonitorDuration(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseMonitorDuration(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMonitorDuration(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseMonitorDuration(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
