# Configuration

mitsume の挙動は設定 JSON、CLI 引数、環境変数の 3 者で決まる。本 doc は設定 JSON の schema、mitsume が設定 JSON をどう探すか、秘密情報をどう受け取るかを定義する。用語の定義は [architecture.md § 用語](architecture.md#用語) を参照する。

`mitsume check` と `mitsume watch` は設定 JSON を必須とする。`mitsume ping` / `mitsume notify` / `mitsume run` は設定 JSON なしでも動作し、設定 JSON が探索順で見つかった場合は次の値のみを利用する。

- `mitsume notify` / `mitsume run` — `notify` section (Slack Webhook の接続情報)
- `mitsume ping` — `heartbeat_file` field と、`deadman` checker が 1 個だけ定義されている場合の `<job>` fallback ([cli.md § 識別子解決](cli.md#識別子解決) を参照)

## 最小サンプル

HTTP checker 1 個と Slack 通知先で構成される、動く最小の設定 JSON である。

```json
{
  "notify": {
    "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"
  },
  "checks": [
    {
      "type": "http",
      "name": "api-health",
      "url": "https://api.example.com/health",
      "expect": { "status": 200 },
      "interval": "1h"
    }
  ]
}
```

これを `./mitsume.json` に置いて次のように起動する。

```bash
export MITSUME_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
mitsume watch
```

`deadman` checker を追加する場合は `heartbeat_file` を明示するか、config と同じ directory の隣接 file (basename の `.json` を `.heartbeat.json` に置換した path、例: `production.json` → `production.heartbeat.json`) を使う。詳細は [heartbeat.md § File location](heartbeat.md#file-location) を参照する。

```json
{
  "notify": { "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL" },
  "defaults": { "interval": "1h" },
  "checks": [
    { "type": "http", "name": "api-health",
      "url": "https://api.example.com/health",
      "expect": { "status": 200 } },
    { "type": "deadman", "job": "nightly-backup",
      "expect": { "within": "25h" } }
  ]
}
```

## 設定 JSON の場所

`mitsume check` と `mitsume watch` は次の順で最初に見つかった 1 個を読む。複数が見つかっても merge しない。

1. `--config <path>` (明示指定)
2. `$MITSUME_CONFIG` (環境変数)
3. `./mitsume.json` (カレント directory、自動探索)

自動探索の対象は `./mitsume.json` のみである。`~/.config/mitsume/` や `/etc/mitsume/` のような暗黙 path は参照しない。どの設定 JSON が有効かは呼び出しコンテキストから一意に決まる。

## トップレベルフィールド

設定 JSON の top-level はこの形である。`checks[]` に監視対象を 1 本の list で並べ、active checker と `deadman` checker を混在できる。

```json
{
  "host": "api-prod-01",
  "heartbeat_file": "/var/lib/mitsume/heartbeat.json",
  "notify": {
    "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"
  },
  "defaults": {
    "interval": "1h",
    "timeout": "10s"
  },
  "checks": [
    { "type": "http", "name": "api-health", "url": "https://api.example.com/health",
      "expect": { "status": 200 } },
    { "type": "deadman", "job": "nightly-backup",
      "expect": { "within": "25h" } }
  ]
}
```

`notify` は単一 object である。named map、配列指定、check ごとの notifier 振り分けは持たない。理由は [architecture.md § Design decisions](architecture.md#design-decisions) を参照する。

| Field | 必須 | 型 | 説明 |
|---|---|---|---|
| `checks` | Yes | array | 監視対象の list。要素は `type` field で checker 種別を指定する。 |
| `notify` | Yes | object | Slack 通知先の接続情報。 |
| `host` | No | string | 通知メッセージに載せる host 識別子。省略時の解決順は [Host identifier](#host-identifier) を参照する。 |
| `heartbeat_file` | No | string | heartbeat file の絶対 path。CLI (`--heartbeat-file`) と `$MITSUME_HEARTBEAT_FILE` が上書きする。両者省略時は本 field を使い、それも省略時は config と同じ directory の隣接 file (basename の `.json` を `.heartbeat.json` に置換した path) を使う。詳細は [heartbeat.md § File location](heartbeat.md#file-location) を参照する。 |
| `defaults` | No | object | 全 check に適用する共通 default 値。 |

### `checks[]` common fields

各要素は `type` に応じて必須 / 任意 field が変わる。全 checker で共通の field はこの 5 つである。

| Field | 必須 | 型 | 説明 |
|---|---|---|---|
| `type` | Yes | string | `http` / `deadman` / `file` / `container` / `cmd` のいずれか。 |
| `name` | No | string | 通知文の表示ラベル。省略時は `type` 別ルールで自動生成する。 |
| `interval` | Yes (defaults 継承可) | duration | 評価周期。`watch` で使用する。`check` では無視する。`deadman` checker における `interval` の意味は [checkers.md § Deadman checker](checkers.md#deadman-checker) を参照する。 |
| `expect` | Yes | object | 成功条件を宣言的に書く。field は `type` ごとに異なる。詳細は [checkers.md](checkers.md) を参照する。 |
| `confirm` | No | object または `false` | 連続確認 (confirm burst) の設定。詳細は [`confirm`](#confirm) を参照する。 |

`name` は自動生成後を含めて `checks[]` 内で一意でなければならない。重複は起動時 validation でエラーとする。

`type: "deadman"` は追加で `job` field を必須とする。`job` は `[a-zA-Z0-9_-]{1,64}` に従い、`checks[]` 内で一意である必要がある。

checker 固有の field は [checkers.md](checkers.md) にまとめる。

### `defaults`

| Field | 必須 | 型 | 説明 |
|---|---|---|---|
| `interval` | No | duration | 全 check の `interval` の初期値。 |
| `timeout` | No | duration | HTTP / cmd checker の request / command timeout の初期値。 |

各 check の同名 field で上書きする。`defaults.notify` は持たない。`notify` field は check ごとに切り替えられない単一 object であり、上書き対象が存在しないため defaults を持つ意味がない (理由は [architecture.md § Design decisions](architecture.md#design-decisions) を参照)。

### `confirm`

failure 検知後に短い間隔で連続確認する一連の動作を confirm burst と呼ぶ。合計評価回数は `confirm.checks` である。うち 1 回目は通常サイクルでの初回 failure 検知、残り `confirm.checks - 1` 回が `confirm.interval` 間隔での追加確認となる。全評価が failure だった場合に failure を確定する。時間スケールを 2 種類 (通常 `interval` と `confirm.interval`) に分ける設計上の理由は [architecture.md § Failure confirmation](architecture.md#failure-confirmation) を参照する。

| Field | 必須 | 型 | Default | 説明 |
|---|---|---|---|---|
| `checks` | No | int (>= 1) | `3` | 合計評価回数。初回 failure 検知を含む。 |
| `interval` | No | duration | `30s` | 追加確認の間隔。 |

`confirm` を省略した場合は上記 default (3 回 × 30s) を適用する。特殊値と部分指定の例を次に示す。

```json
{ "type": "http", "url": "...", "interval": "1m", "confirm": false }
```

`confirm: false` を指定した場合は confirm burst を実行せず、1 回目の failure で即通知を送信する (one-strike out)。

```json
{ "type": "http", "url": "...", "interval": "5m", "confirm": { "checks": 5 } }
```

`checks` のみを指定した場合、`confirm.interval` は default (`30s`) を維持する。

confirm burst の動作は次の通りである。

1. 通常時は `interval` ごとに評価する。
2. failure を検知したら `confirm.interval` に切り替え、`confirm.checks - 1` 回まで連続確認する。
3. 全部 failure の場合は alert を送信する。
4. 途中で成功した場合は状態を reset し、通常 `interval` に戻る。

`confirm.checks` に `0` 以下を指定した場合、および `confirm.interval` の parse 失敗は起動時 validation でエラーとする。

## 値の型

### Duration

duration 文字列は次の単位を組み合わせた表記である。`d` 以外は Go 標準の duration 書式に準拠し、`d` (日) は mitsume の spec 拡張である。

| 単位 | 例 |
|---|---|
| ナノ秒 | `500ns` |
| マイクロ秒 | `500us` (`500µs` も許容) |
| ミリ秒 | `500ms` |
| 秒 | `30s` |
| 分 | `5m` |
| 時 | `1h`、`24h` |
| 日 | `3d`、`1d1h` (時単位との混在可) |

複合表記 (`1h30m`、`2d12h`) は許容する。`w` (週) と ISO 8601 (`PT30S`、`P1D`) は許容しない。

duration を受け取る field:

- `checks[].interval`
- `checks[].timeout`
- `checks[].confirm.interval`
- `checks[].expect.within` (`deadman`)
- `checks[].expect.mtime_within` (`file`)
- `checks[].expect.latency_under` (`http`)
- `defaults.interval`、`defaults.timeout`

parse 失敗は起動時 validation でエラーとする。

### Size

size 文字列は 1024 base の human-readable 表記である。整数 byte の直書きも許容する。

| 表記 | 意味 |
|---|---|
| `100B` | 100 byte |
| `512KB` | 512 × 1024 byte |
| `100MB` | 100 × 1024^2 byte |
| `10GB` | 10 × 1024^3 byte |
| `1TB` | 1 × 1024^4 byte |
| `1` | 1 byte (整数直書き) |

`file` checker の `expect.size_min` / `expect.size_max` で使用する。

## 秘密情報

Slack Incoming Webhook URL などの秘密情報を CLI 引数の値として直接渡す方式は用意しない。渡し方は次の 3 通りに限る。

1. **JSON の `_env` サフィックス.** field 名末尾を `_env` にし、環境変数名を値に書く。

   ```json
   { "notify": { "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL" } }
   ```

2. **CLI の `--*-env` フラグ.** フラグ名末尾を `-env` にし、環境変数名を渡す。

   ```bash
   mitsume notify --slack-webhook-url-env MITSUME_SLACK_WEBHOOK_URL "hello"
   ```

3. **既知名 env の export.** 既知名 (現在は `MITSUME_SLACK_WEBHOOK_URL` のみ) を環境変数として export しておくと、CLI 引数と JSON field を両方省略しても値を取得する。他の `MITSUME_*` env (例: `MITSUME_HOST`、`MITSUME_HEARTBEAT_FILE`、`MITSUME_CONFIG`、`MITSUME_JOB`) は秘密情報用途ではなく、fallback 対象や設定 path の指定に用いる ([cli.md § 環境変数](cli.md#環境変数) を参照)。

   ```bash
   export MITSUME_SLACK_WEBHOOK_URL=https://hooks.slack.com/...
   mitsume notify "hello"
   ```

`--slack-webhook-url https://...` のような値直渡し flag は提供しない。理由は [architecture.md § Security invariants](architecture.md#security-invariants) を参照する。

`_env` / `--*-env` で指定した環境変数が起動時点で未定義の場合、起動時 validation で fail-fast する。process 起動時点の env を snapshot して使用するため、起動後の env 変更 (`watch` の走行中に対話 shell で `export` を再定義するなど) は反映しない。

## Host identifier

通知メッセージに載せる `host` field は次の順で解決する。

1. 設定 JSON の `host` field
2. `MITSUME_HOST` 環境変数
3. OS が返す hostname

Docker container ごとに識別子を差し替える設定例は [recipes.md](recipes.md) を参照する。

複数の host から同じ check の failure 通知が並ぶ場合の grouping は行わない。各 host は独立して通知する。

## Validation

起動時 validation は次の条件で fail-fast する。

- `name` の重複 (`checks[]` 内)
- `job` の重複 (`checks[]` 内)、および `job` の regex 違反 (`[a-zA-Z0-9_-]{1,64}` に一致しない)
- `confirm.checks` が `0` 以下
- `confirm.interval` の duration parse 失敗
- `interval` / `timeout` / `expect.within` などの duration field の parse 失敗
- `expect.size_min` / `expect.size_max` の size 表記 parse 失敗
- `_env` / `--*-env` で指定した環境変数が未定義
- checker 別の必須 field 欠落 (詳細は [checkers.md](checkers.md))

`watch` の実行中に設定 JSON を再読み込みしないため、設定を変更した場合は process supervisor から `watch` を再起動する。理由は [architecture.md § Design decisions](architecture.md#design-decisions) を参照する。

## 関連

- [checkers.md](checkers.md) — checker 別 field と評価 logic
- [notify.md](notify.md) — Slack payload と delivery retry
- [heartbeat.md](heartbeat.md) — heartbeat file の schema と path 解決
- [cli.md](cli.md) — subcommand と共通 flag
- [architecture.md](architecture.md) — core components、failure confirmation、design decisions の背景
