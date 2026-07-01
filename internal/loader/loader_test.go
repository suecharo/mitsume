package loader

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suecharo/mitsume/internal/config"
)

func mkConfig(t *testing.T, raw string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mitsume.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("MITSUME_SLACK_WEBHOOK_URL", "https://slack.example.com")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	return cfg
}

func TestBuildCheckers_MixedTypes(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h", "timeout": "10s"},
		"checks": [
			{"type": "http", "name": "api", "url": "https://x.example.com", "expect": {"status": 200}},
			{"type": "deadman", "job": "backup", "expect": {"within": "25h"}},
			{"type": "file", "name": "backup-file", "path": "/tmp/backup", "expect": {"exists": true}},
			{"type": "cmd", "command": ["/bin/true"], "expect": {"exit_code": 0}}
		]
	}`
	cfg := mkConfig(t, raw)
	checkers, err := BuildCheckers(cfg, Options{HeartbeatFile: "/tmp/hb.json"})
	if err != nil {
		t.Fatalf("BuildCheckers: %v", err)
	}
	if len(checkers) != 4 {
		t.Fatalf("len=%d, want 4", len(checkers))
	}
	types := make([]string, len(checkers))
	for i, c := range checkers {
		types[i] = c.Type()
	}
	want := []string{"http", "deadman", "file", "cmd"}
	for i, wt := range want {
		if types[i] != wt {
			t.Errorf("types[%d]=%s, want %s", i, types[i], wt)
		}
	}
}

func TestBuildCheckers_DefaultsInherit(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "2h", "timeout": "20s"},
		"checks": [
			{"type": "http", "url": "https://x.example.com", "expect": {"status": 200}}
		]
	}`
	cfg := mkConfig(t, raw)
	checkers, err := BuildCheckers(cfg, Options{})
	if err != nil {
		t.Fatalf("BuildCheckers: %v", err)
	}
	if checkers[0].Interval() != 2*time.Hour {
		t.Fatalf("interval=%s, want 2h", checkers[0].Interval())
	}
}

func TestBuildCheckers_ExplicitOverridesDefaults(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "2h"},
		"checks": [
			{"type": "http", "url": "https://x.example.com", "interval": "30m", "expect": {"status": 200}}
		]
	}`
	cfg := mkConfig(t, raw)
	checkers, _ := BuildCheckers(cfg, Options{})
	if checkers[0].Interval() != 30*time.Minute {
		t.Fatalf("interval=%s, want 30m", checkers[0].Interval())
	}
}

func TestBuildCheckers_NameDuplicateExplicit(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "http", "name": "same", "url": "https://a", "expect": {"status": 200}},
			{"type": "http", "name": "same", "url": "https://b", "expect": {"status": 200}}
		]
	}`
	cfg := mkConfig(t, raw)
	if _, err := BuildCheckers(cfg, Options{}); err == nil {
		t.Fatalf("expected duplicate name error")
	}
}

func TestBuildCheckers_NameDuplicateAutoAndExplicit(t *testing.T) {
	// http は url を name に、file は path を name に自動生成。同じ文字列で重複を作る。
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "http", "url": "same-string", "expect": {"status": 200}},
			{"type": "file", "name": "same-string", "path": "/tmp/a", "expect": {"exists": true}}
		]
	}`
	cfg := mkConfig(t, raw)
	if _, err := BuildCheckers(cfg, Options{}); err == nil {
		t.Fatalf("expected duplicate name error")
	}
}

func TestBuildCheckers_NameDuplicateBothAuto(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "http", "url": "https://x.example.com/a", "expect": {"status": 200}},
			{"type": "http", "url": "https://x.example.com/a", "expect": {"status": 200}}
		]
	}`
	cfg := mkConfig(t, raw)
	if _, err := BuildCheckers(cfg, Options{}); err == nil {
		t.Fatalf("expected duplicate name error")
	}
}

func TestBuildCheckers_HTTPUnknownField(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "http", "url": "https://x", "expect": {"status": 200}, "wat": 1}
		]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "mitsume.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("%v", err)
	}
	t.Setenv("MITSUME_SLACK_WEBHOOK_URL", "https://slack.example.com")
	cfg, err := config.Parse(path)
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	if _, err := BuildCheckers(cfg, Options{}); err == nil {
		t.Fatalf("expected error for unknown field")
	}
}

func TestBuildCheckers_DeadmanJobFieldOnNonDeadmanRejected(t *testing.T) {
	// http checker に job field を書いても DisallowUnknownFields で reject される
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "http", "url": "https://x", "job": "backup", "expect": {"status": 200}}
		]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "mitsume.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("%v", err)
	}
	t.Setenv("MITSUME_SLACK_WEBHOOK_URL", "https://slack.example.com")
	cfg, err := config.Parse(path)
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	_, err = BuildCheckers(cfg, Options{HeartbeatFile: "/tmp/hb.json"})
	if err == nil {
		t.Fatalf("expected error for job on http checker")
	}
	if !strings.Contains(err.Error(), "job") && !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error should mention job / unknown, got: %v", err)
	}
}

