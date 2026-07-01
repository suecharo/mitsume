// Package checker は 5 種の checker (http / deadman / file / container / cmd) が
// 共有する Checker interface と評価結果型 Result を提供する。個別実装は
// internal/checker/<type>/ 配下。
package checker

import (
	"context"
	"time"

	"github.com/suecharo/mitsume/internal/confirm"
)

// Result は 1 回の評価結果。Slack payload の fields (docs/notify.md § payload 形式)
// と 1 対 1 で対応する。
type Result struct {
	// OK は評価成功なら true。failure なら false。
	OK bool
	// Error は failure の理由。docs/notify.md の text 行末括弧内 "<type>: <error>"
	// に載る。成功時は空。
	Error string
	// Observed は Slack payload の "observed" field。checker が観測した実値。
	Observed string
	// Expected は Slack payload の "expected" field。checker の期待値。
	Expected string
	// Stderr は cmd checker のみ非空 (docs/checkers.md § cmd の 20 行 / 2KB
	// truncate 済み)。他 checker では常に空。
	Stderr string
}

// Success は成功結果を返すヘルパ。
func Success() Result { return Result{OK: true} }

// Failure は失敗結果を組み立てるヘルパ。
func Failure(errMsg, observed, expected string) Result {
	return Result{OK: false, Error: errMsg, Observed: observed, Expected: expected}
}

// Checker は 1 単位の監視対象。docs/checkers.md § 共通契約 の必須 / 任意
// フィールドを reflect する。個別 checker の固有 field は各実装が保持する。
type Checker interface {
	// Type は "http" / "deadman" / "file" / "container" / "cmd" のいずれか。
	Type() string
	// Name は表示ラベル。docs/checkers.md § name の自動生成 で決まる値を
	// 返す (自動生成後、checks[] 内で一意)。
	Name() string
	// Interval は評価周期 (docs/checkers.md § interval)。
	Interval() time.Duration
	// Confirm は失敗確信 burst 設定。
	Confirm() confirm.Config
	// Evaluate は 1 回評価する。ctx.Done で abort する。
	Evaluate(ctx context.Context) Result
}
