# checker リファレンス

「HTTP エンドポイントを叩きたい」「バックアップ file の mtime を見たい」「container が動いているか確認したい」といった監視項目を書くときに読む doc。5 種類の checker (`http` / `deadman` / `file` / `container` / `cmd`) の共通契約と、種類ごとの必須 / 任意フィールドと `expect` 演算子をまとめる。

`checks[]` の位置付け、設定 JSON の探索順、`defaults` の継承ルールは [configuration.md](configuration.md) を参照。

## checker 対応表

| type | `expect` の演算子 | 用途 | 依存する外部リソース |
|---|---|---|---|
| `http` | `status`, `body_contains`, `body_jsonpath`, `latency_under` | HTTP endpoint の応答監視 | 監視対象の HTTP endpoint |
| `deadman` | `within` | 走るべき job が指定時間内に到来したかの反転監視 | heartbeat file |
| `file` | `exists`, `mtime_within`, `size_min`, `size_max` | ファイル生成 / 更新の監視 | ローカル FS |
| `container` | `running` | docker / podman container の稼働状態 | docker or podman の unix socket |
| `cmd` | `exit_code`, `stdout_contains`, `stderr_not_contains` | 任意コマンドの exit code 判定 (escape hatch) | 実行環境 (shell / 外部 binary) |

## 共通契約

全 checker は `type` + `interval` + `expect` を必須で持ち、任意で `name` と `confirm` を取る。個別 checker の固有フィールド (`http` の `url`、`file` の `path_glob` など) は各節で扱う。

### 必須フィールド

| フィールド | 意味 |
|---|---|
| `type` | `"http"` / `"deadman"` / `"file"` / `"container"` / `"cmd"` のいずれか |
| `interval` | 通常のポーリング間隔 (duration) |
| `expect` | 判定条件をまとめたオブジェクト |

`interval` は `defaults.interval` から継承できる。checker 固有の必須フィールドは各節の表に記す。

### 任意フィールド

| フィールド | 意味 |
|---|---|
| `name` | 通知に載る表示ラベル。省略時は type 別ルールで自動生成 |
| `confirm` | 失敗確信のための短 retry burst。default `{ "checks": 3, "interval": "30s" }` |

### `interval`

- 単位は duration。Go の `time.ParseDuration` 互換 (`500ms`, `30s`, `5m`, `1h`, `24h`) に `d` (日) を追加した記法を使う (`3d` = 72h)
- 推奨下限は 1h。過剰通知の抑制は `interval` の値で調整する
- `mitsume check` (外部 cron 用の 1 回実行) では `interval` を無視し、呼び出し 1 回で全 check を 1 回ずつ評価する
- `mitsume watch` (常駐) では `interval` ごとに 1 サイクル評価する

### `confirm`

失敗を 1 回検知した後、`confirm.interval` × `confirm.checks` の短 retry burst を回して確信を取る。1 評価サイクル内で burst が完結してから、次の `interval` を待つ。

```json
{
  "type": "http",
  "url": "https://api.example.com/health",
  "interval": "1h",
  "confirm": { "checks": 5, "interval": "10s" },
  "expect": { "status": 200 }
}
```

| 記法 | 意味 |
|---|---|
| 省略 | default 適用 (`{ "checks": 3, "interval": "30s" }`) |
| `{ "checks": N }` | `interval` は default `"30s"`、`checks` のみ上書き |
| `{ "checks": N, "interval": D }` | 両方指定 |
| `false` | one-strike (1 回失敗で即 alert) |

- `checks` は 1 以上の整数、または `false`。0 や負値は validation error
- burst の途中で成功が返れば状態リセット、通常 `interval` に戻る
- burst 中の全評価が失敗なら alert を発火する
- `interval` (通常ポーリング間隔) と `confirm.interval` (短 retry の粒度) は時間スケールが違う。前者は 1h 以上、後者は default 30s

### `expect`

- 判定条件を集約する 1 オブジェクト
- 全 checker が `expect: { ... }` の対称構造を持ち、checker 固有の判定条件は必ず `expect` の中に置く
- 演算子はすべて optional で複数併用可、AND で評価する
- 任意式の eval / 数値比較 (`gt` / `lt`) は演算子に含めない。数値閾値が必要なら `cmd` checker を使う

### `name` の自動生成

省略時は type 別ルールで自動生成する。

| type | 自動生成元 |
|---|---|
| `http` | `url` |
| `file` | `path` (or `path_glob`) |
| `cmd` | `command` 先頭 32 文字 |
| `container` | `container` フィールド |
| `deadman` | `job` をそのまま使う |

