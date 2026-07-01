package tailio_test

import (
	"bytes"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/suecharo/mitsume/internal/tailio"
)

func TestTruncate_ShortInputReturnedAsIs(t *testing.T) {
	t.Parallel()
	in := []byte("hello\nworld\n")
	got := tailio.Truncate(in, 20, 2048)
	if !bytes.Equal(got, in) {
		t.Fatalf("short input should be returned as-is, got %q", got)
	}
}

func TestTruncate_ManyLinesSmallBytesUsesLineCap(t *testing.T) {
	t.Parallel()
	// 100 行 × 5 bytes = 500 bytes、maxLines=20 → tail=20 行、maxBytes=2048 は
	// 制約に掛からず、20 行 tail を返す (line cap が勝つ)。
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "aaaa"
	}
	in := []byte(strings.Join(lines, "\n") + "\n")
	got := tailio.Truncate(in, 20, 2048)
	gotLines := bytes.Count(got, []byte{'\n'})
	if gotLines != 20 {
		t.Fatalf("expected 20 lines in tail, got %d (tail=%q)", gotLines, got)
	}
}

func TestTruncate_LongLinesUsesByteCap(t *testing.T) {
	t.Parallel()
	// 10 行 × 500 bytes = 5000 bytes、maxLines=20 (制約に掛からない)、maxBytes=2048
	// → byte tail が 20 行 tail (=全体) より小さいので byte cap が勝つ。
	line := strings.Repeat("x", 500)
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = line
	}
	in := []byte(strings.Join(lines, "\n") + "\n")
	got := tailio.Truncate(in, 20, 2048)
	if len(got) != 2048 {
		t.Fatalf("expected byte cap 2048, got %d", len(got))
	}
}

func TestTruncate_ZeroMaxLinesIgnoresLineConstraint(t *testing.T) {
	t.Parallel()
	line := strings.Repeat("y", 100)
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = line
	}
	in := []byte(strings.Join(lines, "\n") + "\n")
	// maxLines=0, maxBytes=2048 → byte tail が返る (5050 bytes → 2048)
	got := tailio.Truncate(in, 0, 2048)
	if len(got) != 2048 {
		t.Fatalf("expected byte cap when maxLines <= 0, got %d", len(got))
	}
}

func TestTruncate_ZeroMaxBytesIgnoresByteConstraint(t *testing.T) {
	t.Parallel()
	line := strings.Repeat("z", 100)
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = line
	}
	in := []byte(strings.Join(lines, "\n") + "\n")
	// maxLines=5, maxBytes=0 → 5 行 tail が返る
	got := tailio.Truncate(in, 5, 0)
	gotLines := bytes.Count(got, []byte{'\n'})
	if gotLines != 5 {
		t.Fatalf("expected 5 lines when maxBytes <= 0, got %d (tail=%q)", gotLines, got)
	}
}

func TestTruncate_BothZeroReturnsInput(t *testing.T) {
	t.Parallel()
	in := []byte("abc\ndef\n")
	got := tailio.Truncate(in, 0, 0)
	if !bytes.Equal(got, in) {
		t.Fatalf("both <= 0 should return input as-is, got %q", got)
	}
}

func TestTruncate_EmptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := tailio.Truncate([]byte{}, 20, 2048)
	if len(got) != 0 {
		t.Fatalf("empty input should return empty, got %q", got)
	}
}

func TestTruncate_ExactBoundaryLinesReturnsAll(t *testing.T) {
	t.Parallel()
	// 20 行ちょうど → 全部返す
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "aa"
	}
	in := []byte(strings.Join(lines, "\n") + "\n")
	got := tailio.Truncate(in, 20, 2048)
	if !bytes.Equal(got, in) {
		t.Fatalf("20 lines with maxLines=20 should return all, got len=%d", len(got))
	}
}

func TestTruncate_ExactBoundaryBytesReturnsAll(t *testing.T) {
	t.Parallel()
	in := []byte(strings.Repeat("a", 2048))
	got := tailio.Truncate(in, 0, 2048)
	if len(got) != 2048 {
		t.Fatalf("exactly maxBytes should return all, got len=%d", len(got))
	}
}

