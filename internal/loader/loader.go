// Package loader は config.Config の checks[] を type ごとに checker package
// の Parse に振り分け、name 自動生成後の一意性を検証して []checker.Checker を
// 組み立てる。config package と checker/* の中間層に置くことで、config package
// が checker/* を import して cyclic dependency になるのを避ける。
package loader

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/suecharo/mitsume/internal/checker"
	"github.com/suecharo/mitsume/internal/checker/cmdcheck"
	"github.com/suecharo/mitsume/internal/checker/container"
	"github.com/suecharo/mitsume/internal/checker/deadman"
	"github.com/suecharo/mitsume/internal/checker/filecheck"
	"github.com/suecharo/mitsume/internal/checker/httpcheck"
	"github.com/suecharo/mitsume/internal/config"
)

// Options は BuildCheckers に渡す実行時情報。
type Options struct {
	// HeartbeatFile は deadman checker が read する heartbeat file の絶対パス。
	// deadman を含む config なら必須 (docs/heartbeat.md § 場所)。
	HeartbeatFile string
	// ClockNow は現在時刻 provider。nil なら time.Now。test 用注入。
	ClockNow func() time.Time
	// ContainerSocketPath は container checker が使う engine socket path を
	// 明示的に指定する経路。空なら container package の default resolver
	// (docs/checkers.md § container checker § 固有の挙動 の探索順) が回る。
	// test で net.Listen("unix", ...) の fake socket を差し込む用途。
	ContainerSocketPath string
}

// BuildCheckers は cfg.Checks を parse し、name 一意性検証を通した
// []checker.Checker を返す。
func BuildCheckers(cfg *config.Config, opts Options) ([]checker.Checker, error) {
	defaultsInterval := cfg.Defaults.Interval.Value()
	defaultsTimeout := cfg.Defaults.Timeout.Value()
	checkers := make([]checker.Checker, 0, len(cfg.Checks))
	firstSeen := map[string]int{}
	for i, raw := range cfg.Checks {
		c, err := parseSingle(raw, defaultsInterval, defaultsTimeout, opts)
		if err != nil {
			return nil, fmt.Errorf("checks[%d]: %w", i, err)
		}
		if prev, dup := firstSeen[c.Name()]; dup {
			return nil, fmt.Errorf("checks[%d]: name %q duplicates checks[%d]", i, c.Name(), prev)
		}
		firstSeen[c.Name()] = i
		checkers = append(checkers, c)
	}

	return checkers, nil
}

func parseSingle(
	raw json.RawMessage, defaultsInterval, defaultsTimeout time.Duration, opts Options,
) (checker.Checker, error) {
	var meta struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parse type: %w", err)
	}
	switch meta.Type {
	case "http":
		return httpcheck.Parse(raw, httpcheck.Options{
			Defaults: httpcheck.DefaultsFallback{Interval: defaultsInterval, Timeout: defaultsTimeout},
		})
	case "deadman":
		return deadman.Parse(raw, deadman.Options{
			Defaults:      deadman.DefaultsFallback{Interval: defaultsInterval},
			HeartbeatFile: opts.HeartbeatFile,
			ClockNow:      opts.ClockNow,
		})
	case "file":
		return filecheck.Parse(raw, filecheck.Options{
			Defaults: filecheck.DefaultsFallback{Interval: defaultsInterval},
			ClockNow: opts.ClockNow,
		})
	case "container":
		return container.Parse(raw, container.Options{
			Defaults:   container.DefaultsFallback{Interval: defaultsInterval},
			SocketPath: opts.ContainerSocketPath,
		})
	case "cmd":
		return cmdcheck.Parse(raw, cmdcheck.Options{
			Defaults: cmdcheck.DefaultsFallback{Interval: defaultsInterval, Timeout: defaultsTimeout},
		})
	case "":
		return nil, fmt.Errorf("type is required")
	default:
		return nil, fmt.Errorf("unknown type %q", meta.Type)
	}
}
