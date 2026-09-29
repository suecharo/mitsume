// Package notify は Slack Incoming Webhook 用の payload builder と、retry 付きの
// 送信 client を提供する。webhook URL は秘密情報なので error / ログに含めない。
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Field は Slack attachment field。
type Field struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short,omitempty"`
}

// Attachment は Slack payload の attachments 要素。
type Attachment struct {
	Color  string  `json:"color,omitempty"`
	Fields []Field `json:"fields,omitempty"`
}

// SlackPayload は Slack Incoming Webhook に POST する JSON のうち mitsume が使う部分。
// Slack App の Webhook は username / icon / channel の上書きを無視するため持たない。
type SlackPayload struct {
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// BuildAnnouncement は msg をそのまま text に載せた、attachments の無い payload を作る。
func BuildAnnouncement(msg string) SlackPayload {
	return SlackPayload{Text: msg}
}

// Failure は failure payload の材料。
type Failure struct {
	Host     string
	Check    string
	Type     string
	Error    string
	Observed string
	Expected string
	Time     time.Time
}

// BuildFailure は failure payload を作る。
func BuildFailure(f Failure) SlackPayload {
	ts := f.Time.Format(time.RFC3339)
	text := fmt.Sprintf("[mitsume] %s failed (%s: %s)\nhost: %s\ntime: %s",
		f.Check, f.Type, f.Error, f.Host, ts)

	return SlackPayload{
		Text:        text,
		Attachments: buildAttachments("danger", f.Host, f.Check, f.Type, ts, f.Observed, f.Expected),
	}
}

// Success は mitsume run の成功 payload の材料。observed / expected は常に exit=0。
type Success struct {
	Host  string
	Check string
	Type  string
	Time  time.Time
}

// BuildSuccess は mitsume run の成功 payload を作る。
func BuildSuccess(s Success) SlackPayload {
	ts := s.Time.Format(time.RFC3339)
	text := fmt.Sprintf("[mitsume] %s succeeded (%s: exit=0)\nhost: %s\ntime: %s",
		s.Check, s.Type, s.Host, ts)

	return SlackPayload{
		Text:        text,
		Attachments: buildAttachments("good", s.Host, s.Check, s.Type, ts, "exit=0", "exit=0"),
	}
}

func buildAttachments(color, host, check, typ, ts, observed, expected string) []Attachment {
	return []Attachment{{
		Color: color,
		Fields: []Field{
			{Title: "host", Value: host, Short: true},
			{Title: "check", Value: check, Short: true},
			{Title: "type", Value: typ, Short: true},
			{Title: "time", Value: ts, Short: true},
			{Title: "observed", Value: observed, Short: false},
			{Title: "expected", Value: expected, Short: false},
		},
	}}
}

// DefaultBackoffs は retry の前に挟む待ち時間。
var DefaultBackoffs = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
}

// Client は Slack Incoming Webhook への送信 client。Backoffs が nil なら
// DefaultBackoffs を使う。
type Client struct {
	HTTPClient *http.Client
	WebhookURL string
	Backoffs   []time.Duration
}

// Send は payload を POST する。4xx は設定の誤りで retry しても直らないので
// 即座に error を返す。それ以外の失敗は Backoffs に従って retry する。
func (c *Client) Send(ctx context.Context, payload SlackPayload) error {
	if c.WebhookURL == "" {
		return fmt.Errorf("notify: webhook URL is empty")
	}
	backoffs := c.Backoffs
	if backoffs == nil {
		backoffs = DefaultBackoffs
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: marshal payload: %w", err)
	}
	attempts := len(backoffs) + 1
	var lastErr error
	for i := range attempts {
		if i > 0 {
			timer := time.NewTimer(backoffs[i-1])
			select {
			case <-ctx.Done():
				timer.Stop()

				return ctx.Err()
			case <-timer.C:
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.WebhookURL, bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("notify: build request: %w", sanitizeTransportError(err))
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("notify: HTTP request failed: %w", sanitizeTransportError(err))

			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return fmt.Errorf("notify: client error (status %d)", resp.StatusCode)
		}
		lastErr = fmt.Errorf("notify: server error (status %d)", resp.StatusCode)
	}

	return fmt.Errorf("notify: all attempts failed: %w", lastErr)
}

// sanitizeTransportError は *url.Error から URL を除き、Op と underlying error だけを
// 残す。*url.Error.Error() は URL 全体を含み、Slack Incoming Webhook の URL はパス
// 末尾が秘密トークンなので、そのまま wrap すると stderr に漏れる。
func sanitizeTransportError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Op == "" {
			return urlErr.Err
		}

		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}

	return err
}
