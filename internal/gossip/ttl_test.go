// ABOUTME: Tests for TTL parsing (Go durations plus day suffix) and fail-closed bounds.
// ABOUTME: Out-of-bounds TTLs are validation errors, never silent clamps.
package gossip

import (
	"strings"
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
	// The largest representable day count is 106751 (int64 max ns ÷ 24h); beyond it
	// the multiply wraps. 106752d is the smallest wrapping value (wraps negative).
	// 213504d wraps to a small POSITIVE duration — the case that slips naive d<=0
	// and bounds checks — so the fail-closed rule requires an error for all of these.
	overflowCases := []string{"106752d", "213504d", "999999999d"}
	for _, in := range overflowCases {
		got, err := ParseTTL(in)
		if err == nil {
			t.Errorf("ParseTTL(%q) accepted overflow, returned %v (want error)", in, got)
		}
	}
	// Sanity: large-but-representable day counts parse correctly.
	want := 10000 * 24 * time.Hour
	got, err := ParseTTL("10000d")
	if err != nil {
		t.Fatalf("ParseTTL(%q) rejected valid value: %v", "10000d", err)
	}
	if got != want {
		t.Fatalf("ParseTTL(%q) = %v, want %v", "10000d", got, want)
	}
	// Boundary: 106751d is the largest legal day count and must parse exactly.
	want106751 := 106751 * 24 * time.Hour
	got106751, err := ParseTTL("106751d")
	if err != nil {
		t.Fatalf("ParseTTL(%q) rejected largest-legal value: %v", "106751d", err)
	}
	if got106751 != want106751 {
		t.Fatalf("ParseTTL(%q) = %v, want %v", "106751d", got106751, want106751)
	}
}

func TestCheckConfigBounds(t *testing.T) {
	cases := []struct {
		name       string
		defaultTTL time.Duration
		maxTTL     time.Duration
		wantErr    bool
	}{
		{"default < max", 168 * time.Hour, 720 * time.Hour, false},
		{"default == max", 24 * time.Hour, 24 * time.Hour, false},
		{"default > max", 720 * time.Hour, 24 * time.Hour, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckConfigBounds(tc.defaultTTL, tc.maxTTL)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr && err != nil {
				// Error must name both values.
				if !strings.Contains(err.Error(), tc.defaultTTL.String()) {
					t.Errorf("error missing default_ttl value %q: %v", tc.defaultTTL, err)
				}
				if !strings.Contains(err.Error(), tc.maxTTL.String()) {
					t.Errorf("error missing max_ttl value %q: %v", tc.maxTTL, err)
				}
			}
		})
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
