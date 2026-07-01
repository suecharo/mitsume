# notify

mitsume の通知先は Slack Incoming Webhook 1 本に固定してある。「どうやって Slack につなぐか」「payload はどう組み立てるか」「いつ Slack に飛ぶか」を知りたいときに読む。関連は [configuration.md](configuration.md) (設定 JSON 全体の schema) と [architecture.md](architecture.md) (なぜ Slack 1 本に絞ったか)。

複数 channel への振り分けや recovery / リマインドは持たない。過剰通知の抑制は `interval` の値 1 個で行う。

## セットアップ手順

1. Slack ワークスペースで App を作成し、Incoming Webhook を有効化して webhook URL を取得する。送信先 channel は webhook URL 作成時に固定される。
2. mitsume を動かすホストで、webhook URL を env に export する。default 名を使うなら:

    ```bash
    export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T000/B000/xxxx'
    ```

3. 設定 JSON の `notify` に、URL を保持する env 変数名を書く:

    ```json
    {
      "notify": {
        "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"
      }
    }
    ```

4. 設定 JSON を使わないサブコマンド (`notify` / `run` 単体、`ping` は通知を出さない) では、default 名で export しておけば自動で拾う。任意の env 名にするなら CLI で名前を渡す:

    ```bash
    mitsume notify --slack-webhook-url-env SLACK_WEBHOOK "hello"
    ```

env は起動時に読む。後付け export は反映しない。設定 JSON の validation で `webhook_url_env` が指す env が未定義なら fail-fast で exit 1 する ([configuration.md](configuration.md))。

## 設定 schema

`notify` は config 直下の単一オブジェクトとして 1 つだけ書く。

| フィールド | 必須 | 型 | 意味 |
|---|---|---|---|
| `webhook_url_env` | YES | string | Slack Incoming Webhook URL を保持する env 変数名 |
| `username` | no | string | Slack post の表示名を上書き |
| `icon_emoji` | no | string | Slack post のアイコン絵文字 (例: `":robot_face:"`) |
| `icon_url` | no | string | Slack post のアイコン画像 URL (`icon_emoji` と併用時は `icon_emoji` が勝つ) |

最小例:

```json
{
  "notify": {
    "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"
  }
}
```

表示カスタマイズ込みの例:

```json
{
  "notify": {
    "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL",
    "username": "mitsume@api-prod-01",
    "icon_emoji": ":rotating_light:"
  }
}
```

`notify` に持たないフィールド:

- `channel`: Slack Incoming Webhook では URL 作成時に channel が固定され、payload の `channel` は無視される。書いても効かないので schema から外している。channel を分けたいときは webhook を別々に作って config も分け、systemd unit を複数立てる。
- `type`: Slack 固定なので type discriminator は持たない。

`notify` に持たない構造:

- `notifiers` named map: 通知先が単一なので参照解決を必要としない。
- `checks[].notify` 配列: check ごとの通知先切り替えはできない。
- 並列 fan-out: 送信先が単一なので並列送信の設定 surface を持たない。

## payload 形式

Slack Incoming Webhook に POST する JSON は固定形式。ユーザーカスタマイズは受け付けない。

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

payload の組み立て規則:

- `text` はメイン本文。1 行目に `[mitsume] <name> failed (<type>: <error>)`、続いて `host: <host>` / `time: <timestamp>` を改行区切りで並べる。Slack の `mrkdwn` として扱う。
- `attachments[0].color` は失敗系で `"danger"` 固定。severity 概念を持たないので他の色は使わない。
- `attachments[0].fields` に `host` / `check` / `type` / `time` / `observed` / `expected` を並べる。`observed` / `expected` は type ごとに埋める:
    - `http`: `status=503` / `status=200`
    - `deadman`: `last_ping=25h12m ago` / `within=25h`
    - `file`: `mtime=26h ago, size=80MB` / `mtime_within=25h, size_min=100MB`
    - `container`: `state=exited` / `running=true`
    - `cmd`: `exit=1` / `exit=0`
