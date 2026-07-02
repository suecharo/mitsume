package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suecharo/mitsume/internal/notify"
)

func TestBuildAnnouncement_TextIsMessageVerbatim(t *testing.T) {
	t.Parallel()
	p := notify.BuildAnnouncement("hello world", notify.Options{})
	if p.Text != "hello world" {
		t.Fatalf("Text = %q, want hello world", p.Text)
	}
	if len(p.Attachments) != 0 {
		t.Fatalf("expected no attachments, got %v", p.Attachments)
	}
}

func TestBuildAnnouncement_IncludesUsernameAndIcon(t *testing.T) {
	t.Parallel()
	opts := notify.Options{
		Username:  "mitsume@host",
		IconEmoji: ":rotating_light:",
		IconURL:   "https://example.com/x.png",
	}
	p := notify.BuildAnnouncement("hi", opts)
	if p.Username != opts.Username || p.IconEmoji != opts.IconEmoji || p.IconURL != opts.IconURL {
		t.Fatalf("options not propagated: %+v", p)
	}
}

func TestBuildAnnouncement_MarshalsWithoutAttachmentsField(t *testing.T) {
	t.Parallel()
	p := notify.BuildAnnouncement("hello", notify.Options{Username: "u", IconEmoji: ":e:"})
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if strings.Contains(s, `"attachments"`) {
		t.Fatalf("attachments should be omitted, got %s", s)
	}
	if strings.Contains(s, `"icon_url"`) {
		t.Fatalf("empty icon_url should be omitted, got %s", s)
	}
}

func TestBuildFailure_TextFormatMatchesSpec(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 6, 30, 14, 23, 15, 0, time.FixedZone("JST", 9*3600))
	f := notify.Failure{
		Host: "api-prod-01", Check: "api-health", Type: "http",
		Error: "status=503, want=200", Observed: "status=503", Expected: "status=200",
		Time: ts,
	}
	p := notify.BuildFailure(f, notify.Options{})
	want := "[mitsume] api-health failed (http: status=503, want=200)\n" +
		"host: api-prod-01\ntime: 2026-06-30T14:23:15+09:00"
	if p.Text != want {
		t.Fatalf("Text mismatch\n got: %q\nwant: %q", p.Text, want)
	}
}

func TestBuildFailure_AttachmentColorIsDanger(t *testing.T) {
	t.Parallel()
	p := notify.BuildFailure(notify.Failure{}, notify.Options{})
	if len(p.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(p.Attachments))
	}
	if p.Attachments[0].Color != "danger" {
		t.Fatalf("Color = %q, want danger", p.Attachments[0].Color)
	}
}

func TestBuildFailure_FieldsOrderMatchesSpec(t *testing.T) {
	t.Parallel()
	f := notify.Failure{
		Host: "h", Check: "c", Type: "t", Observed: "o", Expected: "e",
		Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	p := notify.BuildFailure(f, notify.Options{})
	titles := make([]string, 0, len(p.Attachments[0].Fields))
	for _, fld := range p.Attachments[0].Fields {
		titles = append(titles, fld.Title)
	}
	want := []string{"host", "check", "type", "time", "observed", "expected"}
	if strings.Join(titles, ",") != strings.Join(want, ",") {
		t.Fatalf("field order: got %v, want %v", titles, want)
	}
}

func TestSend_Success200(t *testing.T) {
	t.Parallel()
	var received int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&received, 1)
		body, _ := io.ReadAll(r.Body)
		var p notify.SlackPayload
		if err := json.Unmarshal(body, &p); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		if p.Text != "hi" {
			t.Errorf("Text = %q, want hi", p.Text)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := &notify.Client{WebhookURL: srv.URL, Backoffs: []time.Duration{0, 0, 0}}
	if err := c.Send(context.Background(), notify.BuildAnnouncement("hi", notify.Options{})); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := atomic.LoadInt32(&received); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}
}

func TestSend_ClientErrorNotRetried(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	c := &notify.Client{WebhookURL: srv.URL, Backoffs: []time.Duration{0, 0, 0}}
	if err := c.Send(context.Background(), notify.BuildAnnouncement("x", notify.Options{})); err == nil {
		t.Fatalf("expected error for 400")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 attempt (no retry on 4xx), got %d", got)
	}
}

func TestSend_ServerErrorIsRetriedThenSucceeds(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := &notify.Client{WebhookURL: srv.URL, Backoffs: []time.Duration{0, 0, 0}}
	if err := c.Send(context.Background(), notify.BuildAnnouncement("x", notify.Options{})); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 calls (2 failures + 1 success), got %d", got)
	}
}

func TestSend_ServerErrorRetriesExhausted(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &notify.Client{WebhookURL: srv.URL, Backoffs: []time.Duration{0, 0, 0}}
	if err := c.Send(context.Background(), notify.BuildAnnouncement("x", notify.Options{})); err == nil {
		t.Fatalf("expected error after retries")
	}
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Fatalf("expected 4 attempts (initial + 3 retries), got %d", got)
	}
}

