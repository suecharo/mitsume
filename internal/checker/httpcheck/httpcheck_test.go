package httpcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParse_MinimalConfig(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "http", "url": "https://api.example.com/health", "interval": "1h", "expect": {"status": 200}}`)
	c, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Type() != "http" || c.URL() != "https://api.example.com/health" || c.Name() != "https://api.example.com/health" {
		t.Fatalf("got type=%s url=%s name=%s", c.Type(), c.URL(), c.Name())
	}
	if c.Timeout() != DefaultTimeout {
		t.Fatalf("Timeout = %s, want default %s", c.Timeout(), DefaultTimeout)
	}
	if c.method != DefaultMethod {
		t.Fatalf("method = %s, want %s", c.method, DefaultMethod)
	}
}

func TestParse_TypeMismatch(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "url": "https://x", "interval": "1h", "expect": {"status": 200}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_MissingURL(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "http", "interval": "1h", "expect": {"status": 200}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_EmptyExpect(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "http", "url": "https://x", "interval": "1h", "expect": {}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_InvalidStatus(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"type": "http", "url": "https://x", "interval": "1h", "expect": {"status": 99}}`,
		`{"type": "http", "url": "https://x", "interval": "1h", "expect": {"status": 600}}`,
		`{"type": "http", "url": "https://x", "interval": "1h", "expect": {"status": -1}}`,
	}
	for _, in := range cases {
		if _, err := Parse(json.RawMessage(in), Options{}); err == nil {
			t.Errorf("expected error for %s", in)
		}
	}
}

func TestParse_TimeoutResolution(t *testing.T) {
	t.Parallel()
	// explicit
	raw := json.RawMessage(`{"type": "http", "url": "https://x", "interval": "1h", "timeout": "5s", "expect": {"status": 200}}`)
	c, err := Parse(raw, Options{Defaults: DefaultsFallback{Timeout: 20 * time.Second}})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Timeout() != 5*time.Second {
		t.Fatalf("Timeout = %s, want 5s", c.Timeout())
	}
	// defaults fallback
	raw2 := json.RawMessage(`{"type": "http", "url": "https://x", "interval": "1h", "expect": {"status": 200}}`)
	c2, _ := Parse(raw2, Options{Defaults: DefaultsFallback{Timeout: 20 * time.Second}})
	if c2.Timeout() != 20*time.Second {
		t.Fatalf("Timeout = %s, want defaults 20s", c2.Timeout())
	}
	// implicit default
	c3, _ := Parse(raw2, Options{})
	if c3.Timeout() != DefaultTimeout {
		t.Fatalf("Timeout = %s, want %s", c3.Timeout(), DefaultTimeout)
	}
}

func TestParse_RuleMustHaveOneOp(t *testing.T) {
	t.Parallel()
	// 0 op
	raw := json.RawMessage(`{"type": "http", "url": "https://x", "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.a"}]}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for 0-op rule")
	}
	// 2 op
	raw2 := json.RawMessage(`{"type": "http", "url": "https://x", "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.a", "equals": "x", "contains": "y"}]}}`)
	if _, err := Parse(raw2, Options{}); err == nil {
		t.Fatalf("expected error for 2-op rule")
	}
}

func TestParse_InvalidRegex(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "http", "url": "https://x", "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.a", "regex": "["}]}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for invalid regex")
	}
}

func TestParse_InvalidPath(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "http", "url": "https://x", "interval": "1h", "expect": {"body_jsonpath": [{"path": "no-dollar", "equals": "x"}]}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for invalid path")
	}
}

func TestParse_LatencyUnderZero(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "http", "url": "https://x", "interval": "1h", "expect": {"latency_under": "0s"}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error for latency_under=0")
	}
}

func TestParse_UnknownField(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "http", "url": "https://x", "interval": "1h", "expect": {"status": 200}, "wat": 1}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error")
	}
}

// httpcheck (integration-ish): use httptest.

func mkChecker(t *testing.T, expect string, opts Options) *Checker {
	t.Helper()
	raw := json.RawMessage(expect)
	c, err := Parse(raw, opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	return c
}

func TestEvaluate_StatusMatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"status": 200}}`, srv.URL), Options{})
	if r := c.Evaluate(context.Background()); !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func TestEvaluate_StatusMismatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"status": 200}}`, srv.URL), Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(r.Observed, "status=503") || !strings.Contains(r.Expected, "status=200") {
		t.Errorf("Observed=%s Expected=%s", r.Observed, r.Expected)
	}
}

func TestEvaluate_BodyContainsOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_contains": "world"}}`, srv.URL), Options{})
	if r := c.Evaluate(context.Background()); !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func TestEvaluate_BodyContainsFail(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_contains": "foo"}}`, srv.URL), Options{})
	if c.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure")
	}
}

func TestEvaluate_JSONPathEqualsString(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status": "ok"}`))
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.status", "equals": "ok"}]}}`, srv.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
	c2 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.status", "equals": "no"}]}}`, srv.URL), Options{})
	if c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure")
	}
}

func TestEvaluate_JSONPathEqualsNumber(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"count": 42}`))
	}))
	defer srv.Close()
	// int と float の互換 (JSON number は float64)
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.count", "equals": 42}]}}`, srv.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
	c2 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.count", "equals": 42.0}]}}`, srv.URL), Options{})
	if !c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK for equals=42.0")
	}
	c3 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.count", "equals": 43}]}}`, srv.URL), Options{})
	if c3.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure for equals=43")
	}
}

func TestEvaluate_JSONPathEqualsBool(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.ok", "equals": true}]}}`, srv.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
	c2 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.ok", "equals": false}]}}`, srv.URL), Options{})
	if c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure for equals=false")
	}
}

