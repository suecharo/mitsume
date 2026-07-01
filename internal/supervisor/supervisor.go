// Package supervisor は mitsume run サブコマンドの子プロセス supervisor を
// 実装する。子の stdout / stderr を親に tee し、stderr は ring buffer に保存
// して失敗通知に載せる。timeout / grace / signal forward / exit code の詳細は
// docs/cli.md § run と docs/notify.md § payload 形式 に従う。
package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/suecharo/mitsume/internal/lifecycle"
	"github.com/suecharo/mitsume/internal/notify"
	"github.com/suecharo/mitsume/internal/tailio"
)

// exit code 定数 (docs/cli.md § run § exit code)。
const (
	// TimeoutExitCode は --timeout 発火で kill した場合の exit code (GNU
	// timeout(1) 慣習)。
	TimeoutExitCode = 124
	// PermissionDeniedExitCode は子の実行 permission が拒否された場合の
	// exit code (bash 慣習)。
	PermissionDeniedExitCode = 126
	// CommandNotFoundExitCode は子が PATH 上に見つからない場合の exit code
	// (bash 慣習)。
	CommandNotFoundExitCode = 127
	// SignalBase は「128 + signum」慣習の base (bash 慣習)。
	SignalBase = 128
	// InternalErrorExitCode は supervisor 側の設定不正 / 内部異常 (Notifier
	// 未設定など) で使う。
	InternalErrorExitCode = 1
)

// default 値 (docs/cli.md § run の flag default)。
const (
	DefaultGracePeriod       = 5 * time.Second
	DefaultStderrBufferBytes = 16 * 1024
	DefaultStderrTailLines   = 20
	DefaultStderrTailBytes   = 2 * 1024
)

// Config は Run に渡す実行時パラメータ。
type Config struct {
	// Name は表示ラベル (docs/cli.md § 識別子解決)。空なら Command[0] の
	// basename を使う。
	Name string
	// Command は子プロセスとその引数 (直接 exec、shell を経由しない)。
	Command []string
	// Timeout は子プロセスの実行時間上限。0 なら無制限。
	Timeout time.Duration
	// GracePeriod は timeout 発火後 SIGTERM から SIGKILL までの猶予。0 なら
	// DefaultGracePeriod (5s)。
	GracePeriod time.Duration
	// StderrBufferBytes は stderr ring buffer 上限。0 なら DefaultStderrBufferBytes
	// (16KB)。失敗通知に載せる tail は StderrTailLines と StderrTailBytes の
	// 小さい方 (bytes) を採用する (docs/cli.md § run § flag)。
	StderrBufferBytes int
	// StderrTailLines は失敗通知に含める stderr 末尾の最大行数。0 なら
	// DefaultStderrTailLines (20)。
	StderrTailLines int
	// StderrTailBytes は失敗通知に含める stderr 末尾の最大 byte 数。0 なら
	// DefaultStderrTailBytes (2KB)。
	StderrTailBytes int
	// QuietOnSuccess が true なら成功時 (exit 0) は通知しない (失敗時のみ)。
	QuietOnSuccess bool
	// Notifier は Slack 通知 (dry-run 分岐含む)。nil は internal error。
	Notifier *lifecycle.Notifier
	// Host は Slack payload の host field。
	Host string
	// Stdout は子の stdout を親に tee する先。nil なら os.Stdout。
	Stdout io.Writer
	// Stderr は子の stderr を親に tee する先。nil なら os.Stderr。ring buffer
	// にも同時書き込まれる。
	Stderr io.Writer
	// ClockNow は通知 payload の時刻 provider。nil なら time.Now。
	ClockNow func() time.Time
	// DisableSignalForward が true なら SIGINT/SIGTERM/SIGHUP/SIGQUIT の
	// 子への転送を行わない。unit test でグローバル signal 干渉を避けるための
	// 注入経路。production の cmd/mitsume/run.go は false (=default) のまま使う。
	DisableSignalForward bool
}

