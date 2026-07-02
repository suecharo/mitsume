package deadman

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suecharo/mitsume/internal/heartbeat"
)

// setupHeartbeatFile は tmp dir に heartbeat file を書き出してパスを返す。
func setupHeartbeatFile(t *testing.T, jobs map[string]time.Time) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hb.json")
	file := &heartbeat.File{Jobs: map[string]heartbeat.Entry{}}
	for job, ts := range jobs {
		file.Jobs[job] = heartbeat.Entry{LastPingAt: ts}
	}
	if err := heartbeat.SaveAtomic(path, file); err != nil {
		t.Fatalf("save heartbeat: %v", err)
	}

	return path
}

func fixedClock(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

func TestParse_MinimalConfig(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Type() != "deadman" || c.Name() != "backup" || c.Job() != "backup" {
		t.Fatalf("got type=%s name=%s job=%s", c.Type(), c.Name(), c.Job())
	}
	if c.Interval() != time.Hour || c.Within() != 25*time.Hour {
		t.Fatalf("got interval=%s within=%s", c.Interval(), c.Within())
	}
	if c.Confirm().Checks != 3 {
		t.Fatalf("expected default confirm.checks=3, got %d", c.Confirm().Checks)
	}
}

func TestParse_TypeMismatchRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "http", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	if _, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"}); err == nil {
		t.Fatalf("expected error for type=http")
	}
}

func TestParse_MissingJobRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "interval": "1h", "expect": {"within": "25h"}}`)
	if _, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_InvalidJobNameRejected(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"type": "deadman", "job": "bad name", "interval": "1h", "expect": {"within": "25h"}}`,
		`{"type": "deadman", "job": "!!!", "interval": "1h", "expect": {"within": "25h"}}`,
		`{"type": "deadman", "job": "` + strings.Repeat("a", 65) + `", "interval": "1h", "expect": {"within": "25h"}}`,
	}
	for _, in := range cases {
		if _, err := Parse(json.RawMessage(in), Options{HeartbeatFile: "/tmp/hb.json"}); err == nil {
			t.Errorf("expected error for %s", in)
		}
	}
}

func TestParse_JobNameBoundary64(t *testing.T) {
	t.Parallel()
	job := strings.Repeat("a", 64)
	raw := json.RawMessage(`{"type": "deadman", "job": "` + job + `", "interval": "1h", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"})
	if err != nil {
		t.Fatalf("expected success for job of length 64: %v", err)
	}
	if c.Job() != job {
		t.Fatalf("Job mismatch")
	}
}

func TestParse_MissingWithinRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {}}`)
	if _, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"}); err == nil {
		t.Fatalf("expected error for missing within")
	}
}

func TestParse_WithinZeroRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "0s"}}`)
	if _, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"}); err == nil {
		t.Fatalf("expected error for within=0")
	}
}

func TestParse_MissingIntervalUsesDefaultsInterval(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{
		HeartbeatFile: "/tmp/hb.json",
		Defaults:      DefaultsFallback{Interval: 2 * time.Hour},
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Interval() != 2*time.Hour {
		t.Fatalf("Interval = %s, want 2h", c.Interval())
	}
}

func TestParse_MissingIntervalAndDefaultsRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "expect": {"within": "25h"}}`)
	if _, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"}); err == nil {
		t.Fatalf("expected error when both interval and defaults.interval are missing")
	}
}

func TestParse_ExplicitIntervalOverridesDefaults(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "30m", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{
		HeartbeatFile: "/tmp/hb.json",
		Defaults:      DefaultsFallback{Interval: 2 * time.Hour},
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Interval() != 30*time.Minute {
		t.Fatalf("Interval = %s, want 30m", c.Interval())
	}
}

func TestParse_MissingHeartbeatFileRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for empty HeartbeatFile")
	}
}

func TestParse_ExplicitName(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "name": "nightly", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Name() != "nightly" {
		t.Fatalf("Name = %s, want nightly", c.Name())
	}
}

