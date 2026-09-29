package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/suecharo/mitsume/internal/lifecycle"
)

func runWatch(parentCtx context.Context, args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to mitsume.json")
	hbPath := fs.String("heartbeat-file", "", "path to heartbeat file")
	dryRun := fs.Bool("dry-run", false, "skip Slack POST; print payload to stderr")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: mitsume watch [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "mitsume watch: unexpected positional arguments")

		return 1
	}

	r, exitCode := setupRunner(runnerSetupOpts{
		ConfigPath:    *cfgPath,
		HeartbeatPath: *hbPath,
		DryRun:        *dryRun,
		Subcommand:    "watch",
	})
	if exitCode != 0 {
		return exitCode
	}

	// 停止の通知に signal 名を載せるため、signal を受ける場所を 1 つに絞り、
	// signal.Notify のチャネルを受ける goroutine の中で cancel() を呼ぶ。
	// signal.NotifyContext を併用すると、その内部の cancel と並行に走り、select の
	// 時点で ctx.Done も ready だと一様ランダムに ctx.Done が選ばれて signal 名を
	// 取りこぼす。
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var (
		receivedSig os.Signal
		sigWg       sync.WaitGroup
	)
	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		select {
		case s := <-sigCh:
			receivedSig = s
			cancel()
		case <-ctx.Done():
			// parent ctx cancel での終了。receivedSig は nil のまま fallback
			// text になる。
		}
	}()

	// 起動の通知は送り終えてから評価を始め、Slack 上で failure より先に届くようにする。
	// ctx を渡すので、送っている間に SIGTERM を受けても待たされない。
	if err := lifecycle.SendStartup(ctx, r.Notifier, r.Host, len(r.Checkers), time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume watch: startup notify failed: %v\n", err)
	}
	r.RunLoop(ctx)
	// capture goroutine の完了を待ってから receivedSig を read することで
	// happens-before を確立する (RunLoop return と capture goroutine の write は
	// 独立に走っているため、join なしでは Load が nil を返す race がある)。
	sigWg.Wait()

	sigName := signalName(receivedSig)
	// 停止の通知は best-effort。ctx は cancel 済みなので background ctx で送る。
	if err := lifecycle.SendShutdown(context.Background(), r.Notifier, r.Host, sigName, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume watch: shutdown notify failed: %v\n", err)
	}

	return 0
}

// signalName は停止の通知に載せる signal 名を返す。Go の Signal.String() は
// terminated / interrupt を返すので、SIGTERM / SIGINT の名前を自前で返す。
// nil は signal によらない停止 (parent ctx の cancel) のときの値。
func signalName(sig os.Signal) string {
	switch sig {
	case nil:
		return "shutdown"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGINT:
		return "SIGINT"
	}

	return sig.String()
}
