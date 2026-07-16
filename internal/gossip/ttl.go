// ABOUTME: TTL parsing and bounds checking for gossip decay.
// ABOUTME: Accepts Go durations plus an Nd day suffix; out-of-bounds is an error, never a clamp.
package gossip

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseTTL parses "72h"/"30m" Go durations plus "7d" whole-day shorthand.
func ParseTTL(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("ttl: empty")
	}
	var d time.Duration
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("ttl: invalid day count %q", s)
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("ttl: %w", err)
		}
	}
	if d <= 0 {
		return 0, fmt.Errorf("ttl: must be positive, got %q", s)
	}
	return d, nil
}

// CheckTTLBounds fails closed: an out-of-bounds TTL is the speaker's error to
// fix, never the store's to silently rewrite.
func CheckTTLBounds(ttl, max time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("ttl: must be positive")
	}
	if ttl > max {
		return fmt.Errorf("ttl: %v exceeds store max_ttl %v", ttl, max)
	}
	return nil
}
