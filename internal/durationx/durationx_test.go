package durationx_test

import (
	"testing"
	"time"

	"github.com/suecharo/mitsume/internal/durationx"
)

func TestParse_AcceptsGoStandardFormats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"500ms", 500 * time.Millisecond},
		{"30s", 30 * time.Second},
		{"5m", 5 * time.Minute},
		{"1h", time.Hour},
		{"24h", 24 * time.Hour},
		{"1h30m", 90 * time.Minute},
		{"-1h", -time.Hour},
		{"0s", 0},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := durationx.Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParse_AcceptsDaysUnit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"1d", 24 * time.Hour},
		{"3d", 72 * time.Hour},
		{"1d1h", 25 * time.Hour},
		{"2d12h", 60 * time.Hour},
		{"0d", 0},
		{"-1d", -24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := durationx.Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParse_RejectsInvalid(t *testing.T) {
	t.Parallel()
	tests := []string{
		"",
		"1w",
		"P1D",
		"PT30S",
		"abc",
		"1d-1h",
		"d",
		"5",
		"1.5.0d",
		"1.5d.5d",
		".5d",
		"1d.",
		"1d1",
		"1h1",
		"1..5d",
		"1d1w",
	}
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()
			if _, err := durationx.Parse(tt); err == nil {
				t.Fatalf("Parse(%q) should have errored", tt)
			}
		})
	}
}

func TestFormat_TrimsTrailingZeroUnits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   time.Duration
		want string
	}{
		{26 * time.Hour, "26h"},
		{25*time.Hour + 12*time.Minute, "25h12m"},
		{2*time.Hour + 24*time.Second, "2h0m24s"},
		{time.Hour, "1h"},
		{90 * time.Minute, "1h30m"},
		{10 * time.Minute, "10m"},
		{90 * time.Second, "1m30s"},
		{30 * time.Second, "30s"},
		{15 * time.Second, "15s"},
		{2 * time.Second, "2s"},
		{1500 * time.Millisecond, "1.5s"},
		{123 * time.Millisecond, "123ms"},
		{0, "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := durationx.Format(tt.in); got != tt.want {
				t.Fatalf("Format(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
