package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
)

// release build 時に goreleaser の ldflags で上書きされる。source build では
// default 値のまま (docs/cli.md § mitsume version § 動作)。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func runVersion(args []string) int {
	return writeVersion(os.Stdout, os.Stderr, args, runtime.Version())
}

func writeVersion(stdout, stderr io.Writer, args []string, goVersion string) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "mitsume version: no arguments expected")

		return 1
	}
	if _, err := fmt.Fprintln(stdout, formatVersion(version, commit, date, goVersion)); err != nil {
		fmt.Fprintf(stderr, "mitsume version: %v\n", err)

		return 1
	}

	return 0
}

func formatVersion(version, commit, date, goVersion string) string {
	return fmt.Sprintf(
		"mitsume version=%s, commit=%s, built=%s, go=%s",
		version, commit, date, goVersion,
	)
}
