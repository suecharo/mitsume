package main

import (
	"flag"
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

// env 変数名の既定。
const (
	defaultWebhookEnvKey = "MITSUME_SLACK_WEBHOOK_URL"
	heartbeatEnvKey      = "MITSUME_HEARTBEAT_FILE"
	jobEnvKey            = "MITSUME_JOB"
)

// resolveWebhookEnvName は Slack webhook URL を保持する env 変数名を返す。
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
// error。URL は秘密情報なので error にも stderr にも書かず、env 変数の名前だけ
// error に載せる。
func resolveWebhookURL(envName string) (string, error) {
	url := os.Getenv(envName)
	if url == "" {
		return "", fmt.Errorf("webhook URL env %q is not defined", envName)
	}

	return url, nil
}

// resolveHeartbeatPath は heartbeat file の path を解決する。
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

// newNotifier は dryRun なら Sender を持たない Notifier を、それ以外は
// notify.Client を Sender に持つ Notifier を返す。
func newNotifier(webhookURL string, dryRun bool) *lifecycle.Notifier {
	if dryRun {
		return &lifecycle.Notifier{DryRun: true}
	}

	return &lifecycle.Notifier{
		Sender: &notify.Client{
			WebhookURL: webhookURL,
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
		},
	}
}

// durationFlag は flag.Var で使う durationx.Parse 準拠の duration 型。標準の
// flag.Duration は time.ParseDuration を使い `d` 表記を扱わないため、他の
// duration 入力と揃えるためにカスタム型を用意する。
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
// webhook URL を解決し、heartbeat file の pre-flight まで済ませて *runner.Runner
// を返す。評価を始める前の検査はすべてここで行う。exitCode != 0 のときは error は
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
	r := &runner.Runner{
		Checkers:      checkers,
		HeartbeatFile: hbFile,
		Notifier:      newNotifier(url, opts.DryRun),
		Host:          hostName,
		Subcommand:    opts.Subcommand,
	}
	if err := r.PreflightHeartbeat(); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume %s: %v\n", opts.Subcommand, err)

		return nil, 1
	}

	return r, 0
}

// splitFlags は args を flag 群と位置引数に分ける。Go の flag package は最初の
// 非 flag 引数で parse を打ち切るので、位置引数の後ろに flag を置く形
// (mitsume ping nightly-backup --dry-run) にはこの前処理が要る。"--" 以降は
// すべて位置引数として扱う。値を取る flag は次の引数も flag 側に入れる (bool
// flag かどうかは fs の定義で判定する)。未定義の flag は flag 側に残し、
// fs.Parse にエラーを報告させる。
func splitFlags(fs *flag.FlagSet, args []string) (flags, positionals []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)

			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			positionals = append(positionals, arg)

			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		def := fs.Lookup(name)
		if def == nil || isBoolFlag(def) || i+1 >= len(args) {
			continue
		}
		i++
		flags = append(flags, args[i])
	}

	return flags, positionals
}

// isBoolFlag は flag package と同じ規約 (Value が IsBoolFlag() bool を実装し
// true を返すか) で bool flag を判定する。
func isBoolFlag(def *flag.Flag) bool {
	bf, ok := def.Value.(interface{ IsBoolFlag() bool })

	return ok && bf.IsBoolFlag()
}
