# Notifications

mitsume の通知先は Slack Incoming Webhook 1 本に固定である。本 doc は payload 形式、通知を送信する trigger、delivery retry、秘密情報の受け渡し、`notify` field の設定 schema を定義する。「なぜ Slack 1 本に絞ったか」「なぜ debounce や recovery を持たないか」の背景は [architecture.md § Design decisions](architecture.md#design-decisions) を参照する。

用語の使い分け:

- `notifier` — 内部の通知送信 component
- `mitsume notify` — 単発通知 subcommand (以降 subcommand は必ず backtick で表記する)
- `notify` field — 設定 JSON top-level の Slack 接続情報 object (subcommand と field は同じ名前だが文脈で区別する)

セットアップ手順 (Webhook 発行から動作確認まで) は [getting-started.md](getting-started.md) を参照する。

## Payload

Slack Incoming Webhook に POST する JSON は固定形式である。ユーザーカスタマイズは受け付けない。

```json
{
  "username": "mitsume@api-prod-01",
  "icon_emoji": ":rotating_light:",
  "text": "[mitsume] api-health failed (http: status=503, want=200)\nhost: api-prod-01\ntime: 2026-06-30T14:23:15+09:00",
  "attachments": [
    {
      "color": "danger",
      "fields": [
        { "title": "host", "value": "api-prod-01", "short": true },
        { "title": "check", "value": "api-health", "short": true },
        { "title": "type", "value": "http", "short": true },
        { "title": "time", "value": "2026-06-30T14:23:15+09:00", "short": true },
        { "title": "observed", "value": "status=503", "short": false },
        { "title": "expected", "value": "status=200", "short": false }
      ]
    }
  ]
}
```

payload の組み立て規則は次の通りである。

- `text` は本文である。1 行目に `[mitsume] <name> failed (<type>: <error>)`、続いて `host: <host>` と `time: <timestamp>` を改行区切りで並べる。Slack の `mrkdwn` として扱われる。
  - `mitsume run` の失敗では、`text` 末尾に子プロセスの stderr 末尾を改行区切りで追加する。切り詰め幅は `--stderr-tail-lines` / `--stderr-tail-bytes` (default 20 行 / 2KB) で変更できる。
  - `cmd` checker の失敗でも同様に stderr 末尾を追加する。切り詰め幅は 20 行 / 2KB 固定であり、設定 JSON からの上書きは受け付けない (`mitsume run --stderr-tail-*` は cmd checker には効かない)。
- `attachments[0].color` は失敗系で `"danger"` 固定である。severity 概念を持たないため、他の色は使わない。
- `attachments[0].fields` に `host` / `check` / `type` / `time` / `observed` / `expected` を並べる。`observed` と `expected` は type ごとに次の形式で埋める。

| Type | `observed` の例 | `expected` の例 |
|---|---|---|
| `http` | `status=503` | `status=200` |
| `deadman` | `last_ping=25h12m ago` | `within=25h` |
| `file` | `mtime=26h ago, size=80MB` | `mtime_within=25h, size_min=100MB` |
| `container` | `state=exited` | `running=true` |
| `cmd` | `exit=1` | `exit=0` |

- `text` / `observed` / `expected` に載せる duration は、Go 標準表記から末尾のゼロ単位を省いた形式とする (`26h0m0s` ではなく `26h`、`25h12m0s` ではなく `25h12m`)。経過時間の観測値 (`last_ping` / `mtime`) は秒精度に、`latency` はミリ秒精度に切り詰めて sub-second の生値を載せない。
- `host` の解決順は [configuration.md § Host identifier](configuration.md#host-identifier) を参照する。
- `time` は binary を実行するプロセスの local timezone で ISO 8601 化する。timezone を上書きする設定 field は持たない。
- [confirm burst](architecture.md#用語) を通過して failure が確定した場合、`observed` / `expected` と `text` の `<type>: <error>` 括弧内には burst 最終確認 (最も新しい観測) の値を載せる。burst 途中で観測値が変わった場合でも、Slack に送信する時点で最新の観測を反映する。
- Slack の 4000 文字制限を超える場合は `text` を末尾から truncate する。stderr tail が付与されているときは、stderr tail 側を先に `--stderr-tail-lines` / `--stderr-tail-bytes` で刈った上で、なお超える場合はさらに末尾から詰める。`observed` / `expected` / `host` / `time` の各要素は本 spec では上限を超えない前提とする。

### Announcement payload

`mitsume notify <msg>` は user が明示的に送る payload であり、上記の failure payload とは形式が異なる。

- `text` は `<msg>` の内容をそのまま載せる (prefix、`host`、`time` の追記は行わない)。
- `attachments` は付けない (severity や `observed` / `expected` の概念がないため)。
- `username` / `icon_emoji` / `icon_url` は設定 JSON の `notify` field の値を適用する。

### Shutdown announcement payload

`mitsume watch` が `SIGINT` / `SIGTERM` を受けて graceful shutdown する時、および予期せぬ fatal error による異常終了時には、best-effort で 1 通の shutdown announcement を送信する。

- `text` の 1 行目は `[mitsume] watch stopped on host=<host> (signal=<name>, time=<RFC3339>)`。fatal error 時は `signal=` の代わりに `error=<summary>` を載せる。
- `attachments` は付けない。
- `username` / `icon_emoji` / `icon_url` は設定 JSON の `notify` field の値を適用する。

### Success payload

`mitsume run` の子プロセスが exit code `0` で正常終了した場合、上記の failure payload の代わりに次の差分を持つ success payload を送信する (`--quiet-on-success` を指定した場合は送信を抑止する)。他 subcommand および checker の failure 確定では success payload を送らない。

- `text` の 1 行目は `[mitsume] <name> succeeded (run: exit=0)`。
- `attachments[0].color` は `"good"` に切り替える。
- `attachments[0].fields` の `observed` は `exit=0`、`expected` は `exit=0` を載せる。
- stderr tail は含めない。

### Common exclusions

payload に含めない項目 (failure / success 共通):

- severity、arbitrary tags、mitsume の version
- `consecutive_failures` 相当の内部カウンタ
- Block Kit の block 構造 (`attachments` 形式で固定する)

## 通知トリガー

通知は failure 確定のたびに 1 通を送信し、debounce、recovery 通知、リマインドはいずれも持たない。以下の 4 点は設定で変更できない。設計上の理由は [architecture.md § Design decisions](architecture.md#design-decisions) を参照する。

- 前回状態を保持しないため、次サイクルで再度 failure と判定した場合は再度 1 通を送信する。一定間隔での自動リマインドや exponential backoff での再送タイマーの類は持たない (Alertmanager の `repeat_interval` に相当する field も存在しない)。「failure のたびに通知」で常時通知相当が自然に成立する。
- ok に戻った場合の recovery 通知は送信しない。復旧は「次の failure 通知が来ない」ことで判断する。
- 過剰通知の抑制は `interval` の値で行う (1h 以上を推奨する。最短でも 1h に 1 通のペースに収まる)。
- `watch` プロセスを再起動しても挙動は変わらない。前回状態を持たないため、直前の通知有無に影響されない。

Slack に通知が送信される trigger は次の 2 系統のみである。

### Explicit trigger

ユーザーが明示イベントとして呼び出す通知である。

- `mitsume notify <msg>` — 引数の文字列をそのまま `text` に載せて即時送信する。checker 評価や heartbeat file 更新は伴わない。
- `mitsume run` — 子プロセスが exit した時点で notifier を内部で呼び出す。失敗時 (exit code が 0 以外) は failure payload、成功時は success payload を送信する。`--quiet-on-success` で成功通知を抑止できる。

### Active detection trigger

mitsume 側の能動評価で failure が確定した場合の通知である。

- `mitsume check` / `mitsume watch` — 評価サイクルで confirm burst を通過して failure が確定した場合に 1 通を送信する。次サイクルで再度 failure と判定した場合は再度 1 通を送信する。

### 通知を送信しない subcommand

- `mitsume ping [<job>]` — heartbeat file の `last_ping_at` を更新するのみで、通知は送信しない。dead-man's switch の判定は `check` / `watch` 側が heartbeat file を読んで評価する ([heartbeat.md](heartbeat.md) を参照)。

各 subcommand の引数と exit code は [cli.md](cli.md) を参照する。

## Retry

Slack への HTTP POST が失敗した場合の retry は固定値である。

- 3 回まで retry する。exponential backoff で `1s` → `2s` → `4s` の間隔を挟む (合計 7s の猶予)。
- HTTP 4xx は retry しない。auth error や webhook URL の typo は retry しても解消しないためである。
- HTTP 5xx は retry する (Slack 側の一時障害)。
- 全 retry が失敗した場合は stderr に log を出力する。checker の評価結果は変更しない (通知が届かなくても check の成功 / 失敗は確定する)。heartbeat file にも書き込まない (heartbeat file は `ping` の到来時刻専用である)。

## 秘密情報

Slack Webhook URL は秘密情報として扱う。CLI 引数に値を直接渡す方式は提供しない。理由は [architecture.md § Security invariants](architecture.md#security-invariants) を参照する。

- 禁止: `mitsume notify --slack-webhook-url https://hooks.slack.com/... "msg"`
- 許可: `MITSUME_SLACK_WEBHOOK_URL=https://... mitsume notify "msg"`
- 許可: `mitsume notify --slack-webhook-url-env SLACK_WEBHOOK "msg"` (env 変数の**名前**を渡す形式)

env 変数名の指定方法は 3 通りである。

| 方法 | 例 | 用途 |
|---|---|---|
| default 名を export | `export MITSUME_SLACK_WEBHOOK_URL=...` | CLI と設定 JSON の両方で自動的に取得する |
| CLI で env 名を指定 | `--slack-webhook-url-env SLACK_WEBHOOK` | default 名以外の env 変数を使う場合 |
| 設定 JSON で env 名を指定 | `"webhook_url_env": "SLACK_WEBHOOK"` | 設定 JSON 経由で指定する場合 |

`_env` / `--*-env` で指定した env 変数が起動時点で未定義の場合は fail-fast する (後付けの `export` は検出しない)。webhook URL の値を通知 payload、log、heartbeat file に出力しない。

## 設定 schema

`notify` は設定 JSON 直下の単一 object である。

| Field | 必須 | 型 | 意味 |
|---|---|---|---|
| `webhook_url_env` | Conditional | string | Slack Incoming Webhook URL を保持する env 変数名。省略した場合は既定名 `MITSUME_SLACK_WEBHOOK_URL` を参照する (default 名の env を export していない運用では必須) |
| `username` | No | string | Slack post の表示名を上書きする |
| `icon_emoji` | No | string | Slack post のアイコン絵文字 (例: `":robot_face:"`) |
| `icon_url` | No | string | Slack post のアイコン画像 URL。`icon_emoji` と併用した場合は `icon_emoji` が優先する |

最小例:

```json
{
  "notify": {
    "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"
  }
}
```

表示カスタマイズを含む例:

```json
{
  "notify": {
    "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL",
    "username": "mitsume@api-prod-01",
    "icon_emoji": ":rotating_light:"
  }
}
```

`notify` が持たない field:

- `channel` — Slack Incoming Webhook では URL 作成時に送信先 channel が固定され、payload の `channel` field は無視される。指定しても効果がないため schema から除外する。channel を分ける場合は Webhook を別々に作成して config を分割し、systemd unit を複数立てる。
- `type` — 通知先が Slack で固定であるため、type discriminator を持たない。

`notify` が持たない構造:

- `notifiers` named map — 通知先が単一であるため参照解決を必要としない。
- `checks[].notify` 配列 — check ごとの通知先切り替えは行わない。
- 並列 fan-out — 送信先が単一であるため並列送信の設定 surface を持たない。

## Dry-run

`--dry-run` は `mitsume ping` / `mitsume notify` / `mitsume check` / `mitsume watch` / `mitsume run` で共通に利用できる flag である (`mitsume version` は副作用がないため対象外)。通知の試運転や payload の事前確認に使用する。共通 flag 全体の挙動は [cli.md § 共通 flag](cli.md#共通-flag) を参照する。

- Slack への HTTP POST を行わない。
- 送信するはずだった payload を stderr に JSON 形式で出力する (`text` / `attachments` を含む同じ形式)。
- heartbeat file を書き換えない (`ping` の `last_ping_at` 更新も抑止する)。
- 設定 JSON の validation は通常どおり実行する (schema エラーは exit 1)。
- `mitsume run --dry-run` は子プロセスを実際に起動する。通知と heartbeat file への副作用のみを止める。

heartbeat file との切り分けは [heartbeat.md](heartbeat.md) を参照する。

典型的な用途:

```bash
# 設定と通知文の事前確認
mitsume check --config ./mitsume.json --dry-run

# 通知文だけ確認して Slack には送らない
mitsume notify --dry-run "test message"

# heartbeat file に副作用を出さず ping payload を確認
mitsume ping nightly-backup --dry-run
```

## 関連

- [getting-started.md](getting-started.md) — Slack Webhook 発行から動作確認までの通し tutorial
- [checkers.md](checkers.md) — checker が返す failure 情報の source
- [heartbeat.md](heartbeat.md) — heartbeat file の schema と `ping` との切り分け
- [configuration.md](configuration.md) — 設定 JSON 全体の schema と探索順
- [cli.md](cli.md) — subcommand の引数と exit code
- [recipes.md](recipes.md) — 運用パターン別の設定例
- [architecture.md](architecture.md) — Slack 単一 Webhook 固定、debounce と recovery なしの設計背景