func TestParse_UnknownFieldRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}, "wat": 1}`)
	if _, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"}); err == nil {
		t.Fatalf("expected error for unknown field")
	}
}

func TestParse_ConfirmFalse(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "confirm": false, "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !c.Confirm().OneStrike {
		t.Fatalf("OneStrike = false, want true")
	}
}

func TestParse_ConfirmInvalidRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "confirm": {"checks": 0}, "expect": {"within": "25h"}}`)
	if _, err := Parse(raw, Options{HeartbeatFile: "/tmp/hb.json"}); err == nil {
		t.Fatalf("expected error for confirm.checks=0")
	}
}

func TestEvaluate_JobPingedRecently(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	past := now.Add(-1 * time.Hour)
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": past})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{HeartbeatFile: hbFile, ClockNow: fixedClock(now)})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := c.Evaluate(context.Background())
	if !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func TestEvaluate_JobNeverPinged(t *testing.T) {
	t.Parallel()
	hbFile := setupHeartbeatFile(t, map[string]time.Time{})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{HeartbeatFile: hbFile})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure, got OK")
	}
	if !strings.Contains(r.Observed, "never pinged") {
		t.Errorf("Observed should mention never pinged, got %s", r.Observed)
	}
}

func TestEvaluate_HeartbeatFileMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hbFile := filepath.Join(dir, "nonexistent.json")
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{HeartbeatFile: hbFile})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure for missing heartbeat file")
	}
	if !strings.Contains(r.Observed, "never pinged") {
		t.Errorf("Observed should mention never pinged when file missing, got %s", r.Observed)
	}
}

func TestEvaluate_HeartbeatFileCorrupt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hbFile := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(hbFile, []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{HeartbeatFile: hbFile})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure for corrupt heartbeat file")
	}
	if !strings.Contains(r.Error, "read") && !strings.Contains(r.Error, "parse") {
		t.Errorf("Error should mention read/parse failure, got %s", r.Error)
	}
}

func TestEvaluate_ElapsedBelowWithin(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	// elapsed = 24h59m59s < within=25h → success
	past := now.Add(-(25*time.Hour - time.Second))
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": past})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, _ := Parse(raw, Options{HeartbeatFile: hbFile, ClockNow: fixedClock(now)})
	r := c.Evaluate(context.Background())
	if !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func TestEvaluate_ElapsedExactlyWithinBoundary(t *testing.T) {
	t.Parallel()
	// docs: 「now - last_ping_at >= expect.within」なら failure。
	// つまり elapsed == within は failure。
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	past := now.Add(-25 * time.Hour)
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": past})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, _ := Parse(raw, Options{HeartbeatFile: hbFile, ClockNow: fixedClock(now)})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure at boundary elapsed==within, got OK")
	}
}

func TestEvaluate_ElapsedAboveWithin(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	past := now.Add(-(25*time.Hour + time.Second))
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": past})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, _ := Parse(raw, Options{HeartbeatFile: hbFile, ClockNow: fixedClock(now)})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure, got OK")
	}
	if !strings.Contains(r.Observed, "last_ping") {
		t.Errorf("Observed should mention last_ping, got %s", r.Observed)
	}
	if !strings.Contains(r.Expected, "within=25h") {
		t.Errorf("Expected should mention within=25h, got %s", r.Expected)
	}
}

func TestEvaluate_FailureFormatsDurationsHumanReadable(t *testing.T) {
	t.Parallel()
	// docs/notify.md § Payload: observed は last_ping=25h12m ago のような
	// 可読形式。sub-second の生値 (15.043201262s) を載せない。
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	past := now.Add(-(15*time.Second + 43201262*time.Nanosecond))
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": past})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "2s"}}`)
	c, _ := Parse(raw, Options{HeartbeatFile: hbFile, ClockNow: fixedClock(now)})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure, got OK")
	}
	if r.Error != "no ping for 15s" {
		t.Errorf("Error = %q, want %q", r.Error, "no ping for 15s")
	}
	if r.Observed != "last_ping=15s ago" {
		t.Errorf("Observed = %q, want %q", r.Observed, "last_ping=15s ago")
	}
	if r.Expected != "within=2s" {
		t.Errorf("Expected = %q, want %q", r.Expected, "within=2s")
	}
}

