# 設定 JSON

`check` と `watch` が読む設定ファイルの書き方をまとめる。checker の種類ごとの field は [checkers.md](checkers.md) にある。

## 最小の例

```json
{
  "notify": { "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL" },
  "checks": [
    {
      "type": "http",
      "url": "https://api.example.com/health",
      "interval": "1h",
      "expect": { "status": 200 }
    }
  ]
}
```

これを `mitsume.json` という名前でカレントディレクトリに置き、`MITSUME_SLACK_WEBHOOK_URL` に Webhook URL を入れて `mitsume watch` を起動すると、1 時間ごとに URL を呼び、200 以外が返れば Slack に通知する。

## 設定 JSON の探し方

次の順に探し、最初に見つかった 1 つだけを読む。複数を合わせて読むことはしない。

1. `--config <path>`
2. 環境変数 `MITSUME_CONFIG`
3. カレントディレクトリの `mitsume.json`

1 と 2 で指定した path にファイルが無ければエラーになる。3 は無くてもエラーにならない。`~/.config/` や `/etc/` などは探さないので、どの設定が使われるかは呼び出し方だけで決まる。

`check` と `watch` は設定 JSON が無いと動かない。`notify` / `run` / `ping` は無くても動き、見つかったときは次の値だけを使う。

| subcommand | 使う値 |
|---|---|
| `notify` | `notify.webhook_url_env` |
| `run` | `notify.webhook_url_env`、`host` |
| `ping` | `heartbeat_file`、`<job>` を省略したときの `deadman` の `job` |

このとき中身の検査 (下の「起動時の検査」) はしない。JSON として読めて、トップレベルに知らない field が無ければよい。

## トップレベル

