package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/suecharo/mitsume/internal/config"
	"github.com/suecharo/mitsume/internal/heartbeat"
)

const (
	jobEnvKey       = "MITSUME_JOB"
	heartbeatEnvKey = "MITSUME_HEARTBEAT_FILE"
)

func runPing(_ context.Context, args []string) int {
	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to mitsume.json")
	hbPath := fs.String("heartbeat-file", "", "path to heartbeat file")
	dryRun := fs.Bool("dry-run", false, "skip write; print updated content to stderr")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: mitsume ping [<job>] [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	positional := fs.Args()
	if len(positional) > 1 {
		fmt.Fprintln(os.Stderr, "mitsume ping: too many positional arguments")

		return 1
	}

	cwd, _ := os.Getwd()
	cfgFilePath, cfgFound, err := config.Search(*cfgPath, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume ping: %v\n", err)

		return 1
	}
	var cfg *config.Config
	if cfgFound {
		cfg, err = config.Parse(cfgFilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mitsume ping: %v\n", err)

			return 1
		}
	}

	job, err := resolveJob(positional, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume ping: %v\n", err)

		return 1
	}

	hbFilePath, err := resolveHeartbeatPath(*hbPath, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume ping: %v\n", err)

		return 1
	}

	file, err := heartbeat.Load(hbFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mitsume ping: %v\n", err)

		return 1
	}
	now := time.Now()
	file.Update(job, now)

	if *dryRun {
		data, err := heartbeat.Marshal(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mitsume ping: %v\n", err)

			return 1
		}
		fmt.Fprintf(os.Stderr,
			"[mitsume ping --dry-run] would update %s (job=%s, at=%s):\n%s",
			hbFilePath, job, now.Format(time.RFC3339), data)

		return 0
	}
	if err := heartbeat.SaveAtomic(hbFilePath, file); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume ping: %v\n", err)

		return 1
	}

	return 0
}

func resolveJob(positional []string, cfg *config.Config) (string, error) {
	job, err := pickJob(positional, cfg)
	if err != nil {
		return "", err
	}
	if err := config.ValidateJobName(job); err != nil {
		return "", err
	}

	return job, nil
}

func pickJob(positional []string, cfg *config.Config) (string, error) {
	if len(positional) == 1 && positional[0] != "" {
		return positional[0], nil
	}
	if v := os.Getenv(jobEnvKey); v != "" {
		return v, nil
	}
	if cfg != nil {
		jobs := cfg.DeadmanJobs()
		switch len(jobs) {
		case 0:
			// no fallback
		case 1:
			return jobs[0], nil
		default:
			return "", fmt.Errorf("cannot infer <job>: config has %d deadman entries, specify explicitly", len(jobs))
		}
	}

	return "", fmt.Errorf("<job> must be given as arg, $%s, or a single deadman entry in config", jobEnvKey)
}

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

	return "", fmt.Errorf("cannot resolve heartbeat file path (use --heartbeat-file, $%s, or a config with heartbeat_file / adjacent .heartbeat.json)", heartbeatEnvKey)
}
