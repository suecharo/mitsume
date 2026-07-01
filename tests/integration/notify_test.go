package integration

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

func envWithout(prefixes ...string) []string {
	out := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		drop := false
		for _, p := range prefixes {
			if strings.HasPrefix(e, p) {
				drop = true

				break
			}
		}
		if !drop {
			out = append(out, e)
		}
	}

	return out
}

func TestIntegrationNotify_PostsPayloadToWebhook(t *testing.T) {
	var received int32
	var body atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&received, 1)
		b, _ := io.ReadAll(r.Body)
		body.Store(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cmd := exec.Command(mitsumeBin, "notify", "hello from integration")
	cmd.Dir = t.TempDir()
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+srv.URL)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume notify failed: %v\nstderr: %s", err, stderr.String())
	}
	if got := atomic.LoadInt32(&received); got != 1 {
		t.Fatalf("expected 1 POST, got %d", got)
	}
	raw, _ := body.Load().([]byte)
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, raw)
	}
	if p.Text != "hello from integration" {
		t.Fatalf("Text = %q", p.Text)
	}
}

func TestIntegrationNotify_DryRunDoesNotPost(t *testing.T) {
	var received int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&received, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cmd := exec.Command(mitsumeBin, "notify", "--dry-run", "hello dry")
	cmd.Dir = t.TempDir()
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+srv.URL)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mitsume notify --dry-run failed: %v\nstderr: %s", err, stderr.String())
	}
	if got := atomic.LoadInt32(&received); got != 0 {
		t.Fatalf("dry-run should not POST, got %d POSTs", got)
	}
	s := stderr.String()
	if !strings.Contains(s, `"text"`) {
		t.Fatalf("stderr should include payload text, got: %s", s)
	}
	if !strings.Contains(s, "hello dry") {
		t.Fatalf("stderr should include the msg, got: %s", s)
	}
}

func TestIntegrationNotify_4xxExits1WithoutRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	cmd := exec.Command(mitsumeBin, "notify", "x")
	cmd.Dir = t.TempDir()
	cmd.Env = append(envWithout("MITSUME_"), "MITSUME_SLACK_WEBHOOK_URL="+srv.URL)
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected exit 1 on 400")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if code := exitErr.ExitCode(); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 call (no retry on 4xx), got %d", got)
	}
}

func TestIntegrationNotify_MissingWebhookEnvExits1(t *testing.T) {
	cmd := exec.Command(mitsumeBin, "notify",
		"--slack-webhook-url-env", "MITSUME_INTEGRATION_MISSING_WEBHOOK", "x")
	cmd.Dir = t.TempDir()
	cmd.Env = envWithout("MITSUME_", "MITSUME_INTEGRATION_MISSING_WEBHOOK")
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected exit 1 for missing webhook env")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if code := exitErr.ExitCode(); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}