func TestBuildCheckers_ConfirmFalseOK(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "http", "url": "https://x", "confirm": false, "expect": {"status": 200}}
		]
	}`
	cfg := mkConfig(t, raw)
	checkers, err := BuildCheckers(cfg, Options{})
	if err != nil {
		t.Fatalf("BuildCheckers: %v", err)
	}
	if !checkers[0].Confirm().OneStrike {
		t.Fatalf("expected OneStrike")
	}
}

func TestBuildCheckers_ConfirmInvalidRejected(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "http", "url": "https://x", "confirm": {"checks": 0}, "expect": {"status": 200}}
		]
	}`
	cfg := mkConfig(t, raw)
	if _, err := BuildCheckers(cfg, Options{}); err == nil {
		t.Fatalf("expected error for confirm.checks=0")
	}
}

func TestBuildCheckers_DeadmanNeedsHeartbeatFile(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "deadman", "job": "backup", "expect": {"within": "25h"}}
		]
	}`
	cfg := mkConfig(t, raw)
	if _, err := BuildCheckers(cfg, Options{}); err == nil {
		t.Fatalf("expected error for missing HeartbeatFile")
	}
}

func TestBuildCheckers_ErrorIndexInMessage(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "http", "url": "https://x", "expect": {"status": 200}},
			{"type": "unknown-type", "expect": {}}
		]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "mitsume.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("%v", err)
	}
	t.Setenv("MITSUME_SLACK_WEBHOOK_URL", "https://slack.example.com")
	cfg, err := config.Parse(path)
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	// Load 側で reject されるので Parse 経由で cfg を作る
	_, err = BuildCheckers(cfg, Options{})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "checks[1]") {
		t.Errorf("error should mention index checks[1], got: %v", err)
	}
}

// fakeEngineSocket は net.Listen("unix", ...) で fake Docker Engine socket を
// 立て、path を返す。container branch を loader.BuildCheckers 経由で貫通させる。
func fakeEngineSocket(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "engine.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: handler, ReadTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = l.Close()
	})

	return sock
}

func TestBuildCheckers_ContainerBranchViaFakeSocket(t *testing.T) {
	sock := fakeEngineSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"State": {"Status": "running"}}`)
	}))
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "container", "container": "myapp", "expect": {"running": true}}
		]
	}`
	cfg := mkConfig(t, raw)
	checkers, err := BuildCheckers(cfg, Options{ContainerSocketPath: sock})
	if err != nil {
		t.Fatalf("BuildCheckers: %v", err)
	}
	if len(checkers) != 1 || checkers[0].Type() != "container" {
		t.Fatalf("got %d checkers, first type=%s", len(checkers), checkers[0].Type())
	}
	if r := checkers[0].Evaluate(context.Background()); !r.OK {
		t.Fatalf("expected Evaluate OK, got %+v", r)
	}
}

func TestBuildCheckers_EmptyChecksReturnsEmptySlice(t *testing.T) {
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"}
	}`
	cfg := mkConfig(t, raw)
	checkers, err := BuildCheckers(cfg, Options{})
	if err != nil {
		t.Fatalf("BuildCheckers: %v", err)
	}
	if len(checkers) != 0 {
		t.Fatalf("len=%d, want 0", len(checkers))
	}
}

func TestBuildCheckers_PassesConsumingRaw(t *testing.T) {
	// json.RawMessage が config.Load → BuildCheckers で正しく流れる
	raw := `{
		"notify": {"webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"},
		"defaults": {"interval": "1h"},
		"checks": [
			{"type": "http", "name": "test", "url": "https://x", "expect": {"status": 200}}
		]
	}`
	cfg := mkConfig(t, raw)
	var rawJSON []byte
	rawJSON, _ = json.Marshal(map[string]interface{}{"type": "http", "url": "https://y", "expect": map[string]int{"status": 200}, "name": "extra"})
	// mutate cfg.Checks に別 raw を追加する形の test 経路 (直接 raw を流す)
	cfg.Checks = append(cfg.Checks, rawJSON)
	// interval 未指定 + defaults 1h → 1h
	checkers, err := BuildCheckers(cfg, Options{})
	if err != nil {
		t.Fatalf("BuildCheckers: %v", err)
	}
	if len(checkers) != 2 {
		t.Fatalf("len=%d, want 2", len(checkers))
	}
	if checkers[1].Name() != "extra" || checkers[1].Type() != "http" {
		t.Fatalf("got name=%s type=%s", checkers[1].Name(), checkers[1].Type())
	}
}
