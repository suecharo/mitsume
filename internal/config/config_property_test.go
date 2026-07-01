package config_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/suecharo/mitsume/internal/config"
)

func TestDuration_Property_JSONRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		nanos := rapid.Int64Range(0, int64(365*24)*int64(time.Hour)).Draw(t, "nanos")
		d := time.Duration(nanos)
		payload := fmt.Sprintf(`{"interval":%q}`, d.String())
		var defaults config.Defaults
		if err := json.Unmarshal([]byte(payload), &defaults); err != nil {
			t.Fatalf("Unmarshal(%q) unexpected error: %v", payload, err)
		}
		if defaults.Interval.Value() != d {
			t.Fatalf("Interval = %v, want %v", defaults.Interval.Value(), d)
		}
	})
}

func TestDuration_Property_DaysUnit(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		days := rapid.IntRange(0, 100).Draw(t, "days")
		payload := fmt.Sprintf(`{"timeout":"%dd"}`, days)
		var defaults config.Defaults
		if err := json.Unmarshal([]byte(payload), &defaults); err != nil {
			t.Fatalf("Unmarshal(%q) unexpected error: %v", payload, err)
		}
		want := time.Duration(days) * 24 * time.Hour
		if defaults.Timeout.Value() != want {
			t.Fatalf("Timeout = %v, want %v", defaults.Timeout.Value(), want)
		}
	})
}
