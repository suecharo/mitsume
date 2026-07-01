// Package container は container checker (docker / podman) を実装する。仕様は
// docs/checkers.md § container checker と docs/architecture.md § container
// checker の実装方針 に従う。Docker Engine API の /v1.43/containers/<id>/json
// を unix socket 経由で直接叩き、.State.Status を判定する。
package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/suecharo/mitsume/internal/checker"
	"github.com/suecharo/mitsume/internal/config"
	"github.com/suecharo/mitsume/internal/confirm"
)

// APIVersion は Docker Engine API のバージョン。
const APIVersion = "v1.43"

// RunningState は Docker Engine API の .State.Status で running とみなす値。
const RunningState = "running"

// Config は container checker の raw JSON schema。
type Config struct {
	Type      string          `json:"type"`
	Name      string          `json:"name,omitempty"`
	Container string          `json:"container"`
	Engine    string          `json:"engine,omitempty"`
	Interval  config.Duration `json:"interval,omitempty"`
	Confirm   json.RawMessage `json:"confirm,omitempty"`
	Expect    Expect          `json:"expect"`
}

// Expect は container checker の判定条件。
type Expect struct {
	Running *bool `json:"running,omitempty"`
}

// Options は Parse に渡す外部情報。
type Options struct {
	Defaults DefaultsFallback
	// SocketPath は既に解決済みの unix socket path。空なら Parse 内で
	// CandidatePathsFunc (nil なら CandidatePaths) を回して socket を探索する。
	// テスト用注入経路 (net.Listen("unix", ...) で立てた path を渡す)。
	SocketPath string
	// HTTPClient は test 用に上書き。nil なら SocketPath ベースで作る。
	HTTPClient *http.Client
	// CandidatePathsFunc は SocketPath が空のときに Parse が使う socket 候補
	// 列挙関数。nil なら CandidatePaths (env と hardcoded default) を使う。
	// テスト側は「存在しない tmp path を返す関数」「fake socket path を返す
	// 関数」を渡して, host に docker.sock がある / 無い場合の両方を deterministic
	// に検証する DI 経路。
	CandidatePathsFunc func(engine string) []string
}

// DefaultsFallback は defaults セクションから container checker が継承する値。
// docs/configuration.md § defaults は timeout を HTTP / cmd checker に限定して
// おり、container は timeout を継承しないので Interval のみ。
type DefaultsFallback struct {
	Interval time.Duration
}

// Checker は container checker の実装。
type Checker struct {
	name          string
	interval      time.Duration
	confirmCfg    confirm.Config
	container     string
	engine        string
	socketPath    string
	expectRunning bool
	httpClient    *http.Client
}

// Parse は raw JSON + Options を検証して Checker を作る。socket 探索は起動時
// validation として fail-fast (docs/architecture.md § container checker)。
func Parse(raw json.RawMessage, opts Options) (*Checker, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("container: parse: %w", err)
	}
	if cfg.Type != "container" {
		return nil, fmt.Errorf("container: type must be \"container\", got %q", cfg.Type)
	}
	if cfg.Container == "" {
		return nil, fmt.Errorf("container: container is required")
	}
	if cfg.Engine != "" && cfg.Engine != "docker" && cfg.Engine != "podman" {
		return nil, fmt.Errorf("container: engine must be \"docker\" or \"podman\", got %q", cfg.Engine)
	}
	if cfg.Expect.Running == nil {
		return nil, fmt.Errorf("container: expect.running is required")
	}
	interval := opts.Defaults.Interval
	if cfg.Interval.IsSet() {
		interval = cfg.Interval.Value()
	}
	if interval <= 0 {
		return nil, fmt.Errorf("container: interval must be > 0 (set checks[].interval or defaults.interval)")
	}
	confirmCfg, err := confirm.Parse(cfg.Confirm)
	if err != nil {
		return nil, fmt.Errorf("container: %w", err)
	}
	socketPath := opts.SocketPath
	if socketPath == "" {
		socketPath, err = resolveSocketWith(cfg.Engine, opts.CandidatePathsFunc)
		if err != nil {
			return nil, err
		}
	}
	name := cfg.Name
	if name == "" {
		name = cfg.Container
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = newSocketClient(socketPath)
	}

	return &Checker{
		name:          name,
		interval:      interval,
		confirmCfg:    confirmCfg,
		container:     cfg.Container,
		engine:        cfg.Engine,
		socketPath:    socketPath,
		expectRunning: *cfg.Expect.Running,
		httpClient:    httpClient,
	}, nil
}

func newSocketClient(path string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
		},
	}
}

