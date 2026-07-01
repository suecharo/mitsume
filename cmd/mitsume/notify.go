package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/suecharo/mitsume/internal/config"
	"github.com/suecharo/mitsume/internal/notify"
)

func runNotify(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to mitsume.json")
	webhookEnv := fs.String("slack-webhook-url-env", "", "env variable name holding the webhook URL")
	dryRun := fs.Bool("dry-run", false, "skip Slack POST; print payload to stderr")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: mitsume notify [flags] <msg>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	positional := fs.Args()
	if len(positional) < 1 {
		fmt.Fprintln(os.Stderr, "mitsume notify: <msg> is required")

		return 1
	}
	if len(positional) > 1 {
		fmt.Fprintln(os.Stderr, "mitsume notify: too many positional arguments")

		return 1
	}
	msg := positional[0]

	cwd, _ := os.Getwd()
	cfgFilePath, cfgFound, err := config.Search(*cfgPath, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume notify: %v\n", err)

		return 1
	}
	var cfg *config.Config
	if cfgFound {
		cfg, err = config.Parse(cfgFilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mitsume notify: %v\n", err)

			return 1
		}
	}

	envName := resolveWebhookEnvName(*webhookEnv, cfg)
	url := os.Getenv(envName)
	if url == "" {
		fmt.Fprintf(os.Stderr, "mitsume notify: webhook URL env %q is not defined\n", envName)

		return 1
	}

	var opts notify.Options
	if cfg != nil {
		opts = notify.Options{
			Username:  cfg.Notify.Username,
			IconEmoji: cfg.Notify.IconEmoji,
			IconURL:   cfg.Notify.IconURL,
		}
	}
	payload := notify.BuildAnnouncement(msg, opts)

	if *dryRun {
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "mitsume notify: %v\n", err)

			return 1
		}
		fmt.Fprintln(os.Stderr, string(data))

		return 0
	}

	client := &notify.Client{
		WebhookURL: url,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
	if err := client.Send(ctx, payload); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume notify: %v\n", err)

		return 1
	}

	return 0
}
