package sizex_test

import (
	"testing"

	"github.com/suecharo/mitsume/internal/sizex"
)

func TestParse_AcceptsUnits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want int64
	}{
		{"100B", 100},
		{"1B", 1},
		{"0B", 0},
		{"512KB", 512 * 1024},
		{"1KB", 1024},
		{"100MB", 100 * 1024 * 1024},
		{"10GB", 10 * (1 << 30)},
		{"1TB", 1 << 40},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := sizex.Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParse_AcceptsIntegerBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"1", 1},
		{"1024", 1024},
		{"1048576", 1024 * 1024},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := sizex.Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParse_RejectsInvalid(t *testing.T) {
	t.Parallel()
	tests := []string{
		"",
		"KB",
		"MB",
		"1KiB",
		"1MiB",
		"1.5KB",
		"-1B",
		"-100",
		"1 KB",
		"1kb",
		"abc",
		"1KB1",
	}
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()
			if _, err := sizex.Parse(tt); err == nil {
				t.Fatalf("Parse(%q) should have errored", tt)
			}
		})
	}
}

func TestParse_OverflowIsError(t *testing.T) {
	t.Parallel()
	tests := []string{
		"9999999999999TB",
		"8388608TB",               // MaxInt64/2^40 + 1
		"8796093022208MB",         // MaxInt64/2^20 + 1
		"9223372036854775808",     // MaxInt64 + 1 (bare integer)
		"99999999999999999999999", // 明らかに桁溢れ
	}
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()
			if _, err := sizex.Parse(tt); err == nil {
				t.Fatalf("Parse(%q) should error on overflow", tt)
			}
		})
	}
}

func TestParse_AcceptsExactUpperBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want int64
	}{
		{"8388607TB", 8388607 * (1 << 40)},             // MaxInt64/2^40
		{"8796093022207MB", 8796093022207 * (1 << 20)}, // MaxInt64/2^20
		{"9223372036854775807", 9223372036854775807},   // MaxInt64 bare
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := sizex.Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
