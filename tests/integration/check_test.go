package integration

import (
	"encoding/json"
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
	"sync/atomic"
	"testing"
	"time"
)

// writeConfigJSON は config JSON を tmp dir に書いて path を返す。
func writeConfigJSON(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "mitsume.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

// writeHeartbeatJSON は heartbeat file を tmp dir に書いて path を返す。
// jobs は job 名 → last_ping_at (RFC3339) の map。
func writeHeartbeatJSON(t *testing.T, dir, name string, jobs map[string]time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	obj := struct {
		Jobs map[string]struct {
			LastPingAt string `json:"last_ping_at"`
		} `json:"jobs"`
	}{Jobs: map[string]struct {
		LastPingAt string `json:"last_ping_at"`
	}{}}
	for k, v := range jobs {
		obj.Jobs[k] = struct {
			LastPingAt string `json:"last_ping_at"`
		}{LastPingAt: v.Format(time.RFC3339)}
	}
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	return path
}

// captureWebhook は Slack Incoming Webhook を模した httptest server を立て、
// 受信 payload を全部貯めて返す。tests/README.md § cross-boundary の Slack
// 通知検証用。cleanup で srv.Close は t.Cleanup 経由。
func captureWebhook(t *testing.T) (url string, received func() []string) {
	t.Helper()
	var (
		mu    sync.Mutex
		calls []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	return srv.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(calls))
		copy(out, calls)

		return out
	}
}

func TestIntegrationCheck_HttpSuccessSendsNoAlert(t *testing.T) {
	dir := t.TempDir()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	webhookURL, received := captureWebhook(t)
	cfgBody := fmt.Sprintf(`{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "http", "name": "ok", "url": %q, "interval": "1h", "expect": {"status": 200}}
  ]
}`, target.URL)
	cfg := writeConfigJSON(t, dir, cfgBody)
	cmd := exec.Command(mitsumeBin, "check", "--config", cfg)
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume check failed: %v\nstderr: %s", err, stderr.String())
	}
	if got := len(received()); got != 0 {
		t.Fatalf("expected 0 alerts on success, got %d (payloads: %v)", got, received())
	}
}

func TestIntegrationCheck_HttpFailureAlerts(t *testing.T) {
	dir := t.TempDir()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()
	webhookURL, received := captureWebhook(t)
	cfgBody := fmt.Sprintf(`{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "http", "name": "bad-endpoint", "url": %q, "interval": "1h",
     "confirm": false, "expect": {"status": 200}}
  ]
}`, target.URL)
	cfg := writeConfigJSON(t, dir, cfgBody)
	cmd := exec.Command(mitsumeBin, "check", "--config", cfg)
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// 個別 failure でも exit 0 (docs/cli.md § check § exit code)
	if err := cmd.Run(); err != nil {
		t.Fatalf("check must exit 0 even on individual failure: %v\nstderr: %s", err, stderr.String())
	}
	calls := received()
	if len(calls) != 1 {
		t.Fatalf("expected 1 alert, got %d (payloads: %v)", len(calls), calls)
	}
	if !strings.Contains(calls[0], "bad-endpoint") {
		t.Fatalf("alert payload must include check name, got %s", calls[0])
	}
	if !strings.Contains(calls[0], "\"danger\"") {
		t.Fatalf("alert payload must have danger color attachment, got %s", calls[0])
	}
}

func TestIntegrationCheck_DeadmanFreshPingNoAlert(t *testing.T) {
	dir := t.TempDir()
	webhookURL, received := captureWebhook(t)
	hb := writeHeartbeatJSON(t, dir, "mitsume.heartbeat.json", map[string]time.Time{
		"backup": time.Now().Add(-1 * time.Hour),
	})
	cfgBody := `{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "deadman", "job": "backup", "interval": "1h",
     "confirm": false, "expect": {"within": "25h"}}
  ]
}`
	cfg := writeConfigJSON(t, dir, cfgBody)
	cmd := exec.Command(mitsumeBin, "check", "--config", cfg, "--heartbeat-file", hb)
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume check failed: %v\nstderr: %s", err, stderr.String())
	}
	if got := len(received()); got != 0 {
		t.Fatalf("expected 0 alerts for fresh ping, got %d", got)
	}
}

func TestIntegrationCheck_DeadmanStalePingAlerts(t *testing.T) {
	dir := t.TempDir()
	webhookURL, received := captureWebhook(t)
	hb := writeHeartbeatJSON(t, dir, "mitsume.heartbeat.json", map[string]time.Time{
		"backup": time.Now().Add(-30 * time.Hour), // stale (> within=25h)
	})
	cfgBody := `{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "deadman", "name": "nightly-backup", "job": "backup", "interval": "1h",
     "confirm": false, "expect": {"within": "25h"}}
  ]
}`
	cfg := writeConfigJSON(t, dir, cfgBody)
	cmd := exec.Command(mitsumeBin, "check", "--config", cfg, "--heartbeat-file", hb)
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume check failed: %v\nstderr: %s", err, stderr.String())
	}
	calls := received()
	if len(calls) != 1 {
		t.Fatalf("expected 1 alert for stale ping, got %d (%v)", len(calls), calls)
	}
	if !strings.Contains(calls[0], "nightly-backup") {
		t.Fatalf("alert payload must include check name, got %s", calls[0])
	}
}

