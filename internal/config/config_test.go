package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suecharo/mitsume/internal/config"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}

	return p
}

const minimalConfig = `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "checks": []
}`

func TestSearch_CLIPathTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "mitsume.json", minimalConfig)
	cli := writeFile(t, dir, "explicit.json", minimalConfig)
	t.Setenv(config.EnvKey, "/nonexistent/should/be/ignored")
	path, found, err := config.Search(cli, dir)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !found || path != cli {
		t.Fatalf("got path=%s found=%v, want path=%s found=true", path, found, cli)
	}
}

func TestSearch_EnvUsedWhenCLIEmpty(t *testing.T) {
	dir := t.TempDir()
	envPath := writeFile(t, dir, "env.json", minimalConfig)
	t.Setenv(config.EnvKey, envPath)
	path, found, err := config.Search("", dir)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !found || path != envPath {
		t.Fatalf("got path=%s found=%v, want %s", path, found, envPath)
	}
}

func TestSearch_EnvBeatsCWDWhenBothPresent(t *testing.T) {
	envDir := t.TempDir()
	cwdDir := t.TempDir()
	envPath := writeFile(t, envDir, "env.json", minimalConfig)
	writeFile(t, cwdDir, "mitsume.json", minimalConfig)
	t.Setenv(config.EnvKey, envPath)
	path, found, err := config.Search("", cwdDir)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !found || path != envPath {
		t.Fatalf("env should win over cwd: got path=%s, want %s", path, envPath)
	}
}

func TestSearch_CLIBeatsEnvAndCWDWhenAllPresent(t *testing.T) {
	dir := t.TempDir()
	cwdDir := t.TempDir()
	cliPath := writeFile(t, dir, "explicit.json", minimalConfig)
	envPath := writeFile(t, dir, "env.json", minimalConfig)
	writeFile(t, cwdDir, "mitsume.json", minimalConfig)
	t.Setenv(config.EnvKey, envPath)
	path, found, err := config.Search(cliPath, cwdDir)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !found || path != cliPath {
		t.Fatalf("cli should win over env and cwd: got path=%s, want %s", path, cliPath)
	}
}

func TestSearch_CWDDefaultUsedWhenBothEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "mitsume.json", minimalConfig)
	t.Setenv(config.EnvKey, "")
	path, found, err := config.Search("", dir)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := filepath.Join(dir, "mitsume.json")
	if !found || path != want {
		t.Fatalf("got path=%s found=%v, want %s", path, found, want)
	}
}

func TestSearch_CWDDefaultAbsentIsNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvKey, "")
	path, found, err := config.Search("", dir)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if found || path != "" {
		t.Fatalf("expected not found, got path=%s found=%v", path, found)
	}
}

func TestSearch_EmptyCWDDefaultsToNotFound(t *testing.T) {
	t.Setenv(config.EnvKey, "")
	_, found, err := config.Search("", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if found {
		t.Fatalf("expected not found when cwd is empty and no env / cli")
	}
}

func TestSearch_CLIPathMissingIsError(t *testing.T) {
	_, _, err := config.Search("/nonexistent/mitsume.json", "")
	if err == nil {
		t.Fatalf("expected error for missing --config path")
	}
}

func TestSearch_EnvPathMissingIsError(t *testing.T) {
	t.Setenv(config.EnvKey, "/nonexistent/mitsume.json")
	_, _, err := config.Search("", "")
	if err == nil {
		t.Fatalf("expected error for missing $%s path", config.EnvKey)
	}
}

func TestLoad_MinimalConfigParses(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "https://hooks.slack.example/x")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", minimalConfig)
	c, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Notify.WebhookURLEnv != "TEST_WEBHOOK" {
		t.Fatalf("webhook_url_env = %q", c.Notify.WebhookURLEnv)
	}
	if c.SourcePath != p {
		t.Fatalf("SourcePath = %q, want %q", c.SourcePath, p)
	}
}

func TestLoad_WebhookEnvUndefinedFailsFast(t *testing.T) {
	_ = os.Unsetenv("TEST_WEBHOOK_UNDEF")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK_UNDEF" },
  "checks": []
}`)
	_, err := config.Load(p)
	if err == nil {
		t.Fatalf("expected fail-fast on undefined webhook env")
	}
	if !strings.Contains(err.Error(), "TEST_WEBHOOK_UNDEF") {
		t.Fatalf("error should name the missing env: %v", err)
	}
}

func TestLoad_MissingWebhookEnvFieldIsError(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", `{
  "notify": {},
  "checks": []
}`)
	_, err := config.Load(p)
	if err == nil {
		t.Fatalf("expected error when webhook_url_env is empty")
	}
}

func TestLoad_UnknownTopLevelFieldIsError(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "checks": [],
  "unknown_top_level": true
}`)
	_, err := config.Load(p)
	if err == nil {
		t.Fatalf("expected error for unknown top-level field")
	}
}