`checks[]` 内で `name` が重複してはならない (自動生成後の値も含む)。

### `--dry-run` 時の挙動

- checker の実測は行う: HTTP request を発火する、file を stat する、container socket を叩く、cmd を exec する
- alert は Slack に送らず、payload を stderr に出力する
- heartbeat file の書き換えも行わない (deadman 評価は元々 read only なので影響なし)

### `defaults` からの継承

top-level の `defaults` オブジェクトに書いた値は全 check の default になる。個別 check で同名フィールドを指定した場合は個別値が優先する。

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

上の例では 1 番目の `interval` は `1h` (継承)、2 番目は `30m` (上書き)。`defaults` の対象は `interval` / `timeout` のみ。`confirm` / `expect` / `name` / checker 固有フィールドは継承対象外。

## `http` checker

HTTP endpoint を呼び出し、status / body / latency を判定する。

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

`expect` に 1 項目だけ書く最小例。

```json
{
  "type": "http",
  "url": "https://api.example.com/health",
  "interval": "1h",
  "expect": { "status": 200 }
}
```

### 必須フィールド

| フィールド | 意味 |
|---|---|
| `type` | `"http"` |
| `url` | 監視対象 URL |
| `interval` | ポーリング間隔 |
| `expect` | 判定条件 |

### 任意フィールド

| フィールド | 意味 |
|---|---|
| `name` | 表示ラベル |
| `confirm` | 失敗確信の設定 |
| `method` | HTTP method (default `"GET"`) |
| `headers` | 送信 request header の object (`{ "Authorization": "Bearer ..." }`) |
| `body` | 送信 request body (string) |
| `timeout` | 1 回あたりの HTTP timeout (duration、default `defaults.timeout`) |

### `expect` の演算子

| key | 意味 | 例 |
|---|---|---|
| `status` | HTTP status の完全一致 (整数) | `200` |
| `body_contains` | response body の部分文字列マッチ | `"ok"` |
| `body_jsonpath` | response body を JSON parse し jsonpath で評価 | 下記 |
| `latency_under` | response 完了までの上限時間 (duration) | `"3s"` |

`body_jsonpath` は `[{ "path": "$....", "<op>": <value> }, ...]` の配列で、各要素を AND で評価する。演算子は 4 つのみ。

`path` はサポートする書式を絞る。dot notation の property access と array index の組み合わせのみ受ける。

| 書式 | 例 | 意味 |
|---|---|---|
| `$` | `$` | root |
| `$.<field>` | `$.status` | root 直下の property |
| `$.<field>.<sub>` | `$.data.value` | ネストされた property |
| `$.<field>[N]` | `$.items[0]` | 配列 index (0 origin) |
| `$.<field>[N].<sub>` | `$.items[0].id` | 配列要素の property |

bracket notation (`['key']`)、再帰探索 (`..`)、wildcard (`*`)、filter (`?(...)`)、slice (`[a:b]`) は非対応。field 名の文字集合は英数字と `_` `-` のみ。

| 演算子 | 意味 | 適用型 |
|---|---|---|
| `equals` | 完全一致 | string / number / bool |
| `contains` | 部分一致 | string |
| `regex` | 正規表現マッチ (RE2 構文) | string |
| `exists` | フィールドが JSON 上に存在する | bool (`true` / `false`) |

配列で複数演算子を組み合わせる例。

```json
"body_jsonpath": [
  { "path": "$.status", "equals": "ok" },
  { "path": "$.errors", "exists": false },
  { "path": "$.version", "regex": "^v\\d+\\.\\d+" }
]
```

### 固有の挙動

- TLS 検証は常に有効。無効化 flag は持たない
- redirect は Go `net/http` の default に従う (最大 10 hop)
- retry は `confirm` に一本化する。checker 側で独立した retry は行わない
- connection error / timeout / TLS handshake 失敗は failure と判定する
- `status` を書かなければ status の判定は行わない (2xx / 3xx を暗黙成功にしない)
- `body_jsonpath` は response body が JSON parse 可能であることを前提にする。JSON parse 失敗は failure と判定する
- `body_contains` は raw byte 上での部分文字列マッチ。`Content-Type` に関係なくそのまま照合する
- `timeout` は checker の `timeout` > `defaults.timeout` > 暗黙 default `30s` の順で解決する。すべて未指定なら `30s` (net/http の default である無制限にしない: watch を無期限ハングさせるリスクを構造的に排除する)

## `deadman` checker