func TestIntegrationCheck_DryRunSkipsSlackAndPrintsPayload(t *testing.T) {
	dir := t.TempDir()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()
	webhookURL, received := captureWebhook(t)
	cfgBody := fmt.Sprintf(`{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "http", "name": "api", "url": %q, "interval": "1h",
     "confirm": false, "expect": {"status": 200}}
  ]
}`, target.URL)
	cfg := writeConfigJSON(t, dir, cfgBody)
	cmd := exec.Command(mitsumeBin, "check", "--config", cfg, "--dry-run")
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume check --dry-run failed: %v\nstderr: %s", err, stderr.String())
	}
	if got := len(received()); got != 0 {
		t.Fatalf("dry-run must not POST, got %d POSTs", got)
	}
	if !strings.Contains(stderr.String(), "api") {
		t.Fatalf("dry-run stderr must include check name, got: %s", stderr.String())
	}
}

func TestIntegrationCheck_ConfigNotFoundExits1(t *testing.T) {
	cmd := exec.Command(mitsumeBin, "check", "--config", "/nonexistent/mitsume.json")
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

func TestIntegrationCheck_DeadmanAdjacentHeartbeatFallbackWithNoPingAlerts(t *testing.T) {
	// heartbeat file を明示せず config 隣接 (mitsume.heartbeat.json) の自動探索に
	// 委ねる。ファイルが存在しない場合、heartbeat.Load は空 File を返すので
	// PreflightHeartbeat は成功、deadman は "job has never been pinged" で failure
	// → alert 1 通、exit 0 (docs/heartbeat.md § 読み込みモデル)。
	dir := t.TempDir()
	webhookURL, received := captureWebhook(t)
	cfgBody := `{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "deadman", "job": "backup", "interval": "1h",
     "confirm": false, "expect": {"within": "25h"}}
  ]
}`
	cfg := writeConfigJSON(t, dir, cfgBody)
	cmd := exec.Command(mitsumeBin, "check", "--config", cfg)
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume check failed: %v\nstderr: %s", err, stderr.String())
	}
	calls := received()
	if len(calls) != 1 {
		t.Fatalf("expected 1 alert for never-pinged deadman, got %d (%v)", len(calls), calls)
	}
}

func TestIntegrationCheck_UsesMitsumeConfigEnv(t *testing.T) {
	dir := t.TempDir()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()
	webhookURL, received := captureWebhook(t)
	cfgBody := fmt.Sprintf(`{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "http", "name": "env-cfg", "url": %q, "interval": "1h",
     "confirm": false, "expect": {"status": 200}}
  ]
}`, target.URL)
	cfg := writeConfigJSON(t, dir, cfgBody)
	// --config を渡さず MITSUME_CONFIG のみで解決させる。cwd も config が
	// 見えない場所にして flag / env / cwd fallback の混同を除外する。
	cmd := exec.Command(mitsumeBin, "check")
	cmd.Env = append(envWithout("MITSUME_"),
		"MITSUME_CONFIG="+cfg,
		"MITSUME_SLACK_WEBHOOK_URL="+webhookURL,
	)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume check via MITSUME_CONFIG failed: %v\nstderr: %s", err, stderr.String())
	}
	calls := received()
	if len(calls) != 1 {
		t.Fatalf("expected 1 alert (config loaded via env), got %d (%v)", len(calls), calls)
	}
	if !strings.Contains(calls[0], "env-cfg") {
		t.Fatalf("alert payload must include check name from env-loaded config, got %s", calls[0])
	}
}

func TestIntegrationCheck_UsesMitsumeHeartbeatFileEnv(t *testing.T) {
	dir := t.TempDir()
	webhookURL, received := captureWebhook(t)
	hb := writeHeartbeatJSON(t, dir, "custom.heartbeat.json", map[string]time.Time{
		"env-job": time.Now().Add(-30 * time.Hour), // stale (> within=25h)
	})
	cfgBody := `{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "deadman", "name": "env-deadman", "job": "env-job", "interval": "1h",
     "confirm": false, "expect": {"within": "25h"}}
  ]
}`
	cfg := writeConfigJSON(t, dir, cfgBody)
	// --heartbeat-file を渡さず MITSUME_HEARTBEAT_FILE のみで解決させる。
	// config file の heartbeat_file 未指定・cwd fallback も見つからない状態で
	// env だけを頼りに resolve できるかを見る。
	cmd := exec.Command(mitsumeBin, "check", "--config", cfg)
	cmd.Env = append(envWithout("MITSUME_"),
		"MITSUME_HEARTBEAT_FILE="+hb,
		"MITSUME_SLACK_WEBHOOK_URL="+webhookURL,
	)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume check via MITSUME_HEARTBEAT_FILE failed: %v\nstderr: %s", err, stderr.String())
	}
	calls := received()
	if len(calls) != 1 {
		t.Fatalf("expected 1 alert (heartbeat file resolved via env), got %d", len(calls))
	}
	if !strings.Contains(calls[0], "env-deadman") {
		t.Fatalf("alert payload must include check name, got %s", calls[0])
	}
}

func TestIntegrationCheck_HttpFailureBurstThreeAlertsOnce(t *testing.T) {
	dir := t.TempDir()
	var attempts int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()
	webhookURL, received := captureWebhook(t)
	// burst: checks=3, interval=1ms → 3 回 evaluate、alert 1 通
	cfgBody := fmt.Sprintf(`{
  "notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
  "checks": [
    {"type": "http", "name": "burst-api", "url": %q, "interval": "1h",
     "confirm": {"checks": 3, "interval": "1ms"},
     "expect": {"status": 200}}
  ]
}`, target.URL)
	cfg := writeConfigJSON(t, dir, cfgBody)
	cmd := exec.Command(mitsumeBin, "check", "--config", cfg)
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+webhookURL)
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume check failed: %v\nstderr: %s", err, stderr.String())
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("expected 3 HTTP attempts (initial + burst), got %d", got)
	}
	if calls := received(); len(calls) != 1 {
		t.Fatalf("expected exactly 1 alert (burst dedup), got %d", len(calls))
	}
}
