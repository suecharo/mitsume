package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer は子プロセスの stderr を受けながら、テストから並行に読める buffer。
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// waitFor は cond が true になるまで最大 timeout 待ち、最後の cond の値を返す。
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}

	return cond()
}

// webhookWithStatus は、n 回目 (0 始まり) の POST に status(n) を返す Slack
// Incoming Webhook の fake。受けた body はすべて記録する。
func webhookWithStatus(t *testing.T, status func(n int) int) (url string, received func() []string) {
	t.Helper()
	var (
		mu    sync.Mutex
		calls []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		n := len(calls)
		calls = append(calls, string(b))
		mu.Unlock()
		w.WriteHeader(status(n))
	}))
	t.Cleanup(srv.Close)

	return srv.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), calls...)
	}
}

// failingTarget は常に 500 を返す監視対象の HTTP server。
func failingTarget(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

// oneShotHTTPConfig は host=it-host で、http check を 1 つ持ち、最初の失敗で
// 通知する設定 JSON を返す。
func oneShotHTTPConfig(targetURL string) string {
	return fmt.Sprintf(`{
  "host": "it-host",
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "http", "name": "api", "url": %q, "interval": "1h",
     "confirm": false, "expect": {"status": 200}}
  ]
}`, targetURL)
}

// startWatch は watch を起動し、stderr の buffer を返す。
func startWatch(t *testing.T, webhookURL string, args ...string) (*exec.Cmd, *syncBuffer) {
	t.Helper()
	cmd := exec.Command(mitsumeBin, append([]string{"watch"}, args...)...)
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start watch: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	return cmd, stderr
}

// stopWatch は SIGTERM を送り、exit 0 で終わることを確かめる。
func stopWatch(t *testing.T, cmd *exec.Cmd, stderr *syncBuffer) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("watch should exit 0 on SIGTERM, got %v\nstderr: %s", err, stderr.String())
	}
}

func TestIntegrationWatch_StartupSentBeforeFirstFailureAndShutdownLast(t *testing.T) {
	dir := t.TempDir()
	webhookURL, received := captureWebhook(t)
	cfg := writeConfigJSON(t, dir, oneShotHTTPConfig(failingTarget(t)))
	cmd, stderr := startWatch(t, webhookURL, "--config", cfg)
	if !waitFor(10*time.Second, func() bool { return len(received()) >= 2 }) {
		t.Fatalf("expected startup and failure notices, got %v\nstderr: %s", received(), stderr.String())
	}
	stopWatch(t, cmd, stderr)

	calls := received()
	if len(calls) != 3 {
		t.Fatalf("expected exactly 3 notices (started, failed, stopped), got %d: %v", len(calls), calls)
	}
	wants := []string{
		"[mitsume] watch started on host=it-host (checks=1, time=",
		"[mitsume] api failed (http: status=500, want=200)",
		"[mitsume] watch stopped on host=it-host (signal=SIGTERM, time=",
	}
	for i, want := range wants {
		if !strings.Contains(calls[i], want) {
			t.Fatalf("notice %d should contain %q, got %s", i, want, calls[i])
		}
	}
}

func TestIntegrationWatch_DryRunPrintsStartupToStderrOnly(t *testing.T) {
	dir := t.TempDir()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	webhookURL, received := captureWebhook(t)
	cfg := writeConfigJSON(t, dir, oneShotHTTPConfig(target.URL))
	cmd, stderr := startWatch(t, webhookURL, "--config", cfg, "--dry-run")
	if !waitFor(10*time.Second, func() bool { return strings.Contains(stderr.String(), "watch started") }) {
		t.Fatalf("startup notice not printed to stderr: %s", stderr.String())
	}
	stopWatch(t, cmd, stderr)

	if got := received(); len(got) != 0 {
		t.Fatalf("dry-run must not POST, got %v", got)
	}
	if !strings.Contains(stderr.String(), "watch started on host=it-host (checks=1, time=") {
		t.Fatalf("stderr should carry the startup text, got %s", stderr.String())
	}
}