走るべき job が走らなかったことを検知する反転監視。`ping` サブコマンドが heartbeat file に書き込む per-`job` の `last_ping_at` を読み、`expect.within` を超えて古ければ failure と判定する。

```json
{
  "type": "deadman",
  "job": "nightly-backup",
  "interval": "1h",
  "expect": { "within": "25h" }
}
```

### 必須フィールド

| フィールド | 意味 |
|---|---|
| `type` | `"deadman"` |
| `job` | 監視対象 job の一意識別子。`[a-zA-Z0-9_-]{1,64}` |
| `interval` | 評価スケジュール上の間隔 |
| `expect` | `within` を含む |

### 任意フィールド

| フィールド | 意味 |
|---|---|
| `name` | 表示ラベル (省略時 `job` をそのまま使う) |
| `confirm` | 失敗確信の設定 |

### `expect` の演算子

| key | 意味 | 例 |
|---|---|---|
| `within` | 最後の `ping` から許容できる経過時間 (duration) | `"25h"` |

### 固有の挙動

- heartbeat file から read only で `jobs.<job>.last_ping_at` を参照する。file の schema と探索順は [heartbeat.md](heartbeat.md) を参照
- heartbeat file に該当 `job` の record が存在しない (まだ一度も `ping` を受けていない) 状態は failure と判定する
- 判定は `now - last_ping_at >= expect.within` で完結する。外部 endpoint への polling は発生しないため、`interval` を短くしても実測コストは増えない
- `job` は `checks[]` 内 (および `type: deadman` 同士) で重複してはならない
- `mitsume ping <job>` の位置引数の解決順は [cli.md](cli.md) を参照

## `file` checker

ローカル FS 上のファイルの存在 / mtime / size を判定する。バックアップ成果物や、外部プロセスが書き出す health file の監視に使う。

固定 path を見る例。

```json
{
  "type": "file",
  "name": "app-health-file",
  "path": "/var/log/app/health.json",
  "interval": "1h",
  "expect": { "exists": true, "mtime_within": "10m" }
}
```

glob で match した中の mtime 最新 1 個を見る例。

```json
{
  "type": "file",
  "name": "db-backup-artifact",
  "path_glob": "/backup/db-*.dump",
  "interval": "1h",
  "expect": { "exists": true, "mtime_within": "25h", "size_min": "100MB" }
}
```

### 必須フィールド

| フィールド | 意味 |
|---|---|
| `type` | `"file"` |
| `path` または `path_glob` | 監視対象パス (どちらか一方のみ指定) |
| `interval` | ポーリング間隔 |
| `expect` | 判定条件 |

### 任意フィールド

| フィールド | 意味 |
|---|---|
| `name` | 表示ラベル (省略時 `path` または `path_glob` を使う) |
| `confirm` | 失敗確信の設定 |

### `expect` の演算子

| key | 意味 | 例 |
|---|---|---|
| `exists` | ファイルが存在する | `true` / `false` |
| `mtime_within` | 最終更新時刻が指定 duration 以内 | `"25h"` |
| `size_min` | 最小サイズ | `"100MB"`, `1024` |
| `size_max` | 最大サイズ | `"10GB"` |

size 表記は 1024 ベースの human-readable (`B` / `KB` / `MB` / `GB` / `TB`)。整数 byte 直書きも許可 (`"size_min": 1` で 1 byte)。

### 固有の挙動

- `path` と `path_glob` は排他。両方指定、または両方未指定は validation error
- `path_glob` は match した候補の中で mtime 最新の 1 個を評価対象にする。選択規則は mtime 最新のみで、`select` フィールドは持たない
- match が 0 件のとき `expect.exists: true` は failure、`expect.exists: false` は success
- ファイル中身の判定 (`content_contains` など) はサポートしない。必要なら `cmd` checker で `grep` を呼ぶ
- ファイル自体は読まず `stat(2)` 相当の情報だけを見る。read 権限が無くても `stat` できれば判定可能
- `stat` に失敗した (permission denied、親ディレクトリ不在など) 場合は failure と判定する

## `container` checker

docker / podman container の稼働状態を確認する。Docker Engine API の `/containers/<container>/json` を unix socket 経由で直接叩き、`.State.Status` を評価する。

`engine` を明示する例。

```json
{
  "type": "container",
  "container": "jellyfin",
  "engine": "docker",
  "interval": "1h",
  "expect": { "running": true }
}
```

`engine` を自動検出させる例。

```json
{
  "type": "container",
  "container": "myproject-api-1",
  "interval": "1h",
  "expect": { "running": true }
}
```

### 必須フィールド

