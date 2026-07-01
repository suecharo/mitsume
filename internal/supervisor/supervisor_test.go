package supervisor_test

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/suecharo/mitsume/internal/lifecycle"
	"github.com/suecharo/mitsume/internal/notify"
	"github.com/suecharo/mitsume/internal/supervisor"
)

// TestMain 分岐: 子プロセスとして起動された自身を fake binary として振る舞わせる
// (cmd checker と同じ pattern)。
//   - trap_term_sleep: SIGTERM を無視して 30s sleep (SIGKILL fallback を発火させる)
//   - stderr_flood_exit1: stderr に 8192 バイトの 'Z' を出力して exit 1
//     (ring buffer overflow → tail truncation の検証用)
func TestMain(m *testing.M) {
	switch os.Getenv("MITSUME_SUPERVISOR_TEST_FAKE") {
	case "trap_term_sleep":
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "stderr_flood_exit1":
		buf := make([]byte, 8192)
		for i := range buf {
			buf[i] = 'Z'
		}
		_, _ = os.Stderr.Write(buf)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// -------- fakes --------

type recordingSender struct {
	mu    sync.Mutex
	calls []notify.SlackPayload
}

func (r *recordingSender) Send(_ context.Context, p notify.SlackPayload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, p)

	return nil
}

func (r *recordingSender) received() []notify.SlackPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]notify.SlackPayload, len(r.calls))
	copy(out, r.calls)

	return out
}

func newTestNotifier() (*lifecycle.Notifier, *recordingSender) {
	s := &recordingSender{}

	return &lifecycle.Notifier{Sender: s, Options: notify.Options{Username: "mitsume"}}, s
}

func discardWriter() *bytes.Buffer { return &bytes.Buffer{} }

// -------- validation --------

func TestRun_NotifierNilReturnsInternalError(t *testing.T) {
	t.Parallel()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Command:              []string{"/bin/true"},
		DisableSignalForward: true,
	})
	if code != supervisor.InternalErrorExitCode {
		t.Fatalf("expected InternalErrorExitCode, got %d", code)
	}
}

func TestRun_EmptyCommandReturnsInternalError(t *testing.T) {
	t.Parallel()
	n, _ := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Notifier:             n,
		DisableSignalForward: true,
	})
	if code != supervisor.InternalErrorExitCode {
		t.Fatalf("expected InternalErrorExitCode, got %d", code)
	}
}

// -------- success paths --------

func TestRun_SuccessNotifies(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Name:                 "job",
		Command:              []string{"/bin/true"},
		Notifier:             n,
		Host:                 "h1",
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 success notify, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "job succeeded") {
		t.Fatalf("success text must mention name, got %q", got[0].Text)
	}
	if len(got[0].Attachments) != 0 {
		t.Fatalf("success announcement must not carry attachments, got %d", len(got[0].Attachments))
	}
}

func TestRun_QuietOnSuccessSuppressesNotify(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Name:                 "job",
		Command:              []string{"/bin/true"},
		Notifier:             n,
		QuietOnSuccess:       true,
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if len(sender.received()) != 0 {
		t.Fatalf("expected 0 notifies with QuietOnSuccess, got %d", len(sender.received()))
	}
}

// -------- failure paths --------

func TestRun_ExitCodePassThrough(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Name:                 "job",
		Command:              []string{"/bin/sh", "-c", "exit 3"},
		Notifier:             n,
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != 3 {
		t.Fatalf("expected exit 3, got %d", code)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 failure notify, got %d", len(got))
	}
	if len(got[0].Attachments) == 0 {
		t.Fatalf("failure payload must carry attachments")
	}
	if got[0].Attachments[0].Color != "danger" {
		t.Fatalf("failure attachment color must be danger, got %q", got[0].Attachments[0].Color)
	}
}

func TestRun_StderrTailInFailureNotify(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Name:                 "job",
		Command:              []string{"/bin/sh", "-c", "echo 'bang!' >&2; exit 2"},
		Notifier:             n,
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 failure notify, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "bang!") {
		t.Fatalf("failure text must include stderr tail, got %q", got[0].Text)
	}
}

func TestRun_CommandNotFoundReturns127(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Command:              []string{"/definitely/nonexistent/binary-xyz"},
		Notifier:             n,
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != supervisor.CommandNotFoundExitCode {
		t.Fatalf("expected 127 for missing command, got %d", code)
	}
	if len(sender.received()) != 1 {
		t.Fatalf("expected 1 start-failure notify, got %d", len(sender.received()))
	}
}