func TestTruncate_NoTrailingNewline(t *testing.T) {
	t.Parallel()
	// 末尾 \n 無しの short 入力は全体を返す。maxLines=1 → 全体 (改行 0 個は
	// n 未満なので全体)。
	in := []byte("only-one-line-no-newline")
	got := tailio.Truncate(in, 1, 2048)
	if !bytes.Equal(got, in) {
		t.Fatalf("no-newline short input should be returned as-is, got %q", got)
	}
}

func TestTruncate_Property_ResultLengthLeInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b := rapid.SliceOfN(rapid.Byte(), 0, 4096).Draw(t, "b")
		maxLines := rapid.IntRange(-1, 50).Draw(t, "maxLines")
		maxBytes := rapid.IntRange(-1, 4096).Draw(t, "maxBytes")
		got := tailio.Truncate(b, maxLines, maxBytes)
		if len(got) > len(b) {
			t.Fatalf("tail must not exceed input: len(got)=%d, len(b)=%d", len(got), len(b))
		}
		// tail は必ず b の suffix
		if !bytes.HasSuffix(b, got) {
			t.Fatalf("tail must be a suffix of input")
		}
	})
}

func TestTruncate_Property_BytesCapEnforced(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b := rapid.SliceOfN(rapid.Byte(), 0, 4096).Draw(t, "b")
		maxBytes := rapid.IntRange(1, 4096).Draw(t, "maxBytes")
		got := tailio.Truncate(b, 0, maxBytes)
		if len(got) > maxBytes {
			t.Fatalf("byte cap violated: len(got)=%d, maxBytes=%d", len(got), maxBytes)
		}
	})
}

func TestTruncate_Property_LinesCapEnforced(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		b := rapid.SliceOfN(rapid.Byte(), 0, 4096).Draw(t, "b")
		maxLines := rapid.IntRange(1, 50).Draw(t, "maxLines")
		got := tailio.Truncate(b, maxLines, 0)
		// bytes.Count は "\n" の数を返すので、末尾に "\n" が無い最終行が
		// カウントから漏れる。実際の行数は「改行数 + (末尾非改行なら 1)」で
		// 計算する必要がある (unterminated final line も docs 上は 1 行)。
		nl := bytes.Count(got, []byte{'\n'})
		realLines := nl
		if len(got) > 0 && got[len(got)-1] != '\n' {
			realLines++
		}
		if realLines > maxLines {
			t.Fatalf("real line cap violated: got %d lines (nl=%d), maxLines=%d, tail=%q",
				realLines, nl, maxLines, got)
		}
	})
}

func TestTruncate_NoTrailingNewlineRespectsLineCap(t *testing.T) {
	t.Parallel()
	// b="a\nb\nc" は 3 行 (末尾 "c" は unterminated だが 1 行と数える)。
	// maxLines=1 は末尾 1 行 = "c" だけを返すべき。bytes.Count(got,"\n")==0 の
	// tail を返しても、真の行数が 2 (="b\nc") になれば cap 違反。
	got := tailio.Truncate([]byte("a\nb\nc"), 1, 100)
	if string(got) != "c" {
		t.Fatalf("no-trailing-newline tail 1 should be \"c\", got %q", got)
	}
}

func TestTruncate_NoTrailingNewlineBoundaryCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		maxLines int
		want     string
	}{
		{"a\nb\nc", 2, "b\nc"},
		{"a\nb\nc", 3, "a\nb\nc"},
		{"a\nb\nc", 5, "a\nb\nc"},
		{"a", 1, "a"},
		{"a\nb\n", 1, "b\n"},
		{"a\nb\n", 2, "a\nb\n"},
	}
	for _, tc := range cases {
		got := tailio.Truncate([]byte(tc.in), tc.maxLines, 0)
		if string(got) != tc.want {
			t.Errorf("Truncate(%q, %d, 0) = %q, want %q", tc.in, tc.maxLines, got, tc.want)
		}
	}
}
