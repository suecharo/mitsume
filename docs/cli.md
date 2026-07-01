# CLI リファレンス

mitsume の 6 サブコマンドの引数・環境変数・exit code をまとめたリファレンス。「このサブコマンドはどう呼ぶんだっけ」を引くのに使う。用途で使い分けるためのパターン集は [recipes.md](recipes.md) 側にある。

## サブコマンド一覧

| サブコマンド | 通知 | heartbeat file | 設定 JSON | 主用途 |
|---|---|---|---|---|
| `mitsume ping [<job>]` | 出さない | write (`last_ping_at`) | 無くても動く | dead-man's switch の heartbeat |
| `mitsume notify <msg>` | 即時 1 通 | 触らない | 無くても動く | 単発 Slack 通知 |
| `mitsume check [--config <path>]` | 失敗 check ごとに 1 通 | read only | 必須 | 外部 cron 型ヘルスチェック |
| `mitsume watch [--config <path>]` | 失敗 check ごとに 1 通 | read only | 必須 | 常駐ヘルスチェック |
| `mitsume run [--name <name>] -- <cmd>` | 子プロセス終了時に 1 通 | 触らない | 無くても動く | 子プロセス supervisor |
| `mitsume version` | 出さない | 触らない | 触らない | binary の version / commit / build date を出す |

`ping` / `notify` / `run` / `version` は設定 JSON 無しで動く。`check` / `watch` は設定 JSON が必要。

