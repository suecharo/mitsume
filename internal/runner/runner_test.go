package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suecharo/mitsume/internal/checker"
	"github.com/suecharo/mitsume/internal/checker/deadman"
	"github.com/suecharo/mitsume/internal/confirm"
	"github.com/suecharo/mitsume/internal/heartbeat"
	"github.com/suecharo/mitsume/internal/lifecycle"
	"github.com/suecharo/mitsume/internal/notify"
	"github.com/suecharo/mitsume/internal/runner"
)

// -------- fakes --------

type recordingSender struct {
	mu    sync.Mutex
	calls []notify.SlackPayload
	err   error
}

func (r *recordingSender) Send(_ context.Context, p notify.SlackPayload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, p)

	return r.err
}

func (r *recordingSender) received() []notify.SlackPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]notify.SlackPayload, len(r.calls))
	copy(out, r.calls)

	return out
}

type fakeChecker struct {
	typ, name  string
	interval   time.Duration
	confirmCfg confirm.Config

	mu        sync.Mutex
	responses []checker.Result
	calls     int
	onEval    func(ctx context.Context, callNo int) checker.Result
}

func (f *fakeChecker) Type() string            { return f.typ }
func (f *fakeChecker) Name() string            { return f.name }
func (f *fakeChecker) Interval() time.Duration { return f.interval }
func (f *fakeChecker) Confirm() confirm.Config { return f.confirmCfg }
func (f *fakeChecker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls
}

func (f *fakeChecker) Evaluate(ctx context.Context) checker.Result {
	f.mu.Lock()
	f.calls++
	n := f.calls
	responses := f.responses
	onEval := f.onEval
	f.mu.Unlock()

	if onEval != nil {
		return onEval(ctx, n)
	}
	idx := n - 1
	if idx < len(responses) {
		return responses[idx]
	}

	return checker.Success()
}

type fakeSleeper struct {
	mu      sync.Mutex
	calls   []time.Duration
	onCall  func(callNo int, d time.Duration)
	respErr []error
}

func (s *fakeSleeper) Sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.calls = append(s.calls, d)
	n := len(s.calls)
	onCall := s.onCall
	respErr := s.respErr
	s.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if onCall != nil {
		onCall(n, d)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	idx := n - 1
	if idx < len(respErr) {
		return respErr[idx]
	}

	return nil
}

func (s *fakeSleeper) callDurations() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]time.Duration, len(s.calls))
	copy(out, s.calls)

	return out
}

func newTestNotifier() (*lifecycle.Notifier, *recordingSender) {
	s := &recordingSender{}

	return &lifecycle.Notifier{Sender: s, Options: notify.Options{Username: "mitsume"}}, s
}

func failure(err, obs, exp string) checker.Result {
	return checker.Failure(err, obs, exp)
}

func writeHeartbeat(t *testing.T, path string, jobs map[string]time.Time) {
	t.Helper()
	file := &heartbeat.File{Jobs: map[string]heartbeat.Entry{}}
	for job, ts := range jobs {
		file.Jobs[job] = heartbeat.Entry{LastPingAt: ts}
	}
	if err := heartbeat.SaveAtomic(path, file); err != nil {
		t.Fatalf("SaveAtomic: %v", err)
	}
}

func makeDeadman(t *testing.T, job, within, interval, hbFile, confirmJSON string) *deadman.Checker {
	t.Helper()
	confirmField := ""
	if confirmJSON != "" {
		confirmField = fmt.Sprintf(`,"confirm":%s`, confirmJSON)
	}
	raw := json.RawMessage(fmt.Sprintf(
		`{"type":"deadman","job":%q,"interval":%q,"expect":{"within":%q}%s}`,
		job, interval, within, confirmField,
	))
	c, err := deadman.Parse(raw, deadman.Options{HeartbeatFile: hbFile})
	if err != nil {
		t.Fatalf("deadman.Parse: %v", err)
	}

	return c
}

// -------- PreflightHeartbeat --------