func TestEvaluate_JSONPathContains(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"msg": "hello world"}`))
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.msg", "contains": "world"}]}}`, srv.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
	c2 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.msg", "contains": "no"}]}}`, srv.URL), Options{})
	if c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure")
	}
}

func TestEvaluate_JSONPathRegex(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version": "v1.2.3"}`))
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.version", "regex": "^v\\d+\\.\\d+"}]}}`, srv.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
	c2 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.version", "regex": "^x"}]}}`, srv.URL), Options{})
	if c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure")
	}
}

func TestEvaluate_JSONPathExistsTrueFalse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"present": true}`))
	}))
	defer srv.Close()
	// exists: true when present
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.present", "exists": true}]}}`, srv.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
	// exists: false when missing
	c2 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.missing", "exists": false}]}}`, srv.URL), Options{})
	if !c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK for missing with exists:false")
	}
	// exists: true when missing → failure
	c3 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.missing", "exists": true}]}}`, srv.URL), Options{})
	if c3.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure")
	}
	// exists: false when present → failure
	c4 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.present", "exists": false}]}}`, srv.URL), Options{})
	if c4.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure")
	}
}

func TestEvaluate_JSONPathMultipleRulesAND(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status": "ok", "errors": null, "version": "v1.2.0"}`))
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [
		{"path": "$.status", "equals": "ok"},
		{"path": "$.errors", "exists": true},
		{"path": "$.version", "regex": "^v\\d+"}
	]}}`, srv.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK for all rules matching")
	}
	// one rule fails
	c2 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [
		{"path": "$.status", "equals": "ok"},
		{"path": "$.absent", "exists": true}
	]}}`, srv.URL), Options{})
	if c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure when one rule fails")
	}
}

func TestEvaluate_JSONParseFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"body_jsonpath": [{"path": "$.x", "equals": 1}]}}`, srv.URL), Options{})
	if c.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure for non-JSON body")
	}
}

func TestEvaluate_LatencyUnderExceeded(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"latency_under": "10ms"}}`, srv.URL), Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure for latency exceeded, got %+v", r)
	}
	if !strings.Contains(r.Observed, "latency=") {
		t.Errorf("Observed=%s", r.Observed)
	}
}

func TestEvaluate_LatencyUnderOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"latency_under": "5s"}}`, srv.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
}

func TestEvaluate_TimeoutTriggersFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "timeout": "50ms", "expect": {"status": 200}}`, srv.URL), Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure for timeout")
	}
}

func TestEvaluate_ContextCanceled(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"status": 200}}`, srv.URL), Options{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	r := c.Evaluate(ctx)
	if r.OK {
		t.Fatalf("expected failure for canceled ctx")
	}
}

func TestEvaluate_ConnectionRefused(t *testing.T) {
	t.Parallel()
	// listen on ephemeral port then close so the connection is refused
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "timeout": "1s", "expect": {"status": 200}}`, url), Options{})
	if c.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure for connection refused")
	}
}

func TestEvaluate_RedirectDefault(t *testing.T) {
	t.Parallel()
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer dst.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dst.URL, http.StatusFound)
	}))
	defer redir.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"status": 200}}`, redir.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK, redirect should be followed")
	}
}

func TestEvaluate_HeadersSent(t *testing.T) {
	t.Parallel()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Auth")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "headers": {"X-Auth": "secret"}, "expect": {"status": 200}}`, srv.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
	if got != "secret" {
		t.Errorf("X-Auth header not sent, got %q", got)
	}
}

func TestEvaluate_BodySent(t *testing.T) {
	t.Parallel()
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "method": "POST", "body": "hello", "interval": "1h", "expect": {"status": 200}}`, srv.URL), Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
	if string(got) != "hello" {
		t.Errorf("body = %q, want hello", got)
	}
}

func TestEvaluate_ANDShortCircuitOnStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	// body_contains "hello" は match するが status で fail → failure、observed は status
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"status": 200, "body_contains": "hello"}}`, srv.URL), Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(r.Observed, "status=503") {
		t.Errorf("expected status short-circuit, got Observed=%s", r.Observed)
	}
}

// sequenceClock は times[i] を i 番目の呼び出しで返す fixed clock。goroutine-safe
// ではないが Evaluate は sequential なので安全。
func sequenceClock(times []time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		v := times[i]
		i++

		return v
	}
}

func TestEvaluate_LatencyBoundaryExactlyEqualIsFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t0 := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	clk := sequenceClock([]time.Time{t0, t0.Add(100 * time.Millisecond)})
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"latency_under": "100ms"}}`, srv.URL), Options{ClockNow: clk})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure at boundary elapsed==latency_under, got %+v", r)
	}
}

func TestEvaluate_LatencyJustUnderBoundaryIsOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t0 := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	clk := sequenceClock([]time.Time{t0, t0.Add(100*time.Millisecond - time.Nanosecond)})
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"latency_under": "100ms"}}`, srv.URL), Options{ClockNow: clk})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK at latency_under - 1ns")
	}
}

func TestEvaluate_TLSVerificationRejectsSelfSigned(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// default HTTPClient (nil in Options) uses default TLS transport which
	// should reject the httptest self-signed cert.
	c := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "timeout": "1s", "expect": {"status": 200}}`, srv.URL), Options{})
	if c.Evaluate(context.Background()).OK {
		t.Fatalf("expected TLS verification failure")
	}
	// injecting the server's own client (which trusts its cert) should succeed.
	c2 := mkChecker(t, fmt.Sprintf(`{"type": "http", "url": %q, "interval": "1h", "expect": {"status": 200}}`, srv.URL), Options{HTTPClient: srv.Client()})
	if !c2.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK with trusted client")
	}
}
