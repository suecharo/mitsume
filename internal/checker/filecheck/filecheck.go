// Package filecheck は file checker を実装する。仕様は docs/checkers.md § file
// checker と docs/configuration.md § size 表記 に従う。
package filecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/suecharo/mitsume/internal/checker"
	"github.com/suecharo/mitsume/internal/config"
	"github.com/suecharo/mitsume/internal/confirm"
	"github.com/suecharo/mitsume/internal/sizex"
)

// Config は file checker の raw JSON schema。path と path_glob は排他 (Parse
// で検証)。
type Config struct {
	Type     string          `json:"type"`
	Name     string          `json:"name,omitempty"`
	Path     string          `json:"path,omitempty"`
	PathGlob string          `json:"path_glob,omitempty"`
	Interval config.Duration `json:"interval,omitempty"`
	Confirm  json.RawMessage `json:"confirm,omitempty"`
	Expect   Expect          `json:"expect"`
}

// Expect は file checker の判定条件 (docs/checkers.md § file § expect)。
// 全 field optional で複数併用可、AND で評価する (共通契約)。
type Expect struct {
	Exists      *bool           `json:"exists,omitempty"`
	MtimeWithin config.Duration `json:"mtime_within,omitempty"`
	SizeMin     *SizeValue      `json:"size_min,omitempty"`
	SizeMax     *SizeValue      `json:"size_max,omitempty"`
}

// SizeValue は size 表記 (100MB / 1024) の JSON parse wrap。
type SizeValue struct {
	value int64
	set   bool
}

// UnmarshalJSON は string 表記と integer 直書きの両方を受ける
// (docs/configuration.md § size 表記)。
func (s *SizeValue) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		if n < 0 {
			return fmt.Errorf("size must be >= 0, got %d", n)
		}
		s.value = n
		s.set = true

		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return fmt.Errorf("size must be string or integer: %w", err)
	}
	v, err := sizex.Parse(str)
	if err != nil {
		return err
	}
	s.value = v
	s.set = true

	return nil
}

// Value は byte 数。IsSet が false なら 0。
func (s SizeValue) Value() int64 { return s.value }

// IsSet は JSON で明示指定されていれば true。
func (s SizeValue) IsSet() bool { return s.set }

// Options は Parse に渡す外部情報。
type Options struct {
	Defaults DefaultsFallback
	ClockNow func() time.Time
}

// DefaultsFallback は defaults セクションから file checker が継承する値。
type DefaultsFallback struct {
	Interval time.Duration
}

// Checker は file checker の実装。
type Checker struct {
	name        string
	interval    time.Duration
	confirmCfg  confirm.Config
	path        string
	pathGlob    string
	exists      *bool
	mtimeWithin time.Duration
	sizeMin     *int64
	sizeMax     *int64
	clockNow    func() time.Time
}

// Parse は raw JSON + Options を検証して Checker を作る。
func Parse(raw json.RawMessage, opts Options) (*Checker, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("filecheck: parse: %w", err)
	}
	if cfg.Type != "file" {
		return nil, fmt.Errorf("filecheck: type must be \"file\", got %q", cfg.Type)
	}
	if (cfg.Path == "") == (cfg.PathGlob == "") {
		return nil, fmt.Errorf("filecheck: exactly one of path / path_glob must be set")
	}
	interval := opts.Defaults.Interval
	if cfg.Interval.IsSet() {
		interval = cfg.Interval.Value()
	}
	if interval <= 0 {
		return nil, fmt.Errorf("filecheck: interval must be > 0 (set checks[].interval or defaults.interval)")
	}
	if err := validateExpect(&cfg.Expect); err != nil {
		return nil, fmt.Errorf("filecheck: %w", err)
	}
	confirmCfg, err := confirm.Parse(cfg.Confirm)
	if err != nil {
		return nil, fmt.Errorf("filecheck: %w", err)
	}
	clockNow := opts.ClockNow
	if clockNow == nil {
		clockNow = time.Now
	}
	name := cfg.Name
	if name == "" {
		if cfg.Path != "" {
			name = cfg.Path
		} else {
			name = cfg.PathGlob
		}
	}
	c := &Checker{
		name:       name,
		interval:   interval,
		confirmCfg: confirmCfg,
		path:       cfg.Path,
		pathGlob:   cfg.PathGlob,
		exists:     cfg.Expect.Exists,
		clockNow:   clockNow,
	}
	if cfg.Expect.MtimeWithin.IsSet() {
		c.mtimeWithin = cfg.Expect.MtimeWithin.Value()
	}
	if cfg.Expect.SizeMin != nil && cfg.Expect.SizeMin.IsSet() {
		v := cfg.Expect.SizeMin.Value()
		c.sizeMin = &v
	}
	if cfg.Expect.SizeMax != nil && cfg.Expect.SizeMax.IsSet() {
		v := cfg.Expect.SizeMax.Value()
		c.sizeMax = &v
	}

	return c, nil
}

func validateExpect(e *Expect) error {
	if e.Exists == nil && !e.MtimeWithin.IsSet() && e.SizeMin == nil && e.SizeMax == nil {
		return fmt.Errorf("expect must contain at least one of exists / mtime_within / size_min / size_max")
	}
	if e.MtimeWithin.IsSet() && e.MtimeWithin.Value() <= 0 {
		return fmt.Errorf("expect.mtime_within must be > 0")
	}
	if e.SizeMin != nil && e.SizeMax != nil {
		if e.SizeMin.Value() > e.SizeMax.Value() {
			return fmt.Errorf("expect.size_min (%d) must be <= size_max (%d)", e.SizeMin.Value(), e.SizeMax.Value())
		}
	}

	return nil
}

