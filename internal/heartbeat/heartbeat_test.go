package heartbeat_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suecharo/mitsume/internal/heartbeat"
)

func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hb.json")
	f, err := heartbeat.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Jobs) != 0 {
		t.Fatalf("expected empty jobs, got %v", f.Jobs)
	}
}

func TestLoad_ParsesExistingFileWithOffsetTimestamp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hb.json")
	body := `{"jobs":{"backup":{"last_ping_at":"2026-06-30T12:34:56+09:00"}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := heartbeat.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, ok := f.Jobs["backup"]
	if !ok {
		t.Fatalf("job not found: %v", f.Jobs)
	}
	want, _ := time.Parse(time.RFC3339, "2026-06-30T12:34:56+09:00")
	if !e.LastPingAt.Equal(want) {
		t.Fatalf("got %v, want %v", e.LastPingAt, want)
	}
}

func TestLoad_ParsesUTCZTimestamp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hb.json")
	body := `{"jobs":{"j":{"last_ping_at":"2026-06-30T12:34:56Z"}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := heartbeat.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := time.Date(2026, 6, 30, 12, 34, 56, 0, time.UTC)
	if !f.Jobs["j"].LastPingAt.Equal(want) {
		t.Fatalf("got %v, want %v", f.Jobs["j"].LastPingAt, want)
	}
}

func TestLoad_MalformedJSONIsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hb.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := heartbeat.Load(path); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestUpdate_OverwritesExistingJob(t *testing.T) {
	t.Parallel()
	f := &heartbeat.File{Jobs: map[string]heartbeat.Entry{
		"a": {LastPingAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}}
	newTs := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	f.Update("a", newTs)
	if !f.Jobs["a"].LastPingAt.Equal(newTs) {
		t.Fatalf("expected overwrite, got %v", f.Jobs["a"].LastPingAt)
	}
}

func TestUpdate_AddsNewJob(t *testing.T) {
	t.Parallel()
	f := &heartbeat.File{Jobs: map[string]heartbeat.Entry{}}
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.Update("new", ts)
	if !f.Jobs["new"].LastPingAt.Equal(ts) {
		t.Fatalf("expected new job, got %v", f.Jobs)
	}
}

func TestUpdate_InitializesNilJobsMap(t *testing.T) {
	t.Parallel()
	f := &heartbeat.File{}
	f.Update("j", time.Now())
	if _, ok := f.Jobs["j"]; !ok {
		t.Fatalf("expected job entry, got %v", f.Jobs)
	}
}

func TestSaveAtomicLoadRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hb.json")
	orig := &heartbeat.File{Jobs: map[string]heartbeat.Entry{
		"backup": {LastPingAt: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)},
		"renew":  {LastPingAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
	}}
	if err := heartbeat.SaveAtomic(path, orig); err != nil {
		t.Fatalf("SaveAtomic: %v", err)
	}
	loaded, err := heartbeat.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(loaded.Jobs))
	}
	for k, v := range orig.Jobs {
		got := loaded.Jobs[k].LastPingAt
		if !got.Equal(v.LastPingAt) {
			t.Fatalf("%s: got %v, want %v", k, got, v.LastPingAt)
		}
	}
}

func TestSaveAtomic_PreservesExistingFileMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hb.json")
	if err := os.WriteFile(path, []byte(`{"jobs":{}}`), 0o640); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	f := &heartbeat.File{Jobs: map[string]heartbeat.Entry{
		"a": {LastPingAt: time.Now()},
	}}
	if err := heartbeat.SaveAtomic(path, f); err != nil {
		t.Fatalf("SaveAtomic: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode: got %o, want 0640", got)
	}
}

func TestSaveAtomic_LeavesNoTmpFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hb.json")
	f := &heartbeat.File{Jobs: map[string]heartbeat.Entry{
		"a": {LastPingAt: time.Now()},
	}}
	if err := heartbeat.SaveAtomic(path, f); err != nil {
		t.Fatalf("SaveAtomic: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("tmp file leaked: %s", e.Name())
		}
	}
}

func TestSaveAtomic_MissingDirectoryIsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent-subdir", "hb.json")
	f := &heartbeat.File{Jobs: map[string]heartbeat.Entry{"a": {LastPingAt: time.Now()}}}
	if err := heartbeat.SaveAtomic(path, f); err == nil {
		t.Fatalf("expected error for missing parent directory")
	}
}

func TestSaveAtomic_ConcurrentWritesConverge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hb.json")
	base := &heartbeat.File{Jobs: map[string]heartbeat.Entry{
		"backup": {LastPingAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}}
	if err := heartbeat.SaveAtomic(path, base); err != nil {
		t.Fatalf("initial: %v", err)
	}
	var wg sync.WaitGroup
	const n = 20
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f, err := heartbeat.Load(path)
			if err != nil {
				t.Errorf("Load: %v", err)

				return
			}
			f.Update("backup", time.Date(2026, 6, 30, 0, 0, i, 0, time.UTC))
			if err := heartbeat.SaveAtomic(path, f); err != nil {
				t.Errorf("SaveAtomic: %v", err)
			}
		}(i)
	}
	wg.Wait()
	loaded, err := heartbeat.Load(path)
	if err != nil {
		t.Fatalf("Load after concurrent: %v", err)
	}
	if _, ok := loaded.Jobs["backup"]; !ok {
		t.Fatalf("expected backup entry to survive concurrent writes")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("tmp file leaked after concurrent: %s", e.Name())
		}
	}
}

func TestMarshal_IsPrettyPrintedWithTrailingNewline(t *testing.T) {
	t.Parallel()
	f := &heartbeat.File{Jobs: map[string]heartbeat.Entry{
		"a": {LastPingAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}}
	data, err := heartbeat.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("expected trailing newline: %q", data)
	}
	if !strings.Contains(string(data), "  ") {
		t.Fatalf("expected 2-space indent, got: %q", data)
	}
}

func TestMarshal_SortsJobsAlphabetically(t *testing.T) {
	t.Parallel()
	f := &heartbeat.File{Jobs: map[string]heartbeat.Entry{
		"zeta":  {LastPingAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		"alpha": {LastPingAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		"gamma": {LastPingAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}}
	data, err := heartbeat.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	ai := strings.Index(s, `"alpha"`)
	gi := strings.Index(s, `"gamma"`)
	zi := strings.Index(s, `"zeta"`)
	if ai >= gi || gi >= zi {
		t.Fatalf("jobs not sorted: alpha=%d gamma=%d zeta=%d", ai, gi, zi)
	}
}