func TestSend_NetworkErrorIsRetried(t *testing.T) {
	t.Parallel()
	// http.RoundTripper を差し替え、各試行を確実にカウントしつつ必ず network error を返す
	var calls int32
	client := &http.Client{
		Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)

			return nil, errors.New("simulated network failure")
		}),
	}
	c := &notify.Client{
		WebhookURL: "http://secret-token.invalid/services/T00000000/B00000000/xxxx",
		HTTPClient: client,
		Backoffs:   []time.Duration{0, 0, 0},
	}
	err := c.Send(context.Background(), notify.BuildAnnouncement("x", notify.Options{}))
	if err == nil {
		t.Fatalf("expected network error")
	}
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Fatalf("expected 4 attempts (initial + 3 retries), got %d", got)
	}
	if strings.Contains(err.Error(), "secret-token.invalid") ||
		strings.Contains(err.Error(), "xxxx") {
		t.Fatalf("error leaked webhook URL / token: %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSend_ErrorDoesNotLeakWebhookURL_4xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	c := &notify.Client{WebhookURL: srv.URL, Backoffs: []time.Duration{0, 0, 0}}
	err := c.Send(context.Background(), notify.BuildAnnouncement("x", notify.Options{}))
	if err == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("error leaked webhook URL: %v", err)
	}
}

func TestSend_ErrorDoesNotLeakWebhookURL_5xxRetriesExhausted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &notify.Client{WebhookURL: srv.URL, Backoffs: []time.Duration{0, 0, 0}}
	err := c.Send(context.Background(), notify.BuildAnnouncement("x", notify.Options{}))
	if err == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("error leaked webhook URL: %v", err)
	}
}

func TestSend_ErrorDoesNotLeakWebhookURL_TransportFailure(t *testing.T) {
	t.Parallel()
	const secretToken = "SECRET-TOKEN-DO-NOT-LEAK"
	client := &http.Client{
		Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp 127.0.0.1: connect: connection refused")
		}),
	}
	c := &notify.Client{
		WebhookURL: "https://hooks.slack.example/services/T0/B0/" + secretToken,
		HTTPClient: client,
		Backoffs:   []time.Duration{0, 0, 0},
	}
	err := c.Send(context.Background(), notify.BuildAnnouncement("x", notify.Options{}))
	if err == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Fatalf("error leaked webhook URL token: %v", err)
	}
	if strings.Contains(err.Error(), "hooks.slack.example") {
		t.Fatalf("error leaked webhook URL host: %v", err)
	}
}

func TestSend_ErrorDoesNotLeakWebhookURL_MalformedURL(t *testing.T) {
	t.Parallel()
	const secretToken = "MALFORMED-URL-SECRET"
	// URL に control 文字を入れて http.NewRequestWithContext を失敗させる
	c := &notify.Client{
		WebhookURL: "https://hooks.slack.example/services/T0/B0/" + secretToken + "\n",
		Backoffs:   []time.Duration{0, 0, 0},
	}
	err := c.Send(context.Background(), notify.BuildAnnouncement("x", notify.Options{}))
	if err == nil {
		t.Fatalf("expected build request error")
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Fatalf("error leaked webhook URL token: %v", err)
	}
	if strings.Contains(err.Error(), "hooks.slack.example") {
		t.Fatalf("error leaked webhook URL host: %v", err)
	}
}

func TestSend_ContextCancelInterruptsBackoff(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c := &notify.Client{
		WebhookURL: srv.URL,
		Backoffs:   []time.Duration{5 * time.Second, 5 * time.Second, 5 * time.Second},
	}
	done := make(chan error, 1)
	go func() {
		done <- c.Send(ctx, notify.BuildAnnouncement("x", notify.Options{}))
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected error from cancelled context")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Send did not return after cancel")
	}
}

func TestSend_EmptyWebhookIsError(t *testing.T) {
	t.Parallel()
	c := &notify.Client{}
	if err := c.Send(context.Background(), notify.BuildAnnouncement("x", notify.Options{})); err == nil {
		t.Fatalf("expected error for empty webhook")
	}
}

func TestBuildSuccess_TextFormatMatchesSpec(t *testing.T) {
	t.Parallel()
	p := notify.BuildSuccess(notify.Success{
		Host:  "api-prod-01",
		Check: "nightly-backup",
		Type:  "run",
		Time:  time.Date(2026, 6, 30, 14, 23, 15, 0, time.FixedZone("JST", 9*3600)),
	}, notify.Options{})
	want := "[mitsume] nightly-backup succeeded (run: exit=0)\nhost: api-prod-01\ntime: 2026-06-30T14:23:15+09:00"
	if p.Text != want {
		t.Fatalf("Text = %q, want %q", p.Text, want)
	}
}

func TestBuildSuccess_AttachmentColorIsGood(t *testing.T) {
	t.Parallel()
	p := notify.BuildSuccess(notify.Success{
		Host: "h1", Check: "c1", Type: "run", Time: time.Now(),
	}, notify.Options{})
	if len(p.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(p.Attachments))
	}
	if p.Attachments[0].Color != "good" {
		t.Fatalf("Color = %q, want good", p.Attachments[0].Color)
	}
}

func TestBuildSuccess_FieldsCarryExitZero(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 6, 30, 14, 23, 15, 0, time.UTC)
	p := notify.BuildSuccess(notify.Success{
		Host: "h1", Check: "c1", Type: "run", Time: ts,
	}, notify.Options{})
	fields := p.Attachments[0].Fields
	want := []notify.Field{
		{Title: "host", Value: "h1", Short: true},
		{Title: "check", Value: "c1", Short: true},
		{Title: "type", Value: "run", Short: true},
		{Title: "time", Value: "2026-06-30T14:23:15Z", Short: true},
		{Title: "observed", Value: "exit=0", Short: false},
		{Title: "expected", Value: "exit=0", Short: false},
	}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d", len(want), len(fields))
	}
	for i, w := range want {
		if fields[i] != w {
			t.Fatalf("fields[%d] = %+v, want %+v", i, fields[i], w)
		}
	}
}
