// Package lifecycle は check / watch / run が共有する、dry-run に対応した
// notifier と、process の停止・panic を知らせる通知を提供する。
package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/suecharo/mitsume/internal/notify"
)

// Sender は 1 通の送信。notify.Client とテスト用の fake が実装する。
type Sender interface {
	Send(ctx context.Context, payload notify.SlackPayload) error
}

// Notifier は DryRun のとき Sender を呼ばず、payload を JSON で Stderr に書く。
// Stderr が nil なら os.Stderr を使う。
type Notifier struct {
	Sender Sender
	DryRun bool
	Stderr io.Writer
}

// Send は payload を送る。
func (n *Notifier) Send(ctx context.Context, payload notify.SlackPayload) error {
	if n.DryRun {
		w := n.Stderr
		if w == nil {
			w = os.Stderr
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return fmt.Errorf("lifecycle: marshal payload: %w", err)
		}
		fmt.Fprintln(w, string(data))

		return nil
	}
	if n.Sender == nil {
		return fmt.Errorf("lifecycle: notifier sender is nil")
	}

	return n.Sender.Send(ctx, payload)
}

// SendShutdown は watch の停止を知らせる。
func SendShutdown(ctx context.Context, n *Notifier, host, signalName string, now time.Time) error {
	text := fmt.Sprintf("[mitsume] watch stopped on host=%s (signal=%s, time=%s)",
		host, signalName, now.Format(time.RFC3339))

	return n.Send(ctx, notify.BuildAnnouncement(text))
}

// SendPanicNotice は panic を知らせる。subcommand は "check" / "watch" など。
func SendPanicNotice(ctx context.Context, n *Notifier, subcommand, host string, panicVal any, now time.Time) error {
	if subcommand == "" {
		subcommand = "unknown"
	}
	text := fmt.Sprintf("[mitsume] %s panicked on host=%s (panic=%v, time=%s)",
		subcommand, host, panicVal, now.Format(time.RFC3339))

	return n.Send(ctx, notify.BuildAnnouncement(text))
}

// GuardPanic は fn の panic を捕捉して通知を送り、同じ値で panic し直す。
// 通知に失敗しても panic し直すことは止めず、process は Go runtime によって
// exit code 2 で終わる。clockNow が nil なら time.Now を使う。
func GuardPanic(ctx context.Context, n *Notifier, subcommand, host string, clockNow func() time.Time, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		now := time.Now()
		if clockNow != nil {
			now = clockNow()
		}
		if err := SendPanicNotice(ctx, n, subcommand, host, r, now); err != nil {
			fmt.Fprintf(os.Stderr, "mitsume: panic notify failed: %v\n", err)
		}
		panic(r)
	}()
	fn()
}
