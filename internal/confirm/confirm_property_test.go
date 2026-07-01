package confirm

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestParseProperty_ObjectRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		checks := rapid.IntRange(1, 10000).Draw(t, "checks")
		intervalMs := rapid.IntRange(1, 24*60*60*1000).Draw(t, "intervalMs")
		interval := time.Duration(intervalMs) * time.Millisecond
		raw := fmt.Sprintf(`{"checks": %d, "interval": %q}`, checks, interval.String())
		c, err := Parse(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("Parse(%s): %v", raw, err)
		}
		if c.Checks != checks {
			t.Fatalf("checks: got %d, want %d (raw=%s)", c.Checks, checks, raw)
		}
		if c.Interval != interval {
			t.Fatalf("interval: got %v, want %v (raw=%s)", c.Interval, interval, raw)
		}
		if c.OneStrike {
			t.Fatalf("OneStrike should be false for object form")
		}
	})
}

func TestParseProperty_ChecksZeroOrNegativeAlwaysRejected(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(-100000, 0).Draw(t, "n")
		raw := fmt.Sprintf(`{"checks": %d}`, n)
		if _, err := Parse(json.RawMessage(raw)); err == nil {
			t.Fatalf("expected error for checks=%d", n)
		}
	})
}

func TestParseProperty_PositiveChecksAlwaysAccepted(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 1000000).Draw(t, "n")
		raw := fmt.Sprintf(`{"checks": %d}`, n)
		c, err := Parse(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if c.Checks != n {
			t.Fatalf("Checks = %d, want %d", c.Checks, n)
		}
	})
}