- `run` および `cmd` checker の失敗では、`text` 末尾に stderr 末尾を改行で追加する。切り詰め幅は `run` が [cli.md](cli.md) の `--stderr-tail-lines` / `--stderr-tail-bytes` (default 20 行 / 2KB) で可変、`cmd` checker は 20 行 / 2KB 固定。Slack の 4000 文字制限に収まる範囲で truncate する。
- host 識別子は「config `host` フィールド > `MITSUME_HOST` env > OS hostname」の順で決まる ([configuration.md](configuration.md#host-識別子))。
- `time` は実行環境の TZ (`time.Local`) で ISO 8601 化する。TZ 上書き用の設定フィールドは持たない。

payload に載せない項目:

- severity / arbitrary tags / mitsume version
- `consecutive_failures` 相当の内部カウンタ
- Block Kit の block 構造 (attachments 固定)

## 発火モデル

通知は「失敗のたびに 1 通」を守る。次の 5 点は設定で変えられない。

- failure 確定のたびに 1 通送る。前回状態を保持しないので debounce しない。次サイクルで再度 failure なら再度送る。
- ok に戻ったときの recovery 通知は出さない。復旧は「次の失敗通知が来ない」ことで判る。
- `always` / `backoff` のような再送タイマーは持たない。「失敗のたび通知」で `always` 相当が自然に成立する。
- 過剰通知の抑制は `interval` の値で行う (1h 以上推奨、最短 1h に 1 通のペースに収まる)。
- `watch` プロセスを再起動しても挙動は変わらない。前回状態を持たないため、直前の通知有無に影響されない。

`interval` と `confirm.interval` は時間スケールが違う。混同しない。

| パラメータ | 時間スケール | 役割 |
|---|---|---|
| `interval` | 1h 〜 数日 | 通常の評価サイクル間隔。過剰通知抑制の主たる調整点 |
| `confirm.interval` | 秒 〜 分 (default 30s) | 失敗検知後の short retry burst の粒度 |

`confirm` の詳細は [configuration.md](configuration.md#confirm-失敗確信モデル) を参照。

### 通知失敗時の retry

Slack への HTTP POST が失敗したときの retry は固定値。

- 3 回 retry、exponential backoff (1s → 2s → 4s、合計 7 秒の猶予)
- HTTP 4xx は retry せず即 fail (auth error / webhook URL typo は retry しても直らない)
- HTTP 5xx は retry する (Slack 側の一時障害)
- 全 retry 失敗時は stderr にログを出す。check 評価結果は変えない (通知が届かなくても check の成功 / 失敗は確定する)。heartbeat file にも書かない (heartbeat file は `ping` の到来時刻専用)。

## 通知の起点 (明示 / 検知)

Slack に通知が飛ぶ起点は 2 系統だけ。

### 明示通知

ユーザーが明示イベントとして呼び出す通知。

- `mitsume notify <msg>`: 引数の文字列をそのまま `text` に載せて即時送信する。job 概念とは無関係。
- `mitsume run` 内部: 子プロセスが exit したときに内部で `notify` 相当を呼ぶ。失敗時 (exit code 非 0) は失敗 payload、成功時は成功 payload を送る。`--quiet-on-success` で成功通知を抑止できる。

### 検知通知

mitsume 側の能動評価で failure が確定したときの通知。

- `mitsume check` / `mitsume watch`: 評価ループで `confirm.checks × confirm.interval` の burst を通り抜けて failure が確定したときに 1 通送る。次サイクルで再度 failure なら再度 1 通送る。

### 通知を出さないサブコマンド

- `mitsume ping [<job>]`: heartbeat file の `last_ping_at` を更新するだけで通知は出さない。dead-man's switch の反転監視は `check` / `watch` 側が heartbeat file を read して評価する。

各サブコマンドの引数と exit code は [cli.md](cli.md) を参照。

## 秘密情報の扱い

Slack webhook URL は秘密情報として扱う。CLI 引数に値を直接渡す方式はサポートしない。

- 禁止: `mitsume notify --slack-webhook-url https://hooks.slack.com/... "msg"`
- 許可: `MITSUME_SLACK_WEBHOOK_URL=https://... mitsume notify "msg"`
- 許可: `mitsume notify --slack-webhook-url-env SLACK_WEBHOOK "msg"` (env 変数の **名前** を渡す)

値渡しを禁じるのは、`ps aux` / `/proc/<pid>/cmdline` から同一ホストの他ユーザーに漏れるため。値は env に置き、CLI からは env 変数名を経由して参照する。

env 変数名の指定方法は 3 通り、どれもサポートする。

| 方法 | 例 | 用途 |
|---|---|---|
| default 名を export | `export MITSUME_SLACK_WEBHOOK_URL=...` | CLI / config 双方で自動的に拾う |
| CLI で env 名を指定 | `--slack-webhook-url-env SLACK_WEBHOOK` | default 名以外を使いたいとき |
| config で env 名を指定 | `"webhook_url_env": "SLACK_WEBHOOK"` | 設定 JSON 経由での指定 |

起動時に env が定義されていなければ fail-fast で exit 1 する (後付け export は検出しない)。webhook URL の値を通知 payload / ログ / heartbeat file に出力しないのも同じ理由。

## `--dry-run` 時の挙動

`--dry-run` は全サブコマンドで受け付ける共通フラグ。通知の試運転や payload の事前確認に使う。

- Slack への HTTP POST を行わない
- 送るはずだった payload を stderr に JSON で print する (`text` / `attachments` を含む同じ形式)
- heartbeat file を書き換えない (`ping` の `last_ping_at` 更新も抑止)
- 設定 JSON の validation は通常どおり実行する (schema エラーは exit 1)
- `mitsume run --dry-run` では子プロセスは実際に実行する。通知と heartbeat file への副作用だけ止める

heartbeat file との切り分けは [heartbeat.md](heartbeat.md) を参照。

典型用途:

```bash
# 設定と通知文の事前確認
mitsume check --config ./mitsume.json --dry-run

# 通知文だけ確認して Slack には送らない
mitsume notify --dry-run "test message"

# heartbeat file に副作用を出さず ping payload を確認
mitsume ping nightly-backup --dry-run
```

## 関連

- [getting-started.md](getting-started.md) — Slack Webhook 発行から通し tutorial
- [recipes.md](recipes.md) — 運用パターン別の設定例
- [cli.md](cli.md) — サブコマンドの引数と exit code
- [configuration.md](configuration.md) — 設定 JSON 全体の schema
- [checkers.md](checkers.md) — checker が返す failure 情報の source
- [architecture.md](architecture.md) — なぜ Slack 1 本 + debounce なしか
