package main

import (
	"syscall"
	"testing"
)

func TestSignalName_UsesConventionalNames(t *testing.T) {
	t.Parallel()
	// docs/notify.md § Shutdown announcement payload: signal=<name> には
	// SIGTERM / SIGINT の慣用名を載せる (Go の String() は terminated / interrupt)。
	if got := signalName(syscall.SIGTERM); got != "SIGTERM" {
		t.Errorf("signalName(SIGTERM) = %q, want SIGTERM", got)
	}
	if got := signalName(syscall.SIGINT); got != "SIGINT" {
		t.Errorf("signalName(SIGINT) = %q, want SIGINT", got)
	}
}

func TestSignalName_NilFallsBackToShutdown(t *testing.T) {
	t.Parallel()
	if got := signalName(nil); got != "shutdown" {
		t.Errorf("signalName(nil) = %q, want shutdown", got)
	}
}
