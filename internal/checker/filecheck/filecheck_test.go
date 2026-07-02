package filecheck

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, contents string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func fixedClock(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

func TestParse_MinimalWithPath(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "path": "/tmp/a.txt", "interval": "1h", "expect": {"exists": true}}`)
	c, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Type() != "file" || c.Name() != "/tmp/a.txt" || c.Path() != "/tmp/a.txt" {
		t.Fatalf("got type=%s name=%s path=%s", c.Type(), c.Name(), c.Path())
	}
	if c.Interval() != time.Hour {
		t.Fatalf("Interval = %s", c.Interval())
	}
}

func TestParse_MinimalWithPathGlob(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "path_glob": "/backup/*.dump", "interval": "1h", "expect": {"exists": true}}`)
	c, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Name() != "/backup/*.dump" || c.PathGlob() != "/backup/*.dump" || c.Path() != "" {
		t.Fatalf("got name=%s pathGlob=%s path=%s", c.Name(), c.PathGlob(), c.Path())
	}
}

func TestParse_BothPathAndPathGlobRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "path": "/a", "path_glob": "/b/*", "interval": "1h", "expect": {"exists": true}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for both path and path_glob")
	}
}

func TestParse_NeitherPathNorPathGlobRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "interval": "1h", "expect": {"exists": true}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for missing path/path_glob")
	}
}

func TestParse_EmptyExpectRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "path": "/a", "interval": "1h", "expect": {}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for empty expect")
	}
}

func TestParse_MissingIntervalUsesDefaults(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "path": "/a", "expect": {"exists": true}}`)
	c, err := Parse(raw, Options{Defaults: DefaultsFallback{Interval: 2 * time.Hour}})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Interval() != 2*time.Hour {
		t.Fatalf("Interval = %s", c.Interval())
	}
}

func TestParse_SizeStringAndInt(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "path": "/a", "interval": "1h", "expect": {"size_min": "100MB", "size_max": 1073741824}}`)
	c, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.sizeMin == nil || *c.sizeMin != 100*1024*1024 {
		t.Fatalf("sizeMin mismatch: %v", c.sizeMin)
	}
	if c.sizeMax == nil || *c.sizeMax != 1073741824 {
		t.Fatalf("sizeMax mismatch: %v", c.sizeMax)
	}
}

func TestParse_SizeMinGreaterThanMaxRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "path": "/a", "interval": "1h", "expect": {"size_min": 100, "size_max": 50}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for size_min > size_max")
	}
}

func TestParse_SizeNegativeRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "path": "/a", "interval": "1h", "expect": {"size_min": -1}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for negative size")
	}
}

func TestParse_UnknownFieldRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "path": "/a", "interval": "1h", "expect": {"exists": true}, "wat": 1}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for unknown field")
	}
}

func TestParse_MtimeWithinZeroRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "path": "/a", "interval": "1h", "expect": {"mtime_within": "0s"}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for mtime_within=0")
	}
}

func TestParse_ExplicitName(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "name": "backup-file", "path": "/tmp/x", "interval": "1h", "expect": {"exists": true}}`)
	c, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Name() != "backup-file" {
		t.Fatalf("Name = %s", c.Name())
	}
}

func TestEvaluate_ExistsTrueFileExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "x", time.Now())
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"exists": true}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func TestEvaluate_ExistsTrueFileMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.txt")
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"exists": true}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure for missing file")
	}
}

func TestEvaluate_ExistsFalseFileMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.txt")
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"exists": false}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if !r.OK {
		t.Fatalf("expected OK for missing file with exists:false, got %+v", r)
	}
}

func TestEvaluate_ExistsFalseFileExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "x", time.Now())
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"exists": false}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure for existing file with exists:false")
	}
}

func TestEvaluate_MtimeWithinOK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "x", now.Add(-5*time.Minute))
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"mtime_within": "10m"}}`)
	c, _ := Parse(raw, Options{ClockNow: fixedClock(now)})
	r := c.Evaluate(context.Background())
	if !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func TestEvaluate_MtimeWithinBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	// elapsed == 10m → failure (>= mtime_within)
	writeFile(t, path, "x", now.Add(-10*time.Minute))
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"mtime_within": "10m"}}`)
	c, _ := Parse(raw, Options{ClockNow: fixedClock(now)})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure at mtime boundary elapsed==within")
	}
}

func TestEvaluate_MtimeWithinExceeded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "x", now.Add(-20*time.Minute))
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"mtime_within": "10m"}}`)
	c, _ := Parse(raw, Options{ClockNow: fixedClock(now)})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure")
	}
}

