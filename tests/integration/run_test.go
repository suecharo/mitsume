package integration

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIntegrationRun_SuccessNotifies(t *testing.T) {
	webhookURL, received := captureWebhook(t)
	cmd := exec.Command(mitsumeBin, "run", "--name", "hello", "--", "/bin/true")
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume run failed: %v\nstderr: %s", err, stderr.String())
	}
	calls := received()
	if len(calls) != 1 {
		t.Fatalf("expected 1 success notify, got %d", len(calls))
	}
	if !strings.Contains(calls[0], "hello succeeded") {
		t.Fatalf("success text should include name, got %s", calls[0])
	}
}

func TestIntegrationRun_ExitCodePassThrough(t *testing.T) {
	webhookURL, received := captureWebhook(t)
	cmd := exec.Command(mitsumeBin, "run", "--", "/bin/sh", "-c", "exit 3")
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if code := exitErr.ExitCode(); code != 3 {
		t.Fatalf("exit code = %d, want 3 (child pass-through)", code)
	}
	if len(received()) != 1 {
		t.Fatalf("expected 1 failure notify, got %d", len(received()))
	}
}

func TestIntegrationRun_CommandNotFoundReturns127(t *testing.T) {
	webhookURL, _ := captureWebhook(t)
	cmd := exec.Command(mitsumeBin, "run", "--", "/definitely/no/such/binary-xyz")
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if code := exitErr.ExitCode(); code != 127 {
		t.Fatalf("exit code = %d, want 127 (command not found)", code)
	}
}

func TestIntegrationRun_TimeoutReturns124(t *testing.T) {
	webhookURL, received := captureWebhook(t)
	cmd := exec.Command(mitsumeBin, "run",
		"--timeout", "100ms",
		"--grace-period", "500ms",
		"--", "/bin/sleep", "10")
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if code := exitErr.ExitCode(); code != 124 {
		t.Fatalf("exit code = %d, want 124 (timeout)", code)
	}
	calls := received()
	if len(calls) != 1 {
		t.Fatalf("expected 1 failure notify for timeout, got %d", len(calls))
	}
	if !strings.Contains(calls[0], "timed out") {
		t.Fatalf("failure text should mention timeout, got %s", calls[0])
	}
}

func TestIntegrationRun_QuietOnSuccessSuppressesNotify(t *testing.T) {
	var received int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&received, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cmd := exec.Command(mitsumeBin, "run", "--quiet-on-success", "--", "/bin/true")
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+srv.URL)
	cmd.Dir = t.TempDir()
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume run failed: %v", err)
	}
	if got := atomic.LoadInt32(&received); got != 0 {
		t.Fatalf("expected 0 POSTs with --quiet-on-success, got %d", got)
	}
}

func TestIntegrationRun_MissingCommandExits1(t *testing.T) {
	// `--` の後に <cmd> 無し → exit 1
	cmd := exec.Command(mitsumeBin, "run")
	cmd.Env = envWithout("MITSUME_")
	cmd.Dir = t.TempDir()
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if code := exitErr.ExitCode(); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestIntegrationRun_DryRunSkipsSlackButRunsChild(t *testing.T) {
	webhookURL, received := captureWebhook(t)
	cmd := exec.Command(mitsumeBin, "run", "--dry-run", "--", "/bin/true")
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume run --dry-run failed: %v\nstderr: %s", err, stderr.String())
	}
	if got := len(received()); got != 0 {
		t.Fatalf("dry-run must not POST, got %d", got)
	}
	if !strings.Contains(stderr.String(), "succeeded") {
		t.Fatalf("dry-run stderr must include success payload, got: %s", stderr.String())
	}
}

func TestIntegrationRun_StderrTailAppendedToFailureNotify(t *testing.T) {
	webhookURL, received := captureWebhook(t)
	cmd := exec.Command(mitsumeBin, "run", "--", "/bin/sh", "-c",
		"echo 'canary-boom' >&2; exit 2")
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if code := exitErr.ExitCode(); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	calls := received()
	if len(calls) != 1 {
		t.Fatalf("expected 1 failure notify, got %d", len(calls))
	}
	if !strings.Contains(calls[0], "canary-boom") {
		t.Fatalf("failure notify must include stderr tail, got %s", calls[0])
	}
}