func TestLoad_DefaultsDurationParses(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "defaults": { "interval": "1d", "timeout": "10s" },
  "checks": []
}`)
	c, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Defaults.Interval.IsSet() {
		t.Fatalf("Interval should be set")
	}
	if got := c.Defaults.Interval.Value(); got != 24*time.Hour {
		t.Fatalf("Interval = %v, want 24h", got)
	}
	if got := c.Defaults.Timeout.Value(); got != 10*time.Second {
		t.Fatalf("Timeout = %v, want 10s", got)
	}
}

func TestLoad_DefaultsDurationParseError(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "defaults": { "interval": "1w" },
  "checks": []
}`)
	_, err := config.Load(p)
	if err == nil {
		t.Fatalf("expected error for unsupported duration w")
	}
}

func TestLoad_CheckTypeIsRequired(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "checks": [ { "url": "https://a" } ]
}`)
	_, err := config.Load(p)
	if err == nil {
		t.Fatalf("expected error when check.type missing")
	}
}

func TestLoad_UnknownCheckTypeIsError(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "checks": [ { "type": "banana" } ]
}`)
	_, err := config.Load(p)
	if err == nil {
		t.Fatalf("expected error for unknown check type")
	}
}

func TestLoad_DeadmanRequiresJob(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "checks": [ { "type": "deadman", "expect": { "within": "25h" } } ]
}`)
	_, err := config.Load(p)
	if err == nil {
		t.Fatalf("expected error when deadman job missing")
	}
}

func TestLoad_DeadmanJobNamingRegex(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	tests := []struct {
		name    string
		job     string
		wantErr bool
	}{
		{"simple", "nightly-backup", false},
		{"underscore", "renew_cert", false},
		{"digits", "job123", false},
		{"upper", "Job", false},
		{"empty", "", true},
		{"dot", "job.name", true},
		{"slash", "job/name", true},
		{"space", "job name", true},
		{"unicode", "ジョブ", true},
		{"len1", "a", false},
		{"len64", strings.Repeat("a", 64), false},
		{"len65", strings.Repeat("a", 65), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "checks": [ { "type": "deadman", "job": "` + tt.job + `", "expect": { "within": "25h" } } ]
}`
			p := writeFile(t, t.TempDir(), "mitsume.json", body)
			_, err := config.Load(p)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for job %q", tt.job)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for job %q: %v", tt.job, err)
			}
		})
	}
}

func TestLoad_DeadmanJobDuplicateIsError(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "checks": [
    { "type": "deadman", "job": "same", "expect": { "within": "25h" } },
    { "type": "deadman", "job": "same", "expect": { "within": "1h" } }
  ]
}`)
	_, err := config.Load(p)
	if err == nil {
		t.Fatalf("expected error for duplicate deadman job")
	}
}

func TestDeadmanJobs_ReturnsAllDeadmanJobsInOrder(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	dir := t.TempDir()
	p := writeFile(t, dir, "mitsume.json", `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "checks": [
    { "type": "http", "url": "https://a", "expect": { "status": 200 } },
    { "type": "deadman", "job": "nightly-backup", "expect": { "within": "25h" } },
    { "type": "http", "url": "https://b", "expect": { "status": 200 } },
    { "type": "deadman", "job": "renew-cert", "expect": { "within": "24h" } }
  ]
}`)
	c, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	jobs := c.DeadmanJobs()
	want := []string{"nightly-backup", "renew-cert"}
	if len(jobs) != len(want) {
		t.Fatalf("got %v, want %v", jobs, want)
	}
	for i, j := range jobs {
		if j != want[i] {
			t.Fatalf("[%d] got %s, want %s", i, j, want[i])
		}
	}
}

func TestDeadmanJobs_NoneReturnsNil(t *testing.T) {
	t.Setenv("TEST_WEBHOOK", "x")
	p := writeFile(t, t.TempDir(), "mitsume.json", `{
  "notify": { "webhook_url_env": "TEST_WEBHOOK" },
  "checks": [ { "type": "http", "url": "https://a", "expect": { "status": 200 } } ]
}`)
	c, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if jobs := c.DeadmanJobs(); jobs != nil {
		t.Fatalf("expected nil, got %v", jobs)
	}
}
