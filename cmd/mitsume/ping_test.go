package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suecharo/mitsume/internal/config"
)

func TestResolveJob_PositionalWins(t *testing.T) {
	t.Setenv(jobEnvKey, "env-job")
	got, err := resolveJob([]string{"cli-job"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cli-job" {
		t.Fatalf("got %q, want cli-job", got)
	}
}

func TestResolveJob_EnvWhenNoPositional(t *testing.T) {
	t.Setenv(jobEnvKey, "env-job")
	got, err := resolveJob(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "env-job" {
		t.Fatalf("got %q, want env-job", got)
	}
}

func TestResolveJob_SingleDeadmanFromConfig(t *testing.T) {
	t.Setenv(jobEnvKey, "")
	cfg := &config.Config{
		Checks: []json.RawMessage{
			json.RawMessage(`{"type":"http","url":"https://a"}`),
			json.RawMessage(`{"type":"deadman","job":"nightly-backup"}`),
		},
	}
	got, err := resolveJob(nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "nightly-backup" {
		t.Fatalf("got %q, want nightly-backup", got)
	}
}

func TestResolveJob_MultipleDeadmanIsError(t *testing.T) {
	t.Setenv(jobEnvKey, "")
	cfg := &config.Config{
		Checks: []json.RawMessage{
			json.RawMessage(`{"type":"deadman","job":"a"}`),
			json.RawMessage(`{"type":"deadman","job":"b"}`),
		},
	}
	if _, err := resolveJob(nil, cfg); err == nil {
		t.Fatalf("expected error for multiple deadman entries")
	}
}

func TestResolveJob_NoSourceIsError(t *testing.T) {
	t.Setenv(jobEnvKey, "")
	if _, err := resolveJob(nil, nil); err == nil {
		t.Fatalf("expected error when nothing resolves")
	}
}

func TestResolveJob_RejectsInvalidNaming(t *testing.T) {
	t.Setenv(jobEnvKey, "")
	tests := map[string]string{
		"space":      "my job",
		"slash":      "job/name",
		"unicode":    "ジョブ",
		"tooLong":    strings.Repeat("a", 65),
		"newline":    "job\n",
		"dot":        "job.name",
		"pathEscape": "..",
	}
	for name, job := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := resolveJob([]string{job}, nil); err == nil {
				t.Fatalf("resolveJob(%q) should reject invalid identifier", job)
			}
		})
	}
}

func TestResolveJob_AcceptsBoundaryLengths(t *testing.T) {
	t.Setenv(jobEnvKey, "")
	tests := []string{
		"a",
		strings.Repeat("a", 64),
		"abc-def_012",
	}
	for _, job := range tests {
		t.Run(job, func(t *testing.T) {
			got, err := resolveJob([]string{job}, nil)
			if err != nil {
				t.Fatalf("resolveJob(%q) unexpected error: %v", job, err)
			}
			if got != job {
				t.Fatalf("got %q, want %q", got, job)
			}
		})
	}
}

func TestResolveJob_EnvValueIsAlsoValidated(t *testing.T) {
	t.Setenv(jobEnvKey, "bad name")
	if _, err := resolveJob(nil, nil); err == nil {
		t.Fatalf("expected env-provided job to be validated")
	}
}