`--dry-run` はどのサブコマンドでも使える (`version` を除く)。詳細は [共通 flag](#共通-flag) を参照。

## `mitsume ping`

### 概要

dead-man's switch の heartbeat を打つ。heartbeat file の該当 `job` の `last_ping_at` を今の時刻で書き換えるだけで、通知は出さない。`check` / `watch` 側で `type: deadman` を評価する仕組みとセットで使う。

### 使用例

```bash
mitsume ping nightly-backup
```

### 引数と flag

| 名前 | 種別 | 必須 | 説明 |
|---|---|---|---|
| `<job>` | 位置引数 | 条件付き | dead-man's switch の一意識別子。省略時は [識別子解決](#識別子解決) の順で決まる |
| `--heartbeat-file <path>` | flag | 任意 | heartbeat file のパスを明示指定 |
| `--config <path>` | flag | 任意 | 設定 JSON のパスを明示指定 (`<job>` fallback と `heartbeat_file` フィールドの参照用) |
| `--dry-run` | flag | 任意 | heartbeat file を書き換えず、更新予定の payload を stderr に出す |

### 参照する環境変数

| 変数 | 説明 |
|---|---|
| `MITSUME_JOB` | `<job>` 位置引数の fallback |
| `MITSUME_CONFIG` | 設定 JSON のパス |
| `MITSUME_HEARTBEAT_FILE` | heartbeat file のパス |

### 動作

- `<job>` を [識別子解決](#識別子解決) の順で決める。決まらなければ exit 1。
- heartbeat file のパスを [heartbeat.md](heartbeat.md) の探索順で決める。決まらなければ exit 1。
- heartbeat file を read し、`jobs.<job>.last_ping_at` を現在時刻 (ISO 8601 with TZ) に書き換え、atomic rename で write する。
- 通知は出さない。

### exit code

| code | 意味 |
|---|---|
| `0` | heartbeat file の更新成功 |
| `1` | `<job>` 解決失敗、heartbeat file パス未決定、read / write 失敗、設定 JSON parse 失敗 |

### 通知 / heartbeat file / 設定 JSON

通知は出さない。heartbeat file を write する。設定 JSON は無くても動く。

## `mitsume notify`

### 概要

Slack に単発通知を送る。`job` にも heartbeat file にも触らないので、既存の script の末尾に 1 行差し込むだけで使える。webhook URL は env 経由でのみ受け取り、CLI 引数で直接値を渡すことはできない (`ps aux` からの漏洩防止)。

### 使用例

```bash
my-batch || mitsume notify "my-batch failed: $(date)"
```

### 引数と flag

| 名前 | 種別 | 必須 | 説明 |
|---|---|---|---|
| `<msg>` | 位置引数 | 必須 | Slack に送るメッセージ本文 |
| `--config <path>` | flag | 任意 | 設定 JSON のパスを明示指定 (`notify` セクションを再利用する場合) |
| `--slack-webhook-url-env <name>` | flag | 任意 | webhook URL を保持する env 変数の名前 |
| `--dry-run` | flag | 任意 | Slack に送らず payload を stderr に出す |

### 参照する環境変数

| 変数 | 説明 |
|---|---|
| `MITSUME_SLACK_WEBHOOK_URL` | 設定 JSON / CLI flag 未指定時の既定 webhook URL env |
| `MITSUME_CONFIG` | 設定 JSON のパス |
| `MITSUME_HOST` | host 識別子 (通知文に載る) |
| (`--slack-webhook-url-env` で指定した任意の名前) | webhook URL を保持する env |

### 動作

- webhook URL を保持する env 変数名を、`--slack-webhook-url-env` → 設定 JSON の `notify.webhook_url_env` → 既定の `MITSUME_SLACK_WEBHOOK_URL` の順で解決する。
- 解決した env 変数から webhook URL を読み込む。未定義なら exit 1。
- payload の中身は固定形式で作る。詳細は [notify.md](notify.md) を参照。
- Slack に POST する。失敗したら 3 回 retry (1s → 2s → 4s、固定)。HTTP 4xx は retry せず即失敗。
- heartbeat file には触らない。

### exit code

| code | 意味 |
|---|---|
| `0` | Slack への送信成功 (`--dry-run` 時は payload を stderr に出せば成功扱い) |
| `1` | webhook URL 未解決、`<msg>` 未指定、Slack への retry 全滅、設定 JSON parse 失敗 |

### 通知 / heartbeat file / 設定 JSON

即時 1 通の通知を出す。heartbeat file には触らない。`--slack-webhook-url-env` または `MITSUME_SLACK_WEBHOOK_URL` で webhook URL が解決すれば設定 JSON は無くてもよい。

## `mitsume check`

### 概要

設定 JSON を読んで全 check を 1 回評価し、failure が確定した check ごとに Slack へ通知して exit する。外部 cron から `interval` に合わせた頻度で呼ぶ運用向け。1 回限りの実行なので、各 check の `interval` フィールドは無視する。

### 使用例

```bash
mitsume check --config /etc/mitsume/mitsume.json
```

外部 cron:

```text
0 * * * * /usr/local/bin/mitsume check --config /etc/mitsume/mitsume.json
```

### 引数と flag

| 名前 | 種別 | 必須 | 説明 |
|---|---|---|---|
| `--config <path>` | flag | 任意 | 設定 JSON のパスを明示指定 |
| `--heartbeat-file <path>` | flag | 任意 | heartbeat file のパスを明示指定 (deadman を含む config のみ影響) |
| `--dry-run` | flag | 任意 | Slack に送らず payload を stderr に出す |

### 参照する環境変数

| 変数 | 説明 |
|---|---|
| `MITSUME_CONFIG` | 設定 JSON のパス |
| `MITSUME_HEARTBEAT_FILE` | heartbeat file のパス |
| `MITSUME_HOST` | host 識別子 |
| `MITSUME_SLACK_WEBHOOK_URL` | 既定 webhook URL env |
| (`notify.webhook_url_env` で指定した任意の名前) | webhook URL を保持する env |

### 動作

- 設定 JSON を [configuration.md](configuration.md) の探索順で決める。決まらなければ exit 1。
- 設定 JSON を fail-fast で validate する。1 件でも違反があれば監視を開始せず exit 1。
- 各 check の `interval` フィールドは無視する。
- deadman を含む config なら heartbeat file を read only で開く。含まなければ heartbeat file は不要。
- 全 check を並列に評価する (1 check あたり 1 goroutine)。総経過時間は最も遅い 1 check の burst 完了時間に収束する。
- failure を検知したら `confirm.checks` 回まで `confirm.interval` 間隔で連続確認し、全滅で failure を確定する。
- failure が確定した check それぞれについて Slack に 1 通ずつ通知する。debounce しない。
- 全 check の評価と通知が終わったら exit する。

### exit code

| code | 意味 |
|---|---|
| `0` | 設定を読んで評価を完了した (個別の check が failure でも 0) |
| `1` | 設定 JSON 未検出 / parse 失敗 / validation 失敗、heartbeat file read 失敗、通知の retry 全滅など、mitsume 側の異常 |

個別 check の failure は exit code に反映しない。cron 側の on-failure ハンドラを不用意に発火させないため、失敗は通知で伝える。

### 通知 / heartbeat file / 設定 JSON

failure が確定した check ごとに 1 通通知する。heartbeat file は deadman を含む config のときだけ read only で参照する (更新しない)。設定 JSON は必須。

## `mitsume watch`

### 概要

常駐モードで設定 JSON の全 check を `interval` ごとに評価し、failure が確定した check ごとに Slack へ通知する。systemd unit の `ExecStart` として起動する運用を想定している。能動 check (`http` / `file` / `cmd` / `container`) と `deadman` を同じ daemon 内で並列に評価する。

### 使用例

```bash
mitsume watch --config /etc/mitsume/mitsume.json
```

systemd unit:

```ini
[Service]
ExecStart=/usr/local/bin/mitsume watch --config /etc/mitsume/mitsume.json
Restart=on-failure
User=mitsume
```

### 引数と flag

| 名前 | 種別 | 必須 | 説明 |
|---|---|---|---|
| `--config <path>` | flag | 任意 | 設定 JSON のパスを明示指定 |
| `--heartbeat-file <path>` | flag | 任意 | heartbeat file のパスを明示指定 (deadman を含む config のみ影響) |
| `--dry-run` | flag | 任意 | Slack に送らず payload を stderr に出す |

### 参照する環境変数

| 変数 | 説明 |
|---|---|
| `MITSUME_CONFIG` | 設定 JSON のパス |
| `MITSUME_HEARTBEAT_FILE` | heartbeat file のパス |
| `MITSUME_HOST` | host 識別子 |
| `MITSUME_SLACK_WEBHOOK_URL` | 既定 webhook URL env |
| (`notify.webhook_url_env` で指定した任意の名前) | webhook URL を保持する env |

### 動作

- 起動時に設定 JSON を fail-fast で validate する。1 件でも違反があれば監視を開始せず exit 1。
- deadman を含む config なら起動時 pre-flight で heartbeat file の read + parse を検証する (evaluation loop に入る前)。以降は評価サイクルの起点で 1 度だけ read し、同一サイクル (burst 含む) 内の deadman 評価はその snapshot を共有する。
- 能動 check と deadman を同じ daemon 内で並列に評価する。
- 起動直後に 1 回目の評価を全 check に対して行い、以降は各 check ごとに独立に `interval` ごとの評価サイクルを回す。failure 検知時は `confirm.checks` 回まで `confirm.interval` 間隔で連続確認し、全滅で failure を確定する (payload には最終確認時の観測値を載せる)。
- failure が確定した check ごとに Slack に 1 通通知する。次の `interval` サイクルで再度 failure なら再度 1 通、debounce しない。
- `SIGINT` / `SIGTERM` を受けたら graceful shutdown する。走行中の評価は結果を破棄して次サイクルに入らず、停止時には best-effort で 1 発 announcement を送る (`text` は `[mitsume] watch stopped on host=<host> (signal=<name>, time=<RFC3339>)`、attachments は付けない)。
- `panic` は recover して 1 発通知を送ってから re-panic する (Go runtime が stack trace を stderr に出し、プロセスは exit code `2` で終わる)。

### exit code

| code | 意味 |
|---|---|
| `0` | `SIGINT` / `SIGTERM` による graceful shutdown |
| `1` | 起動時の設定 validation 失敗、fatal な runtime error |
| `2` | panic (Go runtime が re-panic を受けて出す標準 code) |

### 通知 / heartbeat file / 設定 JSON

failure が確定した check ごとに 1 通通知する。shutdown 時に best-effort で 1 発通知する。heartbeat file は deadman を含む config のときだけ read only で参照する。設定 JSON は必須。

## `mitsume run`

### 概要

子プロセスを fork で起動する supervisor。子の exit code で成功 / 失敗を判定して、内部で `notify` 相当を呼んで Slack に通知する。子の死亡は `SIGCHLD` で即時検知するので、外部から `pgrep` / pid_file を polling するより遅延なく気づける。

`mitsume run` は `notify` 相当だけを呼び、`ping` 相当は呼ばない。dead-man's switch と組み合わせたい場合は shell 側でつなぐ:

```bash
mitsume run --name nightly-backup -- /usr/local/bin/nightly-backup.sh && mitsume ping nightly-backup
```

### 使用例

```bash
mitsume run --name nightly-backup -- /usr/local/bin/nightly-backup.sh
```

Dockerfile での ENTRYPOINT 利用:

```bash
mitsume run --name api-server -- /app/server
```

### 引数と flag

| 名前 | 種別 | 必須 | 説明 |
|---|---|---|---|
| `<cmd> [args...]` | 位置引数 (`--` の後) | 必須 | 実行する子プロセスと引数 |
| `--name <name>` | flag | 任意 | 通知文に出す表示ラベル。省略時は `cmd` の basename から自動生成 |
| `--timeout <duration>` | flag | 任意 | 子プロセスの実行時間上限。デフォルトなし (無制限) |
| `--grace-period <duration>` | flag | 任意 | timeout 発火後、`SIGTERM` から `SIGKILL` までの猶予。デフォルト `5s` |
| `--stderr-buffer-bytes <n>` | flag | 任意 | 子の stderr ring buffer サイズ。デフォルト `16KB` |
| `--stderr-tail-lines <n>` | flag | 任意 | 通知に含める stderr 末尾の行数。デフォルト `20` |
| `--stderr-tail-bytes <n>` | flag | 任意 | 通知に含める stderr 末尾のバイト数。デフォルト `2KB` (`--stderr-tail-lines` と両方効いていれば小さい方を採用) |
| `--quiet-on-success` | flag | 任意 | 成功時 (exit code 0) の通知を抑止し、失敗時のみ通知する |
| `--config <path>` | flag | 任意 | 設定 JSON のパスを明示指定 (`notify` セクションを再利用する場合) |
| `--slack-webhook-url-env <name>` | flag | 任意 | webhook URL を保持する env 変数の名前 |
| `--dry-run` | flag | 任意 | 子プロセスは実際に実行するが、通知を Slack に送らず stderr に出す |

### 参照する環境変数

| 変数 | 説明 |
|---|---|
| `MITSUME_SLACK_WEBHOOK_URL` | 既定 webhook URL env |
| `MITSUME_CONFIG` | 設定 JSON のパス |
| `MITSUME_HOST` | host 識別子 |
| (`--slack-webhook-url-env` で指定した任意の名前) | webhook URL を保持する env |

### 動作

- `--` 以降を子プロセスとして fork する。
- 子の stdout / stderr は親に tee する。同時に末尾を ring buffer (`--stderr-buffer-bytes`) に持ち、失敗通知に載せる。
- `SIGCHLD` で子の終了を即時検知する。
- 子の exit code が `0` なら成功、非 0 なら失敗と判定する。
- 成功時 / 失敗時とも内部で `notify` 相当を呼んで Slack に通知する。`--quiet-on-success` を付けると成功時は通知しない。
- 失敗通知には exit code と stderr 末尾 (`--stderr-tail-lines` と `--stderr-tail-bytes` の小さい方) を含める。
- 子の起動失敗 (PATH 不在 / permission denied) も失敗として通知する。
- `--timeout` 指定時、超過したら `SIGTERM` を送り、`--grace-period` を超えても終わらなければ `SIGKILL` を送る。
- 外部から届いた `SIGINT` / `SIGTERM` / `SIGHUP` / `SIGQUIT` は子に forward する。`SIGKILL` は forward 不能なので best-effort で諦める。
- heartbeat file には触らない。`ping` 相当の処理は呼ばない。
- daemonize しない。バックグラウンド化はユーザー側で `nohup mitsume run ... &` を使う。

### exit code

| code | 意味 |
|---|---|
| `0` | 子プロセスが exit code 0 で正常終了 |
| 子の exit code | 子プロセスが非 0 で終了した場合、その値を透過 |
| `124` | `--timeout` 超過で kill (GNU `timeout(1)` 慣習) |
| `126` | 子プロセスの実行 permission が拒否された (bash 慣習) |
| `127` | 子プロセスが `PATH` 上に見つからない (bash 慣習) |
| `128 + signum` | 子プロセスが外部 signal で kill された (bash 慣習、例: `SIGINT` → `130`、`SIGTERM` → `143`)。`--timeout` 由来の kill は `124` を優先し、こちらは使わない |

### 通知 / heartbeat file / 設定 JSON

子プロセス終了時に 1 通通知する (`--quiet-on-success` 時は失敗時のみ)。heartbeat file には触らない。`--slack-webhook-url-env` または `MITSUME_SLACK_WEBHOOK_URL` で webhook URL が解決すれば設定 JSON は無くてもよい。

## `mitsume version`

### 概要

binary に埋め込まれた version / commit / build date と runtime の Go version を stdout に出して exit する。release binary の identification と bug report のときの照合用。

### 使用例

```bash
mitsume version
```

出力例:

```text
mitsume version=0.1.0, commit=abc123..., built=2026-07-01T12:34:56Z, go=go1.23.5
```

### 引数と flag

引数と flag は受け取らない。位置引数を渡すと exit 1。

### 参照する環境変数

無し。

### 動作

- `version` / `commit` / `date` は release build 時に goreleaser の ldflags で埋め込まれる。source build (`go build`) では default 値 (`dev` / `none` / `unknown`) が入る。
- `go` は `runtime.Version()` (build で使った Go toolchain の version)。
- 通知は出さない。heartbeat file にも触らない。設定 JSON も読まない。

### exit code

| code | 意味 |
|---|---|
| `0` | version を出して正常終了 |
| `1` | 位置引数を渡された、または stdout write 失敗 |

### 通知 / heartbeat file / 設定 JSON

通知は出さない。heartbeat file には触らない。設定 JSON は読まない。

## 共通 flag

### `--dry-run`

`ping` / `notify` / `check` / `watch` / `run` で効く (`version` は副作用が無いので対象外)。設定の試運転、通知文の事前確認、systemd unit の commissioning に使う。

| 対象 | 挙動 |
|---|---|
| Slack への送信 | 抑制し、payload は stderr に出す (`watch` の SIGTERM / SIGINT 受信時と panic recover 時の best-effort 通知も抑制対象) |
| heartbeat file の書き込み | 行わない (`ping` の `last_ping_at` 更新) |
| 子プロセス (`run` のみ) | 実際に実行する (通知 / heartbeat file 更新だけを抑止) |

## 環境変数

env 経由で受け取る変数の一覧。CLI 引数 / 設定 JSON との優先順は各サブコマンドの節と参照先の doc で決まる。

| 変数 | 参照するサブコマンド | 説明 |
|---|---|---|
| `MITSUME_CONFIG` | `ping` / `notify` / `check` / `watch` / `run` | 設定 JSON のパス。探索順は [configuration.md](configuration.md) を参照 |
| `MITSUME_JOB` | `ping` | `<job>` 位置引数の fallback |
| `MITSUME_HOST` | 全サブコマンド | host 識別子 (通知文に載る)。決定順は [notify.md](notify.md) を参照 |
| `MITSUME_HEARTBEAT_FILE` | `ping` / `check` / `watch` | heartbeat file のパス。探索順は [heartbeat.md](heartbeat.md) を参照 |
| `MITSUME_SLACK_WEBHOOK_URL` | `notify` / `check` / `watch` / `run` | Slack Incoming Webhook の URL を保持する既定 env |
| (`--slack-webhook-url-env <name>` / `notify.webhook_url_env` で指定した任意の名前) | `notify` / `check` / `watch` / `run` | webhook URL を保持する env 変数の名前を任意に指定できる |

秘密情報 (webhook URL 等) は env 経由でだけ渡す。CLI 引数で値を直接受け取る flag は用意しない (`ps aux` からの漏洩を防ぐため)。

## 識別子解決

mitsume が扱う識別子は `job` と `name` の 2 語だけ。設計の全体像は [architecture.md](architecture.md) を参照。

### `job` (deadman 識別子)

- 走るべき job の一意名。`mitsume ping <job>` の位置引数、`type: deadman` の必須フィールドで使う。
- 必須。自動生成しない。
- 命名規則: `[a-zA-Z0-9_-]{1,64}`
- 通知文にも載る。

`mitsume ping` の `<job>` 解決順:

1. 位置引数 (`mitsume ping nightly-backup`)
2. `$MITSUME_JOB` env
3. 設定 JSON 中の唯一の `type: deadman` の `job` (deadman entry が 2 件以上あれば解決しない、エラー)

どれでも解決できなければ exit 1。

### `name` (表示ラベル)

- 通知文の見やすさ用のラベル。全 checker と `mitsume run` の任意フィールド。
- 一意性: `checks[]` 内で重複しない (自動生成後の値も含めて validation でチェック)。
- 省略時は type 別ルールで自動生成する。

| type | 自動生成元 |
|---|---|
| `http` | `url` |
| `file` | `path` または `path_glob` |
| `cmd` | `command` の先頭 32 文字 |
| `container` | `container` フィールド (name または id) |
| `deadman` | `job` をそのまま使う |
| `run --name` 省略時 | `<cmd>` の basename |

## 関連

- [configuration.md](configuration.md) — 設定 JSON の schema と探索順
- [checkers.md](checkers.md) — 5 種の checker の評価ロジック
- [notify.md](notify.md) — Slack payload と通知 trigger
- [heartbeat.md](heartbeat.md) — heartbeat file の schema と paths
- [recipes.md](recipes.md) — 用途別の設定パターン集
- [architecture.md](architecture.md) — 3 直交軸と失敗確信モデルの設計思想
