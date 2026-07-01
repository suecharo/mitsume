package sizex_test

import (
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"github.com/suecharo/mitsume/internal/sizex"
)

func TestParse_Property_KBEquals1024B(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Int64Range(0, 1<<30).Draw(t, "n")
		got, err := sizex.Parse(fmt.Sprintf("%dKB", n))
		if err != nil {
			t.Fatalf("Parse(%dKB) unexpected error: %v", n, err)
		}
		want := n * 1024
		if got != want {
			t.Fatalf("Parse(%dKB) = %d, want %d", n, got, want)
		}
	})
}

func TestParse_Property_MBEqualsKBTimes1024(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Int64Range(0, 1<<20).Draw(t, "n")
		mb, err := sizex.Parse(fmt.Sprintf("%dMB", n))
		if err != nil {
			t.Fatalf("Parse(%dMB) unexpected error: %v", n, err)
		}
		kb, err := sizex.Parse(fmt.Sprintf("%dKB", n*1024))
		if err != nil {
			t.Fatalf("Parse(%dKB) unexpected error: %v", n*1024, err)
		}
		if mb != kb {
			t.Fatalf("%dMB (%d) != %dKB (%d)", n, mb, n*1024, kb)
		}
	})
}

func TestParse_Property_GBEqualsMBTimes1024(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Int64Range(0, 1<<10).Draw(t, "n")
		gb, err := sizex.Parse(fmt.Sprintf("%dGB", n))
		if err != nil {
			t.Fatalf("Parse(%dGB) unexpected error: %v", n, err)
		}
		mb, err := sizex.Parse(fmt.Sprintf("%dMB", n*1024))
		if err != nil {
			t.Fatalf("Parse(%dMB) unexpected error: %v", n*1024, err)
		}
		if gb != mb {
			t.Fatalf("%dGB (%d) != %dMB (%d)", n, gb, n*1024, mb)
		}
	})
}

func TestParse_Property_IntegerRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Int64Range(0, 1<<50).Draw(t, "n")
		got, err := sizex.Parse(fmt.Sprintf("%d", n))
		if err != nil {
			t.Fatalf("Parse(%d) unexpected error: %v", n, err)
		}
		if got != n {
			t.Fatalf("Parse(%d) = %d, want %d", n, got, n)
		}
	})
}
