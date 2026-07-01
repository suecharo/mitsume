package main

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
)

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestFormatVersion_IncludesAllFields(t *testing.T) {
	got := formatVersion("0.1.0", "abc123def", "2026-07-01T12:34:56Z", "go1.23.5")
	for _, want := range []string{
		"mitsume ",
		"version=0.1.0",
		"commit=abc123def",
		"built=2026-07-01T12:34:56Z",
		"go=go1.23.5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatVersion missing %q, got %q", want, got)
		}
	}
}

func TestFormatVersion_ArgumentOrderStable(t *testing.T) {
	got := formatVersion("VER", "COM", "DAT", "GOV")
	want := "mitsume version=VER, commit=COM, built=DAT, go=GOV"
	if got != want {
		t.Errorf("formatVersion order: got %q, want %q", got, want)
	}
}

func TestWriteVersion_WritesPackageVarsToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := writeVersion(&stdout, &stderr, nil, "go1.99.99-test")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty, got %q", stderr.String())
	}
	want := "mitsume version=" + version + ", commit=" + commit + ", built=" + date + ", go=go1.99.99-test\n"
	if stdout.String() != want {
		t.Errorf("stdout mismatch:\n got=%q\nwant=%q", stdout.String(), want)
	}
}

func TestWriteVersion_StdoutWriteError_Exits1(t *testing.T) {
	var stderr bytes.Buffer
	code := writeVersion(failWriter{}, &stderr, nil, "go1.23.5")
	if code != 1 {
		t.Fatalf("code = %d, want 1 on stdout write error", code)
	}
	if !strings.Contains(stderr.String(), "write failed") {
		t.Errorf("stderr should include the write error, got %q", stderr.String())
	}
}

func TestWriteVersion_ExtraArgExits1(t *testing.T) {
	for _, args := range [][]string{
		{"unexpected"},
		{"a", "b"},
		{""},
	} {
		var stdout, stderr bytes.Buffer
		code := writeVersion(&stdout, &stderr, args, "go1.23.5")
		if code != 1 {
			t.Fatalf("writeVersion(%q) = %d, want 1", args, code)
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout should be empty on error path, got %q", stdout.String())
		}
		if !strings.Contains(stderr.String(), "no arguments expected") {
			t.Errorf("stderr missing error message, got %q", stderr.String())
		}
	}
}

func TestRunVersion_NoArgsExits0(t *testing.T) {
	if code := runVersion(nil); code != 0 {
		t.Fatalf("runVersion(nil) = %d, want 0", code)
	}
	if code := runVersion([]string{}); code != 0 {
		t.Fatalf("runVersion([]) = %d, want 0", code)
	}
}

func TestRunVersion_ExtraArgExits1(t *testing.T) {
	if code := runVersion([]string{"unexpected"}); code != 1 {
		t.Fatalf("runVersion([unexpected]) = %d, want 1", code)
	}
}

func TestVersionDefaults_MatchSourceBuild(t *testing.T) {
	if version != "dev" {
		t.Errorf("version = %q, want dev", version)
	}
	if commit != "none" {
		t.Errorf("commit = %q, want none", commit)
	}
	if date != "unknown" {
		t.Errorf("date = %q, want unknown", date)
	}
	if !strings.HasPrefix(runtime.Version(), "go") {
		t.Errorf("runtime.Version() = %q, want go-prefixed", runtime.Version())
	}
}
