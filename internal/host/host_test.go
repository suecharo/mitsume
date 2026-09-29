package host_test

import (
	"os"
	"testing"

	"github.com/suecharo/mitsume/internal/host"
)

func osHostname(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil || h == "" {
		t.Skipf("os.Hostname unavailable: %q, %v", h, err)
	}

	return h
}

func TestResolve_ConfigHostSet_ReturnsConfigHost(t *testing.T) {
	got, err := host.Resolve("cfg-host")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cfg-host" {
		t.Fatalf("got %q, want cfg-host", got)
	}
}

func TestResolve_ConfigHostEmpty_ReturnsOSHostname(t *testing.T) {
	want := osHostname(t)
	got, err := host.Resolve("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolve_ConfigHostEmpty_IgnoresEnvironment(t *testing.T) {
	want := osHostname(t)
	t.Setenv("MITSUME_HOST", "env-host")
	got, err := host.Resolve("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want OS hostname %q", got, want)
	}
}
