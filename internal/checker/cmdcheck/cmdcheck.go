// Package cmdcheck は cmd checker (任意の外部コマンドの exit code 判定 escape
// hatch) を実装する。仕様は docs/checkers.md § cmd checker に従う。
package cmdcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/suecharo/mitsume/internal/checker"
	"github.com/suecharo/mitsume/internal/config"
	"github.com/suecharo/mitsume/internal/confirm"
)

// GracePeriod は timeout 超過後 SIGTERM を送ってから SIGKILL までの猶予
// (docs/checkers.md § cmd checker § 固有の挙動、run --grace-period default と揃える)。
const GracePeriod = 5 * time.Second

// TimeoutExitCode は timeout kill 時に mitsume が内部的に扱う exit code
// (GNU timeout(1) 慣習、docs/checkers.md § cmd checker § 固有の挙動)。
const TimeoutExitCode = 124

// DefaultTimeout は checker.timeout / defaults.timeout が両方未指定のときの
// 暗黙 default (docs/checkers.md § cmd checker § 固有の挙動)。
const DefaultTimeout = 30 * time.Second

// stderrTailLines は failure 通知に含める stderr 末尾の最大行数。
const stderrTailLines = 20

// stderrTailBytes は failure 通知に含める stderr 末尾の最大 byte 数。
const stderrTailBytes = 2 * 1024

// nameTruncateChars は自動生成 name の command 先頭からの最大文字数。
const nameTruncateChars = 32

// Config は cmd checker の raw JSON schema。
type Config struct {
	Type     string            `json:"type"`
	Name     string            `json:"name,omitempty"`
	Command  []string          `json:"command"`
	Env      map[string]string `json:"env,omitempty"`
	Cwd      string            `json:"cwd,omitempty"`
	Interval config.Duration   `json:"interval,omitempty"`
	Timeout  config.Duration   `json:"timeout,omitempty"`
	Confirm  json.RawMessage   `json:"confirm,omitempty"`
	Expect   Expect            `json:"expect"`
}

// Expect は cmd checker の判定条件。全 field optional (exit_code の default は 0)。
type Expect struct {
	ExitCode          *int   `json:"exit_code,omitempty"`
	StdoutContains    string `json:"stdout_contains,omitempty"`
	StderrNotContains string `json:"stderr_not_contains,omitempty"`
}

// Options は Parse に渡す外部情報。
type Options struct {
	Defaults DefaultsFallback
	// GracePeriod は timeout 発火後の SIGTERM → SIGKILL 猶予。0 (未指定) の
	// ときは constant の GracePeriod (5s) を使う。docs/checkers.md は 5s 固定
	// と規定するので production では常に 0 (default)。テストで SIGKILL fallback
	// を検証するときだけ短い値を差し込む注入経路。
	GracePeriod time.Duration
}

// DefaultsFallback は defaults セクションから cmd checker が継承する値。
type DefaultsFallback struct {
	Interval time.Duration
	Timeout  time.Duration
}

// Checker は cmd checker の実装。
type Checker struct {
	name              string
	interval          time.Duration
	confirmCfg        confirm.Config
	command           []string
	env               map[string]string
	cwd               string
	timeout           time.Duration
	grace             time.Duration
	expectExit        int
	expectStdout      string
	expectStderrNot   string
	expectStdoutSet   bool
	expectStderrNoSet bool
}