func TestPreflightHeartbeat_NoDeadmanIsNoop(t *testing.T) {
	t.Parallel()
	n, _ := newTestNotifier()
	r := &runner.Runner{
		Checkers: []checker.Checker{&fakeChecker{typ: "http", name: "a", interval: time.Second}},
		Notifier: n,
	}
	if err := r.PreflightHeartbeat(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPreflightHeartbeat_MissingPathReturnsError(t *testing.T) {
	t.Parallel()
	dead := makeDeadman(t, "backup", "25h", "1h", filepath.Join(t.TempDir(), "hb.json"), "")
	n, _ := newTestNotifier()
	r := &runner.Runner{Checkers: []checker.Checker{dead}, Notifier: n}
	if err := r.PreflightHeartbeat(); err == nil {
		t.Fatalf("expected error for missing HeartbeatFile")
	}
}

func TestPreflightHeartbeat_CorruptFileReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hbFile := filepath.Join(dir, "hb.json")
	if err := os.WriteFile(hbFile, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	dead := makeDeadman(t, "backup", "25h", "1h", hbFile, "")
	n, _ := newTestNotifier()
	r := &runner.Runner{Checkers: []checker.Checker{dead}, HeartbeatFile: hbFile, Notifier: n}
	if err := r.PreflightHeartbeat(); err == nil {
		t.Fatalf("expected error for corrupt heartbeat file")
	}
}

func TestPreflightHeartbeat_ValidFilePasses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hbFile := filepath.Join(dir, "hb.json")
	writeHeartbeat(t, hbFile, map[string]time.Time{"backup": time.Now()})
	dead := makeDeadman(t, "backup", "25h", "1h", hbFile, "")
	n, _ := newTestNotifier()
	r := &runner.Runner{Checkers: []checker.Checker{dead}, HeartbeatFile: hbFile, Notifier: n}
	if err := r.PreflightHeartbeat(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// -------- RunOnce basic --------

func TestRunOnce_AllOKSendsNoAlert(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	c := &fakeChecker{
		typ: "http", name: "a", interval: time.Second,
		confirmCfg: confirm.Default(),
		responses:  []checker.Result{checker.Success()},
	}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n, Host: "h1"}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(sender.received()) != 0 {
		t.Fatalf("expected 0 alerts, got %d", len(sender.received()))
	}
	if c.callCount() != 1 {
		t.Fatalf("expected 1 evaluate call, got %d", c.callCount())
	}
}

func TestRunOnce_AllCheckersRunInParallel(t *testing.T) {
	t.Parallel()
	n, _ := newTestNotifier()
	var (
		concurrent atomic.Int64
		peak       atomic.Int64
	)
	makeCh := func(name string) *fakeChecker {
		return &fakeChecker{
			typ: "http", name: name, interval: time.Second,
			confirmCfg: confirm.Default(),
			onEval: func(_ context.Context, _ int) checker.Result {
				now := concurrent.Add(1)
				for {
					prev := peak.Load()
					if now <= prev || peak.CompareAndSwap(prev, now) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				concurrent.Add(-1)

				return checker.Success()
			},
		}
	}
	r := &runner.Runner{
		Checkers: []checker.Checker{makeCh("a"), makeCh("b"), makeCh("c")},
		Notifier: n,
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if peak.Load() < 3 {
		t.Fatalf("expected peak concurrency 3, got %d", peak.Load())
	}
}

// -------- RunOnce burst --------

func TestRunOnce_BurstAllFailAlertsWithLastResult(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	c := &fakeChecker{
		typ: "http", name: "api", interval: time.Second,
		confirmCfg: confirm.Config{Checks: 3, Interval: time.Millisecond},
		responses: []checker.Result{
			failure("first", "s=500", "s=200"),
			failure("second", "s=502", "s=200"),
			failure("third-and-final", "s=503", "s=200"),
		},
	}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n, Host: "h1"}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "third-and-final") {
		t.Fatalf("payload must reflect last burst result, got text=%q", got[0].Text)
	}
	var observed, expected string
	for _, f := range got[0].Attachments[0].Fields {
		switch f.Title {
		case "observed":
			observed = f.Value
		case "expected":
			expected = f.Value
		}
	}
	if observed != "s=503" || expected != "s=200" {
		t.Fatalf("observed/expected mismatch: observed=%q expected=%q", observed, expected)
	}
	if c.callCount() != 3 {
		t.Fatalf("expected 3 evaluate calls, got %d", c.callCount())
	}
}

func TestRunOnce_BurstMidSuccessResetsNoAlert(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	c := &fakeChecker{
		typ: "http", name: "api", interval: time.Second,
		confirmCfg: confirm.Config{Checks: 3, Interval: time.Millisecond},
		responses: []checker.Result{
			failure("first", "s=500", "s=200"),
			checker.Success(),
		},
	}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n, Host: "h1"}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(sender.received()) != 0 {
		t.Fatalf("expected 0 alerts (burst reset), got %d", len(sender.received()))
	}
	if c.callCount() != 2 {
		t.Fatalf("expected 2 evaluate calls, got %d", c.callCount())
	}
}

func TestRunOnce_ConfirmOneStrikeSkipsBurstAndAlertsImmediately(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	sleeper := &fakeSleeper{}
	c := &fakeChecker{
		typ: "http", name: "api", interval: time.Second,
		confirmCfg: confirm.Config{OneStrike: true},
		responses:  []checker.Result{failure("boom", "s=500", "s=200")},
	}
	r := &runner.Runner{
		Checkers: []checker.Checker{c},
		Notifier: n, Host: "h1",
		Sleeper: sleeper,
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(sender.received()) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(sender.received()))
	}
	if c.callCount() != 1 {
		t.Fatalf("expected 1 evaluate (no burst), got %d", c.callCount())
	}
	if len(sleeper.callDurations()) != 0 {
		t.Fatalf("expected 0 sleeper calls (no burst), got %v", sleeper.callDurations())
	}
}

