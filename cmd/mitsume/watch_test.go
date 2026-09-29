package main

import (
	"syscall"
	"testing"
)

func TestSignalName_UsesConventionalNames(t *testing.T) {
	t.Parallel()
	// Go の String() は terminated / interrupt を返すが、慣用名を載せる。
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
