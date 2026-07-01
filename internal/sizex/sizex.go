// Package sizex は 1024 ベースの human-readable size 表記 (100B, 512KB, 100MB,
// 10GB, 1TB) と整数 byte 直書きを受ける size parser を提供する。書式の詳細は
// docs/configuration.md § size 表記 に従う。
package sizex

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type unit struct {
	suffix string
	mul    int64
}

var units = []unit{
	{"TB", 1 << 40},
	{"GB", 1 << 30},
	{"MB", 1 << 20},
	{"KB", 1 << 10},
	{"B", 1},
}

// Format は byte 数を docs/configuration.md § size 表記 の 1024 ベース単位で
// human-readable な文字列にする (payload の observed / expected に載せる用途、
// docs/notify.md § payload 形式 の "size=80MB" 形式)。負値は "-<abs>" 形式で
// 出す。単位が丸められない (余りが出る) 場合は下位単位で表現する。
func Format(n int64) string {
	if n == 0 {
		return "0B"
	}
	if n < 0 {
		return "-" + Format(-n)
	}
	for _, u := range units {
		if u.mul == 1 {
			return fmt.Sprintf("%dB", n)
		}
		if n%u.mul == 0 {
			return fmt.Sprintf("%d%s", n/u.mul, u.suffix)
		}
	}

	return fmt.Sprintf("%dB", n)
}

// Parse は 1024 ベースの size 表記 (B / KB / MB / GB / TB) と整数 byte 直書きを
// 受ける。負値、小数、KiB / MiB、空文字、単位のみ、末尾の余分な文字、大文字小文字
// 違いはすべて error。
func Parse(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("sizex: empty string")
	}
	for _, u := range units {
		if !strings.HasSuffix(s, u.suffix) {
			continue
		}
		numStr := strings.TrimSuffix(s, u.suffix)
		if numStr == "" {
			return 0, fmt.Errorf("sizex: missing number in %q", s)
		}
		n, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("sizex: parse %q: %w", s, err)
		}
		if n < 0 {
			return 0, fmt.Errorf("sizex: negative size not allowed (input: %q)", s)
		}
		if u.mul > 1 && n > math.MaxInt64/u.mul {
			return 0, fmt.Errorf("sizex: overflow (input: %q)", s)
		}

		return n * u.mul, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("sizex: parse %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("sizex: negative size not allowed (input: %q)", s)
	}

	return n, nil
}
