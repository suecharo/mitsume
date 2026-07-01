package durationx_test

import (
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/suecharo/mitsume/internal/durationx"
)

func TestParse_Property_DaysEqualTwentyFourHours(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		days := rapid.IntRange(0, 100).Draw(t, "days")
		got, err := durationx.Parse(fmt.Sprintf("%dd", days))
		if err != nil {
			t.Fatalf("Parse(%dd) unexpected error: %v", days, err)
		}
		want := time.Duration(days) * 24 * time.Hour
		if got != want {
			t.Fatalf("Parse(%dd) = %v, want %v", days, got, want)
		}
	})
}

func TestParse_Property_CompositeSumsComponents(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		d := rapid.IntRange(0, 100).Draw(t, "d")
		h := rapid.IntRange(0, 100).Draw(t, "h")
		m := rapid.IntRange(0, 100).Draw(t, "m")
		in := fmt.Sprintf("%dd%dh%dm", d, h, m)
		got, err := durationx.Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) unexpected error: %v", in, err)
		}
		want := time.Duration(d)*24*time.Hour +
			time.Duration(h)*time.Hour +
			time.Duration(m)*time.Minute
		if got != want {
			t.Fatalf("Parse(%q) = %v, want %v", in, got, want)
		}
	})
}

func TestParse_Property_GoStandardDurationRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		nanos := rapid.Int64Range(0, int64(365*24)*int64(time.Hour)).Draw(t, "nanos")
		d := time.Duration(nanos)
		s := d.String()
		got, err := durationx.Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q) unexpected error (from d=%v): %v", s, d, err)
		}
		if got != d {
			t.Fatalf("Parse(%q) = %v, want %v", s, got, d)
		}
	})
}

func TestParse_Property_NegativeDaysReflectSign(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		days := rapid.IntRange(1, 100).Draw(t, "days")
		got, err := durationx.Parse(fmt.Sprintf("-%dd", days))
		if err != nil {
			t.Fatalf("Parse(-%dd) unexpected error: %v", days, err)
		}
		want := -time.Duration(days) * 24 * time.Hour
		if got != want {
			t.Fatalf("Parse(-%dd) = %v, want %v", days, got, want)
		}
	})
}
