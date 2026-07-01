package integration

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestIntegrationPing_UpdatesHeartbeatFileAtomically(t *testing.T) {
	dir := t.TempDir()
	hb := filepath.Join(dir, "hb.json")
	cmd := exec.Command(mitsumeBin, "ping", "--heartbeat-file", hb, "backup")
	cmd.Env = envWithout("MITSUME_")
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume ping failed: %v\nstderr: %s", err, stderr.String())
	}
	data, err := os.ReadFile(hb)
	if err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	var f struct {
		Jobs map[string]struct {
			LastPingAt string `json:"last_ping_at"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse heartbeat: %v\n%s", err, data)
	}
	if _, ok := f.Jobs["backup"]; !ok {
		t.Fatalf("expected backup entry, got: %v", f.Jobs)
	}
	if f.Jobs["backup"].LastPingAt == "" {
		t.Fatalf("last_ping_at is empty")
	}
}

func TestIntegrationPing_DryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	hb := filepath.Join(dir, "hb.json")
	cmd := exec.Command(mitsumeBin, "ping", "--heartbeat-file", hb, "--dry-run", "backup")
	cmd.Env = envWithout("MITSUME_")
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume ping --dry-run failed: %v\nstderr: %s", err, stderr.String())
	}
	if _, err := os.Stat(hb); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("heartbeat file should not exist after --dry-run, err=%v", err)
	}
	s := stderr.String()
	if !strings.Contains(s, "backup") {
		t.Fatalf("stderr should contain job name, got: %s", s)
	}
	if !strings.Contains(s, "last_ping_at") {
		t.Fatalf("stderr should contain payload, got: %s", s)
	}
}

func TestIntegrationPing_ConcurrentInvocationsConverge(t *testing.T) {
	dir := t.TempDir()
	hb := filepath.Join(dir, "hb.json")
	seed := exec.Command(mitsumeBin, "ping", "--heartbeat-file", hb, "backup")
	seed.Env = envWithout("MITSUME_")
	seed.Dir = t.TempDir()
	if err := seed.Run(); err != nil {
		t.Fatalf("seed ping failed: %v", err)
	}
	var wg sync.WaitGroup
	const n = 10
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(mitsumeBin, "ping", "--heartbeat-file", hb, "backup")
			cmd.Env = envWithout("MITSUME_")
			cmd.Dir = t.TempDir()
			if err := cmd.Run(); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent ping failed: %v", err)
	}
	data, err := os.ReadFile(hb)
	if err != nil {
		t.Fatalf("read after concurrent: %v", err)
	}
	var f struct {
		Jobs map[string]struct {
			LastPingAt string `json:"last_ping_at"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	if _, ok := f.Jobs["backup"]; !ok {
		t.Fatalf("backup entry missing after concurrent ping")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("tmp file leaked after concurrent: %s", e.Name())
		}
	}
}

func TestIntegrationPing_MissingHeartbeatPathExits1(t *testing.T) {
	cmd := exec.Command(mitsumeBin, "ping", "backup")
	cmd.Env = envWithout("MITSUME_")
	cmd.Dir = t.TempDir()
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected exit 1 for unresolvable heartbeat path")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if code := exitErr.ExitCode(); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestIntegrationPing_ResolvesJobFromEnv(t *testing.T) {
	dir := t.TempDir()
	hb := filepath.Join(dir, "hb.json")
	cmd := exec.Command(mitsumeBin, "ping", "--heartbeat-file", hb)
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_JOB=renew-cert")
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume ping (env job) failed: %v\nstderr: %s", err, stderr.String())
	}
	data, err := os.ReadFile(hb)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "renew-cert") {
		t.Fatalf("expected renew-cert entry, got: %s", data)
	}
}
