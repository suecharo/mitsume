// Command mitsume は死活監視 CLI の entry point。5 サブコマンド (ping / notify /
// check / watch / run) を持つ。実仕様は docs/cli.md に従う。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const usage = `mitsume: single-binary uptime monitor with Slack notifications.

Usage: mitsume <subcommand> [flags] [args...]

Subcommands:
  ping [<job>]           dead-man's switch: touch heartbeat file
  notify <msg>           send a one-shot Slack notification
  check [--config PATH]  evaluate all checks once (external cron use)
  watch [--config PATH]  daemon: evaluate checks on their intervals
  run --name X -- CMD    supervise a child process

Common flags:
  --dry-run              skip Slack POST / heartbeat write; print payload to stderr

Full docs: https://github.com/suecharo/mitsume
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)

		return 1
	}
	sub := args[0]
	subArgs := args[1:]
	if sub == "-h" || sub == "--help" || sub == "help" {
		fmt.Fprint(os.Stdout, usage)

		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	switch sub {
	case "ping":
		return runPing(ctx, subArgs)
	case "notify":
		return runNotify(ctx, subArgs)
	case "check", "watch", "run":
		fmt.Fprintf(os.Stderr, "mitsume: subcommand %q is not implemented yet\n", sub)

		return 2
	default:
		fmt.Fprintf(os.Stderr, "mitsume: unknown subcommand %q\n\n", sub)
		fmt.Fprint(os.Stderr, usage)

		return 1
	}
}
