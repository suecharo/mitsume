package host_test

import (
	"os"
	"testing"

	"github.com/suecharo/mitsume/internal/host"
)

func TestResolve_ConfigHostWins(t *testing.T) {
	t.Setenv(host.EnvKey, "env-host")
	got, err := host.Resolve("cfg-host")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cfg-host" {
		t.Fatalf("got %q, want cfg-host", got)
	}
}

func TestResolve_EnvWinsWhenConfigEmpty(t *testing.T) {
	t.Setenv(host.EnvKey, "env-host")
	got, err := host.Resolve("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "env-host" {
		t.Fatalf("got %q, want env-host", got)
	}
}

func TestResolve_HostnameFallbackWhenBothEmpty(t *testing.T) {
	t.Setenv(host.EnvKey, "")
	want, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname unavailable: %v", err)
	}
	if want == "" {
		t.Skip("os.Hostname returned empty on this system")
	}
	got, err := host.Resolve("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolve_EmptyEnvValueTreatedAsUnset(t *testing.T) {
	t.Setenv(host.EnvKey, "")
	sys, err := os.Hostname()
	if err != nil || sys == "" {
		t.Skip("os.Hostname unavailable / empty")
	}
	got, err := host.Resolve("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != sys {
		t.Fatalf("empty env should be ignored, got %q want %q", got, sys)
	}
}
