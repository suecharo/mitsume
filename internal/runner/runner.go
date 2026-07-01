// Package runner は check / watch サブコマンドの評価ループを担う。全 checker
// を並列に評価し、docs/checkers.md § confirm の失敗確信 burst を回し、失敗
// 確定した checker について Slack alert を送る。docs/cli.md § check / watch と
// docs/architecture.md § 失敗確信モデル に従う。
package runner

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/suecharo/mitsume/internal/checker"
	"github.com/suecharo/mitsume/internal/checker/deadman"
	"github.com/suecharo/mitsume/internal/heartbeat"
	"github.com/suecharo/mitsume/internal/lifecycle"
	"github.com/suecharo/mitsume/internal/notify"
)

// DefaultContainerEvaluationTimeout は container checker 呼び出しに runner が
// 被せる hard cap timeout。docs/checkers.md § container § 固有の挙動 に従い
// HTTP / cmd の暗黙 default と揃えた 30s。config field は持たない。
const DefaultContainerEvaluationTimeout = 30 * time.Second

// Sleeper は runner の interval / confirm.interval 待ちを抽象化する。realSleeper
// を default とし、テスト用 fake で決定的にサイクルを進められる形にする
// (tests/README.md § mock 境界: 時刻)。
type Sleeper interface {
	// Sleep は d だけ待機する。ctx が cancel されたら即座に ctx.Err を返す。
	// d <= 0 の場合は即座に nil を返す。
	Sleep(ctx context.Context, d time.Duration) error
}

// realSleeper は time.NewTimer ベースの production 実装。
type realSleeper struct{}

// Sleep は time.NewTimer 経由で d だけ待つ。ctx.Done で即座に unblock する。
func (realSleeper) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Runner は check モード (RunOnce) と watch モード (RunLoop) の両方を担う。
// 状態は持たず、Checkers に対する評価の orchestration に責務を絞る。
type Runner struct {
	// Checkers は評価対象。loader.BuildCheckers の返り値をそのまま渡す想定。
	Checkers []checker.Checker
	// HeartbeatFile は deadman を含む config で必須。pre-flight と per-cycle
	// snapshot load で使う (docs/heartbeat.md § 読み込みモデル)。
	HeartbeatFile string
	// Notifier は Slack alert / dry-run 分岐を含む notify wrapper。呼び出し側は
	// notify.Client (production) か test 用 fake Sender を Sender に埋め込む。
	// nil は runtime error なので cmd 側は必ず設定する。
	Notifier *lifecycle.Notifier
	// Host は Slack payload の host field に載せる識別子。
	Host string
	// ClockNow は alert / panic notify の time stamp provider。nil なら time.Now。
	ClockNow func() time.Time
	// Sleeper は interval / confirm.interval 待ち。nil なら realSleeper。
	Sleeper Sleeper
	// ContainerEvaluationTimeout は container checker 呼び出しに被せる hard cap。
	// 0 なら DefaultContainerEvaluationTimeout。
	ContainerEvaluationTimeout time.Duration
}

// RunOnce は全 checker を並列に 1 回評価する (check サブコマンド用)。個別 check
// の failure は exit code に反映しない (docs/cli.md § check § exit code)。返り値の
// error は pre-flight の heartbeat 読み込み失敗など、mitsume 側の異常のみ。
func (r *Runner) RunOnce(ctx context.Context) error {
	if err := r.PreflightHeartbeat(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, c := range r.Checkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lifecycle.GuardPanic(ctx, r.Notifier, r.Host, r.ClockNow, func() {
				r.evaluateWithBurst(ctx, c)
			})
		}()
	}
	wg.Wait()

	return nil
}

// RunLoop は起動直後に各 checker を 1 回評価し、以降は checker ごとに独立して
// interval ごとに評価する (watch サブコマンド用)。ctx.Done で graceful shutdown
// し、走行中の評価結果は破棄する (docs/cli.md § watch § 動作)。
func (r *Runner) RunLoop(ctx context.Context) error {
	if err := r.PreflightHeartbeat(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, c := range r.Checkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lifecycle.GuardPanic(ctx, r.Notifier, r.Host, r.ClockNow, func() {
				r.checkerLoop(ctx, c)
			})
		}()
	}
	wg.Wait()

	return nil
}

// PreflightHeartbeat は deadman を含む config で起動時に heartbeat file の
// read + parse が成功するかを確認する (docs/heartbeat.md § 読み込みモデル の
// 起動時 fail-fast)。deadman を含まない場合は no-op。
func (r *Runner) PreflightHeartbeat() error {
	if !r.hasDeadman() {
		return nil
	}
	if r.HeartbeatFile == "" {
		return fmt.Errorf("runner: heartbeat file path is required (config contains deadman checker)")
	}
	if _, err := heartbeat.Load(r.HeartbeatFile); err != nil {
		return err
	}

	return nil
}