| field | 省略したとき | 説明 |
|---|---|---|
| `notify.webhook_url_env` | `check` / `watch` は起動時にエラー | Slack Incoming Webhook の URL を入れた環境変数の名前 ([秘密情報の渡し方](#秘密情報の渡し方)) |
| `checks` | 何も監視しない | 監視する check の配列 |
| `defaults.interval` | 各 check で指定する | 全 check の `interval` の既定値 |
| `defaults.timeout` | 各 check で指定する | `http` と `cmd` の `timeout` の既定値 |
| `host` | OS の hostname | 通知に載せる host 名 |
| `heartbeat_file` | 設定 JSON の隣のファイル | heartbeat file の path ([決まり方](heartbeat.md#場所の決まり方))。相対 path はカレントディレクトリからの path になるので、絶対 path で書く |

知らない field があるとエラーになる。書き間違いに起動時に気づけるようにするためである。

## check に共通の field

`checks[]` の各要素は `type` で checker の種類を選ぶ。次の field はどの種類でも使える。

| field | 省略したとき | 説明 |
|---|---|---|
| `type` | 省略できない | `http` / `deadman` / `file` / `container` / `cmd` |
| `name` | 種類ごとに決まる (下の表) | 通知に載る名前。`checks[]` の中で重複できない (自動で付いた名前も含む) |
| `interval` | `defaults.interval`。どちらも無ければエラー | `watch` が評価する間隔。`check` は使わない |
| `expect` | 種類ごとに決まる ([checkers.md](checkers.md)) | 成功とする条件。複数の条件を書くと、すべてを満たしたときに成功になる |
| `confirm` | [下の節](#confirm) の既定値 | 失敗したときの再確認 |
| `timeout` | `defaults.timeout`。どちらも無ければ 30s | `http` と `cmd` だけが持つ。1 回の評価にかける時間の上限 |

`name` を省略したときは次の値になる。

| `type` | `name` |
|---|---|
| `http` | `url` |
| `deadman` | `job` |
| `file` | `path` か `path_glob` |
| `container` | `container` |
| `cmd` | `command` を空白でつないだものの先頭 32 文字 |

失敗が続く間は `interval` ごとに通知が届くので、`interval` は通知の頻度でもある。1 時間程度を目安にし、短くしすぎない。

## confirm

1 回の失敗ですぐに通知すると、一時的なネットワークの揺れでも通知が届いてしまう。そこで、失敗したら短い間隔で評価をやり直し、失敗が続いたときだけ通知する。これを `confirm` で設定する。

| field | 省略したとき | 説明 |
|---|---|---|
| `confirm.checks` | `3` | 通知するまでに続けて失敗する回数。最初の失敗を含む。1 以上 |
| `confirm.interval` | `30s` | やり直すまでの間隔 |

`"confirm": false` と書くと、やり直さずに最初の失敗で通知する。

`interval` が 1h で `confirm` が既定値のとき、`watch` は次のように動く。

```text
10:00:00  失敗    やり直しに入る
10:00:30  失敗
10:01:00  失敗    3 回続けて失敗したので通知する。通知には最後の評価の結果を載せる
11:01:00  失敗    評価が終わってから 1h 後に次の評価。また失敗したのでやり直しに入る
```

途中で成功すれば通知しない。

```text
10:00:00  失敗    やり直しに入る
10:00:30  成功    通知しない
11:00:30  成功
```

`interval` ごとの評価で「N 回続けて失敗したら通知する」と数えると、失敗に気づくまで N × `interval` かかる。かといって `interval` を短くすると、失敗が続く間の通知が増える。通知の頻度 (`interval`) と、失敗を確かめる速さ (`confirm.interval`) を別々に持つのはこのためである。

## 値の書き方

### duration

`30s`、`5m`、`1h`、`1d` のように数と単位を並べて書く。`1h30m` や `2d12h` のように組み合わせることも、`1.5h` のように小数で書くこともできる。

| 単位 | 意味 |
|---|---|
| `ns` / `us` (`µs`) / `ms` | ナノ秒 / マイクロ秒 / ミリ秒 |
| `s` / `m` / `h` | 秒 / 分 / 時 |
| `d` | 日 (24h) |

週 (`w`) と ISO 8601 の書き方 (`PT30S`) は使えない。

### size

`file` checker の `expect.size_min` / `expect.size_max` で使う。`B` / `KB` / `MB` / `GB` / `TB` を付けた整数の文字列か、byte 数の整数で書く。単位は 1024 倍ずつで (`100MB` は 100 × 1024 × 1024 byte)、大文字だけを受け付ける。

## 秘密情報の渡し方

Slack の Webhook URL は、知っていれば誰でも投稿できる秘密情報なので、CLI の引数にも設定 JSON にも値を書かない ([architecture.md](architecture.md#セキュリティ上の制約))。値は環境変数に入れ、mitsume にはその環境変数の名前を渡す。

どの環境変数を読むかは次の順で決まる。

1. `--slack-webhook-url-env <name>` (`notify` と `run` だけ)
2. 設定 JSON の `notify.webhook_url_env` (`check` と `watch` では必須)
3. `MITSUME_SLACK_WEBHOOK_URL`

```bash
# 3 の既定の名前を使う
export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/...'
mitsume notify "hello"

# 別の名前の環境変数を使う
export SLACK_OPS_WEBHOOK='https://hooks.slack.com/services/...'
mitsume notify --slack-webhook-url-env SLACK_OPS_WEBHOOK "hello"
```

環境変数が無いか空なら、何も送らずに exit 1 になる。`--dry-run` でも同じなので、試すときは `dummy` などを入れておく。環境変数は起動したときに読むので、起動したあとに値を変えても反映されない。

## 起動時の検査

`check` と `watch` は評価を始める前に設定 JSON 全体を検査し、1 つでも問題があれば何も評価せずに exit 1 する。エラーメッセージには `checks[2]: ...` のように問題の場所が出る。検査するのは次のようなことである。

- JSON として読めるか、知らない field が無いか
- 必須の field があるか、duration や size が読めるか、値が範囲の中にあるか
- `name` と `job` が `checks[]` の中で重複していないか
- `notify.webhook_url_env` の環境変数があるか
- `container` checker の socket が見つかるか