func TestEvaluate_ExpectedTrimsZeroUnits(t *testing.T) {
	t.Parallel()
	// within=1h を 1h0m0s の Go 生表記ではなく 1h と載せる (docs/notify.md § Payload)。
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	past := now.Add(-(2*time.Hour + 123*time.Millisecond))
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": past})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "1h"}}`)
	c, _ := Parse(raw, Options{HeartbeatFile: hbFile, ClockNow: fixedClock(now)})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure, got OK")
	}
	if r.Observed != "last_ping=2h ago" {
		t.Errorf("Observed = %q, want %q", r.Observed, "last_ping=2h ago")
	}
	if r.Expected != "within=1h" {
		t.Errorf("Expected = %q, want %q", r.Expected, "within=1h")
	}
}

func TestEvaluate_ContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": time.Now()})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, _ := Parse(raw, Options{HeartbeatFile: hbFile})
	r := c.Evaluate(ctx)
	if r.OK {
		t.Fatalf("expected failure for canceled ctx")
	}
}

func TestEvaluate_NilClockUsesRealTime(t *testing.T) {
	t.Parallel()
	// 5s 前に ping → within=1h なら success
	past := time.Now().Add(-5 * time.Second)
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": past})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "1h"}}`)
	c, err := Parse(raw, Options{HeartbeatFile: hbFile})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := c.Evaluate(context.Background())
	if !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func TestEvaluate_SnapshotProviderOverridesFileLoad(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	// File 上には old timestamp があるが、provider は fresh timestamp を返す。
	// provider 優先なら success、file load ならば failure になる境界。
	old := now.Add(-30 * time.Hour)
	fresh := now.Add(-5 * time.Minute)
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": old})
	snapshot := &heartbeat.File{Jobs: map[string]heartbeat.Entry{"backup": {LastPingAt: fresh}}}
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{
		HeartbeatFile: hbFile,
		ClockNow:      fixedClock(now),
		SnapshotProvider: func() (*heartbeat.File, error) {
			return snapshot, nil
		},
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := c.Evaluate(context.Background())
	if !r.OK {
		t.Fatalf("expected OK from provider snapshot, got %+v", r)
	}
}

func TestEvaluate_SnapshotProviderErrorSurfacesFailure(t *testing.T) {
	t.Parallel()
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": time.Now()})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{
		HeartbeatFile: hbFile,
		SnapshotProvider: func() (*heartbeat.File, error) {
			return nil, errors.New("permission denied")
		},
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure when snapshot provider errors")
	}
	if !strings.Contains(r.Error, "permission denied") {
		t.Errorf("Error should surface underlying provider error, got %q", r.Error)
	}
}

func TestSetSnapshotProvider_RuntimeSwap(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	freshSnapshot := &heartbeat.File{Jobs: map[string]heartbeat.Entry{
		"backup": {LastPingAt: now.Add(-5 * time.Minute)},
	}}
	staleSnapshot := &heartbeat.File{Jobs: map[string]heartbeat.Entry{
		"backup": {LastPingAt: now.Add(-30 * time.Hour)},
	}}
	hbFile := setupHeartbeatFile(t, map[string]time.Time{"backup": now.Add(-5 * time.Minute)})
	raw := json.RawMessage(`{"type": "deadman", "job": "backup", "interval": "1h", "expect": {"within": "25h"}}`)
	c, err := Parse(raw, Options{HeartbeatFile: hbFile, ClockNow: fixedClock(now)})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	c.SetSnapshotProvider(func() (*heartbeat.File, error) { return freshSnapshot, nil })
	if r := c.Evaluate(context.Background()); !r.OK {
		t.Fatalf("expected OK from fresh snapshot, got %+v", r)
	}
	c.SetSnapshotProvider(func() (*heartbeat.File, error) { return staleSnapshot, nil })
	if r := c.Evaluate(context.Background()); r.OK {
		t.Fatalf("expected failure from stale snapshot, got OK")
	}
	c.SetSnapshotProvider(nil)
	// nil に戻したら再度 file load fallback。file には fresh ping があるので success。
	if r := c.Evaluate(context.Background()); !r.OK {
		t.Fatalf("expected OK after clearing snapshot provider (fallback to file), got %+v", r)
	}
}
