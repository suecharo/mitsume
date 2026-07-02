# Checkers

本 doc は 5 種類の checker (`http` / `deadman` / `file` / `container` / `cmd`) それぞれの field と評価 logic を定義する。用語の定義は [architecture.md § 用語](architecture.md#用語) を、`checks[]` の設定位置と探索順は [configuration.md](configuration.md) を参照する。

## 概要

| Type | `expect` の演算子 | 用途 | 依存する外部リソース |
|---|---|---|---|
| `http` | `status` / `body_contains` / `body_jsonpath` / `latency_under` | HTTP endpoint の応答監視 | 監視対象の HTTP endpoint |
| `deadman` | `within` | 指定時間内に job が `ping` を送ったかを判定する dead-man's switch | heartbeat file |
| `file` | `exists` / `mtime_within` / `size_min` / `size_max` | file の生成・更新の監視 | ローカル filesystem |
| `container` | `running` | Docker / podman container の稼働状態 | Docker Engine API socket |
| `cmd` | `exit_code` / `stdout_contains` / `stderr_not_contains` | 任意コマンドの exit code 判定 (escape hatch) | 実行環境 (shell / 外部 binary) |

`checks[]` は active checker (`http` / `file` / `container` / `cmd`) と `deadman` checker を混在させる 1 本の list である。

## 共通フィールド

全 checker は `type`、`interval`、`expect` を必須とし、任意で `name` と `confirm` を取る。checker 固有の field (`http` の `url`、`file` の `path` など) は各節で定義する。

### `type` / `interval` / `expect`

| Field | 意味 |
|---|---|
| `type` | `"http"` / `"deadman"` / `"file"` / `"container"` / `"cmd"` のいずれか |
| `interval` | 通常の評価間隔 (duration) |
| `expect` | 判定条件を集約する object |

`interval` の解釈:

- 単位は duration。書式は [configuration.md § 値の型](configuration.md#値の型) を参照する。
- 推奨下限は 1h である。過剰通知の抑制は `interval` の値で調整する。
- `mitsume check` (外部 cron 用の 1 回実行) は `interval` を無視し、呼び出し 1 回で全 check を 1 回ずつ評価する。
- `mitsume watch` (常駐) は `interval` ごとに 1 サイクル評価する。

`expect` の総則:

- 判定条件を集約する 1 個の object である。checker 固有の判定条件は必ず `expect` の中に置く。
- 演算子はすべて optional であり、複数を併用した場合は AND で評価する。
- 任意式の eval、数値比較 (`gt` / `lt`) は演算子に含めない。数値閾値が必要な場合は `cmd` checker を使用する。
- 各 checker の演算子は以下の checker 別の節で定義する。

### `name`

省略時は `type` 別ルールで自動生成する。

| Type | 自動生成元 |
|---|---|
| `http` | `url` |
| `file` | `path` または `path_glob` |
| `cmd` | `command` 先頭 32 文字 |
| `container` | `container` field |
| `deadman` | `job` をそのまま使う |

`checks[]` 内で `name` は自動生成後を含めて一意でなければならない。重複は起動時 validation でエラーとする。

### `confirm`

failure 検知後の連続確認 (confirm burst) の設定である。default および schema は [configuration.md § `confirm`](configuration.md#confirm) を参照する。設計上の理由は [architecture.md § Failure confirmation](architecture.md#failure-confirmation) を参照する。

### `defaults` からの継承

top-level の `defaults` object に書いた値を各 check の初期値として継承する。個別 check で同名 field を指定した場合は個別値が優先する。

```json
{
  "defaults": {
    "interval": "1h",
    "timeout": "10s"
  },
  "checks": [
    { "type": "http", "url": "https://a.example.com/health", "expect": { "status": 200 } },
    { "type": "http", "url": "https://b.example.com/health", "interval": "30m", "expect": { "status": 200 } }
  ]
}
```

上の例では 1 番目の `interval` は `1h` (継承)、2 番目は `30m` (上書き) である。`defaults` の対象は `interval` と `timeout` のみである。`confirm` / `expect` / `name` / checker 固有 field は継承しない。

## Checker 側の `--dry-run` の挙動

- checker の実測は行う。HTTP request の送信、file の stat、container socket の呼び出し、cmd の exec を含む。
- 通知を Slack に送信せず、payload を stderr に出力する。
- heartbeat file への書き込みは checker からは発生しない (`deadman` checker のみが `--dry-run` の有無にかかわらず read-only で参照する)。書き込みは `ping` subcommand のみが行う (詳細は [heartbeat.md § Dry-run](heartbeat.md#dry-run) を参照)。

`--dry-run` 全体の挙動は [cli.md § 共通 flag](cli.md#共通-flag) を参照する。

## HTTP checker

`type: "http"` の checker である。HTTP endpoint を呼び出し、status / body / latency で判定する。

```json
{
  "type": "http",
  "name": "api-health",
  "url": "https://api.example.com/health",
  "method": "GET",
  "interval": "1h",
  "timeout": "10s",
  "expect": {
    "status": 200,
    "body_jsonpath": [
      { "path": "$.status", "equals": "ok" }
    ],
    "latency_under": "3s"
  }
}
```

`expect` に 1 項目だけ書く最小例:

```json
{
  "type": "http",
  "url": "https://api.example.com/health",
  "interval": "1h",
  "expect": { "status": 200 }
}
```

### Fields

必須:

| Field | 意味 |
|---|---|
| `type` | `"http"` |
| `url` | 監視対象 URL |
| `interval` | 評価間隔 |
| `expect` | 判定条件 |

任意:

| Field | 意味 |
|---|---|
| `name` | 表示ラベル |
| `confirm` | confirm burst 設定 |
| `method` | HTTP method (default `"GET"`) |
| `headers` | 送信 request header の object (例: `{ "Authorization": "Bearer ..." }`) |
| `body` | 送信 request body (string) |
| `timeout` | 1 回あたりの HTTP timeout (duration)。default は `defaults.timeout` |

### `expect` operators

| Key | 意味 | 例 |
|---|---|---|
| `status` | HTTP status の完全一致 (整数) | `200` |
| `body_contains` | response body の部分文字列 match | `"ok"` |
| `body_jsonpath` | response body を JSON parse し JSONPath で評価 | 下記参照 |
| `latency_under` | response 完了までの上限時間 (duration) | `"3s"` |

`body_jsonpath` は `[{ "path": "$....", "<op>": <value> }, ...]` の配列である。各要素を AND で評価する。演算子は 4 つのみを許容する。

`path` は書式を絞る。dot notation の property access と array index の組み合わせのみを許容する。

| 書式 | 例 | 意味 |
|---|---|---|
| `$` | `$` | root |
| `$.<field>` | `$.status` | root 直下の property |
| `$.<field>.<sub>` | `$.data.value` | ネストされた property |
| `$.<field>[N]` | `$.items[0]` | 配列 index (0 origin) |
| `$.<field>[N].<sub>` | `$.items[0].id` | 配列要素の property |

bracket notation (`['key']`)、再帰探索 (`..`)、wildcard (`*`)、filter (`?(...)`)、slice (`[a:b]`) は許容しない。field 名の文字集合は英数字と `_`、`-` のみである。

| Operator | 意味 | 適用型 |
|---|---|---|
| `equals` | 完全一致 | string / number / bool |
| `contains` | 部分一致 | string |
| `regex` | 正規表現 match (RE2 構文) | string |
| `exists` | field が JSON 上に存在するか | bool (`true` / `false`) |

配列で複数演算子を組み合わせる例:

```json
"body_jsonpath": [
  { "path": "$.status", "equals": "ok" },
  { "path": "$.errors", "exists": false },
  { "path": "$.version", "regex": "^v\\d+\\.\\d+" }
]
```

### Behavior

- TLS 検証は常に有効である。無効化 flag は持たない。
- redirect は最大 10 hop まで自動追跡する。
- retry は `confirm` に一本化する。checker 側で独立した retry は行わない。
- connection error、timeout、TLS handshake 失敗は failure と判定する。
- `status` を書かなかった場合は status の判定を行わない (2xx / 3xx を暗黙成功にしない)。
- `body_jsonpath` は response body が JSON parse 可能であることを前提とする。JSON parse 失敗は failure と判定する。
- `body_contains` は raw byte 上での部分文字列 match である。`Content-Type` に関係なくそのまま照合する。
- `timeout` は checker の `timeout` → `defaults.timeout` → 暗黙 default `30s` の順で解決する。`watch` を無期限にハングさせないための上限であり、この暗黙 default を無効化する手段は提供しない。

## Deadman checker

`type: "deadman"` の checker である。指定 job が指定時間内に `ping` を送ったかを判定する dead-man's switch の実装である。`ping` subcommand が heartbeat file に書き込む per-job の `last_ping_at` を読み、`expect.within` を超えて古い場合を failure と判定する。

```json
{
  "type": "deadman",
  "job": "nightly-backup",
  "interval": "1h",
  "expect": { "within": "25h" }
}
```

### Fields

必須:

| Field | 意味 |
|---|---|
| `type` | `"deadman"` |
| `job` | 監視対象 job の一意識別子。書式は `[a-zA-Z0-9_-]{1,64}` |
| `interval` | heartbeat file を再読して `within` 判定を回す schedule 間隔。外部への polling は発生せず、短くしても評価 cost は増えない (詳細は Behavior 節を参照) |
| `expect` | `within` を含む object |

任意:

| Field | 意味 |
|---|---|
| `name` | 表示ラベル。省略時は `job` をそのまま使う |
| `confirm` | confirm burst 設定 |

### `expect` operators

| Key | 意味 | 例 |
|---|---|---|
| `within` | 最後の `ping` から許容できる経過時間 (duration) | `"25h"` |

### Behavior

- heartbeat file から read-only で `jobs.<job>.last_ping_at` を参照する。file の schema は [heartbeat.md](heartbeat.md) を参照する。
- heartbeat file に該当 job の record が存在しない状態 (一度も `ping` を受けていない状態) は failure と判定する。
- 判定は `now - last_ping_at >= expect.within` で完結する。外部 endpoint への polling を発生させないため、`interval` を短くしても実測 cost は増えない。
- `job` は `checks[]` 内 (および `type: "deadman"` 同士) で重複してはならない。
- `mitsume ping <job>` の位置引数の解決順は [cli.md § mitsume ping](cli.md#mitsume-ping) を参照する。

## File checker

`type: "file"` の checker である。ローカル filesystem 上の file の存在、mtime、size で判定する。バックアップ成果物や、外部プロセスが書き出す health file の監視に用いる。

固定 path を見る例:

```json
{
  "type": "file",
  "name": "app-health-file",
  "path": "/var/log/app/health.json",
  "interval": "1h",
  "expect": { "exists": true, "mtime_within": "10m" }
}
```

glob で match した中から mtime 最新 1 個を見る例:

```json
{
  "type": "file",
  "name": "db-backup-artifact",
  "path_glob": "/backup/db-*.dump",
  "interval": "1h",
  "expect": { "exists": true, "mtime_within": "25h", "size_min": "100MB" }
}
```

### Fields

必須:

| Field | 意味 |
|---|---|
| `type` | `"file"` |
| `path` または `path_glob` | 監視対象 path。どちらか一方のみを指定する |
| `interval` | 評価間隔 |
| `expect` | 判定条件 |

任意:

| Field | 意味 |
|---|---|
| `name` | 表示ラベル。省略時は `path` または `path_glob` を使う |
| `confirm` | confirm burst 設定 |

### `expect` operators

| Key | 意味 | 例 |
|---|---|---|
| `exists` | file が存在するか | `true` / `false` |
| `mtime_within` | 最終更新時刻が指定 duration 以内か | `"25h"` |
| `size_min` | 最小 size | `"100MB"` / `1024` |
| `size_max` | 最大 size | `"10GB"` |

size 表記は [configuration.md § 値の型](configuration.md#値の型) を参照する。

### Behavior

- `path` と `path_glob` は排他である。両方指定、または両方未指定は validation でエラーとする。
- `path_glob` で複数 match した場合は mtime 最新の 1 個のみを評価対象とする。
- match が 0 件の場合、`expect.exists: true` は failure、`expect.exists: false` は success と判定する。
- file の中身は読まず、ファイル属性 (存在、mtime、size) のみを参照する。read 権限がなくても属性を取得できれば判定可能である。
- 属性取得に失敗した場合 (permission denied、親 directory 不在など) は failure と判定する。
- file 内容に対する条件が必要な場合は `cmd` checker で `grep` を呼び出す。

## Container checker

`type: "container"` の checker である。Docker / podman container の稼働状態を判定する。Docker Engine API の `/containers/<container>/json` を UNIX domain socket 経由で直接呼び出し、返り値の container status を評価する。Docker SDK を使わない理由は [architecture.md § Design decisions](architecture.md#design-decisions) を参照する。

`engine` を明示する例:

```json
{
  "type": "container",
  "container": "jellyfin",
  "engine": "docker",
  "interval": "1h",
  "expect": { "running": true }
}
```

`engine` を自動検出させる例:

```json
{
  "type": "container",
  "container": "myproject-api-1",
  "interval": "1h",
  "expect": { "running": true }
}
```

### Fields

必須:

| Field | 意味 |
|---|---|
| `type` | `"container"` |
| `container` | container 名または id。docker compose の場合は `{project}-{service}-{N}` を直接指定する |
| `interval` | 評価間隔 |
| `expect` | 判定条件 |

任意:

| Field | 意味 |
|---|---|
| `name` | 表示ラベル。省略時は `container` field をそのまま使う |
| `confirm` | confirm burst 設定 |
| `engine` | `"docker"` または `"podman"`。省略時は自動検出する |

### `expect` operators

| Key | 意味 | 例 |
|---|---|---|
| `running` | container status が `running` であるか | `true` |

### Behavior

- Docker Engine API `GET /v1.43/containers/<container>/json` を UNIX domain socket 経由で呼び出し、返り値の container status を評価する。
- socket path の探索順は次の通りである。
  - `engine: "docker"` — `$DOCKER_HOST` (`unix://` 形式のみ) → `/var/run/docker.sock`
  - `engine: "podman"` — `$XDG_RUNTIME_DIR/podman/podman.sock` → `/run/podman/podman.sock`
  - `engine` 省略 — docker socket → podman socket の順で自動探索
- 起動時 validation で socket が見つからない場合は fail-fast する。
- checker 側では `timeout` config field を持たず、`defaults.timeout` も継承しない。1 回の評価あたり 30s の上限を内部で適用する。socket の停止による無期限のハングを防ぐためであり、この値は変更できない。
- Docker の `HEALTHCHECK` 連動 (`.State.Health.Status`) は提供しない。`expect.healthy` field も持たない。
- リモート host の container 監視は対象外である。`mitsume watch` は container host 上で動かす前提となる。

## Cmd checker

`type: "cmd"` の checker である。任意の外部コマンドを実行し、exit code / stdout / stderr で判定する escape hatch である。他 checker で吸収しにくい判定 (数値閾値、外部 CLI 依存、既存 systemd service の傍観など) は本 checker に集約する。

典型的な使い方:

| 用途 | `command` 例 |
|---|---|
| disk 残量 | `["/bin/sh", "-c", "test $(df --output=pcent /data | tail -1 | tr -dc 0-9) -lt 90"]` |
| TLS cert 期限 | `["openssl", "x509", "-checkend", "604800", "-noout", "-in", "/etc/ssl/cert.pem"]` |
| systemd service | `["systemctl", "is-active", "foo.service"]` |
| pid file 生存 | `["/bin/sh", "-c", "kill -0 $(cat /var/run/foo.pid)"]` |

```json
{
  "type": "cmd",
  "name": "tls-cert-expiry",
  "command": ["openssl", "x509", "-checkend", "604800", "-noout", "-in", "/etc/ssl/cert.pem"],
  "interval": "24h",
  "timeout": "10s",
  "expect": { "exit_code": 0 }
}
```

### Fields

必須:

| Field | 意味 |
|---|---|
| `type` | `"cmd"` |
| `command` | 実行コマンド (`string[]`)。shell を経由せず直接 exec する |
| `interval` | 評価間隔 |
| `expect` | 判定条件 |

任意:

| Field | 意味 |
|---|---|
| `name` | 表示ラベル。省略時は `command` 先頭 32 文字 |
| `confirm` | confirm burst 設定 |
| `env` | 追加 env の object (例: `{ "KEY": "value" }`) |
| `cwd` | 実行時のカレント directory |
| `timeout` | 1 回あたりの実行 timeout (duration)。default は `defaults.timeout` |

### `expect` operators

| Key | 意味 | 例 |
|---|---|---|
| `exit_code` | 期待する exit code (default `0`) | `0` |
| `stdout_contains` | stdout に含まれるべき部分文字列 | `"active"` |
| `stderr_not_contains` | stderr に含まれてはならない部分文字列 | `"panic"` |

### Behavior

- `command` は配列で直接 exec する。shell interpolation、pipe、redirect が必要な場合は `["/bin/sh", "-c", "<script>"]` の形式で明示的に呼び出す。
- `timeout` 超過時は `SIGTERM` を送信し、grace period 経過後に `SIGKILL` を送信する。
- 上記 timeout kill が発火した場合、`expect.exit_code` との比較用の観測値として `124` (GNU `timeout(1)` 慣習) を採用する。`expect.exit_code` を省略した場合の default は `0` のため、通常 timeout は必ず failure と判定する。timeout kill を success として扱いたい場合は `expect.exit_code: 124` を明示する。
- `timeout` は checker の `timeout` → `defaults.timeout` → 暗黙 default `30s` の順で解決する。grace period は `5s` 固定であり、`timeout` field や `defaults` からの継承、設定 JSON 側での上書きは受け付けない。
- `stdout_contains` と `stderr_not_contains` は子プロセスの buffer 全体 (起動から終了まで) を対象に評価する。通知 payload に載せる stderr 末尾 (20 行または 2KB のいずれか小さい方) は表示用の切り詰めであり、判定には影響しない。この切り詰め幅は固定であり、設定 JSON からの上書きは受け付けない (`mitsume run --stderr-tail-*` は cmd checker には効かない)。
- 失敗通知には exit code と stderr 末尾を含める。payload の詳細は [notify.md § Payload](notify.md#payload) を参照する。
- `expect.exit_code` を省略した場合の default は `0` である (成功終了以外は failure)。
- 環境変数は親プロセスの env と `env` field の union を渡す (`env` が優先)。

## 関連

- [architecture.md](architecture.md) — core components、failure confirmation、design decisions の背景
- [configuration.md](configuration.md) — 設定 JSON schema、`defaults` の継承、value types
- [notify.md](notify.md) — Slack payload と delivery retry
- [heartbeat.md](heartbeat.md) — heartbeat file の schema (`deadman` の依存先)
- [cli.md](cli.md) — `ping` / `check` / `watch` / `run` / `notify` の呼び分け
