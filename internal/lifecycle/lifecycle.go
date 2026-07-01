// Package lifecycle は watch サブコマンドと run サブコマンドが共有する
// panic recover / graceful shutdown announcement / dry-run 対応 notifier を
// 提供する。仕様は docs/architecture.md § 自身の死活 と docs/cli.md § watch /
// docs/notify.md § --dry-run 時の挙動 に従う。
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

// Sender は Slack Incoming Webhook への 1 通送信の抽象。notify.Client と
// test 用 fake の共通 interface。
type Sender interface {
	Send(ctx context.Context, payload notify.SlackPayload) error
}

// Notifier は dry-run 分岐込みの notify wrapper。DryRun=true なら Sender を
// 呼ばず Stderr に payload を JSON で書き出す (docs/notify.md § --dry-run
// 時の挙動)。Stderr nil の場合は os.Stderr を使う。
type Notifier struct {
	Sender  Sender
	Options notify.Options
	DryRun  bool
	Stderr  io.Writer
}

// Send は payload を送信する。DryRun 時は Sender を呼ばずに JSON pretty print
// を Stderr に書く。
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

// SendShutdown は SIGINT / SIGTERM 由来の graceful shutdown を Slack へ
// best-effort で通知する。docs/cli.md § watch 動作 の text 形式に従い、
// attachments は付けない (severity 概念を持たないため)。
func SendShutdown(ctx context.Context, n *Notifier, host, signalName string, now time.Time) error {
	text := fmt.Sprintf("[mitsume] watch stopped on host=%s (signal=%s, time=%s)",
		host, signalName, now.Format(time.RFC3339))
	payload := notify.BuildAnnouncement(text, n.Options)

	return n.Send(ctx, payload)
}

// SendPanicNotice は panic を Slack に通知する payload を作って送る。
// docs/architecture.md § 自身の死活 の「recover → notify → re-panic」の
// notify 部分に対応する。subcommand は通知文の「どのサブコマンドで起きたか」
// を識別する短い名前 (例: "check" / "watch")。best-effort なので、返り値の
// error は呼び出し側が適宜 stderr にログするだけで re-panic を止めない。
func SendPanicNotice(ctx context.Context, n *Notifier, subcommand, host string, panicVal any, now time.Time) error {
	if subcommand == "" {
		subcommand = "unknown"
	}
	text := fmt.Sprintf("[mitsume] %s panicked on host=%s (panic=%v, time=%s)",
		subcommand, host, panicVal, now.Format(time.RFC3339))
	payload := notify.BuildAnnouncement(text, n.Options)

	return n.Send(ctx, payload)
}

// GuardPanic は fn を実行し、panic を捕捉したら best-effort で notify を打った
// 後、同じ panic 値で re-panic する。呼び出し側は defer で recover して
// os.Exit(...) するか、そのまま Go runtime に握らせて stack trace 出力
// + exit code 2 で die させる (docs/cli.md § watch § exit code)。clockNow が
// nil なら time.Now が使われる。subcommand は SendPanicNotice にそのまま渡す。
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
