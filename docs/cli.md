# CLI reference

mitsume の 6 個の subcommand の引数、環境変数、exit code を定義する reference である。用途別のパターン集は [recipes.md](recipes.md) を、用語の定義は [architecture.md § 用語](architecture.md#用語) を参照する。

## サブコマンド一覧

| Subcommand | 通知 | heartbeat file | 設定 JSON | 主用途 |
|---|---|---|---|---|
| `mitsume ping [<job>]` | 出さない | write (`last_ping_at`) | 無くても動く | dead-man's switch の heartbeat |
| `mitsume notify <msg>` | 即時 1 通 | 触らない | 無くても動く | 単発 Slack 通知 |
| `mitsume check [--config <path>]` | 失敗 check ごとに 1 通 | read-only | 必須 | 外部 cron 型ヘルスチェック |
| `mitsume watch [--config <path>]` | 失敗 check ごとに 1 通 | read-only | 必須 | 常駐ヘルスチェック |
| `mitsume run [--name <name>] -- <cmd>` | 子プロセス終了時に 1 通 | 触らない | 無くても動く | 子プロセス supervisor |
| `mitsume version` | 出さない | 触らない | 触らない | binary の version / commit / build date を表示 |

`ping` / `notify` / `run` / `version` は設定 JSON なしで動作する。`check` と `watch` は設定 JSON を必須とする。

`--dry-run` は `version` を除く全 subcommand で有効である。詳細は [共通 flag](#共通-flag) を参照する。

## `mitsume ping`

dead-man's switch の heartbeat を送る subcommand である。heartbeat file の該当 `job` の `last_ping_at` を現在時刻で更新するのみで、通知は送信しない。`check` / `watch` 側で `type: "deadman"` を評価する仕組みと組み合わせて使う。

### 使用例

```bash
mitsume ping nightly-backup
```

### 引数と flag

| 名前 | 種別 | 必須 | 説明 |
|---|---|---|---|
| `<job>` | 位置引数 | 条件付き | dead-man's switch の一意識別子。省略時は [識別子解決](#識別子解決) の順で決定する |
| `--heartbeat-file <path>` | flag | 任意 | heartbeat file の path を明示指定する |
| `--config <path>` | flag | 任意 | 設定 JSON の path を明示指定する。`<job>` の fallback (設定 JSON 中で唯一存在する `type: "deadman"` の `job` を利用する) と `heartbeat_file` field の解決に使う (詳細は [識別子解決](#識別子解決) を参照) |
| `--dry-run` | flag | 任意 | heartbeat file を書き換えず、更新予定の payload を stderr に出力する |

### 環境変数

| 変数 | 説明 |
|---|---|
| `MITSUME_JOB` | `<job>` 位置引数の fallback |
| `MITSUME_CONFIG` | 設定 JSON の path |
| `MITSUME_HEARTBEAT_FILE` | heartbeat file の path |

### 動作

- `<job>` を [識別子解決](#識別子解決) の順で決定する。決定できない場合は exit 1 とする。
- heartbeat file の path を [heartbeat.md § File location](heartbeat.md#file-location) の探索順で決定する。決定できない場合は exit 1 とする。
- heartbeat file を読み、`jobs.<job>.last_ping_at` を現在時刻 (ISO 8601 with TZ) に書き換え、同一 filesystem 内で atomic rename して書き込む。
- 通知は送信しない。

### Exit code

| Code | 意味 |
|---|---|
| `0` | heartbeat file の更新成功 |
| `1` | `<job>` 解決失敗、heartbeat file path 未決定、read / write 失敗、設定 JSON parse 失敗 |

## `mitsume notify`

Slack に単発通知を送信する subcommand である。`job` にも heartbeat file にも触らないため、既存 script の末尾に 1 行差し込むだけで利用できる。Webhook URL は env 経由のみで受け取り、CLI 引数で直接値を渡す方式は提供しない。理由は [architecture.md § Security invariants](architecture.md#security-invariants) を参照する。

### 使用例

```bash
my-batch || mitsume notify "my-batch failed: $(date)"
```

### 引数と flag

| 名前 | 種別 | 必須 | 説明 |
|---|---|---|---|
| `<msg>` | 位置引数 | 必須 | Slack に送信するメッセージ本文 |
| `--config <path>` | flag | 任意 | 設定 JSON の path を明示指定する (`notify` section を再利用する場合) |
| `--slack-webhook-url-env <name>` | flag | 任意 | webhook URL を保持する env 変数の名前 |
| `--dry-run` | flag | 任意 | Slack に送信せず payload を stderr に出力する |

### 環境変数

| 変数 | 説明 |
|---|---|
| `MITSUME_SLACK_WEBHOOK_URL` | 設定 JSON / CLI flag 未指定時に参照する既定 env 変数 |
| `MITSUME_CONFIG` | 設定 JSON の path |
| `MITSUME_HOST` | host 識別子 (通知文に載せる) |
| `--slack-webhook-url-env` で指定した任意の名前 | Webhook URL を保持する env 変数 |

### 動作

- Webhook URL を保持する env 変数名を、`--slack-webhook-url-env` → 設定 JSON の `notify.webhook_url_env` → 既定の `MITSUME_SLACK_WEBHOOK_URL` の順で解決する。
- 解決した env 変数から Webhook URL を読み込む。未定義の場合は exit 1 とする。
- payload は固定形式で組み立てる。詳細は [notify.md § Payload](notify.md#payload) を参照する。
- Slack に POST する。失敗した場合の retry は [notify.md § Retry](notify.md#retry) を参照する。
- heartbeat file には触らない。

### Exit code

| Code | 意味 |
|---|---|
| `0` | Slack への送信成功 (`--dry-run` 時は payload を stderr に出力した時点で成功扱いとする) |
| `1` | Webhook URL 未解決、`<msg>` 未指定、Slack への retry が全滅、設定 JSON parse 失敗 |

## `mitsume check`

設定 JSON を読み込み、全 check を 1 回評価して failure が確定した check ごとに Slack に通知して exit する subcommand である。外部 cron から `interval` に合わせた頻度で呼び出す運用を想定する。1 回限りの実行のため、各 check の `interval` field は無視する。

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
| `--config <path>` | flag | 任意 | 設定 JSON の path を明示指定する |
| `--heartbeat-file <path>` | flag | 任意 | heartbeat file の path を明示指定する (`deadman` を含む config でのみ影響する) |
| `--dry-run` | flag | 任意 | Slack に送信せず payload を stderr に出力する |

### 環境変数

| 変数 | 説明 |
|---|---|
| `MITSUME_CONFIG` | 設定 JSON の path |
| `MITSUME_HEARTBEAT_FILE` | heartbeat file の path |
| `MITSUME_HOST` | host 識別子 |
| `MITSUME_SLACK_WEBHOOK_URL` | 既定の Webhook URL env |
| `notify.webhook_url_env` で指定した任意の名前 | Webhook URL を保持する env 変数 |

### 動作

- 設定 JSON を [configuration.md](configuration.md) の探索順で決定する。決定できない場合は exit 1 とする。
- 設定 JSON を fail-fast で validate する。1 件でも違反があれば評価を開始せず exit 1 とする。
- 各 check の `interval` field は無視する。
- `deadman` を含む config の場合、起動時に heartbeat file を 1 度 read-only で読み込み、その snapshot を confirm burst を含む全 `deadman` 評価で共有する。含まない場合は heartbeat file を必要としない。
- 全 check を並列に評価する。総経過時間は最も遅い 1 check の confirm burst 完了時間に収束する。
- failure を検知した後、`confirm.interval` 間隔で追加 `confirm.checks - 1` 回を評価し、初回検知を含む合計 `confirm.checks` 回すべてが failure の場合に failure を確定する。
- failure が確定した check ごとに Slack に 1 通ずつ通知する。debounce しない。
- 全 check の評価と通知が終了した時点で exit する。

### Exit code

| Code | 意味 |
|---|---|
| `0` | 設定を読み込んで評価を完了した (個別の check が failure であっても 0) |
| `1` | 設定 JSON 未検出、parse 失敗、validation 失敗、heartbeat file の read 失敗など、mitsume 側の異常。通知の retry 全滅は含まない (詳細は [notify.md § Retry](notify.md#retry)) |

個別 check の failure を exit code に反映しない。cron 側の on-failure ハンドラを不用意に発火させないよう、失敗は通知で伝える。

## `mitsume watch`

常駐モードで設定 JSON の全 check を `interval` ごとに評価し、failure が確定した check ごとに Slack に通知する subcommand である。systemd unit の `ExecStart` として起動する運用を想定する。active checker (`http` / `file` / `cmd` / `container`) と `deadman` を同じ daemon 内で並列に評価する。

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
| `--config <path>` | flag | 任意 | 設定 JSON の path を明示指定する |
| `--heartbeat-file <path>` | flag | 任意 | heartbeat file の path を明示指定する (`deadman` を含む config でのみ影響する) |
| `--dry-run` | flag | 任意 | Slack に送信せず payload を stderr に出力する |

### 環境変数

| 変数 | 説明 |
|---|---|
| `MITSUME_CONFIG` | 設定 JSON の path |
| `MITSUME_HEARTBEAT_FILE` | heartbeat file の path |
| `MITSUME_HOST` | host 識別子 |
| `MITSUME_SLACK_WEBHOOK_URL` | 既定の Webhook URL env |
| `notify.webhook_url_env` で指定した任意の名前 | Webhook URL を保持する env 変数 |

### 動作

- 起動時に設定 JSON を fail-fast で validate する。1 件でも違反があれば評価を開始せず exit 1 とする。
- `deadman` を含む config の場合、起動時 pre-flight で heartbeat file の read と parse を検証する (評価サイクルに入る前)。以降は評価サイクルの起点で 1 度だけ読み、同一サイクル (confirm burst 含む) 内の `deadman` 評価はその snapshot を共有する。
- active checker と `deadman` を同じ daemon 内で並列に評価する。
- 起動直後に 1 回目の評価を全 check に対して行い、以降は各 check ごとに独立に `interval` ごとの評価サイクルを回す。failure 検知時は、`confirm.interval` 間隔で追加 `confirm.checks - 1` 回を評価し、初回検知を含む合計 `confirm.checks` 回すべてが failure の場合に failure を確定する (payload には最終確認時の観測値を載せる)。
- failure が確定した check ごとに Slack に 1 通通知する。次の `interval` サイクルで再度 failure と判定した場合は再度 1 通、debounce しない。
- `SIGINT` / `SIGTERM` を受信した場合は graceful shutdown する。走行中の評価は結果を破棄して次サイクルに入らず、停止時に best-effort で 1 通の shutdown announcement を送信する (payload の書式は [notify.md § Shutdown announcement payload](notify.md#shutdown-announcement-payload) を参照)。
- 予期せぬ fatal error (unrecoverable runtime error) を検知した場合は、best-effort で 1 通の通知を送信してから exit code `2` で異常終了する。error trace は stderr に出力する。

### Exit code

| Code | 意味 |
|---|---|
| `0` | `SIGINT` / `SIGTERM` による graceful shutdown |
| `1` | 起動時の設定 validation 失敗、pre-flight で検知した設定不整合 |
| `2` | 評価サイクル突入後に発生した予期せぬ fatal error による異常終了 |

## `mitsume run`

子プロセスを起動する supervisor である。子の exit code で成功 / 失敗を判定し、内部で notifier を呼び出して Slack に通知する。外部からの `pgrep` や pid file の polling を採用しない理由と設計背景は [architecture.md § Design decisions](architecture.md#design-decisions) を参照する。

`mitsume run` は notifier のみを呼び、`ping` 相当の処理は呼ばない。dead-man's switch と組み合わせる場合は shell 側で連結する。

### 使用例

```bash
mitsume run --name nightly-backup -- /usr/local/bin/nightly-backup.sh
```

dead-man's switch と組み合わせる場合:

```bash
mitsume run --name nightly-backup -- /usr/local/bin/nightly-backup.sh && mitsume ping nightly-backup
```

Dockerfile での ENTRYPOINT 利用:

```dockerfile
ENTRYPOINT ["mitsume", "run", "--name", "api-server", "--", "/app/server"]
```

### 引数と flag

| 名前 | 種別 | 必須 | 説明 |
|---|---|---|---|
| `<cmd> [args...]` | 位置引数 (`--` の後) | 必須 | 実行する子プロセスと引数 |
| `--name <name>` | flag | 任意 | 通知文の表示ラベル。省略時は `<cmd>` の basename から自動生成する |
| `--timeout <duration>` | flag | 任意 | 子プロセスの実行時間上限。default なし (無制限) |
| `--grace-period <duration>` | flag | 任意 | timeout 発火後、`SIGTERM` から `SIGKILL` までの猶予。default `5s` |
| `--stderr-buffer-bytes <n>` | flag | 任意 | 子の stderr ring buffer の size。default `16KB` |
| `--stderr-tail-lines <n>` | flag | 任意 | 通知に含める stderr 末尾の行数。default `20` |
| `--stderr-tail-bytes <n>` | flag | 任意 | 通知に含める stderr 末尾のバイト数。default `2KB`。`--stderr-tail-lines` と両方が有効な場合は小さい方を採用する |
| `--quiet-on-success` | flag | 任意 | 成功時 (exit code 0) の通知を抑止し、失敗時のみ通知する |
| `--config <path>` | flag | 任意 | 設定 JSON の path を明示指定する (`notify` section を再利用する場合) |
| `--slack-webhook-url-env <name>` | flag | 任意 | Webhook URL を保持する env 変数の名前 |
| `--dry-run` | flag | 任意 | 子プロセスは実際に実行するが、通知を Slack に送信せず stderr に出力する |

### 環境変数

| 変数 | 説明 |
|---|---|
| `MITSUME_SLACK_WEBHOOK_URL` | 既定の Webhook URL env |
| `MITSUME_CONFIG` | 設定 JSON の path |
| `MITSUME_HOST` | host 識別子 |
| `--slack-webhook-url-env` で指定した任意の名前 | Webhook URL を保持する env 変数 |

### 動作

- `--` 以降を子プロセスとして起動する。
- 子の stdout / stderr は親に tee する。同時に末尾を ring buffer (`--stderr-buffer-bytes`) に保持し、失敗通知に載せる。
- 子の終了は OS からの子プロセス終了通知 (`SIGCHLD`) で即時検知する。
- 子の exit code が `0` の場合は成功、非 0 の場合は失敗と判定する。
- 成功時と失敗時のいずれも内部で notifier を呼び出して Slack に通知する。`--quiet-on-success` を指定した場合は成功時の通知を抑止する。
- 失敗通知には exit code と stderr 末尾 (`--stderr-tail-lines` と `--stderr-tail-bytes` の小さい方) を含める。
- 子の起動失敗 (PATH 不在、permission denied) も失敗として通知する。
- `--timeout` 指定時、超過した場合は `SIGTERM` を送信し、`--grace-period` を超えても終了しない場合は `SIGKILL` を送信する。
- 外部から届いた `SIGINT` / `SIGTERM` / `SIGHUP` / `SIGQUIT` は子に forward する。`SIGKILL` は forward できないため best-effort で諦める。
- heartbeat file には触らない。`ping` 相当の処理は呼ばない。
- daemonize しない。バックグラウンド化はユーザー側で `nohup mitsume run ... &` などを用いる。

### Exit code

透過ケース (子プロセスの exit code を透過するもの):

| Code | 意味 |
|---|---|
| `0` | 子プロセスが exit code 0 で正常終了 |
| 子の exit code | 子プロセスが非 0 で終了した場合、その値を透過 |
| `128 + signum` | 子プロセスが外部 signal で kill された場合の慣習コード (`SIGINT` → `130`、`SIGTERM` → `143` など) |

子プロセスが external signal で kill された場合は `128 + signum` を返す。ただし `--timeout` 発火由来の kill は下記の mitsume 固有 code (`124`) を優先し、`128 + SIGTERM` (`143`) にはならない。

mitsume 固有:

| Code | 意味 |
|---|---|
| `124` | `--timeout` 超過で kill した (GNU `timeout(1)` 慣習) |
| `126` | 子プロセスの実行 permission が拒否された (bash 慣習) |
| `127` | 子プロセスが `PATH` 上に見つからない (bash 慣習) |

## `mitsume version`

release binary に埋め込まれた version、commit、build date と、build に用いた Go toolchain の version を stdout に出力して exit する subcommand である。binary の identification と bug report 時の照合用である。

### 使用例

```bash
mitsume version
```

出力例:

```text
mitsume version=<version>, commit=<sha>, built=<RFC3339>, go=<go-toolchain>
```

### 引数と flag

引数と flag は受け取らない。位置引数を渡した場合は exit 1 とする。

### 環境変数

参照しない。

### 動作

- `version` / `commit` / `date` は release build 時に埋め込まれる値である。source build (`go build`) では default 値 (`dev` / `none` / `unknown`) が入る。
- `go` は binary を build した Go toolchain の version を示す。
- 通知は送信しない。heartbeat file には触らない。設定 JSON も読み込まない。

### Exit code

| Code | 意味 |
|---|---|
| `0` | version を出力して正常終了 |
| `1` | 位置引数を渡された、または stdout への書き込みが失敗した |

## 共通 flag

### `--dry-run`

`ping` / `notify` / `check` / `watch` / `run` で有効である (`version` は副作用がないため対象外)。設定の試運転、通知文の事前確認、systemd unit の commissioning に用いる。

| 対象 | 挙動 |
|---|---|
| Slack への送信 | 抑止し、payload は stderr に出力する。`watch` の `SIGTERM` / `SIGINT` 受信時と fatal error 時の best-effort 通知も抑止する |
| heartbeat file の書き込み | 抑止する (`ping` の `last_ping_at` 更新を含む) |
| 子プロセス (`run` のみ) | 実際に実行する。通知の送信のみを抑止する (`run` は通常運用でも heartbeat file には触らない) |

## 環境変数

env 経由で受け取る変数の一覧を示す。CLI 引数、設定 JSON との優先順位は各 subcommand の節と参照先の doc を参照する。

| 変数 | 参照する subcommand | 説明 |
|---|---|---|
| `MITSUME_CONFIG` | `ping` / `notify` / `check` / `watch` / `run` | 設定 JSON の path。探索順は [configuration.md](configuration.md) を参照する |
| `MITSUME_JOB` | `ping` | `<job>` 位置引数の fallback |
| `MITSUME_HOST` | 通知を送る 4 subcommand (`notify` / `check` / `watch` / `run`) | host 識別子 (通知文に載せる)。決定順は [configuration.md § Host identifier](configuration.md#host-identifier) を参照する |
| `MITSUME_HEARTBEAT_FILE` | `ping` / `check` / `watch` | heartbeat file の path。探索順は [heartbeat.md § File location](heartbeat.md#file-location) を参照する |
| `MITSUME_SLACK_WEBHOOK_URL` | `notify` / `check` / `watch` / `run` | Slack Incoming Webhook URL を保持する既定の env 変数 |
| `--slack-webhook-url-env <name>` / `notify.webhook_url_env` で指定した任意の名前 | `notify` / `check` / `watch` / `run` | Webhook URL を保持する env 変数の名前を任意に指定する |

秘密情報 (Webhook URL など) は env 経由でのみ受け取る。CLI 引数で値を直接受け取る flag は提供しない。理由は [architecture.md § Security invariants](architecture.md#security-invariants) を参照する。

## 識別子解決

mitsume が扱う識別子は `job` と `name` の 2 語である。設計の全体像は [architecture.md § 用語](architecture.md#用語) を参照する。

### `job` (dead-man's switch 識別子)

- 走るべき job の一意名である。`mitsume ping <job>` の位置引数、`type: "deadman"` の必須 field で使用する。
- 必須である。自動生成しない。
- 命名規則は `[a-zA-Z0-9_-]{1,64}` である。
- 通知文にも載る。

`mitsume ping` の `<job>` 解決順:

1. 位置引数 (`mitsume ping nightly-backup`)
2. `$MITSUME_JOB` env
3. 設定 JSON 中で唯一存在する `type: "deadman"` の `job` (2 件以上ある場合は解決せずエラー)

いずれの経路でも解決できない場合は exit 1 とする。

### `name` (表示ラベル)

- 通知文の見やすさを目的とした表示ラベルである。全 checker と `mitsume run` の任意 field である。
- `name` は `checks[]` 内で重複してはならない。自動生成後の値も validation の対象とする。
- 省略時は type 別ルールで自動生成する。

Checker の場合:

| Type | 自動生成元 |
|---|---|
| `http` | `url` |
| `file` | `path` または `path_glob` |
| `cmd` | `command` の先頭 32 文字 |
| `container` | `container` field (name または id) |
| `deadman` | `job` をそのまま使う |

`mitsume run` の場合は `--name` を省略すると `<cmd>` の basename を自動生成する。

## 関連

- [configuration.md](configuration.md) — 設定 JSON の schema と探索順
- [checkers.md](checkers.md) — 5 種 checker の評価 logic
- [notify.md](notify.md) — Slack payload と通知トリガー
- [heartbeat.md](heartbeat.md) — heartbeat file の schema と path 解決
- [recipes.md](recipes.md) — 用途別の設定パターン集
- [architecture.md](architecture.md) — core components、failure confirmation、design decisions の背景
