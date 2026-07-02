// Package notify は Slack Incoming Webhook 用の payload builder と HTTP retry
// 付き client を提供する。payload 形式と retry policy の仕様は docs/notify.md
// に従う。webhook URL は秘密情報として扱い、error / ログ / payload に含めない。
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

// Options は payload に載る username / icon をまとめる。
// docs/configuration.md § notify の username / icon_emoji / icon_url に対応する。
type Options struct {
	Username  string
	IconEmoji string
	IconURL   string
}

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

// SlackPayload は Slack Incoming Webhook が受ける JSON schema (使う部分のみ)。
type SlackPayload struct {
	Username    string       `json:"username,omitempty"`
	IconEmoji   string       `json:"icon_emoji,omitempty"`
	IconURL     string       `json:"icon_url,omitempty"`
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// BuildAnnouncement は明示イベント用 payload を作る。text に msg をそのまま
// 載せ、attachments は付けない (severity や observed/expected の概念がないため)。
func BuildAnnouncement(msg string, opts Options) SlackPayload {
	return SlackPayload{
		Username:  opts.Username,
		IconEmoji: opts.IconEmoji,
		IconURL:   opts.IconURL,
		Text:      msg,
	}
}

// Failure は checker の失敗検知 payload を組み立てる材料。docs/notify.md
// § payload 形式 の fields (host / check / type / time / observed / expected)
// と 1 対 1。
type Failure struct {
	Host     string
	Check    string
	Type     string
	Error    string
	Observed string
	Expected string
	Time     time.Time
}

// BuildFailure は checker の failure 確定通知用 payload を作る。text の 1 行目は
// [mitsume] <check> failed (<type>: <error>) 形式、続いて host / time を改行区切り。
// attachments[0].color は "danger" 固定。
func BuildFailure(f Failure, opts Options) SlackPayload {
	ts := f.Time.Format(time.RFC3339)
	text := fmt.Sprintf("[mitsume] %s failed (%s: %s)\nhost: %s\ntime: %s",
		f.Check, f.Type, f.Error, f.Host, ts)

	return SlackPayload{
		Username:    opts.Username,
		IconEmoji:   opts.IconEmoji,
		IconURL:     opts.IconURL,
		Text:        text,
		Attachments: buildAttachments("danger", f.Host, f.Check, f.Type, ts, f.Observed, f.Expected),
	}
}

// Success は mitsume run の成功通知 payload を組み立てる材料。docs/notify.md
// § Success payload に従い、observed / expected はともに exit=0 固定のため
// field として持たない。
type Success struct {
	Host  string
	Check string
	Type  string
	Time  time.Time
}

// BuildSuccess は mitsume run の成功通知用 payload を作る。text の 1 行目は
// [mitsume] <check> succeeded (<type>: exit=0) 形式、attachments[0].color は
// "good"、observed / expected は exit=0。stderr tail は含めない (docs/notify.md
// § Success payload)。
func BuildSuccess(s Success, opts Options) SlackPayload {
	ts := s.Time.Format(time.RFC3339)
	text := fmt.Sprintf("[mitsume] %s succeeded (%s: exit=0)\nhost: %s\ntime: %s",
		s.Check, s.Type, s.Host, ts)

	return SlackPayload{
		Username:    opts.Username,
		IconEmoji:   opts.IconEmoji,
		IconURL:     opts.IconURL,
		Text:        text,
		Attachments: buildAttachments("good", s.Host, s.Check, s.Type, ts, "exit=0", "exit=0"),
	}
}

// buildAttachments は failure / success payload 共通の attachment を組み立てる。
// fields の並び (host / check / type / time / observed / expected) は
// docs/notify.md § Payload に従う。
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

// DefaultBackoffs は Slack HTTP retry の待ち時間 (1s → 2s → 4s、合計 7 秒)。
var DefaultBackoffs = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
}

// Client は Slack Incoming Webhook 送信 client。WebhookURL は秘密情報なので
// error / ログに含めない。Backoffs が nil なら DefaultBackoffs を使う。
type Client struct {
	HTTPClient *http.Client
	WebhookURL string
	Backoffs   []time.Duration
}

// Send は payload を Slack に POST する。5xx / network error は Backoffs に
// 従って retry、4xx は即 fail。全滅時は最後の error を wrap して返す。context
// cancel は sleep 中でも即座に効く。
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

// sanitizeTransportError は net/http や net/url が返す *url.Error から URL を
// 除去する。*url.Error.Error() は Slack Incoming Webhook のようにパス末尾に
// 秘密トークンを持つ URL 全体をそのまま含むため、そのまま wrap して error として
// 返すと stderr / ログに credentials が漏れる。docs/notify.md § 秘密情報の扱い の
// 「webhook URL を error / log / payload に出さない」規約を守るため、URL 部分を
// 落として Op と underlying error だけを残す。
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
