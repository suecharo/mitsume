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

	// docs/cli.md § watch § 動作 の shutdown announcement は signal 名を text に
	// 含める。signal 受信の観測点は 1 系統に絞り、signal.Notify チャネルの受信
	// goroutine 内で cancel() を呼ぶ形にする。signal.NotifyContext を併用すると
	// signal.Notify の受信 goroutine と NotifyContext 内部の cancel goroutine が
	// 並行に走り、goroutine の select 評価時点で ctx.Done も ready になると
	// Go の select 仕様どおり一様ランダムに ctx.Done ブランチが選ばれて signal
	// 名の capture が抜け落ちる。1 系統にすればその race が構造的に消える。
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
			// text になる (現状 main.go は background を渡すので通常経路には来ない)。
		}
	}()

	if err := r.RunLoop(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume watch: %v\n", err)

		return 1
	}
	// capture goroutine の完了を待ってから receivedSig を read することで
	// happens-before を確立する (RunLoop return と capture goroutine の write は
	// 独立に走っているため、join なしでは Load が nil を返す race がある)。
	sigWg.Wait()

	sigName := "shutdown"
	if receivedSig != nil {
		sigName = receivedSig.String()
	}
	// shutdown announcement は best-effort。ctx は既に cancel されているので新規
	// background ctx を使う。dry-run 時は Notifier.Send が stderr へ payload を
	// 書き出す (Slack へ POST しない、docs/cli.md § --dry-run)。
	if err := lifecycle.SendShutdown(context.Background(), r.Notifier, r.Host, sigName, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "mitsume watch: shutdown notify failed: %v\n", err)
	}

	return 0
}
