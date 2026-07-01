// Package deadman は dead-man's switch checker を実装する。仕様は docs/checkers.md
// § deadman checker と docs/heartbeat.md § 読み込みモデル に従う。
package deadman

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/suecharo/mitsume/internal/checker"
	"github.com/suecharo/mitsume/internal/config"
	"github.com/suecharo/mitsume/internal/confirm"
	"github.com/suecharo/mitsume/internal/heartbeat"
)

// Config は deadman checker の raw JSON schema。
type Config struct {
	Type     string          `json:"type"`
	Name     string          `json:"name,omitempty"`
	Job      string          `json:"job"`
	Interval config.Duration `json:"interval,omitempty"`
	Confirm  json.RawMessage `json:"confirm,omitempty"`
	Expect   Expect          `json:"expect"`
}

// Expect は deadman の判定条件 (docs/checkers.md § deadman § expect)。
type Expect struct {
	Within config.Duration `json:"within"`
}

// Options は Parse に渡す外部情報。
type Options struct {
	// Defaults は config.defaults からの継承値。deadman は Interval のみ使う。
	Defaults DefaultsFallback
	// HeartbeatFile は heartbeat file の絶対パス (docs/heartbeat.md § 場所)。
	// 呼び出し側 (cmd/mitsume の check/watch) が解決して渡す。
	HeartbeatFile string
	// ClockNow は現在時刻 provider。nil なら time.Now。テストの決定性のため
	// 注入経路を残す (tests/README.md § mock 境界: 時刻)。
	ClockNow func() time.Time
}

// DefaultsFallback は defaults セクションから deadman が継承する値。
type DefaultsFallback struct {
	Interval time.Duration
}

// Checker は deadman checker の実装。
type Checker struct {
	name          string
	interval      time.Duration
	confirmCfg    confirm.Config
	within        time.Duration
	job           string
	heartbeatFile string
	clockNow      func() time.Time
}

// Parse は raw JSON + Options を検証して Checker を作る。
func Parse(raw json.RawMessage, opts Options) (*Checker, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("deadman: parse: %w", err)
	}
	if cfg.Type != "deadman" {
		return nil, fmt.Errorf("deadman: type must be \"deadman\", got %q", cfg.Type)
	}
	if cfg.Job == "" {
		return nil, fmt.Errorf("deadman: job is required")
	}
	if err := config.ValidateJobName(cfg.Job); err != nil {
		return nil, fmt.Errorf("deadman: %w", err)
	}
	if !cfg.Expect.Within.IsSet() {
		return nil, fmt.Errorf("deadman: expect.within is required")
	}
	within := cfg.Expect.Within.Value()
	if within <= 0 {
		return nil, fmt.Errorf("deadman: expect.within must be > 0, got %s", within)
	}
	interval := opts.Defaults.Interval
	if cfg.Interval.IsSet() {
		interval = cfg.Interval.Value()
	}
	if interval <= 0 {
		return nil, fmt.Errorf("deadman: interval must be > 0 (set checks[].interval or defaults.interval)")
	}
	confirmCfg, err := confirm.Parse(cfg.Confirm)
	if err != nil {
		return nil, fmt.Errorf("deadman: %w", err)
	}
	if opts.HeartbeatFile == "" {
		return nil, fmt.Errorf("deadman: heartbeat file path is required")
	}
	clockNow := opts.ClockNow
	if clockNow == nil {
		clockNow = time.Now
	}
	name := cfg.Name
	if name == "" {
		name = cfg.Job
	}

	return &Checker{
		name:          name,
		interval:      interval,
		confirmCfg:    confirmCfg,
		within:        within,
		job:           cfg.Job,
		heartbeatFile: opts.HeartbeatFile,
		clockNow:      clockNow,
	}, nil
}

// Type は "deadman" を返す。
func (c *Checker) Type() string { return "deadman" }

// Name は checks[] 内で一意な表示ラベル。
func (c *Checker) Name() string { return c.name }

// Interval は評価周期。
func (c *Checker) Interval() time.Duration { return c.interval }

// Confirm は失敗確信 burst 設定。
func (c *Checker) Confirm() confirm.Config { return c.confirmCfg }

// Job は監視対象 job 識別子。テストと config 側の一意性検証で使う。
func (c *Checker) Job() string { return c.job }

// Within は expect.within の値。テストと外部からの検査で使う。
func (c *Checker) Within() time.Duration { return c.within }

// Evaluate は heartbeat file を read only で参照し、job の last_ping_at と
// 現在時刻の差を expect.within と比較する。
func (c *Checker) Evaluate(ctx context.Context) checker.Result {
	expected := fmt.Sprintf("within=%s", c.within)
	if err := ctx.Err(); err != nil {
		return checker.Failure(
			fmt.Sprintf("context canceled: %v", err),
			"canceled",
			expected,
		)
	}
	file, err := heartbeat.Load(c.heartbeatFile)
	if err != nil {
		return checker.Failure(
			fmt.Sprintf("heartbeat file read failed: %v", err),
			"heartbeat file unreadable",
			expected,
		)
	}
	entry, ok := file.Jobs[c.job]
	if !ok {
		return checker.Failure(
			"job has never been pinged",
			"never pinged",
			expected,
		)
	}
	elapsed := c.clockNow().Sub(entry.LastPingAt)
	if elapsed >= c.within {
		return checker.Failure(
			fmt.Sprintf("no ping for %s", elapsed),
			fmt.Sprintf("last_ping=%s ago", elapsed),
			expected,
		)
	}

	return checker.Success()
}
