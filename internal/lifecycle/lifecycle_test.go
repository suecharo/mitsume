package lifecycle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suecharo/mitsume/internal/lifecycle"
	"github.com/suecharo/mitsume/internal/notify"
)

// recordingSender は notify.Client 互換の fake。呼び出された payload を貯める。
type recordingSender struct {
	mu    sync.Mutex
	calls []notify.SlackPayload
	err   error
}

func (r *recordingSender) Send(_ context.Context, payload notify.SlackPayload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, payload)

	return r.err
}

func (r *recordingSender) received() []notify.SlackPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]notify.SlackPayload, len(r.calls))
	copy(out, r.calls)

	return out
}

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestNotifier_Send_CallsUnderlyingSender(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	n := &lifecycle.Notifier{Sender: sender}
	payload := notify.BuildAnnouncement("hello", notify.Options{})
	if err := n.Send(context.Background(), payload); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := sender.received()
	if len(got) != 1 || got[0].Text != "hello" {
		t.Fatalf("expected 1 call with text=hello, got %+v", got)
	}
}

func TestNotifier_Send_DryRunWritesJSONToStderrAndSkipsSender(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	var buf bytes.Buffer
	n := &lifecycle.Notifier{Sender: sender, DryRun: true, Stderr: &buf}
	payload := notify.BuildAnnouncement("hi", notify.Options{Username: "u"})
	if err := n.Send(context.Background(), payload); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(sender.received()) != 0 {
		t.Fatalf("expected sender to be skipped in dry-run mode")
	}
	var back notify.SlackPayload
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &back); err != nil {
		t.Fatalf("stderr is not valid JSON payload: %v (raw=%q)", err, buf.String())
	}
	if back.Text != "hi" || back.Username != "u" {
		t.Fatalf("payload round-trip mismatch, got %+v", back)
	}
}

func TestNotifier_Send_SenderNilNonDryRunErrors(t *testing.T) {
	t.Parallel()
	n := &lifecycle.Notifier{Sender: nil, DryRun: false}
	err := n.Send(context.Background(), notify.BuildAnnouncement("x", notify.Options{}))
	if err == nil {
		t.Fatalf("expected error for nil Sender in non-dry-run mode")
	}
}

func TestSendShutdown_UsesAnnouncementWithoutAttachments(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	n := &lifecycle.Notifier{Sender: sender, Options: notify.Options{Username: "mitsume"}}
	now := time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC)
	if err := lifecycle.SendShutdown(context.Background(), n, "api-prod-01", "SIGTERM", now); err != nil {
		t.Fatalf("SendShutdown: %v", err)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	p := got[0]
	if len(p.Attachments) != 0 {
		t.Fatalf("shutdown announcement must not carry attachments, got %d", len(p.Attachments))
	}
	wantText := fmt.Sprintf("[mitsume] watch stopped on host=%s (signal=%s, time=%s)",
		"api-prod-01", "SIGTERM", now.Format(time.RFC3339))
	if p.Text != wantText {
		t.Fatalf("text mismatch\n got: %q\nwant: %q", p.Text, wantText)
	}
	if p.Username != "mitsume" {
		t.Fatalf("Options.Username should propagate, got %q", p.Username)
	}
}