func TestRunOnce_CmdStderrAppendedToAlertText(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	c := &fakeChecker{
		typ: "cmd", name: "disk", interval: time.Second,
		confirmCfg: confirm.Config{OneStrike: true},
		responses: []checker.Result{
			{OK: false, Error: "exit=1, want=0", Observed: "exit=1", Expected: "exit=0", Stderr: "df: full\ndisk 100%"},
		},
	}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n, Host: "h1"}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got := sender.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(got))
	}
	if !strings.HasSuffix(got[0].Text, "df: full\ndisk 100%") {
		t.Fatalf("Stderr must be appended to text tail, got %q", got[0].Text)
	}
}

func TestRunOnce_ContextCanceledSkipsAlert(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	c := &fakeChecker{
		typ: "http", name: "api", interval: time.Second,
		confirmCfg: confirm.Config{OneStrike: true},
		onEval: func(_ context.Context, _ int) checker.Result {
			cancel()

			return failure("boom", "s=500", "s=200")
		},
	}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n, Host: "h1"}
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(sender.received()) != 0 {
		t.Fatalf("expected 0 alerts (ctx canceled mid-evaluate), got %d", len(sender.received()))
	}
}

// -------- container hard cap --------

func TestRunOnce_ContainerCheckerGetsHardCapTimeout(t *testing.T) {
	t.Parallel()
	n, _ := newTestNotifier()
	var deadlineSet bool
	var deadlineDur time.Duration
	c := &fakeChecker{
		typ: "container", name: "c", interval: time.Second,
		confirmCfg: confirm.Default(),
		onEval: func(ctx context.Context, _ int) checker.Result {
			dl, ok := ctx.Deadline()
			deadlineSet = ok
			deadlineDur = time.Until(dl)

			return checker.Success()
		},
	}
	r := &runner.Runner{
		Checkers:                   []checker.Checker{c},
		Notifier:                   n,
		ContainerEvaluationTimeout: 250 * time.Millisecond,
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !deadlineSet {
		t.Fatalf("container checker must receive a ctx with deadline")
	}
	if deadlineDur > 250*time.Millisecond || deadlineDur < 100*time.Millisecond {
		t.Fatalf("deadline duration %s not in expected 100ms..250ms range", deadlineDur)
	}
}

func TestRunOnce_ContainerCheckerHardCapDefaultsTo30s(t *testing.T) {
	t.Parallel()
	n, _ := newTestNotifier()
	var deadlineDur time.Duration
	c := &fakeChecker{
		typ: "container", name: "c", interval: time.Second,
		confirmCfg: confirm.Default(),
		onEval: func(ctx context.Context, _ int) checker.Result {
			dl, _ := ctx.Deadline()
			deadlineDur = time.Until(dl)

			return checker.Success()
		},
	}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if deadlineDur < 25*time.Second || deadlineDur > 30*time.Second {
		t.Fatalf("default hard cap must be ~30s, got %s", deadlineDur)
	}
}

func TestRunOnce_NonContainerCheckerNoDeadlineInjected(t *testing.T) {
	t.Parallel()
	n, _ := newTestNotifier()
	var deadlineSet bool
	c := &fakeChecker{
		typ: "http", name: "h", interval: time.Second,
		confirmCfg: confirm.Default(),
		onEval: func(ctx context.Context, _ int) checker.Result {
			_, deadlineSet = ctx.Deadline()

			return checker.Success()
		},
	}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if deadlineSet {
		t.Fatalf("non-container checker must not receive an injected deadline")
	}
}

// -------- deadman snapshot --------

func TestRunOnce_DeadmanSnapshotFixedAcrossBurst(t *testing.T) {
	// burst 内は snapshot 固定 (docs/heartbeat.md § 読み込みモデル)。
	// 初期 file は stale (failure 検知) → burst 内で file を fresh に書き換えても
	// snapshot 固定なら 3 回全 failure → alert。
	dir := t.TempDir()
	hbFile := filepath.Join(dir, "hb.json")
	staleTS := time.Now().Add(-30 * time.Hour)
	writeHeartbeat(t, hbFile, map[string]time.Time{"backup": staleTS})

	rewritten := make(chan struct{}, 1)
	sleeper := &fakeSleeper{
		onCall: func(n int, _ time.Duration) {
			if n == 1 {
				writeHeartbeat(t, hbFile, map[string]time.Time{"backup": time.Now()})
				rewritten <- struct{}{}
			}
		},
	}
	dead := makeDeadman(t, "backup", "25h", "1h", hbFile, `{"checks":3,"interval":"1ms"}`)
	n, sender := newTestNotifier()
	r := &runner.Runner{
		Checkers:      []checker.Checker{dead},
		HeartbeatFile: hbFile,
		Notifier:      n,
		Host:          "h1",
		Sleeper:       sleeper,
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	<-rewritten
	if len(sender.received()) != 1 {
		t.Fatalf("expected 1 alert (snapshot fixed across burst), got %d", len(sender.received()))
	}
}

func TestRunLoop_DeadmanSnapshotRefreshedNextCycle(t *testing.T) {
	// 1 サイクル目: stale → burst 全滅 → alert 1 通
	//   sleep calls: n=1..2 は burst.Interval=1ms、n=3 は interval sleep=1h
	// interval sleep (n=3) で heartbeat file を fresh に書き換え
	// 2 サイクル目: refreshDeadmanSnapshot が fresh を load → evaluate 1 回で success → no alert
	// 3 サイクル目に入る sleep (n=4) で cancel
	dir := t.TempDir()
	hbFile := filepath.Join(dir, "hb.json")
	staleTS := time.Now().Add(-30 * time.Hour)
	writeHeartbeat(t, hbFile, map[string]time.Time{"backup": staleTS})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleeper := &fakeSleeper{
		onCall: func(n int, d time.Duration) {
			if d == time.Hour && n == 3 {
				writeHeartbeat(t, hbFile, map[string]time.Time{"backup": time.Now()})
			}
			if d == time.Hour && n == 4 {
				cancel()
			}
		},
	}
	dead := makeDeadman(t, "backup", "25h", "1h", hbFile, `{"checks":3,"interval":"1ms"}`)
	n, sender := newTestNotifier()
	r := &runner.Runner{
		Checkers:      []checker.Checker{dead},
		HeartbeatFile: hbFile,
		Notifier:      n,
		Host:          "h1",
		Sleeper:       sleeper,
	}
	if err := r.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if got := len(sender.received()); got != 1 {
		t.Fatalf("expected exactly 1 alert (fresh snapshot on 2nd cycle), got %d", got)
	}
	durations := sleeper.callDurations()
	if len(durations) < 4 {
		t.Fatalf("expected at least 4 sleep calls, got %v", durations)
	}
	// 期待シーケンス: [1ms, 1ms, 1h (書き換え), 1h (cancel)]
	if durations[0] != time.Millisecond || durations[1] != time.Millisecond {
		t.Fatalf("burst intervals should be 1ms, got %v", durations[:2])
	}
	if durations[2] != time.Hour || durations[3] != time.Hour {
		t.Fatalf("interval sleeps should be 1h, got %v", durations[2:4])
	}
}

// -------- RunLoop schedule --------

func TestRunLoop_ImmediateFirstEvaluate(t *testing.T) {
	t.Parallel()
	n, _ := newTestNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &fakeChecker{
		typ: "http", name: "a", interval: time.Hour,
		confirmCfg: confirm.Default(),
	}
	sleeper := &fakeSleeper{
		onCall: func(n int, _ time.Duration) {
			if n == 1 {
				cancel()
			}
		},
	}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n, Sleeper: sleeper}
	if err := r.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if c.callCount() < 1 {
		t.Fatalf("expected at least 1 immediate evaluate, got %d", c.callCount())
	}
}

func TestRunLoop_SecondCycleAfterIntervalSleep(t *testing.T) {
	t.Parallel()
	n, _ := newTestNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &fakeChecker{
		typ: "http", name: "a", interval: time.Hour,
		confirmCfg: confirm.Default(),
	}
	sleeper := &fakeSleeper{
		onCall: func(n int, _ time.Duration) {
			if n == 2 {
				cancel()
			}
		},
	}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n, Sleeper: sleeper}
	if err := r.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if c.callCount() != 2 {
		t.Fatalf("expected 2 evaluate calls, got %d", c.callCount())
	}
	got := sleeper.callDurations()
	if len(got) < 2 || got[0] != time.Hour || got[1] != time.Hour {
		t.Fatalf("expected interval sleep 1h on each cycle, got %v", got)
	}
}

