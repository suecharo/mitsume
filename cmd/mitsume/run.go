package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/suecharo/mitsume/internal/config"
	"github.com/suecharo/mitsume/internal/host"
	"github.com/suecharo/mitsume/internal/supervisor"
)

func runRun(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	name := fs.String("name", "", "display label (default: cmd basename)")
	timeout := &durationFlag{}
	fs.Var(timeout, "timeout", "child process runtime limit (duration, e.g. 30s, 5m, 1h). unlimited if omitted")
	grace := &durationFlag{value: supervisor.DefaultGracePeriod}
	fs.Var(grace, "grace-period", "SIGTERM->SIGKILL grace (duration)")
	bufBytes := fs.Int("stderr-buffer-bytes", supervisor.DefaultStderrBufferBytes,
		"stderr ring buffer size in bytes")
	tailLines := fs.Int("stderr-tail-lines", supervisor.DefaultStderrTailLines,
		"max lines of stderr tail included in the notify text")
	tailBytes := fs.Int("stderr-tail-bytes", supervisor.DefaultStderrTailBytes,
		"max bytes of stderr tail included in the notify text (smaller of tail-lines / tail-bytes wins)")
	quietOnSuccess := fs.Bool("quiet-on-success", false, "suppress notify on exit code 0")
	cfgPath := fs.String("config", "", "path to mitsume.json (for notify section)")
	webhookEnv := fs.String("slack-webhook-url-env", "", "env variable name holding the webhook URL")
	dryRun := fs.Bool("dry-run", false, "run child normally but skip Slack POST; print payload to stderr")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: mitsume run [flags] -- <cmd> [args...]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	command := fs.Args()
	if len(command) == 0 {
		fmt.Fprintln(os.Stderr, "mitsume run: <cmd> is required after `--`")

		return 1
	}

	cwd, _ := os.Getwd()
	cfgFilePath, cfgFound, err := config.Search(*cfgPath, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume run: %v\n", err)

		return 1
	}
	var cfg *config.Config
	if cfgFound {
		cfg, err = config.Parse(cfgFilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mitsume run: %v\n", err)

			return 1
		}
	}

	hostName, err := host.Resolve(cfgHostField(cfg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume run: %v\n", err)

		return 1
	}
	envName := resolveWebhookEnvName(*webhookEnv, cfg)
	url, err := resolveWebhookURL(envName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume run: %v\n", err)

		return 1
	}
	notifier := newNotifier(url, cfg, *dryRun)

	return supervisor.Run(ctx, supervisor.Config{
		Name:              *name,
		Command:           command,
		Timeout:           timeout.Value(),
		GracePeriod:       grace.Value(),
		StderrBufferBytes: *bufBytes,
		StderrTailLines:   *tailLines,
		StderrTailBytes:   *tailBytes,
		QuietOnSuccess:    *quietOnSuccess,
		Notifier:          notifier,
		Host:              hostName,
		ClockNow:          time.Now,
	})
}

func cfgHostField(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}

	return cfg.Host
}