// Type は "file" を返す。
func (c *Checker) Type() string { return "file" }

// Name は checks[] 内で一意な表示ラベル。
func (c *Checker) Name() string { return c.name }

// Interval は評価周期。
func (c *Checker) Interval() time.Duration { return c.interval }

// Confirm は失敗確信 burst 設定。
func (c *Checker) Confirm() confirm.Config { return c.confirmCfg }

// Path は監視対象 path。path_glob 版なら空。
func (c *Checker) Path() string { return c.path }

// PathGlob は監視対象 path_glob。path 版なら空。
func (c *Checker) PathGlob() string { return c.pathGlob }

// Evaluate は path または path_glob を stat して expect の全条件を AND 評価する。
func (c *Checker) Evaluate(ctx context.Context) checker.Result {
	if err := ctx.Err(); err != nil {
		return checker.Failure(
			fmt.Sprintf("context canceled: %v", err),
			"canceled",
			c.expectedString(),
		)
	}
	target, info, resolveErr := c.resolveTarget()
	if resolveErr != nil {
		return c.handleResolveError(target, resolveErr)
	}

	return c.evaluateAttributes(target, info)
}

// resolveTarget は path / path_glob から評価対象の 1 file とその FileInfo を返す。
// path_glob の場合は mtime 最新の 1 個を選ぶ。
func (c *Checker) resolveTarget() (string, os.FileInfo, error) {
	if c.pathGlob != "" {
		matches, err := filepath.Glob(c.pathGlob)
		if err != nil {
			return c.pathGlob, nil, fmt.Errorf("glob failed: %w", err)
		}
		if len(matches) == 0 {
			return c.pathGlob, nil, errNoMatch
		}
		var newest os.FileInfo
		var newestPath string
		for _, m := range matches {
			fi, statErr := os.Stat(m)
			if statErr != nil {
				continue
			}
			if newest == nil || fi.ModTime().After(newest.ModTime()) {
				newest = fi
				newestPath = m
			}
		}
		if newest == nil {
			return c.pathGlob, nil, fmt.Errorf("all glob matches failed to stat")
		}

		return newestPath, newest, nil
	}
	info, err := os.Stat(c.path)
	if err != nil {
		return c.path, nil, err
	}

	return c.path, info, nil
}

// errNoMatch は path_glob で 0 件マッチだったことを示す sentinel。
var errNoMatch = errors.New("no glob matches")

func (c *Checker) handleResolveError(target string, err error) checker.Result {
	if c.exists != nil && !*c.exists {
		if errors.Is(err, errNoMatch) || errors.Is(err, os.ErrNotExist) {
			return checker.Success()
		}
	}
	if errors.Is(err, errNoMatch) {
		return checker.Failure(
			fmt.Sprintf("no files matched glob %q", target),
			"no matches",
			c.expectedString(),
		)
	}
	if errors.Is(err, os.ErrNotExist) {
		return checker.Failure(
			fmt.Sprintf("file does not exist: %s", target),
			"exists=false",
			c.expectedString(),
		)
	}

	return checker.Failure(
		fmt.Sprintf("stat failed for %s: %v", target, err),
		"stat failed",
		c.expectedString(),
	)
}

func (c *Checker) evaluateAttributes(target string, info os.FileInfo) checker.Result {
	if c.exists != nil && !*c.exists {
		return checker.Failure(
			fmt.Sprintf("file exists but expected to not exist: %s", target),
			"exists=true",
			c.expectedString(),
		)
	}
	observed := c.observedString(info)
	if c.mtimeWithin > 0 {
		elapsed := c.clockNow().Sub(info.ModTime())
		if elapsed >= c.mtimeWithin {
			return checker.Failure(
				fmt.Sprintf("mtime is %s old (>= mtime_within %s)", elapsed, c.mtimeWithin),
				observed,
				c.expectedString(),
			)
		}
	}
	if c.sizeMin != nil && info.Size() < *c.sizeMin {
		return checker.Failure(
			fmt.Sprintf("size %d is below size_min %d", info.Size(), *c.sizeMin),
			observed,
			c.expectedString(),
		)
	}
	if c.sizeMax != nil && info.Size() > *c.sizeMax {
		return checker.Failure(
			fmt.Sprintf("size %d exceeds size_max %d", info.Size(), *c.sizeMax),
			observed,
			c.expectedString(),
		)
	}

	return checker.Success()
}

func (c *Checker) observedString(info os.FileInfo) string {
	var parts []string
	parts = append(parts, "exists=true")
	if c.mtimeWithin > 0 {
		elapsed := c.clockNow().Sub(info.ModTime())
		parts = append(parts, fmt.Sprintf("mtime=%s ago", elapsed))
	}
	if c.sizeMin != nil || c.sizeMax != nil {
		parts = append(parts, fmt.Sprintf("size=%s", sizex.Format(info.Size())))
	}

	return strings.Join(parts, ", ")
}

func (c *Checker) expectedString() string {
	var parts []string
	if c.exists != nil {
		parts = append(parts, fmt.Sprintf("exists=%t", *c.exists))
	}
	if c.mtimeWithin > 0 {
		parts = append(parts, fmt.Sprintf("mtime_within=%s", c.mtimeWithin))
	}
	if c.sizeMin != nil {
		parts = append(parts, fmt.Sprintf("size_min=%s", sizex.Format(*c.sizeMin)))
	}
	if c.sizeMax != nil {
		parts = append(parts, fmt.Sprintf("size_max=%s", sizex.Format(*c.sizeMax)))
	}

	return strings.Join(parts, ", ")
}
