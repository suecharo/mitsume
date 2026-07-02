package integration

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
	// shutdown announcement 1 通が届いていることを確認 (docs/cli.md § watch § 動作)。
	// signal 名も text に含まれる (docs/notify.md § Shutdown announcement payload の
	// 慣用名 SIGTERM)。この 2 つを両方 assert することで signal 名 capture 経路の
	// regression も検出する。
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
