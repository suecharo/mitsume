// Package config は設定 JSON の探索・parse・validate を提供する。schema と
// 探索順の仕様は docs/configuration.md に従う。checker 個別のフィールド parse
// はこの package では扱わず、checks[] の各要素は json.RawMessage で持ち越し、
// type discriminator と deadman の job だけを解決する。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/suecharo/mitsume/internal/durationx"
)

// EnvKey は config file パスを保持する env の名前。
const EnvKey = "MITSUME_CONFIG"

// DefaultFileName は自動探索で見に行くカレント配下の config file 名。
const DefaultFileName = "mitsume.json"

// jobNameRe は job 識別子の命名規則 (docs/configuration.md § job)。
var jobNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ValidateJobName は docs/cli.md § 識別子解決 の job 命名規則 [a-zA-Z0-9_-]{1,64}
// を強制する。config 由来だけでなく、CLI positional / 環境変数から取った job
// にも同じ規則が適用される (job は識別子そのものの制約)。
func ValidateJobName(name string) error {
	if name == "" {
		return fmt.Errorf("job identifier is empty")
	}
	if !jobNameRe.MatchString(name) {
		return fmt.Errorf("job identifier %q must match %s", name, jobNameRe.String())
	}

	return nil
}

// Config は設定 JSON の top-level schema。SourcePath は Load で set する
// 実ファイルパス (heartbeat file の隣接 fallback で使う)。
type Config struct {
	Host          string            `json:"host,omitempty"`
	HeartbeatFile string            `json:"heartbeat_file,omitempty"`
	Notify        Notify            `json:"notify"`
	Defaults      Defaults          `json:"defaults,omitempty"`
	Checks        []json.RawMessage `json:"checks,omitempty"`
	SourcePath    string            `json:"-"`
}

// Notify は notify セクション。webhook URL 本体は保持せず、値を保持する env の
// 名前だけを保持する (秘密情報は env 経由: docs/notify.md § 秘密情報の扱い)。
type Notify struct {
	WebhookURLEnv string `json:"webhook_url_env"`
	Username      string `json:"username,omitempty"`
	IconEmoji     string `json:"icon_emoji,omitempty"`
	IconURL       string `json:"icon_url,omitempty"`
}

// Defaults は checks[] に適用する共通デフォルト値。
type Defaults struct {
	Interval Duration `json:"interval,omitempty"`
	Timeout  Duration `json:"timeout,omitempty"`
}

// Duration は string 表記 (d 対応) を time.Duration にラップする JSON 型。
// 未指定と "0" を区別するため IsSet を持つ。
type Duration struct {
	value time.Duration
	set   bool
}

// UnmarshalJSON は文字列を durationx.Parse で time.Duration に変換する。
func (d *Duration) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("config: duration must be a string: %w", err)
	}
	v, err := durationx.Parse(s)
	if err != nil {
		return err
	}
	d.value = v
	d.set = true

	return nil
}

// MarshalJSON は set=false なら null、set=true なら time.Duration.String()。
func (d Duration) MarshalJSON() ([]byte, error) {
	if !d.set {
		return []byte("null"), nil
	}

	return json.Marshal(d.value.String())
}

// Value は保持する duration。未指定なら 0。
func (d Duration) Value() time.Duration { return d.value }

// IsSet は JSON で明示的に指定されていれば true。
func (d Duration) IsSet() bool { return d.set }

// Search は config file を「cliPath > $MITSUME_CONFIG > cwd/mitsume.json」の順で
// 探し、最初に見つかった 1 個を返す。cliPath / env は明示指定なので stat 失敗を
// error として返す。cwd の default は自動探索なので、無くても error にしない。
// cwd は呼び出し側 (通常は os.Getwd()) が渡す。
func Search(cliPath, cwd string) (path string, found bool, err error) {
	if cliPath != "" {
		if _, statErr := os.Stat(cliPath); statErr != nil {
			return "", false, fmt.Errorf("config: stat %s: %w", cliPath, statErr)
		}

		return cliPath, true, nil
	}
	if v := os.Getenv(EnvKey); v != "" {
		if _, statErr := os.Stat(v); statErr != nil {
			return "", false, fmt.Errorf("config: stat $%s=%s: %w", EnvKey, v, statErr)
		}

		return v, true, nil
	}
	if cwd == "" {
		return "", false, nil
	}
	p := filepath.Join(cwd, DefaultFileName)
	if _, statErr := os.Stat(p); statErr == nil {
		return p, true, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", false, fmt.Errorf("config: stat %s: %w", p, statErr)
	}

	return "", false, nil
}

// Parse は path を読んで JSON を parse する。Validate は呼ばない。Validate は
// 呼び出し側が必要なタイミングで実行する。ping / notify のように config 全体
// を必要としないサブコマンドは Parse を使い、check / watch のように起動時に
// config 全体の整合性を確認したいサブコマンドは Load を使う。
func Parse(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	c.SourcePath = path

	return &c, nil
}

// Load は Parse + Validate。
func Load(path string) (*Config, error) {
	c, err := Parse(path)
	if err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}

	return c, nil
}

// Validate は Config の schema 整合性を確認する。checker 個別 field の validation
// はこの package では扱わない (checks[] は type と deadman の job だけを見る)。
func (c *Config) Validate() error {
	if c.Notify.WebhookURLEnv == "" {
		return fmt.Errorf("config: notify.webhook_url_env is required")
	}
	if _, ok := os.LookupEnv(c.Notify.WebhookURLEnv); !ok {
		return fmt.Errorf("config: notify.webhook_url_env=%s is not defined in current environment",
			c.Notify.WebhookURLEnv)
	}
	seenJobs := map[string]int{}
	for i, raw := range c.Checks {
		meta, err := parseCheckMeta(raw)
		if err != nil {
			return fmt.Errorf("config: checks[%d]: %w", i, err)
		}
		switch meta.Type {
		case "":
			return fmt.Errorf("config: checks[%d]: type is required", i)
		case "http", "deadman", "file", "container", "cmd":
		default:
			return fmt.Errorf("config: checks[%d]: unknown type %q", i, meta.Type)
		}
		if meta.Type == "deadman" {
			if meta.Job == "" {
				return fmt.Errorf("config: checks[%d] (deadman): job is required", i)
			}
			if !jobNameRe.MatchString(meta.Job) {
				return fmt.Errorf("config: checks[%d] (deadman): job %q must match %s",
					i, meta.Job, jobNameRe.String())
			}
			if prev, dup := seenJobs[meta.Job]; dup {
				return fmt.Errorf("config: checks[%d] (deadman): job %q duplicates checks[%d]",
					i, meta.Job, prev)
			}
			seenJobs[meta.Job] = i
		}
	}

	return nil
}

// DeadmanJobs は checks[] 中の type: deadman entry の job 一覧を宣言順で返す。
// mitsume ping <job> の位置引数省略時の「唯一の deadman」解決で使う。
func (c *Config) DeadmanJobs() []string {
	var jobs []string
	for _, raw := range c.Checks {
		meta, err := parseCheckMeta(raw)
		if err != nil {
			continue
		}
		if meta.Type == "deadman" {
			jobs = append(jobs, meta.Job)
		}
	}

	return jobs
}

type checkMeta struct {
	Type string `json:"type"`
	Job  string `json:"job,omitempty"`
}

func parseCheckMeta(raw json.RawMessage) (checkMeta, error) {
	var m checkMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return checkMeta{}, fmt.Errorf("parse type/job: %w", err)
	}

	return m, nil
}
