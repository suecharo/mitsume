// Package confirm は confirm (失敗時の再確認) の設定型と JSON parse を提供する。
package confirm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/suecharo/mitsume/internal/durationx"
)

// DefaultChecks は confirm.checks の default。
const DefaultChecks = 3

// DefaultInterval は confirm.interval の default。
const DefaultInterval = 30 * time.Second

// Config は confirm の設定。
type Config struct {
	// OneStrike が true なら 1 回失敗で即 alert (confirm: false 相当)。
	// この場合 Checks / Interval は評価に使わない。
	OneStrike bool
	// Checks は評価回数 (>= 1)。初回失敗を含む。
	Checks int
	// Interval は再確認の間隔。
	Interval time.Duration
}

// Default は confirm 省略時の Config。
func Default() Config {
	return Config{Checks: DefaultChecks, Interval: DefaultInterval}
}

// Parse は confirm の JSON raw を Config に変換する。空 (省略) と null は
// Default、false は OneStrike、object は { checks?, interval? } を受ける
// (片方だけ書けば残りは Default)。true は error。
func Parse(raw json.RawMessage) (Config, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return Default(), nil
	}
	if c, ok, err := parseBool(raw); ok {
		return c, err
	}

	return parseObject(raw)
}

func parseBool(raw json.RawMessage) (Config, bool, error) {
	switch strings.TrimSpace(string(raw)) {
	case "true":
		return Config{}, true, fmt.Errorf("confirm=true is not valid; use false or an object")
	case "false":
		return Config{OneStrike: true}, true, nil
	default:
		return Config{}, false, nil
	}
}

func parseObject(raw json.RawMessage) (Config, error) {
	var obj struct {
		Checks   *int    `json:"checks,omitempty"`
		Interval *string `json:"interval,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		return Config{}, fmt.Errorf("confirm: parse: %w", err)
	}
	c := Default()
	if obj.Checks != nil {
		c.Checks = *obj.Checks
	}
	if obj.Interval != nil {
		d, err := durationx.Parse(*obj.Interval)
		if err != nil {
			return Config{}, fmt.Errorf("confirm.interval: %w", err)
		}
		c.Interval = d
	}
	if c.Checks < 1 {
		return Config{}, fmt.Errorf("confirm.checks must be >= 1, got %d", c.Checks)
	}

	return c, nil
}