func TestResolveJob_PositionalBeatsEnvAndConfigDeadman(t *testing.T) {
	t.Setenv(jobEnvKey, "env-job")
	cfg := &config.Config{
		Checks: []json.RawMessage{
			json.RawMessage(`{"type":"deadman","job":"cfg-only-deadman"}`),
		},
	}
	got, err := resolveJob([]string{"cli-job"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cli-job" {
		t.Fatalf("got %q, want cli-job", got)
	}
}

func TestResolveJob_EnvBeatsConfigDeadmanWhenPositionalEmpty(t *testing.T) {
	t.Setenv(jobEnvKey, "env-job")
	cfg := &config.Config{
		Checks: []json.RawMessage{
			json.RawMessage(`{"type":"deadman","job":"cfg-only-deadman"}`),
		},
	}
	got, err := resolveJob(nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "env-job" {
		t.Fatalf("got %q, want env-job", got)
	}
}

func TestResolveHeartbeatPath_CLIBeatsAllOthers(t *testing.T) {
	t.Setenv(heartbeatEnvKey, "/tmp/env-path")
	cfg := &config.Config{
		HeartbeatFile: "/srv/cfg-field.json",
		SourcePath:    "/etc/mitsume/mitsume.json",
	}
	got, err := resolveHeartbeatPath("/tmp/cli-path", cfg)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "/tmp/cli-path" {
		t.Fatalf("got %q, want /tmp/cli-path", got)
	}
}

func TestResolveHeartbeatPath_EnvBeatsConfigAndAdjacent(t *testing.T) {
	t.Setenv(heartbeatEnvKey, "/tmp/env-path")
	cfg := &config.Config{
		HeartbeatFile: "/srv/cfg-field.json",
		SourcePath:    "/etc/mitsume/mitsume.json",
	}
	got, err := resolveHeartbeatPath("", cfg)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "/tmp/env-path" {
		t.Fatalf("got %q, want /tmp/env-path", got)
	}
}

func TestResolveHeartbeatPath_ConfigFieldBeatsAdjacent(t *testing.T) {
	t.Setenv(heartbeatEnvKey, "")
	cfg := &config.Config{
		HeartbeatFile: "/srv/cfg-field.json",
		SourcePath:    "/etc/mitsume/mitsume.json",
	}
	got, err := resolveHeartbeatPath("", cfg)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "/srv/cfg-field.json" {
		t.Fatalf("got %q, want /srv/cfg-field.json", got)
	}
}

func TestResolveHeartbeatPath_CLIFlagWins(t *testing.T) {
	t.Setenv(heartbeatEnvKey, "/tmp/env-path")
	got, err := resolveHeartbeatPath("/tmp/cli-path", nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "/tmp/cli-path" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveHeartbeatPath_EnvWhenNoCLI(t *testing.T) {
	t.Setenv(heartbeatEnvKey, "/tmp/env-path")
	got, err := resolveHeartbeatPath("", nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "/tmp/env-path" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveHeartbeatPath_ConfigFieldWhenNoCLIorEnv(t *testing.T) {
	t.Setenv(heartbeatEnvKey, "")
	cfg := &config.Config{HeartbeatFile: "/srv/hb.json"}
	got, err := resolveHeartbeatPath("", cfg)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "/srv/hb.json" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveHeartbeatPath_AdjacentFallbackFromJSONExt(t *testing.T) {
	t.Setenv(heartbeatEnvKey, "")
	cfg := &config.Config{SourcePath: "/etc/mitsume/mitsume.json"}
	got, err := resolveHeartbeatPath("", cfg)
	if err != nil {
		t.Fatalf("%v", err)
	}
	want := filepath.Join("/etc/mitsume", "mitsume.heartbeat.json")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveHeartbeatPath_AdjacentFallbackFromArbitraryName(t *testing.T) {
	t.Setenv(heartbeatEnvKey, "")
	cfg := &config.Config{SourcePath: "/opt/config"}
	got, err := resolveHeartbeatPath("", cfg)
	if err != nil {
		t.Fatalf("%v", err)
	}
	want := filepath.Join("/opt", "config.heartbeat.json")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveHeartbeatPath_UnresolvableWithoutConfigIsError(t *testing.T) {
	t.Setenv(heartbeatEnvKey, "")
	if _, err := resolveHeartbeatPath("", nil); err == nil {
		t.Fatalf("expected error when nothing resolves")
	}
}

func TestResolveHeartbeatPath_UnresolvableWithEmptyConfigIsError(t *testing.T) {
	t.Setenv(heartbeatEnvKey, "")
	if _, err := resolveHeartbeatPath("", &config.Config{}); err == nil {
		t.Fatalf("expected error when config has neither heartbeat_file nor SourcePath")
	}
}
