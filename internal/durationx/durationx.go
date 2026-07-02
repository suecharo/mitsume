// Package durationx は、Go 標準の time.ParseDuration に "d" (日、= 24h) 単位を
// 加えた duration parser と、通知向けの human-readable formatter を提供する。
// 書式の詳細は docs/configuration.md § duration 表記 と docs/notify.md § Payload
// に従う。
package durationx

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	daysRe = regexp.MustCompile(`(\d+(?:\.\d+)?)d`)
	// fullDurationRe は Go time.ParseDuration の書式 (符号 + 1 個以上の "数値 +
	// 単位" の連鎖、または単独の "0") に "d" を単位として加えたもの。全文 match
	// を強制することで "1.5.0d" のような regex 部分マッチ経由の silent 誤変換を弾く。
	fullDurationRe = regexp.MustCompile(
		`^-?(?:0|(?:\d+(?:\.\d+)?(?:ns|us|µs|μs|ms|s|m|h|d))+)$`,
	)
)

// Parse は Go の time.ParseDuration 互換に "d" 単位を足した duration parser。
// 1d は 24h と等価、複合 (1d1h, 2d12h) 可。空文字、w (週)、ISO 8601 (P1D 等)、
// 不正表記は error。
func Parse(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("durationx: empty string")
	}
	if !fullDurationRe.MatchString(s) {
		return 0, fmt.Errorf("durationx: invalid duration %q", s)
	}
	converted := daysRe.ReplaceAllStringFunc(s, func(match string) string {
		numStr := match[:len(match)-1]
		n, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return match
		}

		return strconv.FormatFloat(n*24, 'f', -1, 64) + "h"
	})
	d, err := time.ParseDuration(converted)
	if err != nil {
		return 0, fmt.Errorf("durationx: parse %q: %w", s, err)
	}

	return d, nil
}

// Format は duration を Go 標準表記から末尾のゼロ単位を省いた形で文字列化する
// (26h0m0s -> 26h、25h12m0s -> 25h12m)。docs/notify.md § Payload の observed /
// expected に載せる duration の表記に使う。値は一切丸めない。観測した経過時間の
// sub-second を落としたい場合は呼び出し側で Truncate してから渡す。
func Format(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}

	return s
}
