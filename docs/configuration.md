# 設定 JSON

mitsume の挙動は設定 JSON / CLI 引数 / 環境変数の 3 者で決まる。設定 JSON の schema と、mitsume がその JSON をどう探すかをまとめる。

`mitsume watch` を常駐させる前に schema と探索順を確認したいとき、あるいは既存の `mitsume.json` に checker を足すときに引く。

## 設定 JSON の場所

`mitsume check` と `mitsume watch` は設定 JSON がないと動かない。次の順で最初に見つかった 1 個を読み、複数見つかっても merge しない。

1. `--config <path>` (明示指定)
2. `$MITSUME_CONFIG` (環境変数)
3. `./mitsume.json` (カレントディレクトリ、自動探索)

`mitsume ping` / `mitsume notify` / `mitsume run` は設定 JSON がなくても動く。CLI 引数と環境変数だけで完結する。設定 JSON が同じ順で見つかれば、`notify` の接続情報だけ再利用する。

自動探索は `./mitsume.json` のみ。`~/.config/mitsume/` や `/etc/mitsume/` のような暗黙パスは見に行かない。どの config が効いているかは呼び出しコンテキストから一意に決まる。

## 全体構造

設定 JSON の top-level はこの形。`checks: [...]` に監視対象を 1 本のリストで並べ、能動 checker と `deadman` を混在させる。

```jsonc
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

`notify` は単一オブジェクト。named map や配列指定、check ごとの notifier 振り分けは持たない。複数 channel に振り分けたいときは設定 JSON ごと分割し、systemd unit を複数立てる。

## top-level フィールド

| フィールド | 必須 | 型 | 説明 |
|---|---|---|---|
| `checks` | YES | array | 監視対象のリスト。要素は `type` フィールドで checker 種別を指定する |
| `notify` | YES | object | Slack 通知先の接続情報 |
| `host` | NO | string | 通知メッセージに載せる host 識別子。省略時は `MITSUME_HOST` env → OS hostname の順で解決 |
| `heartbeat_file` | NO | string | heartbeat file の絶対パス。CLI (`--heartbeat-file`) と `$MITSUME_HEARTBEAT_FILE` が上書きする。両者省略時は `heartbeat_file`、それも省略時は config 隣接の `mitsume.heartbeat.json` を使う |
| `defaults` | NO | object | `checks[]` 各要素に適用する共通デフォルト値 |

### `checks[]` の共通フィールド

各要素は `type` に応じて必須 / 任意フィールドが変わるが、全 checker で共通に持つのはこの 5 つ。

| フィールド | 必須 | 型 | 説明 |
|---|---|---|---|
| `type` | YES | string | `http` / `deadman` / `file` / `container` / `cmd` のいずれか |
| `name` | NO | string | 通知文の表示ラベル。省略時は `type` 別ルールで自動生成 |
| `interval` | NO | duration | 評価周期。`watch` で使う。`check` では無視。省略時は `defaults.interval` を継承 |
| `expect` | NO (checker 別) | object | 成功条件を宣言的に書く。フィールドは `type` ごとに異なる |
| `confirm` | NO | object / `false` | 失敗確信のための短 retry burst 設定 |

`name` は自動生成後も含めて `checks[]` 内で一意でなければならない。重複は起動時 validation でエラーにする。

`type: deadman` は追加で `job` フィールドを必須にする。`job` は `[a-zA-Z0-9_-]{1,64}` に従い、`checks[]` 内で一意である必要がある。

checker 固有のフィールドは [checkers.md](checkers.md) にまとめてある。

### `defaults`

| フィールド | 必須 | 型 | 説明 |
|---|---|---|---|
| `interval` | NO | duration | 全 check の `interval` の初期値 |
| `timeout` | NO | duration | HTTP / cmd checker の request / command timeout の初期値 |

各 check の同名フィールドで上書きする。`defaults.notify` は持たない (`notify` は全 check 共通の単一オブジェクトなので、default 化する意味がない)。

### `confirm` (失敗確信モデル)

失敗の確信は「短い retry burst」で取る。`interval` を N 回まわして判定する方式は採らない。時間スケールが違うから分ける、と考えると分かりやすい。`interval` は 1 評価サイクルの間隔 (推奨 1h 以上)、`confirm.interval` は 1 サイクル内の短 retry の粒度 (デフォルト 30s)。

| フィールド | 必須 | 型 | デフォルト | 説明 |
|---|---|---|---|---|
| `checks` | NO | int (>= 1) | `3` | 連続確認回数 (初回失敗を含む) |
| `interval` | NO | duration | `30s` | 連続確認の間隔 |

省略形と特殊値。

```jsonc
// デフォルト適用: 3 回 + 30s
{ "type": "http", "url": "...", "interval": "5m" }

// 即 alert (one-strike out)
{ "type": "http", "url": "...", "interval": "1m", "confirm": false }

