// ABOUTME: Tests for TTL parsing (Go durations plus day suffix) and fail-closed bounds.
// ABOUTME: Out-of-bounds TTLs are validation errors, never silent clamps.
package gossip

import (
	"testing"
	"time"
)

func TestParseTTL(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"72h", 72 * time.Hour},
		{"30m", 30 * time.Minute},
		{"7d", 168 * time.Hour},
		{"1d", 24 * time.Hour},
	}
	for _, tc := range cases {
		got, err := ParseTTL(tc.in)
		if err != nil {
			t.Fatalf("ParseTTL(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseTTL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseTTLRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "0h", "-4h", "banana", "1.5d", "d", "7dd"} {
		if _, err := ParseTTL(in); err == nil {
			t.Fatalf("ParseTTL(%q) accepted", in)
		}
	}
}

func TestParseTTLRejectsOverflowDays(t *testing.T) {
	// 213503d is the largest representable value (just under int64 max for days×24h).
	// 213504d wraps to a small positive duration — fail-closed rule requires an error.
	overflowCases := []string{"213504d", "999999999d"}
	for _, in := range overflowCases {
		got, err := ParseTTL(in)
		if err == nil {
			t.Errorf("ParseTTL(%q) accepted overflow, returned %v (want error)", in, got)
		}
	}
	// Sanity: a large-but-representable day count parses correctly.
	want := 10000 * 24 * time.Hour
	got, err := ParseTTL("10000d")
	if err != nil {
		t.Fatalf("ParseTTL(%q) rejected valid value: %v", "10000d", err)
	}
	if got != want {
		t.Fatalf("ParseTTL(%q) = %v, want %v", "10000d", got, want)
	}
}

func TestCheckTTLBoundsErrorsNotClamps(t *testing.T) {
	if err := CheckTTLBounds(24*time.Hour, 720*time.Hour); err != nil {
		t.Fatalf("in-bounds ttl rejected: %v", err)
	}
	if err := CheckTTLBounds(721*time.Hour, 720*time.Hour); err == nil {
		t.Fatal("over-max ttl accepted (must be validation error, never a clamp)")
	}
}
