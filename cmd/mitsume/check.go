package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func runCheck(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to mitsume.json")
	hbPath := fs.String("heartbeat-file", "", "path to heartbeat file")
	dryRun := fs.Bool("dry-run", false, "skip Slack POST; print payload to stderr")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: mitsume check [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "mitsume check: unexpected positional arguments")

		return 1
	}

	r, exitCode := setupRunner(runnerSetupOpts{
		ConfigPath:    *cfgPath,
		HeartbeatPath: *hbPath,
		DryRun:        *dryRun,
		Subcommand:    "check",
	})
	if exitCode != 0 {
		return exitCode
	}
	if err := r.RunOnce(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume check: %v\n", err)

		return 1
	}

	return 0
}
