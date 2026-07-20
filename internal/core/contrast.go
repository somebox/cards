package core

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// MinContrastRatio is the WCAG AA normal-text floor (4.5:1) enforced on
// OptionThemes accent/muted pairs at definition load. See docs/design/style-field.md.
const MinContrastRatio = 4.5

// ParseHexColor parses #RGB or #RRGGBB into gamma-encoded sRGB channels in
// 0–1 (linearization happens in RelativeLuminance).
func ParseHexColor(s string) (r, g, b float64, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, 0, fmt.Errorf("empty color")
	}
	if s[0] != '#' {
		return 0, 0, 0, fmt.Errorf("color %q must be #RGB or #RRGGBB", s)
	}
	hex := s[1:]
	switch len(hex) {
	case 3:
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	case 6:
		// ok
	default:
		return 0, 0, 0, fmt.Errorf("color %q must be #RGB or #RRGGBB", s)
	}
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("color %q: %w", s, err)
	}
	r = float64((n>>16)&0xff) / 255
	g = float64((n>>8)&0xff) / 255
	b = float64(n&0xff) / 255
	return r, g, b, nil
}

// RelativeLuminance returns WCAG relative luminance for an sRGB color in 0–1.
func RelativeLuminance(r, g, b float64) float64 {
	return 0.2126*linearize(r) + 0.7152*linearize(g) + 0.0722*linearize(b)
}

func linearize(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// ContrastRatio returns the WCAG contrast ratio between two #hex colors
// (order-independent). Returns an error if either color is not parseable hex.
func ContrastRatio(a, b string) (float64, error) {
	ar, ag, ab, err := ParseHexColor(a)
	if err != nil {
		return 0, fmt.Errorf("foreground: %w", err)
	}
	br, bg, bb, err := ParseHexColor(b)
	if err != nil {
		return 0, fmt.Errorf("background: %w", err)
	}
	l1 := RelativeLuminance(ar, ag, ab)
	l2 := RelativeLuminance(br, bg, bb)
	hi, lo := l1, l2
	if l2 > l1 {
		hi, lo = l2, l1
	}
	return (hi + 0.05) / (lo + 0.05), nil
}

// MeetsContrastFloor reports whether accent on muted meets MinContrastRatio.
func MeetsContrastFloor(accent, muted string) (float64, error) {
	ratio, err := ContrastRatio(accent, muted)
	if err != nil {
		return 0, err
	}
	if ratio+1e-9 < MinContrastRatio {
		return ratio, fmt.Errorf("contrast %.2f:1 is below %.1f:1 floor (accent %s on muted %s)",
			ratio, MinContrastRatio, accent, muted)
	}
	return ratio, nil
}