func TestSendPanicNotice_TextIncludesPanicValueAndSubcommand(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	n := &lifecycle.Notifier{Sender: sender}
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	if err := lifecycle.SendPanicNotice(context.Background(), n, "check", "h1", "kaboom", now); err != nil {
		t.Fatalf("SendPanicNotice: %v", err)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	p := got[0]
	if len(p.Attachments) != 0 {
		t.Fatalf("panic notice must not carry attachments, got %d", len(p.Attachments))
	}
	if !strings.Contains(p.Text, "kaboom") || !strings.Contains(p.Text, "h1") {
		t.Fatalf("panic text must mention host and panic value, got %q", p.Text)
	}
	if !strings.Contains(p.Text, "check panicked") {
		t.Fatalf("panic text must include the invoking subcommand, got %q", p.Text)
	}
	if !strings.Contains(p.Text, now.Format(time.RFC3339)) {
		t.Fatalf("panic text must include RFC3339 timestamp, got %q", p.Text)
	}
}

func TestSendPanicNotice_SubcommandVaries(t *testing.T) {
	t.Parallel()
	for _, sc := range []string{"check", "watch"} {
		sender := &recordingSender{}
		n := &lifecycle.Notifier{Sender: sender}
		if err := lifecycle.SendPanicNotice(context.Background(), n, sc, "h1", "x", time.Now()); err != nil {
			t.Fatalf("SendPanicNotice(%q): %v", sc, err)
		}
		got := sender.received()
		if len(got) != 1 {
			t.Fatalf("subcommand=%q: expected 1 call, got %d", sc, len(got))
		}
		want := sc + " panicked"
		if !strings.Contains(got[0].Text, want) {
			t.Fatalf("subcommand=%q: text must contain %q, got %q", sc, want, got[0].Text)
		}
	}
}

func TestSendPanicNotice_EmptySubcommandFallsBackToUnknown(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	n := &lifecycle.Notifier{Sender: sender}
	if err := lifecycle.SendPanicNotice(context.Background(), n, "", "h1", "x", time.Now()); err != nil {
		t.Fatalf("SendPanicNotice: %v", err)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "unknown panicked") {
		t.Fatalf("empty subcommand must fall back to 'unknown', got %q", got[0].Text)
	}
}

func TestGuardPanic_NoPanicSkipsNotify(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	n := &lifecycle.Notifier{Sender: sender}
	called := false
	lifecycle.GuardPanic(context.Background(), n, "watch", "h1", nil, func() { called = true })
	if !called {
		t.Fatalf("fn should have been called")
	}
	if len(sender.received()) != 0 {
		t.Fatalf("no notify expected without panic, got %d", len(sender.received()))
	}
}

func TestGuardPanic_PanicNotifiesAndRePanics(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	now := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)
	n := &lifecycle.Notifier{Sender: sender}
	var caught any
	func() {
		defer func() { caught = recover() }()
		lifecycle.GuardPanic(context.Background(), n, "check", "h1", fixedNow(now), func() {
			panic("boom")
		})
	}()
	if caught != "boom" {
		t.Fatalf("re-panic value mismatch: got %v, want %q", caught, "boom")
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 notify call, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "boom") {
		t.Fatalf("notify text must contain panic value, got %q", got[0].Text)
	}
	if !strings.Contains(got[0].Text, "check panicked") {
		t.Fatalf("notify text must reflect the invoking subcommand, got %q", got[0].Text)
	}
	if !strings.Contains(got[0].Text, now.Format(time.RFC3339)) {
		t.Fatalf("notify text must contain clockNow-provided timestamp, got %q", got[0].Text)
	}
}

func TestGuardPanic_NotifyFailureStillRePanics(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{err: errors.New("slack down")}
	n := &lifecycle.Notifier{Sender: sender}
	var caught any
	func() {
		defer func() { caught = recover() }()
		lifecycle.GuardPanic(context.Background(), n, "watch", "h1", nil, func() { panic("x") })
	}()
	if caught != "x" {
		t.Fatalf("panic value should survive notify failure, got %v", caught)
	}
	if len(sender.received()) != 1 {
		t.Fatalf("expected notify attempt even on failure, got %d", len(sender.received()))
	}
}

func TestGuardPanic_DryRunNotifierWritesStderrAndRePanics(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	n := &lifecycle.Notifier{DryRun: true, Stderr: &buf}
	var caught any
	func() {
		defer func() { caught = recover() }()
		lifecycle.GuardPanic(context.Background(), n, "watch", "h1", nil, func() { panic("dry") })
	}()
	if caught != "dry" {
		t.Fatalf("re-panic value mismatch: got %v", caught)
	}
	if !strings.Contains(buf.String(), "dry") {
		t.Fatalf("dry-run stderr must contain panic value, got %q", buf.String())
	}
}

func TestGuardPanic_NilClockUsesRealTime(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	n := &lifecycle.Notifier{Sender: sender}
	before := time.Now().Add(-time.Second)
	var caught any
	func() {
		defer func() { caught = recover() }()
		lifecycle.GuardPanic(context.Background(), n, "watch", "h1", nil, func() { panic("t") })
	}()
	after := time.Now().Add(time.Second)
	if caught != "t" {
		t.Fatalf("re-panic value mismatch: got %v", caught)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	// text は "[mitsume] watch panicked on host=h1 (panic=t, time=<RFC3339>)"
	// の形式。time= の値を実際に parse して [before, after] 範囲内にあることを
	// 検証する ("time=" の存在だけを見る assertion では実装が zero time を
	// 返しても pass してしまい、real clock 使用の invariant を検出できない)。
	ts := extractRFC3339After(t, got[0].Text, "time=")
	if ts.Before(before) || ts.After(after) {
		t.Fatalf("timestamp %s is outside [%s, %s]", ts.Format(time.RFC3339), before.Format(time.RFC3339), after.Format(time.RFC3339))
	}
}

// extractRFC3339After は text から prefix の直後にある RFC3339 timestamp を切り
// 出す。text 内で ")" までを timestamp として扱う。parse 失敗は fatal。
func extractRFC3339After(t *testing.T, text, prefix string) time.Time {
	t.Helper()
	idx := strings.Index(text, prefix)
	if idx < 0 {
		t.Fatalf("prefix %q not found in text %q", prefix, text)
	}
	rest := text[idx+len(prefix):]
	if end := strings.IndexAny(rest, ") \n"); end >= 0 {
		rest = rest[:end]
	}
	ts, err := time.Parse(time.RFC3339, rest)
	if err != nil {
		t.Fatalf("cannot parse RFC3339 timestamp %q: %v", rest, err)
	}

	return ts
}
