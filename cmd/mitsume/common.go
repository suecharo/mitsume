package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/suecharo/mitsume/internal/config"
	"github.com/suecharo/mitsume/internal/durationx"
	"github.com/suecharo/mitsume/internal/host"
	"github.com/suecharo/mitsume/internal/lifecycle"
	"github.com/suecharo/mitsume/internal/loader"
	"github.com/suecharo/mitsume/internal/notify"
	"github.com/suecharo/mitsume/internal/runner"
)

// env 変数名の既定 (docs/cli.md § 環境変数)。
const (
	defaultWebhookEnvKey = "MITSUME_SLACK_WEBHOOK_URL"
	heartbeatEnvKey      = "MITSUME_HEARTBEAT_FILE"
	jobEnvKey            = "MITSUME_JOB"
)

// resolveWebhookEnvName は Slack webhook URL を保持する env 変数名を
// 「CLI flag > config.notify.webhook_url_env > 既定 MITSUME_SLACK_WEBHOOK_URL」の
// 順で解決する (docs/notify.md § 秘密情報の扱い)。
func resolveWebhookEnvName(cliEnv string, cfg *config.Config) string {
	if cliEnv != "" {
		return cliEnv
	}
	if cfg != nil && cfg.Notify.WebhookURLEnv != "" {
		return cfg.Notify.WebhookURLEnv
	}

	return defaultWebhookEnvKey
}

// resolveWebhookURL は envName から webhook URL 本体を取り出す。未定義なら
// error。docs/notify.md § 秘密情報 通り、値そのものは error にも stderr にも
// 書かない (env 変数の名前だけ error に載せる)。
func resolveWebhookURL(envName string) (string, error) {
	url := os.Getenv(envName)
	if url == "" {
		return "", fmt.Errorf("webhook URL env %q is not defined", envName)
	}

	return url, nil
}

// resolveHeartbeatPath は heartbeat file の path を
// 「CLI flag > $MITSUME_HEARTBEAT_FILE > config.heartbeat_file > config 隣接の
// .heartbeat.json」の順で解決する (docs/heartbeat.md § 場所)。
func resolveHeartbeatPath(cliPath string, cfg *config.Config) (string, error) {
	if cliPath != "" {
		return cliPath, nil
	}
	if v := os.Getenv(heartbeatEnvKey); v != "" {
		return v, nil
	}
	if cfg != nil {
		if cfg.HeartbeatFile != "" {
			return cfg.HeartbeatFile, nil
		}
		if cfg.SourcePath != "" {
			stem, _ := strings.CutSuffix(filepath.Base(cfg.SourcePath), ".json")

			return filepath.Join(filepath.Dir(cfg.SourcePath), stem+".heartbeat.json"), nil
		}
	}

	return "", fmt.Errorf(
		"cannot resolve heartbeat file path (use --heartbeat-file, $%s, or a config with heartbeat_file / adjacent .heartbeat.json)",
		heartbeatEnvKey)
}

// notifyOptionsFromConfig は cfg.Notify から notify.Options (username / icon) を
// 取り出す。cfg が nil なら zero value。
func notifyOptionsFromConfig(cfg *config.Config) notify.Options {
	if cfg == nil {
		return notify.Options{}
	}

	return notify.Options{
		Username:  cfg.Notify.Username,
		IconEmoji: cfg.Notify.IconEmoji,
		IconURL:   cfg.Notify.IconURL,
	}
}

// newNotifier は Slack Incoming Webhook 送信用の lifecycle.Notifier を作る。
// dryRun=true なら Sender=nil (Notifier.Send が Stderr 経由で payload を出す)、
// 通常時は notify.Client を Sender に埋め込む。
func newNotifier(webhookURL string, cfg *config.Config, dryRun bool) *lifecycle.Notifier {
	opts := notifyOptionsFromConfig(cfg)
	if dryRun {
		return &lifecycle.Notifier{
			Options: opts,
			DryRun:  true,
		}
	}
	client := &notify.Client{
		WebhookURL: webhookURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}

	return &lifecycle.Notifier{
		Sender:  client,
		Options: opts,
	}
}

// durationFlag は flag.Var で使う durationx.Parse (docs/configuration.md §
// duration 表記、`d` 対応) 準拠の duration 型。標準 flag.Duration は
// `time.ParseDuration` を使い `d` 表記を扱わないため、mitsume の他の duration
// 入力と一貫させるためにカスタム型を用意する。
type durationFlag struct {
	value time.Duration
	isSet bool
}

// String は現在保持している duration の文字列表現。
func (d *durationFlag) String() string {
	if d == nil {
		return "0s"
	}

	return d.value.String()
}

// Set は flag 実装用。durationx.Parse で parse する。
func (d *durationFlag) Set(s string) error {
	v, err := durationx.Parse(s)
	if err != nil {
		return err
	}
	d.value = v
	d.isSet = true

	return nil
}

// Value は保持する duration。Set されていなければ zero value。
func (d *durationFlag) Value() time.Duration { return d.value }

// runnerSetupOpts は setupRunner の入力パラメータ。check / watch から共有する。
type runnerSetupOpts struct {
	ConfigPath    string
	HeartbeatPath string
	DryRun        bool
	Subcommand    string
}

// setupRunner は check / watch 共通の runner 構築。config を探索・load・validate
// し、loader.BuildCheckers で []checker.Checker を作り、host / heartbeat path /
// webhook URL を解決して *runner.Runner を返す。exitCode != 0 のときは error は
// stderr に出力済みで、呼び出し側は exitCode で exit する。
func setupRunner(opts runnerSetupOpts) (*runner.Runner, int) {
	cwd, _ := os.Getwd()
	cfgFilePath, cfgFound, err := config.Search(opts.ConfigPath, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume %s: %v\n", opts.Subcommand, err)

		return nil, 1
	}
	if !cfgFound {
		fmt.Fprintf(os.Stderr,
			"mitsume %s: no config file found (use --config, $%s, or place mitsume.json in cwd)\n",
			opts.Subcommand, config.EnvKey)

		return nil, 1
	}
	cfg, err := config.Load(cfgFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume %s: %v\n", opts.Subcommand, err)

		return nil, 1
	}
	hostName, err := host.Resolve(cfg.Host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume %s: %v\n", opts.Subcommand, err)

		return nil, 1
	}
	envName := resolveWebhookEnvName("", cfg)
	url, err := resolveWebhookURL(envName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume %s: %v\n", opts.Subcommand, err)

		return nil, 1
	}
	// heartbeat path は deadman を含む config で必須。無ければ空でもよい (runner の
	// PreflightHeartbeat が deadman の有無で判定)。ここでは resolve できたときだけ
	// 値を渡し、resolve 失敗 (deadman 無し + 全段未指定) は error を無視する。
	hbFile, _ := resolveHeartbeatPath(opts.HeartbeatPath, cfg)

	checkers, err := loader.BuildCheckers(cfg, loader.Options{
		HeartbeatFile: hbFile,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume %s: %v\n", opts.Subcommand, err)

		return nil, 1
	}
	notifier := newNotifier(url, cfg, opts.DryRun)

	return &runner.Runner{
		Checkers:      checkers,
		HeartbeatFile: hbFile,
		Notifier:      notifier,
		Host:          hostName,
		Subcommand:    opts.Subcommand,
	}, 0
}