| フィールド | 意味 |
|---|---|
| `type` | `"container"` |
| `container` | container 名 or id (compose の場合は `{project}-{service}-{N}` を直接指定) |
| `interval` | ポーリング間隔 |
| `expect` | 判定条件 |

### 任意フィールド

| フィールド | 意味 |
|---|---|
| `name` | 表示ラベル (省略時 `container` フィールドをそのまま使う) |
| `confirm` | 失敗確信の設定 |
| `engine` | `"docker"` / `"podman"` (省略時は自動検出) |

### `expect` の演算子

| key | 意味 | 例 |
|---|---|---|
| `running` | `.State.Status == "running"` かどうか | `true` |

### 固有の挙動

- Docker Engine API の `GET /v1.43/containers/<container>/json` を unix socket 経由で呼び、`.State.Status` を評価する。実装詳細 (Docker SDK を使わない理由など) は [architecture.md](architecture.md#container-checker-の実装方針) を参照
- socket 探索順:
  - `engine: "docker"` → `$DOCKER_HOST` (`unix://` 形式) → `/var/run/docker.sock`
  - `engine: "podman"` → `$XDG_RUNTIME_DIR/podman/podman.sock` → `/run/podman/podman.sock`
  - `engine` 省略 → docker socket → podman socket の順で自動探索
- 起動時 validation で socket が見つからなければ fail-fast する。validation の詳細は [configuration.md](configuration.md) を参照
- `HEALTHCHECK` 連動 (`.State.Health.Status`) はサポートしない。`expect.healthy` フィールドも持たない
- リモート host の container 監視はサポートしない。`mitsume watch` は container host 上で動かす前提

## `cmd` checker

任意の外部コマンドを実行し、exit code / stdout / stderr で成否を判定する escape hatch。他 checker で吸収しにくい判定 (数値閾値、外部 CLI 依存、既存 systemd service の傍観など) はここに集約する。

典型的な使い方:

| 用途 | command 例 |
|---|---|
| ディスク残量 | `["/bin/sh", "-c", "test $(df --output=pcent /data | tail -1 | tr -dc 0-9) -lt 90"]` |
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

### 必須フィールド

| フィールド | 意味 |
|---|---|
| `type` | `"cmd"` |
| `command` | 実行コマンド (`string[]`、shell を経由しない直接 exec) |
| `interval` | ポーリング間隔 |
| `expect` | 判定条件 |

### 任意フィールド

| フィールド | 意味 |
|---|---|
| `name` | 表示ラベル (省略時 `command` 先頭 32 文字) |
| `confirm` | 失敗確信の設定 |
| `env` | 追加 env の object (`{ "KEY": "value" }`) |
| `cwd` | 実行時のカレントディレクトリ |
| `timeout` | 1 回あたりの実行 timeout (duration、default `defaults.timeout`) |

### `expect` の演算子

| key | 意味 | 例 |
|---|---|---|
| `exit_code` | 期待する exit code (default `0`) | `0` |
| `stdout_contains` | stdout に含まれるべき部分文字列 | `"active"` |
| `stderr_not_contains` | stderr に含まれてはならない部分文字列 | `"panic"` |

### 固有の挙動

- `command` は配列で直接 exec する。shell interpolation / パイプ / redirect が必要なら `["/bin/sh", "-c", "<script>"]` を明示的に指定する
- `timeout` 超過時は `SIGTERM` を送信、grace period (5s 固定) 経過後に `SIGKILL`。exit code は `124` を内部的に扱い、`expect.exit_code` と一致しなければ failure
- `timeout` は checker の `timeout` > `defaults.timeout` > 暗黙 default `30s` の順で解決する。grace period は config field を持たず、`run` サブコマンド (`--grace-period` default `5s`) と同じ値に揃える
- 失敗通知には exit code と stderr 末尾 (20 行 or 2KB の小さい方) を含める。payload の詳細は [notify.md](notify.md) を参照
- `expect.exit_code` を省略した場合の default は `0` (成功終了以外は failure)
- 環境変数は親プロセスの env + `env` フィールドの union を渡す (`env` が優先)

## 関連

- [configuration.md](configuration.md) — 設定 JSON の schema、`defaults`、探索順
- [notify.md](notify.md) — Slack 通知の payload と retry
- [heartbeat.md](heartbeat.md) — heartbeat file の schema (`deadman` の依存先)
- [cli.md](cli.md) — `ping` / `check` / `watch` / `run` / `notify` の呼び分け
- [architecture.md](architecture.md) — checker / notify / heartbeat の 3 直交軸と失敗確信モデル