// Run は子プロセスを起動して完了を待ち、docs/cli.md § run § exit code の
// exit code を返す。呼び出し側 (cmd/mitsume/run.go) は os.Exit(int) する想定。
func Run(ctx context.Context, cfg Config) int {
	if cfg.Notifier == nil {
		fmt.Fprintln(os.Stderr, "mitsume run: notifier is required")

		return InternalErrorExitCode
	}
	if len(cfg.Command) == 0 {
		fmt.Fprintln(os.Stderr, "mitsume run: command is required")

		return InternalErrorExitCode
	}

	name := cfg.Name
	if name == "" {
		name = filepath.Base(cfg.Command[0])
	}

	stdout := writerOrDefault(cfg.Stdout, os.Stdout)
	stderr := writerOrDefault(cfg.Stderr, os.Stderr)
	ring := newRingBuffer(bufferBytes(cfg))

	// context.Background を渡して exec 内部の ctx-based kill (SIGKILL 直行) を
	// 無効化する。子の終了制御は waitOrKill が SIGTERM → grace → SIGKILL で自前
	// 管理する (docs/cli.md § run § 動作)。
	proc := exec.CommandContext(context.Background(), cfg.Command[0], cfg.Command[1:]...)
	proc.Stdout = stdout
	proc.Stderr = io.MultiWriter(stderr, ring)

	if err := proc.Start(); err != nil {
		code := classifyStartError(err)
		cfg.sendStartFailure(ctx, name, code, err)

		return code
	}

	stopForward := startSignalForward(cfg, proc)
	defer stopForward()

	completed := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = proc.Wait()
		close(completed)
	}()

	timedOut := waitOrKill(ctx, cfg, proc, completed)

	exitCode := decodeExit(waitErr)
	if timedOut {
		exitCode = TimeoutExitCode
	}

	if exitCode == 0 && !timedOut {
		if !cfg.QuietOnSuccess {
			cfg.sendSuccess(ctx, name)
		}

		return 0
	}
	reason := formatFailureReason(exitCode, timedOut, cfg.Timeout)
	cfg.sendRunFailure(ctx, name, exitCode, reason, ring)

	return exitCode
}

// waitOrKill は子の完了 (completed close)、Timeout、ctx.Done のいずれかを待つ。
// Timeout / ctx.Done を検知したら SIGTERM → grace → SIGKILL の別 goroutine を
// 起こし、waitOrKill 自身は completed を待ってから return する。timedOut フラグは
// timeout 由来なら true、それ以外なら false。
func waitOrKill(
	ctx context.Context, cfg Config, proc *exec.Cmd, completed <-chan struct{},
) bool {
	var timeoutCh <-chan time.Time
	if cfg.Timeout > 0 {
		t := time.NewTimer(cfg.Timeout)
		defer t.Stop()
		timeoutCh = t.C
	}
	timedOut := false
	killed := false
	for {
		select {
		case <-completed:
			return timedOut
		case <-timeoutCh:
			if !killed {
				timedOut = true
				killed = true
				go killChild(proc, completed, gracePeriod(cfg))
				timeoutCh = nil
			}
		case <-ctx.Done():
			if !killed {
				killed = true
				go killChild(proc, completed, gracePeriod(cfg))
			}
			// ctx.Done は persistent。killed=true 経路で以降の fire は空回りする。
			// 次 iteration の select で completed が優先的に選ばれる (Go select は
			// fair、待機時間の長いブランチが最終的に発火する)。
		}
	}
}

// killChild は SIGTERM を送り、grace 期間内に完了しなければ SIGKILL を送る。
// 完了通知は呼び出し側の completed 監視で受け取る。
func killChild(proc *exec.Cmd, completed <-chan struct{}, grace time.Duration) {
	_ = proc.Process.Signal(syscall.SIGTERM)
	select {
	case <-completed:
		return
	case <-time.After(grace):
	}
	_ = proc.Process.Kill()
}

// decodeExit は exec.Cmd.Wait の error を exit code に変換する。signal で
// kill された場合は 128 + signum (bash 慣習、docs/cli.md § run § exit code)。
func decodeExit(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return SignalBase + int(ws.Signal())
			}
		}
		code := exitErr.ExitCode()
		if code < 0 {
			return InternalErrorExitCode
		}

		return code
	}

	return InternalErrorExitCode
}

// classifyStartError は proc.Start が返す error を exit code に分類する
// (bash 慣習、docs/cli.md § run § exit code)。
func classifyStartError(err error) int {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return CommandNotFoundExitCode
	}
	if errors.Is(err, fs.ErrPermission) {
		return PermissionDeniedExitCode
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.EACCES, syscall.EPERM:
			return PermissionDeniedExitCode
		case syscall.ENOENT:
			return CommandNotFoundExitCode
		}
	}

	return CommandNotFoundExitCode
}

// startSignalForward は SIGINT/SIGTERM/SIGHUP/SIGQUIT を子に転送する goroutine を
// start する。DisableSignalForward=true なら no-op。返り値の stop() で goroutine を
// 停止できる。呼び出し側は必ず defer stop() する。
func startSignalForward(cfg Config, proc *exec.Cmd) func() {
	if cfg.DisableSignalForward {
		return func() {}
	}
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case sig := <-sigCh:
				if proc.Process != nil {
					_ = proc.Process.Signal(sig)
				}
			case <-stopCh:
				return
			}
		}
	}()

	return func() {
		signal.Stop(sigCh)
		close(stopCh)
		wg.Wait()
	}
}

