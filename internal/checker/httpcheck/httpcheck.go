// Package httpcheck は http checker を実装する。仕様は docs/checkers.md § http
// checker に従う。expect の 4 演算子 (status / body_contains / body_jsonpath /
// latency_under) を AND で評価する。
package httpcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/suecharo/mitsume/internal/checker"
	"github.com/suecharo/mitsume/internal/config"
	"github.com/suecharo/mitsume/internal/confirm"
	"github.com/suecharo/mitsume/internal/durationx"
)

// DefaultTimeout は checker.timeout / defaults.timeout が両方未指定のときの
// 暗黙 default (docs/checkers.md § http checker § 固有の挙動)。
const DefaultTimeout = 30 * time.Second

// DefaultMethod は method 未指定時の HTTP method (docs/checkers.md § http)。
const DefaultMethod = "GET"

// Config は http checker の raw JSON schema。
type Config struct {
	Type     string            `json:"type"`
	Name     string            `json:"name,omitempty"`
	URL      string            `json:"url"`
	Method   string            `json:"method,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Interval config.Duration   `json:"interval,omitempty"`
	Timeout  config.Duration   `json:"timeout,omitempty"`
	Confirm  json.RawMessage   `json:"confirm,omitempty"`
	Expect   Expect            `json:"expect"`
}

// Expect は http checker の判定条件。全 field optional、AND 評価。
type Expect struct {
	Status       *int            `json:"status,omitempty"`
	BodyContains string          `json:"body_contains,omitempty"`
	BodyJSONPath []JSONPathRule  `json:"body_jsonpath,omitempty"`
	LatencyUnder config.Duration `json:"latency_under,omitempty"`
}

// JSONPathRule は body_jsonpath の 1 要素。演算子は equals / contains / regex /
// exists のうち exactly one を指定する。
type JSONPathRule struct {
	Path     string          `json:"path"`
	Equals   json.RawMessage `json:"equals,omitempty"`
	Contains *string         `json:"contains,omitempty"`
	Regex    *string         `json:"regex,omitempty"`
	Exists   *bool           `json:"exists,omitempty"`
}

// Options は Parse に渡す外部情報。
type Options struct {
	Defaults DefaultsFallback
	// HTTPClient は test 用注入経路。nil なら evaluate 内で http.DefaultClient
	// を使う (TLS 検証有効、redirect 10 hop の default)。
	HTTPClient *http.Client
	// ClockNow は現在時刻 provider (latency の実測に使う)。nil なら time.Now。
	// テストで boundary elapsed==latency_under を deterministic に再現するため
	// の注入経路。
	ClockNow func() time.Time
}

// DefaultsFallback は defaults セクションから http checker が継承する値。
type DefaultsFallback struct {
	Interval time.Duration
	Timeout  time.Duration
}

// Checker は http checker の実装。
type Checker struct {
	name          string
	interval      time.Duration
	confirmCfg    confirm.Config
	url           string
	method        string
	headers       map[string]string
	body          []byte
	timeout       time.Duration
	expectStatus  *int
	expectBody    string
	expectPath    []compiledRule
	expectLatency time.Duration
	httpClient    *http.Client
	clockNow      func() time.Time
}

// Parse は raw JSON + Options を検証して Checker を作る。
func Parse(raw json.RawMessage, opts Options) (*Checker, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("httpcheck: parse: %w", err)
	}
	if cfg.Type != "http" {
		return nil, fmt.Errorf("httpcheck: type must be \"http\", got %q", cfg.Type)
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("httpcheck: url is required")
	}
	interval := opts.Defaults.Interval
	if cfg.Interval.IsSet() {
		interval = cfg.Interval.Value()
	}
	if interval <= 0 {
		return nil, fmt.Errorf("httpcheck: interval must be > 0 (set checks[].interval or defaults.interval)")
	}
	timeout := resolveTimeout(cfg.Timeout, opts.Defaults.Timeout)
	if timeout <= 0 {
		return nil, fmt.Errorf("httpcheck: timeout must be > 0")
	}
	method := cfg.Method
	if method == "" {
		method = DefaultMethod
	}
	compiled, err := compileExpect(cfg.Expect)
	if err != nil {
		return nil, fmt.Errorf("httpcheck: %w", err)
	}
	confirmCfg, err := confirm.Parse(cfg.Confirm)
	if err != nil {
		return nil, fmt.Errorf("httpcheck: %w", err)
	}
	name := cfg.Name
	if name == "" {
		name = cfg.URL
	}
	clockNow := opts.ClockNow
	if clockNow == nil {
		clockNow = time.Now
	}

	return &Checker{
		name:          name,
		interval:      interval,
		confirmCfg:    confirmCfg,
		url:           cfg.URL,
		method:        method,
		headers:       cfg.Headers,
		body:          []byte(cfg.Body),
		timeout:       timeout,
		expectStatus:  cfg.Expect.Status,
		expectBody:    cfg.Expect.BodyContains,
		expectPath:    compiled,
		expectLatency: cfg.Expect.LatencyUnder.Value(),
		httpClient:    opts.HTTPClient,
		clockNow:      clockNow,
	}, nil
}

func resolveTimeout(explicit config.Duration, fallback time.Duration) time.Duration {
	if explicit.IsSet() {
		return explicit.Value()
	}
	if fallback > 0 {
		return fallback
	}

	return DefaultTimeout
}

func compileExpect(e Expect) ([]compiledRule, error) {
	if e.Status == nil && e.BodyContains == "" && len(e.BodyJSONPath) == 0 && !e.LatencyUnder.IsSet() {
		return nil, fmt.Errorf("expect must contain at least one of status / body_contains / body_jsonpath / latency_under")
	}
	if e.Status != nil && (*e.Status < 100 || *e.Status > 599) {
		return nil, fmt.Errorf("expect.status must be a valid HTTP status (100-599), got %d", *e.Status)
	}
	if e.LatencyUnder.IsSet() && e.LatencyUnder.Value() <= 0 {
		return nil, fmt.Errorf("expect.latency_under must be > 0")
	}
	var rules []compiledRule
	for i, r := range e.BodyJSONPath {
		cr, err := compileRule(r)
		if err != nil {
			return nil, fmt.Errorf("body_jsonpath[%d]: %w", i, err)
		}
		rules = append(rules, cr)
	}

	return rules, nil
}

// Type は "http" を返す。
func (c *Checker) Type() string { return "http" }

// Name は checks[] 内で一意な表示ラベル。
func (c *Checker) Name() string { return c.name }

// Interval は評価周期。
func (c *Checker) Interval() time.Duration { return c.interval }

// Confirm は失敗確信 burst 設定。
func (c *Checker) Confirm() confirm.Config { return c.confirmCfg }

// URL は監視対象 URL。テストと外部確認で使う。
func (c *Checker) URL() string { return c.url }

// Timeout は 1 回あたりの request timeout。
func (c *Checker) Timeout() time.Duration { return c.timeout }

// Evaluate は URL を叩いて response と latency を expect に照らす。
func (c *Checker) Evaluate(ctx context.Context) checker.Result {
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	var bodyReader io.Reader
	if len(c.body) > 0 {
		bodyReader = bytes.NewReader(c.body)
	}
	req, err := http.NewRequestWithContext(reqCtx, c.method, c.url, bodyReader)
	if err != nil {
		return checker.Failure(
			fmt.Sprintf("build request failed: %v", err),
			"request build failed",
			c.expectedString(),
		)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	start := c.clockNow()
	resp, err := client.Do(req)
	if err != nil {
		return checker.Failure(
			fmt.Sprintf("request failed: %v", err),
			"request failed",
			c.expectedString(),
		)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return checker.Failure(
			fmt.Sprintf("body read failed: %v", err),
			"body read failed",
			c.expectedString(),
		)
	}
	elapsed := c.clockNow().Sub(start)
	if c.expectStatus != nil && resp.StatusCode != *c.expectStatus {
		return checker.Failure(
			fmt.Sprintf("status=%d, want=%d", resp.StatusCode, *c.expectStatus),
			fmt.Sprintf("status=%d", resp.StatusCode),
			fmt.Sprintf("status=%d", *c.expectStatus),
		)
	}
	if c.expectBody != "" {
		if !bytes.Contains(body, []byte(c.expectBody)) {
			return checker.Failure(
				fmt.Sprintf("body does not contain %q", c.expectBody),
				"body_contains=false",
				fmt.Sprintf("body_contains=%q", c.expectBody),
			)
		}
	}
	if len(c.expectPath) > 0 {
		var root interface{}
		if err := json.Unmarshal(body, &root); err != nil {
			return checker.Failure(
				fmt.Sprintf("body JSON parse failed: %v", err),
				"body_jsonpath=json parse error",
				"valid JSON body",
			)
		}
		for _, rule := range c.expectPath {
			if r := evaluateRule(rule, root); !r.OK {
				return r
			}
		}
	}
	if c.expectLatency > 0 && elapsed >= c.expectLatency {
		// latency は sub-second が本質なので ms 精度に切り詰めて可読化する
		// (docs/notify.md § Payload)。
		elapsedStr := durationx.Format(elapsed.Truncate(time.Millisecond))

		return checker.Failure(
			fmt.Sprintf("latency %s >= latency_under %s", elapsedStr, durationx.Format(c.expectLatency)),
			fmt.Sprintf("latency=%s", elapsedStr),
			fmt.Sprintf("latency_under=%s", durationx.Format(c.expectLatency)),
		)
	}

	return checker.Success()
}

func (c *Checker) expectedString() string {
	var parts []string
	if c.expectStatus != nil {
		parts = append(parts, fmt.Sprintf("status=%d", *c.expectStatus))
	}
	if c.expectBody != "" {
		parts = append(parts, fmt.Sprintf("body_contains=%q", c.expectBody))
	}
	if len(c.expectPath) > 0 {
		parts = append(parts, fmt.Sprintf("body_jsonpath (%d rules)", len(c.expectPath)))
	}
	if c.expectLatency > 0 {
		parts = append(parts, fmt.Sprintf("latency_under=%s", durationx.Format(c.expectLatency)))
	}

	return strings.Join(parts, ", ")
}

type ruleOp int

const (
	opNone ruleOp = iota
	opEquals
	opContains
	opRegex
	opExists
)

type compiledRule struct {
	rawPath  string
	segments []jsonPathSegment
	op       ruleOp
	equals   interface{}
	contains string
	regex    *regexp.Regexp
	exists   bool
}

func compileRule(r JSONPathRule) (compiledRule, error) {
	segs, err := parseJSONPath(r.Path)
	if err != nil {
		return compiledRule{}, err
	}
	ops := 0
	cr := compiledRule{rawPath: r.Path, segments: segs}
	if len(r.Equals) > 0 && string(r.Equals) != "null" {
		var v interface{}
		if err := json.Unmarshal(r.Equals, &v); err != nil {
			return compiledRule{}, fmt.Errorf("equals: %w", err)
		}
		ops++
		cr.op = opEquals
		cr.equals = v
	}
	if r.Contains != nil {
		ops++
		cr.op = opContains
		cr.contains = *r.Contains
	}
	if r.Regex != nil {
		ops++
		cr.op = opRegex
		re, err := regexp.Compile(*r.Regex)
		if err != nil {
			return compiledRule{}, fmt.Errorf("regex: %w", err)
		}
		cr.regex = re
	}
	if r.Exists != nil {
		ops++
		cr.op = opExists
		cr.exists = *r.Exists
	}
	if ops != 1 {
		return compiledRule{}, fmt.Errorf("each body_jsonpath rule must have exactly one of equals/contains/regex/exists, got %d", ops)
	}

	return cr, nil
}

func evaluateRule(rule compiledRule, root interface{}) checker.Result {
	v, found := evalJSONPath(rule.segments, root)
	switch rule.op {
	case opExists:
		if found == rule.exists {
			return checker.Success()
		}

		return checker.Failure(
			fmt.Sprintf("path %s exists=%t, want %t", rule.rawPath, found, rule.exists),
			fmt.Sprintf("%s exists=%t", rule.rawPath, found),
			fmt.Sprintf("%s exists=%t", rule.rawPath, rule.exists),
		)
	case opEquals:
		if !found {
			return checker.Failure(
				fmt.Sprintf("path %s not found (equals check)", rule.rawPath),
				fmt.Sprintf("%s: not found", rule.rawPath),
				fmt.Sprintf("%s equals %v", rule.rawPath, rule.equals),
			)
		}
		if !reflect.DeepEqual(v, rule.equals) {
			return checker.Failure(
				fmt.Sprintf("path %s = %v, want %v", rule.rawPath, v, rule.equals),
				fmt.Sprintf("%s=%v", rule.rawPath, v),
				fmt.Sprintf("%s equals %v", rule.rawPath, rule.equals),
			)
		}
	case opContains:
		if !found {
			return checker.Failure(
				fmt.Sprintf("path %s not found (contains check)", rule.rawPath),
				fmt.Sprintf("%s: not found", rule.rawPath),
				fmt.Sprintf("%s contains %q", rule.rawPath, rule.contains),
			)
		}
		s, ok := v.(string)
		if !ok {
			return checker.Failure(
				fmt.Sprintf("path %s is not a string (contains check)", rule.rawPath),
				fmt.Sprintf("%s: not a string", rule.rawPath),
				fmt.Sprintf("%s contains %q", rule.rawPath, rule.contains),
			)
		}
		if !strings.Contains(s, rule.contains) {
			return checker.Failure(
				fmt.Sprintf("path %s = %q, does not contain %q", rule.rawPath, s, rule.contains),
				fmt.Sprintf("%s=%q", rule.rawPath, s),
				fmt.Sprintf("%s contains %q", rule.rawPath, rule.contains),
			)
		}
	case opRegex:
		if !found {
			return checker.Failure(
				fmt.Sprintf("path %s not found (regex check)", rule.rawPath),
				fmt.Sprintf("%s: not found", rule.rawPath),
				fmt.Sprintf("%s matches /%s/", rule.rawPath, rule.regex),
			)
		}
		s, ok := v.(string)
		if !ok {
			return checker.Failure(
				fmt.Sprintf("path %s is not a string (regex check)", rule.rawPath),
				fmt.Sprintf("%s: not a string", rule.rawPath),
				fmt.Sprintf("%s matches /%s/", rule.rawPath, rule.regex),
			)
		}
		if !rule.regex.MatchString(s) {
			return checker.Failure(
				fmt.Sprintf("path %s = %q does not match /%s/", rule.rawPath, s, rule.regex),
				fmt.Sprintf("%s=%q", rule.rawPath, s),
				fmt.Sprintf("%s matches /%s/", rule.rawPath, rule.regex),
			)
		}
	case opNone:
		return checker.Failure(
			fmt.Sprintf("path %s has no operator", rule.rawPath),
			"no operator",
			"one of equals/contains/regex/exists",
		)
	}

	return checker.Success()
}