func TestRunLoop_GracefulShutdownStopsNewEvaluates(t *testing.T) {
	t.Parallel()
	n, _ := newTestNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	c := &fakeChecker{
		typ: "http", name: "a", interval: time.Hour,
		confirmCfg: confirm.Default(),
		onEval: func(_ context.Context, callNo int) checker.Result {
			if callNo == 1 {
				cancel()
			}

			return checker.Success()
		},
	}
	sleeper := &fakeSleeper{}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n, Sleeper: sleeper}
	if err := r.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if c.callCount() != 1 {
		t.Fatalf("expected exactly 1 evaluate (cancel mid-1st cycle), got %d", c.callCount())
	}
}

// -------- Sender error tolerance --------

func TestRunOnce_SendFailureDoesNotFailRunner(t *testing.T) {
	t.Parallel()
	n, sender := newTestNotifier()
	sender.err = errors.New("slack down")
	c := &fakeChecker{
		typ: "http", name: "api", interval: time.Second,
		confirmCfg: confirm.Config{OneStrike: true},
		responses:  []checker.Result{failure("boom", "s=500", "s=200")},
	}
	r := &runner.Runner{Checkers: []checker.Checker{c}, Notifier: n, Host: "h1"}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce should not return error when notify fails, got %v", err)
	}
	if len(sender.received()) != 1 {
		t.Fatalf("expected 1 notify attempt, got %d", len(sender.received()))
	}
}

// -------- interface guard: fakeChecker satisfies checker.Checker --------

var _ checker.Checker = (*fakeChecker)(nil)