func (r *Runner) hasDeadman() bool {
	for _, c := range r.Checkers {
		if c.Type() == "deadman" {
			return true
		}
	}

	return false
}

// checkerLoop は watch モードで 1 checker を回すループ。起動直後に即時評価し、
// 以降 interval ごとに繰り返す。ctx.Done で loop から抜ける。
func (r *Runner) checkerLoop(ctx context.Context, c checker.Checker) {
	r.evaluateWithBurst(ctx, c)
	if ctx.Err() != nil {
		return
	}
	interval := c.Interval()
	sleeper := r.sleeper()
	for {
		if err := sleeper.Sleep(ctx, interval); err != nil {
			return
		}
		r.evaluateWithBurst(ctx, c)
		if ctx.Err() != nil {
			return
		}
	}
}

// evaluateWithBurst は 1 checker cycle 分の評価を担う。initial 評価 → 失敗検知
// なら confirm burst → 全滅で alert / 途中成功で reset の状態遷移。alert 送信時
// の payload には最終確認 (最新観測) の Result を載せる (docs/notify.md § 発火
// モデル)。
func (r *Runner) evaluateWithBurst(ctx context.Context, c checker.Checker) {
	r.refreshDeadmanSnapshot(c)
	first := r.evaluate(ctx, c)
	if ctx.Err() != nil {
		return
	}
	if first.OK {
		return
	}
	cfg := c.Confirm()
	if cfg.OneStrike {
		r.sendAlert(ctx, c, first)

		return
	}
	last := first
	sleeper := r.sleeper()
	for i := 1; i < cfg.Checks; i++ {
		if err := sleeper.Sleep(ctx, cfg.Interval); err != nil {
			return
		}
		again := r.evaluate(ctx, c)
		if ctx.Err() != nil {
			return
		}
		if again.OK {
			return
		}
		last = again
	}
	r.sendAlert(ctx, c, last)
}

// evaluate は 1 回の Evaluate 呼び出し。container checker には hard cap timeout を
// 被せる (docs/checkers.md § container § 固有の挙動)。他 checker は呼び出し側の
// ctx をそのまま渡す (HTTP / cmd checker は自身で timeout を管理する)。
func (r *Runner) evaluate(ctx context.Context, c checker.Checker) checker.Result {
	if c.Type() == "container" {
		timeout := r.containerEvalTimeout()
		ctx2, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		return c.Evaluate(ctx2)
	}

	return c.Evaluate(ctx)
}

// refreshDeadmanSnapshot は c が deadman なら snapshot を per-cycle で load して
// SetSnapshotProvider で差し込む。checker cycle (通常 interval + burst) の起点で
// のみ呼び、burst 内では snapshot を維持する。deadman 以外は no-op。
// snapshot load 自体が error になった場合は provider が error を返す形にして
// 該当サイクルの deadman 評価を failure として扱う (loop 継続、次サイクルで再度
// load を試みる)。
func (r *Runner) refreshDeadmanSnapshot(c checker.Checker) {
	d, ok := c.(*deadman.Checker)
	if !ok {
		return
	}
	if r.HeartbeatFile == "" {
		return
	}
	file, err := heartbeat.Load(r.HeartbeatFile)
	d.SetSnapshotProvider(func() (*heartbeat.File, error) { return file, err })
}

// sendAlert は failure 確定通知を送る。cmd checker の Stderr は payload text の
// 末尾に改行区切りで追加 (docs/notify.md § payload 形式)。Notifier.Send は
// dry-run 分岐を含み、通知失敗は stderr に log を吐くだけで evaluation は継続
// (docs/notify.md § 通知失敗時の retry: check 評価結果を変えない)。
func (r *Runner) sendAlert(ctx context.Context, c checker.Checker, result checker.Result) {
	if r.Notifier == nil {
		fmt.Fprintf(os.Stderr, "mitsume: alert dropped for %s: notifier is not configured\n", c.Name())

		return
	}
	failure := notify.Failure{
		Host:     r.Host,
		Check:    c.Name(),
		Type:     c.Type(),
		Error:    result.Error,
		Observed: result.Observed,
		Expected: result.Expected,
		Time:     r.now(),
	}
	payload := notify.BuildFailure(failure, r.Notifier.Options)
	if result.Stderr != "" {
		payload.Text = payload.Text + "\n" + result.Stderr
	}
	if err := r.Notifier.Send(ctx, payload); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume: alert notify failed for %s: %v\n", c.Name(), err)
	}
}

func (r *Runner) now() time.Time {
	if r.ClockNow != nil {
		return r.ClockNow()
	}

	return time.Now()
}

func (r *Runner) sleeper() Sleeper {
	if r.Sleeper != nil {
		return r.Sleeper
	}

	return realSleeper{}
}

func (r *Runner) containerEvalTimeout() time.Duration {
	if r.ContainerEvaluationTimeout > 0 {
		return r.ContainerEvaluationTimeout
	}

	return DefaultContainerEvaluationTimeout
}