// Parse は raw JSON + Options を検証して Checker を作る。
func Parse(raw json.RawMessage, opts Options) (*Checker, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("cmdcheck: parse: %w", err)
	}
	if cfg.Type != "cmd" {
		return nil, fmt.Errorf("cmdcheck: type must be \"cmd\", got %q", cfg.Type)
	}
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("cmdcheck: command must be a non-empty array")
	}
	for i, a := range cfg.Command {
		if a == "" {
			return nil, fmt.Errorf("cmdcheck: command[%d] is empty", i)
		}
	}
	interval := opts.Defaults.Interval
	if cfg.Interval.IsSet() {
		interval = cfg.Interval.Value()
	}
	if interval <= 0 {
		return nil, fmt.Errorf("cmdcheck: interval must be > 0 (set checks[].interval or defaults.interval)")
	}
	timeout := resolveTimeout(cfg.Timeout, opts.Defaults.Timeout)
	if timeout <= 0 {
		return nil, fmt.Errorf("cmdcheck: timeout must be > 0")
	}
	expectExit := 0
	if cfg.Expect.ExitCode != nil {
		expectExit = *cfg.Expect.ExitCode
	}
	confirmCfg, err := confirm.Parse(cfg.Confirm)
	if err != nil {
		return nil, fmt.Errorf("cmdcheck: %w", err)
	}
	name := cfg.Name
	if name == "" {
		name = autoName(cfg.Command)
	}
	grace := GracePeriod
	if opts.GracePeriod > 0 {
		grace = opts.GracePeriod
	}

	return &Checker{
		name:              name,
		interval:          interval,
		confirmCfg:        confirmCfg,
		command:           cfg.Command,
		env:               cfg.Env,
		cwd:               cfg.Cwd,
		timeout:           timeout,
		grace:             grace,
		expectExit:        expectExit,
		expectStdout:      cfg.Expect.StdoutContains,
		expectStderrNot:   cfg.Expect.StderrNotContains,
		expectStdoutSet:   cfg.Expect.StdoutContains != "",
		expectStderrNoSet: cfg.Expect.StderrNotContains != "",
	}, nil
}

func resolveTimeout(explicit config.Duration, fallback time.Duration) time.Duration {
	if explicit.IsSet() {
		return explicit.Value()
	}
	if fallback > 0 {
		return fallback
	}

	return DefaultTimeout
}

// autoName は docs/checkers.md § name の自動生成 の cmd 規則 (command 先頭
// 32 文字) に従い、joined command から先頭 32 rune を切り出す。byte スライス
// だと multi-byte UTF-8 (日本語 / emoji) を境界で分断して invalid UTF-8 に
// なるため rune-safe に処理する。
func autoName(command []string) string {
	joined := strings.Join(command, " ")
	if utf8.RuneCountInString(joined) <= nameTruncateChars {
		return joined
	}
	runes := []rune(joined)

	return string(runes[:nameTruncateChars])
}

// Type は "cmd" を返す。
func (c *Checker) Type() string { return "cmd" }

// Name は checks[] 内で一意な表示ラベル (自動生成後の値)。
func (c *Checker) Name() string { return c.name }

// Interval は評価周期。
func (c *Checker) Interval() time.Duration { return c.interval }

// Confirm は失敗確信 burst 設定。
func (c *Checker) Confirm() confirm.Config { return c.confirmCfg }

// Command は監視対象 command (直接 exec、shell 経由しない)。
func (c *Checker) Command() []string { return c.command }

// Timeout は 1 回あたりの実行 timeout。
func (c *Checker) Timeout() time.Duration { return c.timeout }

// GracePeriod は SIGTERM → SIGKILL の猶予。
func (c *Checker) GracePeriod() time.Duration { return c.grace }

// Evaluate は command を子プロセスとして起動し、exit code / stdout / stderr を
// expect に照らす。timeout 超過は SIGTERM → grace → SIGKILL、exit code は 124。
func (c *Checker) Evaluate(ctx context.Context) checker.Result {
	if err := ctx.Err(); err != nil {
		return checker.Failure(
			fmt.Sprintf("context canceled before start: %v", err),
			"canceled",
			c.expectedString(),
		)
	}
	// context 経由の自動 kill (SIGKILL 直行) は使わず、awaitCompletion で
	// SIGTERM → grace → SIGKILL を自前で制御する (docs/checkers.md § cmd)。
	// exec.CommandContext に Background を渡すのはその意図の表明。
	proc := exec.CommandContext(context.Background(), c.command[0], c.command[1:]...)
	proc.Env = mergeEnv(os.Environ(), c.env)
	if c.cwd != "" {
		proc.Dir = c.cwd
	}
	var stdout, stderr bytes.Buffer
	proc.Stdout = &stdout
	proc.Stderr = &stderr
	if err := proc.Start(); err != nil {
		return checker.Failure(
			fmt.Sprintf("start failed: %v", err),
			"start failed",
			c.expectedString(),
		)
	}
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	exitCode, timedOut, canceled, waitErr := c.awaitCompletion(ctx, proc, done)
	if canceled {
		return checker.Failure(
			"context canceled during exec",
			"canceled",
			c.expectedString(),
		)
	}
	if waitErr != nil {
		return checker.Failure(
			fmt.Sprintf("wait failed: %v", waitErr),
			"wait failed",
			c.expectedString(),
		)
	}
	if exitCode != c.expectExit {
		errMsg := fmt.Sprintf("exit=%d, want=%d", exitCode, c.expectExit)
		if timedOut {
			errMsg = fmt.Sprintf("timed out after %s (exit=%d)", c.timeout, exitCode)
		}

		return c.failureWithStderr(errMsg, fmt.Sprintf("exit=%d", exitCode), &stderr)
	}
	if c.expectStdoutSet && !bytes.Contains(stdout.Bytes(), []byte(c.expectStdout)) {
		return c.failureWithStderr(
			fmt.Sprintf("stdout does not contain %q", c.expectStdout),
			"stdout_contains=false",
			&stderr,
		)
	}
	if c.expectStderrNoSet && bytes.Contains(stderr.Bytes(), []byte(c.expectStderrNot)) {
		return c.failureWithStderr(
			fmt.Sprintf("stderr contains %q", c.expectStderrNot),
			"stderr_not_contains=false",
			&stderr,
		)
	}

	return checker.Success()
}