// 確認回数だけ変える (interval は default 30s)
{ "type": "http", "url": "...", "interval": "5m", "confirm": { "checks": 5 } }
```

動きはこう。

1. 通常時は `interval` ごとに評価する
2. 失敗を検知したら `confirm.interval` に切り替えて `confirm.checks - 1` 回まで連続確認する
3. 全部失敗したら alert を発火する
4. 途中で成功したら状態をリセットし、通常 `interval` に戻る

`confirm.checks` に `0` 以下を指定した場合、および `confirm.interval` のパース失敗は起動時 validation でエラーにする。

## duration 表記

duration 文字列は Go の `time.ParseDuration` 互換に、`d` (日) を足したもの。

| 単位 | 例 |
|---|---|
| ミリ秒 | `500ms` |
| 秒 | `30s` |
| 分 | `5m` |
| 時 | `1h`, `24h` |
| 日 | `3d`, `1d1h` (時単位と混在可) |

複合表記 (`1h30m`, `2d12h`) は `time.ParseDuration` の書式に準拠する。`w` (週) や ISO 8601 (`PT30S`, `P1D`) はサポートしない。

duration を受け取るフィールド一覧。

- `checks[].interval`
- `checks[].timeout`
- `checks[].confirm.interval`
- `checks[].expect.within` (`type: deadman`)
- `checks[].expect.mtime_within` (`type: file`)
- `checks[].expect.latency_under` (`type: http`)
- `defaults.interval`, `defaults.timeout`

パース失敗は起動時 validation でエラーにする。

## size 表記

size 文字列は 1024 ベースの human-readable 表記。整数 byte の直書きも受け付ける。

| 表記 | 意味 |
|---|---|
| `100B` | 100 byte |
| `512KB` | 512 × 1024 byte |
| `100MB` | 100 × 1024 × 1024 byte |
| `10GB` | 10 × 1024^3 byte |
| `1TB` | 1 × 1024^4 byte |
| `1` | 1 byte (整数直書き) |

`file` checker の `expect.size_min` / `expect.size_max` で使う。

## 秘密情報 (env 経由のみ)

Slack Incoming Webhook URL のような秘密情報を、CLI 引数の値として直接渡す方式は用意しない。渡し方は次の 3 通りに限る。

1. **JSON の `_env` サフィックス**: フィールド名末尾を `_env` にし、環境変数名を値に書く
   ```jsonc
   { "notify": { "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL" } }
   ```
2. **CLI の `--*-env` フラグ**: フラグ名末尾を `-env` にし、環境変数名を渡す
   ```bash
   mitsume notify --slack-webhook-url-env MITSUME_SLACK_WEBHOOK_URL "hello"
   ```
3. **既知名 env の export**: `MITSUME_*` の既知名を環境変数として export しておくと、CLI 引数と JSON フィールドを両方省略しても拾う
   ```bash
   export MITSUME_SLACK_WEBHOOK_URL=https://hooks.slack.com/...
   mitsume notify "hello"
   ```

`--slack-webhook-url https://...` のような値直接渡しフラグは提供しない。

起動時 validation で、`_env` / `--*-env` に書かれた環境変数が未定義なら fail-fast する。呼び出し時点の env を見るので、後付けの `export` は検出できない。

## host 識別子

通知メッセージに載る `host` フィールドは次の順で解決する。

1. 設定 JSON の `host` フィールド
2. `MITSUME_HOST` 環境変数
3. OS の hostname (`os.Hostname()`)

Docker で運用するとき、Dockerfile に `ENV MITSUME_HOST=api-prod-01` を書けば container ごとに識別子を差し替えられる。

複数 host から同じ check の failure 通知が並ぶ場合の grouping はしない。各 host は独立して通知する。

## 最小サンプル

動く最小の設定 JSON。HTTP checker 1 個と Slack notify で構成される。

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

これを `./mitsume.json` に置いて起動する。

```bash
export MITSUME_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
mitsume watch
```

deadman を足すときは `heartbeat_file` を明示するか、config 隣接の `./mitsume.heartbeat.json` を暗黙に使う。

```json
{
  "notify": { "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL" },
  "checks": [
    { "type": "http", "name": "api-health",
      "url": "https://api.example.com/health",
      "expect": { "status": 200 }, "interval": "1h" },
    { "type": "deadman", "job": "nightly-backup",
      "expect": { "within": "25h" } }
  ]
}
```

## 関連

- [checkers.md](checkers.md) — checker ごとのフィールド定義と評価ロジック
- [notify.md](notify.md) — Slack payload と retry policy
- [heartbeat.md](heartbeat.md) — heartbeat file の schema とパス解決
- [cli.md](cli.md) — サブコマンドと共通フラグ
- [architecture.md](architecture.md) — 3 直交軸と失敗確信モデルの背景