func formatFailureReason(exitCode int, timedOut bool, timeout time.Duration) string {
	if timedOut {
		return fmt.Sprintf("timed out after %s (killed with SIGTERM/SIGKILL)", timeout)
	}
	if exitCode >= SignalBase && exitCode < SignalBase+64 {
		sig := syscall.Signal(exitCode - SignalBase)

		return fmt.Sprintf("killed by signal %s (exit=%d)", sig, exitCode)
	}
	if exitCode == CommandNotFoundExitCode {
		return "command not found"
	}
	if exitCode == PermissionDeniedExitCode {
		return "permission denied"
	}

	return fmt.Sprintf("exit=%d", exitCode)
}

// sendSuccess は成功通知 (announcement) を送る。docs/notify.md § 明示通知
// § mitsume run 内部 に従い、text に success の 1 行を載せる。
func (cfg Config) sendSuccess(ctx context.Context, name string) {
	text := fmt.Sprintf("[mitsume] %s succeeded on host=%s (time=%s)",
		name, cfg.Host, nowFn(cfg).Format(time.RFC3339))
	payload := notify.BuildAnnouncement(text, cfg.Notifier.Options)
	if err := cfg.Notifier.Send(ctx, payload); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume run: success notify failed for %s: %v\n", name, err)
	}
}

// sendStartFailure は proc.Start が失敗した場合の通知。stderr ring buffer は
// 子が実行に至らないため空。start error の内容を reason に載せる。
func (cfg Config) sendStartFailure(ctx context.Context, name string, exitCode int, startErr error) {
	failure := notify.Failure{
		Host:     cfg.Host,
		Check:    name,
		Type:     "run",
		Error:    fmt.Sprintf("start failed: %v", startErr),
		Observed: fmt.Sprintf("exit=%d", exitCode),
		Expected: "exit=0",
		Time:     nowFn(cfg),
	}
	payload := notify.BuildFailure(failure, cfg.Notifier.Options)
	if err := cfg.Notifier.Send(ctx, payload); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume run: start-failure notify failed for %s: %v\n", name, err)
	}
}

// sendRunFailure は子が終わった後の失敗通知。stderr ring buffer の末尾を
// tailio 経由で切り出し、payload text 末尾に付ける (docs/notify.md § payload
// 形式)。
func (cfg Config) sendRunFailure(
	ctx context.Context, name string, exitCode int, reason string, ring *ringBuffer,
) {
	failure := notify.Failure{
		Host:     cfg.Host,
		Check:    name,
		Type:     "run",
		Error:    reason,
		Observed: fmt.Sprintf("exit=%d", exitCode),
		Expected: "exit=0",
		Time:     nowFn(cfg),
	}
	payload := notify.BuildFailure(failure, cfg.Notifier.Options)
	tail := tailio.Truncate(ring.Bytes(), tailLines(cfg), tailBytes(cfg))
	if len(tail) > 0 {
		payload.Text = payload.Text + "\n" + string(bytes.TrimRight(tail, "\n"))
	}
	if err := cfg.Notifier.Send(ctx, payload); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume run: failure notify failed for %s: %v\n", name, err)
	}
}

// -------- helpers --------

func writerOrDefault(w io.Writer, def io.Writer) io.Writer {
	if w != nil {
		return w
	}

	return def
}

func bufferBytes(cfg Config) int {
	if cfg.StderrBufferBytes > 0 {
		return cfg.StderrBufferBytes
	}

	return DefaultStderrBufferBytes
}

func tailLines(cfg Config) int {
	if cfg.StderrTailLines > 0 {
		return cfg.StderrTailLines
	}

	return DefaultStderrTailLines
}

func tailBytes(cfg Config) int {
	if cfg.StderrTailBytes > 0 {
		return cfg.StderrTailBytes
	}

	return DefaultStderrTailBytes
}

func gracePeriod(cfg Config) time.Duration {
	if cfg.GracePeriod > 0 {
		return cfg.GracePeriod
	}

	return DefaultGracePeriod
}

func nowFn(cfg Config) time.Time {
	if cfg.ClockNow != nil {
		return cfg.ClockNow()
	}

	return time.Now()
}

// ringBuffer は末尾を保持する in-memory buffer。max を超えた古い bytes は捨てる。
// io.Writer 実装なので io.MultiWriter に埋め込める。
type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func newRingBuffer(size int) *ringBuffer {
	if size <= 0 {
		size = DefaultStderrBufferBytes
	}

	return &ringBuffer{max: size}
}

// Write は p を buffer 末尾に追加する。追加後に buffer 長が max を超えていれば
// 先頭を切り捨てる (末尾 max bytes だけ残る)。
func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, p...)
	if len(r.data) > r.max {
		r.data = r.data[len(r.data)-r.max:]
	}

	return len(p), nil
}

// Bytes は現在の buffer 内容を copy して返す。
func (r *ringBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.data))
	copy(out, r.data)

	return out
}