func (c *Checker) failureWithStderr(errMsg, observed string, stderr *bytes.Buffer) checker.Result {
	r := checker.Failure(errMsg, observed, c.expectedString())
	r.Stderr = truncateStderr(stderr.Bytes())

	return r
}

// awaitCompletion は timeout / ctx cancel / wait の完了を待ち、exit code と
// 各種フラグを返す。timeout / cancel の場合は SIGTERM → grace → SIGKILL する。
func (c *Checker) awaitCompletion(
	ctx context.Context, proc *exec.Cmd, done <-chan error,
) (exitCode int, timedOut, canceled bool, waitErr error) {
	timer := time.NewTimer(c.timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		code, wErr := decodeExit(err)

		return code, false, false, wErr
	case <-timer.C:
		c.gracefulKill(proc, done)

		return TimeoutExitCode, true, false, nil
	case <-ctx.Done():
		c.gracefulKill(proc, done)

		return 0, false, true, nil
	}
}

func decodeExit(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}

	return 0, err
}

// gracefulKill は SIGTERM を送り、grace 期間内に子が終わらなければ SIGKILL。
// 完了は done で通知される (proc.Wait() が返る)。
func (c *Checker) gracefulKill(proc *exec.Cmd, done <-chan error) {
	_ = proc.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
		return
	case <-time.After(c.grace):
	}
	_ = proc.Process.Kill()
	<-done
}

func (c *Checker) expectedString() string {
	parts := []string{fmt.Sprintf("exit=%d", c.expectExit)}
	if c.expectStdoutSet {
		parts = append(parts, fmt.Sprintf("stdout_contains=%q", c.expectStdout))
	}
	if c.expectStderrNoSet {
		parts = append(parts, fmt.Sprintf("stderr_not_contains=%q", c.expectStderrNot))
	}

	return strings.Join(parts, ", ")
}

// mergeEnv は parent env に user 指定 env を上書きした env slice を返す
// (docs/checkers.md § cmd checker § 固有の挙動: env が優先)。
func mergeEnv(parent []string, override map[string]string) []string {
	if len(override) == 0 {
		return parent
	}
	overrideKeys := make(map[string]struct{}, len(override))
	for k := range override {
		overrideKeys[k] = struct{}{}
	}
	out := make([]string, 0, len(parent)+len(override))
	for _, e := range parent {
		eq := strings.IndexByte(e, '=')
		if eq >= 0 {
			key := e[:eq]
			if _, hit := overrideKeys[key]; hit {
				continue
			}
		}
		out = append(out, e)
	}
	for k, v := range override {
		out = append(out, k+"="+v)
	}

	return out
}

// truncateStderr は stderr の末尾を「20 行 or 2KB の小さい方」で切り出す
// (docs/notify.md § payload 形式 と docs/checkers.md § cmd)。
func truncateStderr(b []byte) string {
	lines := lastNLines(b, stderrTailLines)
	byteTail := b
	if len(byteTail) > stderrTailBytes {
		byteTail = byteTail[len(byteTail)-stderrTailBytes:]
	}
	if len(lines) < len(byteTail) {
		return string(lines)
	}

	return string(byteTail)
}

// lastNLines は 末尾から n 行分の byte slice を返す。改行が n 個以下なら
// 全体を返す (最小限)。
func lastNLines(b []byte, n int) []byte {
	if n <= 0 || len(b) == 0 {
		return b
	}
	count := 0
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			count++
			if count > n {
				return b[i+1:]
			}
		}
	}

	return b
}