func TestEvaluate_SizeMinBoundary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, strings.Repeat("a", 100), time.Now())
	// size==size_min → OK
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"size_min": 100}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if !r.OK {
		t.Fatalf("expected OK at boundary size==size_min, got %+v", r)
	}
	// size==size_min-1 → failure
	raw2 := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"size_min": 101}}`)
	c2, _ := Parse(raw2, Options{})
	if c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure at size < size_min")
	}
}

func TestEvaluate_SizeMaxBoundary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, strings.Repeat("a", 100), time.Now())
	// size==size_max → OK
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"size_max": 100}}`)
	c, _ := Parse(raw, Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK at boundary size==size_max")
	}
	// size==size_max+1 → failure
	raw2 := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"size_max": 99}}`)
	c2, _ := Parse(raw2, Options{})
	if c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure at size > size_max")
	}
}

func TestEvaluate_PathGlobPicksMtimeNewest(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	old := filepath.Join(dir, "a.dump")
	newer := filepath.Join(dir, "b.dump")
	writeFile(t, old, strings.Repeat("x", 200), now.Add(-2*time.Hour))
	writeFile(t, newer, strings.Repeat("x", 100), now.Add(-30*time.Minute))
	raw := json.RawMessage(`{"type": "file", "path_glob": "` + dir + `/*.dump", "interval": "1h", "expect": {"mtime_within": "1h"}}`)
	c, _ := Parse(raw, Options{ClockNow: fixedClock(now)})
	r := c.Evaluate(context.Background())
	// newer (30m ago) < 1h → OK
	if !r.OK {
		t.Fatalf("expected OK from newest match, got %+v", r)
	}
	// mtime_within=15m → newer (30m) は超過 → failure (old も超過なので全体 failure)
	raw2 := json.RawMessage(`{"type": "file", "path_glob": "` + dir + `/*.dump", "interval": "1h", "expect": {"mtime_within": "15m"}}`)
	c2, _ := Parse(raw2, Options{ClockNow: fixedClock(now)})
	if c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure when newest exceeds mtime_within")
	}
}

func TestEvaluate_PathGlobNoMatchExistsTrue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := json.RawMessage(`{"type": "file", "path_glob": "` + dir + `/*.missing", "interval": "1h", "expect": {"exists": true}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure for 0-match glob with exists:true")
	}
	if !strings.Contains(r.Observed, "no matches") {
		t.Errorf("Observed = %s", r.Observed)
	}
}

func TestEvaluate_PathGlobNoMatchExistsFalse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := json.RawMessage(`{"type": "file", "path_glob": "` + dir + `/*.missing", "interval": "1h", "expect": {"exists": false}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if !r.OK {
		t.Fatalf("expected OK for 0-match glob with exists:false, got %+v", r)
	}
}

func TestEvaluate_PermissionDeniedIsFailure(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("running as root, permission denied cannot be reproduced")
	}
	dir := t.TempDir()
	// 親ディレクトリを 0000 にすると子 file の stat が EACCES で失敗する
	// (docs/checkers.md § file § 固有の挙動: stat 失敗は failure)。
	parent := filepath.Join(dir, "locked")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	child := filepath.Join(parent, "target")
	if err := os.WriteFile(child, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o755) // TempDir 削除を成功させるため
	})
	raw := json.RawMessage(`{"type": "file", "path": "` + child + `", "interval": "1h", "expect": {"exists": true}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure for permission denied, got %+v", r)
	}
	if !strings.Contains(r.Error, "stat") && !strings.Contains(r.Observed, "stat") {
		t.Errorf("Error/Observed should mention stat failure, got Error=%s Observed=%s", r.Error, r.Observed)
	}
}

func TestEvaluate_ContextCanceled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "x", time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"exists": true}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(ctx)
	if r.OK {
		t.Fatalf("expected failure for canceled ctx")
	}
}

func TestEvaluate_MultipleConditionsANDTrue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, strings.Repeat("x", 500), now.Add(-5*time.Minute))
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"exists": true, "mtime_within": "10m", "size_min": 100, "size_max": 1000}}`)
	c, _ := Parse(raw, Options{ClockNow: fixedClock(now)})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK when all AND conditions met")
	}
}

func TestEvaluate_MultipleConditionsANDFalse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	// mtime OK but size fails
	writeFile(t, path, strings.Repeat("x", 50), now.Add(-5*time.Minute))
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"exists": true, "mtime_within": "10m", "size_min": 100}}`)
	c, _ := Parse(raw, Options{ClockNow: fixedClock(now)})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure when one AND condition fails, got OK")
	}
}

func TestEvaluate_MtimeFailureFormatsDurationsHumanReadable(t *testing.T) {
	t.Parallel()
	// docs/notify.md § Payload: observed は mtime=26h ago のような可読形式。
	// sub-second の生値を載せず、text と observed は同一の観測値を共有する。
	// clock を呼び出しごとに進め、観測を 2 回取ると text と observed が食い違う
	// 状態を再現できるようにする。
	dir := t.TempDir()
	path := filepath.Join(dir, "stale.txt")
	base := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	writeFile(t, path, "x", base.Add(-(2*time.Hour + 456*time.Millisecond)))
	calls := 0
	clock := func() time.Time {
		calls++

		return base.Add(time.Duration(calls) * 100 * time.Millisecond)
	}
	raw := json.RawMessage(`{"type": "file", "path": "` + path + `", "interval": "1h", "expect": {"exists": true, "mtime_within": "10m"}}`)
	c, err := Parse(raw, Options{ClockNow: clock})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure, got OK")
	}
	if r.Error != "mtime is 2h old (>= mtime_within 10m)" {
		t.Errorf("Error = %q, want %q", r.Error, "mtime is 2h old (>= mtime_within 10m)")
	}
	if r.Observed != "exists=true, mtime=2h ago" {
		t.Errorf("Observed = %q, want %q", r.Observed, "exists=true, mtime=2h ago")
	}
	if r.Expected != "exists=true, mtime_within=10m" {
		t.Errorf("Expected = %q, want %q", r.Expected, "exists=true, mtime_within=10m")
	}
}