// CandidatePaths は engine ("docker" / "podman" / "") に対する socket 探索順を
// 返す。docs/checkers.md § container checker § 固有の挙動 の順序に従う。
// engine=="" のときは docker 候補 → podman 候補 の順で全部返す。テスト側で
// 挙動を差し替えたい場合は Options.CandidatePathsFunc を使う (package-level の
// mutable state ではなく Parse 呼び出しの引数に固定して race を避ける)。
func CandidatePaths(engine string) []string {
	switch engine {
	case "docker":
		return defaultDockerPaths()
	case "podman":
		return defaultPodmanPaths()
	default:
		return append(defaultDockerPaths(), defaultPodmanPaths()...)
	}
}

func defaultDockerPaths() []string {
	var out []string
	if v := os.Getenv("DOCKER_HOST"); v != "" {
		if p, ok := stripUnixScheme(v); ok {
			out = append(out, p)
		}
	}
	out = append(out, "/var/run/docker.sock")

	return out
}

func defaultPodmanPaths() []string {
	var out []string
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		out = append(out, filepath.Join(v, "podman", "podman.sock"))
	}
	out = append(out, "/run/podman/podman.sock")

	return out
}

func stripUnixScheme(s string) (string, bool) {
	if strings.HasPrefix(s, "unix://") {
		return strings.TrimPrefix(s, "unix://"), true
	}

	return "", false
}

// ResolveSocket は engine に応じた socket path を返す。stat で存在確認する。
// 見つからなければ error (fail-fast の対象、docs/architecture.md § container)。
func ResolveSocket(engine string) (string, error) {
	return resolveSocketWith(engine, nil)
}

// resolveSocketWith は cpFunc で socket 候補を列挙し、stat で最初に存在する
// path を返す。cpFunc が nil なら CandidatePaths を使う。
func resolveSocketWith(engine string, cpFunc func(engine string) []string) (string, error) {
	if cpFunc == nil {
		cpFunc = CandidatePaths
	}
	paths := cpFunc(engine)
	var tried []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		tried = append(tried, p)
	}

	return "", fmt.Errorf("container: no engine socket found (engine=%q, tried=%v)", engine, tried)
}

// Type は "container" を返す。
func (c *Checker) Type() string { return "container" }

// Name は checks[] 内で一意な表示ラベル。
func (c *Checker) Name() string { return c.name }

// Interval は評価周期。
func (c *Checker) Interval() time.Duration { return c.interval }

// Confirm は失敗確信 burst 設定。
func (c *Checker) Confirm() confirm.Config { return c.confirmCfg }

// Container は監視対象 container 名 / id。
func (c *Checker) Container() string { return c.container }

// Engine は engine 名 ("docker" / "podman" / "")。
func (c *Checker) Engine() string { return c.engine }

// SocketPath は Parse で解決した unix socket path。
func (c *Checker) SocketPath() string { return c.socketPath }

// Evaluate は engine socket に HTTP GET /v1.43/containers/<name>/json を投げ、
// .State.Status を評価する。timeout は checker 単位では持たず、呼び出し側
// (Phase 3 の runner) の ctx が cancel されるまで待つ。docs/configuration.md
// § defaults は container を timeout の対象外にしているため、暗黙 timeout も
// 付けない。
func (c *Checker) Evaluate(ctx context.Context) checker.Result {
	url := fmt.Sprintf("http://engine/%s/containers/%s/json", APIVersion, c.container)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return checker.Failure(
			fmt.Sprintf("build request failed: %v", err),
			"request build failed",
			c.expectedString(),
		)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return checker.Failure(
			fmt.Sprintf("engine request failed: %v", err),
			"socket unreachable",
			c.expectedString(),
		)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return checker.Failure(
			fmt.Sprintf("container %q not found", c.container),
			"state=not_found",
			c.expectedString(),
		)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return checker.Failure(
			fmt.Sprintf("engine returned HTTP %d", resp.StatusCode),
			fmt.Sprintf("http_status=%d", resp.StatusCode),
			c.expectedString(),
		)
	}
	var body struct {
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return checker.Failure(
			fmt.Sprintf("decode engine response: %v", err),
			"decode failed",
			c.expectedString(),
		)
	}
	isRunning := body.State.Status == RunningState
	if isRunning != c.expectRunning {
		return checker.Failure(
			fmt.Sprintf("state=%s, want running=%t", body.State.Status, c.expectRunning),
			fmt.Sprintf("state=%s", body.State.Status),
			c.expectedString(),
		)
	}

	return checker.Success()
}

func (c *Checker) expectedString() string {
	return fmt.Sprintf("running=%t", c.expectRunning)
}