func TestRun_PermissionDeniedReturns126(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root can bypass permission bits; skip in root context")
	}
	dir := t.TempDir()
	binPath := filepath.Join(dir, "noexec.sh")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho hi\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// permission bit 0o600 = user rw only, no exec
	n, sender := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Command:              []string{binPath},
		Notifier:             n,
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != supervisor.PermissionDeniedExitCode {
		t.Fatalf("expected 126 for permission denied, got %d", code)
	}
	if len(sender.received()) != 1 {
		t.Fatalf("expected 1 start-failure notify, got %d", len(sender.received()))
	}
}

// -------- timeout paths --------

func TestRun_TimeoutSigtermReturns124(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Name:                 "job",
		Command:              []string{"/bin/sleep", "10"},
		Timeout:              100 * time.Millisecond,
		GracePeriod:          500 * time.Millisecond,
		Notifier:             n,
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != supervisor.TimeoutExitCode {
		t.Fatalf("expected 124 for timeout, got %d", code)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 failure notify, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "timed out") {
		t.Fatalf("failure text must mention timeout, got %q", got[0].Text)
	}
}

func TestRun_TimeoutGraceExpiredSigkillReturns124(t *testing.T) {
	// t.Setenv は t.Parallel と併用不可
	t.Setenv("MITSUME_SUPERVISOR_TEST_FAKE", "trap_term_sleep")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	n, sender := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Name:                 "job",
		Command:              []string{self},
		Timeout:              50 * time.Millisecond,
		GracePeriod:          100 * time.Millisecond,
		Notifier:             n,
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != supervisor.TimeoutExitCode {
		t.Fatalf("expected 124 for timeout + SIGKILL fallback, got %d", code)
	}
	if len(sender.received()) != 1 {
		t.Fatalf("expected 1 failure notify, got %d", len(sender.received()))
	}
}

// -------- dry-run --------

func TestRun_DryRunSkipsSlackButRunsChild(t *testing.T) {
	t.Parallel()
	dryStderr := &bytes.Buffer{}
	n := &lifecycle.Notifier{DryRun: true, Stderr: dryStderr}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	code := supervisor.Run(context.Background(), supervisor.Config{
		Name:                 "job",
		Command:              []string{"/bin/sh", "-c", "touch " + marker},
		Notifier:             n,
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("child must run even in dry-run: %v", err)
	}
	if dryStderr.Len() == 0 {
		t.Fatalf("dry-run must write payload to Stderr")
	}
	if !strings.Contains(dryStderr.String(), "succeeded") {
		t.Fatalf("dry-run stderr must contain success payload, got %q", dryStderr.String())
	}
}

// -------- ring buffer --------

func TestRun_StderrRingBufferOverflowUsesTailBytes(t *testing.T) {
	// t.Setenv は t.Parallel と併用不可 (fake binary を env で分岐させるため)。
	t.Setenv("MITSUME_SUPERVISOR_TEST_FAKE", "stderr_flood_exit1")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	n, sender := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		Name:                 "job",
		Command:              []string{self},
		Notifier:             n,
		StderrBufferBytes:    16 * 1024,
		StderrTailLines:      100,
		StderrTailBytes:      512,
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 notify, got %d", len(got))
	}
	// fake binary は 8192 bytes の 'Z' を stderr に出す。maxBytes=512 が勝つので
	// tail は 512 bytes の 'Z'。failure text の他 field ("[mitsume] job failed
	// (run: exit=1)" / "host:" / "time:" / "exit=1") には 'Z' が含まれないため、
	// text 内の 'Z' 数がちょうど 512 なら tail truncation の実挙動を検証できる。
	zCount := strings.Count(got[0].Text, "Z")
	if zCount != 512 {
		t.Fatalf("expected exactly 512 'Z' chars in payload (byte cap), got %d\ntext=%q",
			zCount, got[0].Text)
	}
}

// -------- name fallback --------

func TestRun_NameFallsBackToBasename(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	code := supervisor.Run(context.Background(), supervisor.Config{
		// Name 空、Command[0] は "/bin/true" → basename "true"
		Command:              []string{"/bin/true"},
		Notifier:             n,
		Stdout:               discardWriter(),
		Stderr:               discardWriter(),
		DisableSignalForward: true,
	})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 notify, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "true succeeded") {
		t.Fatalf("name should fall back to basename \"true\", got %q", got[0].Text)
	}
}