func TestIntegrationWatch_StartupNotSentWhenStartupFails(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string) string
	}{
		{
			name: "unknown check type",
			setup: func(t *testing.T, dir string) string {
				t.Helper()

				return writeConfigJSON(t, dir, `{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [{"type": "nope", "interval": "1h", "expect": {}}]
}`)
			},
		},
		{
			name: "corrupt heartbeat file",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				hb := filepath.Join(dir, "hb.json")
				if err := os.WriteFile(hb, []byte("{not json"), 0o600); err != nil {
					t.Fatalf("write heartbeat: %v", err)
				}

				return writeConfigJSON(t, dir, fmt.Sprintf(`{
  "heartbeat_file": %q,
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}]
}`, hb))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			webhookURL, received := captureWebhook(t)
			cfg := tc.setup(t, t.TempDir())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, mitsumeBin, "watch", "--config", cfg)
			cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
			cmd.Dir = t.TempDir()
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("expected exit 1, got %v", err)
			}
			if got := received(); len(got) != 0 {
				t.Fatalf("no notice should be sent when startup fails, got %v", got)
			}
		})
	}
}

func TestIntegrationWatch_StartupRejectedStillEvaluates(t *testing.T) {
	dir := t.TempDir()
	webhookURL, received := webhookWithStatus(t, func(n int) int {
		if n == 0 {
			return http.StatusBadRequest
		}

		return http.StatusOK
	})
	cfg := writeConfigJSON(t, dir, oneShotHTTPConfig(failingTarget(t)))
	cmd, stderr := startWatch(t, webhookURL, "--config", cfg)
	if !waitFor(10*time.Second, func() bool { return len(received()) >= 2 }) {
		t.Fatalf("expected the failure notice after a rejected startup notice, got %v\nstderr: %s",
			received(), stderr.String())
	}
	stopWatch(t, cmd, stderr)

	calls := received()
	if !strings.Contains(calls[0], "watch started") {
		t.Fatalf("first POST should be the startup notice, got %s", calls[0])
	}
	if !strings.Contains(calls[1], "[mitsume] api failed (http: status=500, want=200)") {
		t.Fatalf("second POST should be the failure notice, got %s", calls[1])
	}
	if !strings.Contains(stderr.String(), "startup notify failed") {
		t.Fatalf("stderr should report the rejected startup notice, got %s", stderr.String())
	}
}

func TestIntegrationWatch_SigTermSendsShutdownAnnouncement(t *testing.T) {
	dir := t.TempDir()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	webhookURL, received := captureWebhook(t)
	cfgBody := fmt.Sprintf(`{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "http", "name": "keepalive", "url": %q, "interval": "1h",
     "confirm": false, "expect": {"status": 200}}
  ]
}`, target.URL)
	cfg := writeConfigJSON(t, dir, cfgBody)
	cmd := exec.Command(mitsumeBin, "watch", "--config", cfg)
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start watch: %v", err)
	}
	// 起動即時 evaluate → alert 無し (200 応答)。ある程度時間を置いてから SIGTERM。
	time.Sleep(300 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("watch should exit 0 on SIGTERM, got %v\nstderr: %s", err, stderr.String())
	}
	// 停止の通知が 1 通届き、signal 名 (SIGTERM) が text に含まれる
	// ことを確認する。両方 assert することで signal 名 capture 経路の取りこぼしも
	// 検出する。
	calls := received()
	if len(calls) < 1 {
		t.Fatalf("expected at least 1 shutdown announcement, got %d\nstderr: %s", len(calls), stderr.String())
	}
	found := false
	for _, c := range calls {
		if strings.Contains(c, "watch stopped") && strings.Contains(c, "signal=SIGTERM") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no shutdown announcement with signal=SIGTERM in %d POSTs: %v", len(calls), calls)
	}
}

func TestIntegrationWatch_ConfigNotFoundExits1(t *testing.T) {
	cmd := exec.Command(mitsumeBin, "watch", "--config", "/nonexistent/mitsume.json")
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
