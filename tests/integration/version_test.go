package integration

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrationVersion_PrintsAllFields(t *testing.T) {
	cmd := exec.Command(mitsumeBin, "version")
	cmd.Dir = t.TempDir()
	cmd.Env = envWithout("MITSUME_")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("mitsume version: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		"mitsume ",
		"version=",
		"commit=",
		"built=",
		"go=go1.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stdout missing %q, got %q", want, s)
		}
	}
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("stdout should end with newline, got %q", s)
	}
}

func TestIntegrationVersion_ExtraArgExits1(t *testing.T) {
	cmd := exec.Command(mitsumeBin, "version", "extra")
	cmd.Dir = t.TempDir()
	cmd.Env = envWithout("MITSUME_")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected exit 1")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if code := exitErr.ExitCode(); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no arguments expected") {
		t.Errorf("stderr missing error message, got %q", stderr.String())
	}
}

func TestIntegrationVersion_LdflagsOverrideEmbedsValues(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "mitsume-ldflagged")
	build := exec.Command("go", "build",
		"-ldflags",
		"-X main.version=TESTVER -X main.commit=TESTCOMMIT -X main.date=TESTDATE",
		"-o", bin,
		"../../cmd/mitsume",
	)
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build with ldflags: %v", err)
	}
	cmd := exec.Command(bin, "version")
	cmd.Dir = dir
	cmd.Env = envWithout("MITSUME_")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run ldflagged binary: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		"version=TESTVER",
		"commit=TESTCOMMIT",
		"built=TESTDATE",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ldflags override not embedded: stdout missing %q, got %q", want, s)
		}
	}
}
