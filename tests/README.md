# Testing

mitsume は Go 製の死活監視 CLI である。本 doc は unit / PBT / integration の 3 階層のテスト方針、mock 境界、境界値と異常系の網羅観点、および build / test / lint / release の実行手順を定義する。仕様の詳細は [../docs/](../docs/) を参照する。

実行コマンド (`make test` / `go test ./...` / lint / release dry-run) の一覧は末尾の [Running tests](#running-tests) を参照する。

## テスト階層

3 階層を独立に走らせる。実装コードに 1:1 に近い unit を厚く、外部境界を跨ぐ経路を integration で貫通させ、純粋関数の不変量を PBT で網羅する。

| 階層 | 配置 | 目的 | 実行速度 |
|---|---|---|---|
| unit | 対象パッケージ隣接の `*_test.go` | 関数・型メソッドの入出力検証 | ミリ秒単位 |
| PBT | 対象パッケージ隣接の `*_property_test.go` | 純粋関数の不変量を rapid で網羅 | 秒単位 |
| integration | `tests/integration/` | 外部境界を跨ぐ経路 (HTTP / FS / process / UNIX domain socket) | 秒〜数十秒 |

test 名は `Test<対象>_<条件>_<期待結果>` の形式とし、読むだけで検証内容が分かるようにする。table-driven test は `t.Run(name, ...)` で subtest に分割し、失敗時にどの入力かが即断できるようにする。

同一の対象が複数階層に登場する場合の分担は次を原則とする。

- 純粋関数 / データ変換は PBT (`*_property_test.go`) と unit で厚く覆う。
- 外部境界を跨ぐ経路 (file I/O、HTTP、UNIX domain socket、外部プロセス) は integration で貫通させる。
- 両者に跨る対象 (例: duration parser や 設定 JSON parser) は、pure な logic を PBT で、file / network を経由する経路を integration で分担する。

## TDD

失敗する test を先に書き、それを通す最小実装を後から入れる順序を守る。`docs/` の仕様更新 → test 追加 → 実装、の順で 1 変更単位 (typically 1 PR / 1 論点) を進める。

- 通すためだけの test を書かない。境界値、エッジケース、異常系のうち少なくとも 1 つを含まない test は追加しない。
- 実装を書いた後に test を合わせに行かない。実装が仕様に合っていない場合は test ではなく実装を修正する。
- refactor 中は「既存 test がすべて green」の状態を維持する。1 コミット内で赤を通過させない。

## PBT (property-based testing)

Go の PBT には [rapid](https://github.com/flyingmutant/rapid) (`pgregory.net/rapid`) を採用する。unit と同じ `go test` の runner で走らせる。

- 「panic しない」「error にならない」は不変量として書かない。入出力の意味のある制約を書く。
- generator は対象パッケージの `*_property_test.go` に定義する。複数 test で共用する generator は `tests/internal/gen/` に集約する。
- shrink で縮小された反例を debug の起点とする。反例は fixture として unit test にも落とし、regression の網を残す。
- 実行時間が許す場合は `go test` に `-rapid.checks=<N>` を渡して探索幅を default から増やす。

主な対象は duration parser、設定 JSON parser (schema validation を含む)、heartbeat file parser (write / read の round trip)、Slack payload builder など、入出力が純粋な部分である。境界を跨ぐ経路 (file I/O、HTTP など) は [Cross-boundary の対象](#cross-boundary-の対象) に集約する。

## Mock 境界

mock してよいのは mitsume の外側にあるものだけである。内部関数、内部 struct、内部 interface を mock した瞬間、test は設計の鏡ではなくなる。

| Mock する | Mock しない |
|---|---|
| HTTP client / server (test 内で立てた HTTP server) | 内部関数、内部 struct、内部 interface |
| filesystem (test scope の一時 directory) | JSON encoding、duration parse などの標準ライブラリの純粋関数 |
| 子プロセス (fake binary を `PATH` 越しに差し込む) | 設定 JSON schema、型定義 |
| UNIX domain socket (test 用の local socket server) | — |
| 時刻取得 (対象関数に time provider を依存注入する) | — |

内部の mock が必要に見えた場合は、test ではなく設計を修正する。境界を明示するために interface を切る場合は、本番コードで使われる形で切る。test 専用の interface を新設しない。

## 状態共有と実行順序

test 間で state を共有しない。並列実行と shuffle で全件 green を保つ。

- 一時 file は test scope の一時 directory を使用し、test 終了時に自動で片付ける。
- 環境変数は test 内で set / unset される仕組みを使い (`t.Setenv`)、手書きの env 操作 + defer での復元は書かない。
- global な singleton state を持ち込まない。テスト可能性のため、依存は関数引数か struct field で渡す。
- `t.Parallel()` を積極的に使い、並列実行で通ることを既定とする。
- test 間で fixture file を書き換えて共有しない。fixture は read-only とし、mutation が必要な場合は `t.TempDir()` にコピーする。
- `go test -shuffle=on` で実行順序に依存しないことを確認する。

## 境界値と異常系

TDD と PBT の網から漏れやすいため、追加 test 作成時のチェックリストとして扱う。

- 境界値 (0、1、上限、上限 − 1、上限 + 1) を必ず含める。
- エッジケース (空文字列、空配列、nil、最小・最大 duration、timeout 直前) を明示的に test する。
- 異常系 (parse error、network error、timeout、permission denied、EOF) を必ず含める。
- 「正常系だけ」「happy path だけ」の test file を許可しない。

## Cross-boundary の対象

外部境界を跨ぐ経路は最低限これだけカバーする。unit で内側を、integration で境界を跨ぐ経路を貫通させる。仕様の詳細は各 SSOT にリンクする。

- **HTTP checker.** `httptest.NewServer` で対象 endpoint を模し、status code / body / header の判定 logic、timeout、redirect、TLS 検証失敗を検証する。checker 仕様は [../docs/checkers.md](../docs/checkers.md) を参照する。
- **Slack notifier.** `httptest.NewServer` で Slack Incoming Webhook を模し、payload の JSON 形式、retry 挙動、非 2xx 応答時のエラー伝搬を検証する。payload 仕様は [../docs/notify.md](../docs/notify.md) を参照する。
- **Heartbeat file parser.** test scope の一時 directory 配下で `last_ping_at` の write → read の round trip を検証する。並行 write を含むケースでは、次の invariant を確認する: read 側は常に完全な JSON を観測し、partial write の状態を読める瞬間は存在しない。これは atomic rename による仕様保証である ([../docs/heartbeat.md § Writing](../docs/heartbeat.md#writing-ping) を参照)。
- **Container checker の Docker Engine API 呼び出し.** `net.Listen("unix", ...)` でテスト用サーバーを立て、Docker Engine API `/containers/<id>/json` 応答を模して container status の判定 logic を検証する。checker 仕様は [../docs/checkers.md](../docs/checkers.md) を参照する。
- **Cmd checker.** テスト binary を子プロセスとして再実行し、`TestMain` 内で argv を見て fake external binary として振る舞わせる (Go でよく用いられる pattern)。exit code 0 / 非 0、stdout / stderr の組合せ、timeout での強制終了を検証する。checker 仕様は [../docs/checkers.md](../docs/checkers.md) を参照する。
- **設定 JSON parser.** 設定ファイル探索順、`_env` サフィックスによる env 展開、未定義 env 参照時のエラーなど file / env の外部境界を跨ぐ経路を検証する。pure な schema 検証 (書式・値の型・boundary condition) は PBT 節に集約し、本節では I/O を跨ぐ経路のみを扱う。schema は [../docs/configuration.md](../docs/configuration.md) を参照する。

## Running tests

依存は Go 1.23 以降、`golangci-lint`、`gofumpt`、`goreleaser` である。以下は代表的なコマンドをまとめる。

Build:

```bash
make build
```

Test:

```bash
# 全階層 (unit + PBT + integration) を実行する
make test

# 全階層を go test 経由で直接実行する (./... は tests/integration も含む)
go test ./...

# integration のみを実行する
go test ./tests/integration/...

# unit + PBT のみを実行する (integration package を除外する)
go test $(go list ./... | grep -v /tests/integration)

# 実行順序に依存しないことを確認する
go test -shuffle=on ./...

# データ競合を検出する
go test -race ./...

# PBT の探索幅を default から増やす
go test -rapid.checks=1000 ./...
```

Lint と format:

```bash
golangci-lint run ./...
gofumpt -l -w .
```

Release dry-run (goreleaser を使用する):

```bash
goreleaser release --clean --snapshot --skip=publish,sign
```

## 関連

- [../docs/architecture.md](../docs/architecture.md) — 設計原則、用語集、Design decisions
- [../docs/configuration.md](../docs/configuration.md) — 設定 JSON schema と探索順
- [../docs/checkers.md](../docs/checkers.md) — 5 種 checker の contract
- [../docs/notify.md](../docs/notify.md) — Slack payload と delivery retry
- [../docs/heartbeat.md](../docs/heartbeat.md) — heartbeat file の schema と write / read semantics
