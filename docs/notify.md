# 通知

mitsume がいつ、どんな通知を Slack に送るかをまとめる。通知先は Slack Incoming Webhook 1 つで、Webhook URL の渡し方は [configuration.md](configuration.md#秘密情報の渡し方) にある。

## 通知の種類

| 種類 | 送る subcommand | 送るとき | `text` の 1 行目 |
|---|---|---|---|
| failure | `check` / `watch` | 評価に失敗し、`confirm` の再確認でも失敗が続いたとき | `[mitsume] <name> failed (<type>: <理由>)` |
| `run` の失敗 | `run` | 子プロセスが 0 以外で終わった、timeout で止めた、起動できなかったとき | `[mitsume] <name> failed (run: <理由>)` |
| `run` の成功 | `run` | 子プロセスが 0 で終わったとき。`--quiet-on-success` なら送らない | `[mitsume] <name> succeeded (run: exit=0)` |
| メッセージ | `notify` | 呼んだとき | 引数の文字列そのまま |
| 停止 | `watch` | SIGINT / SIGTERM を受けて止まるとき | `[mitsume] watch stopped on host=<host> (signal=SIGTERM, time=<時刻>)` |
| panic | `check` / `watch` | 評価の途中で panic したとき。送ったあと exit 2 で終わる | `[mitsume] <subcommand> panicked on host=<host> (panic=<値>, time=<時刻>)` |

`ping` と `version` は通知しない。

failure は、同じ check が失敗し続ける間、評価のたびに (`interval` ごとに) 届く。次の通知は送らない。理由は [architecture.md](architecture.md#状態を持たない設計) にある。

- 復旧の通知。復旧したことは、次の failure が届かないことで分かる
- 決まった時間ごとのリマインド。失敗が続けば failure が `interval` ごとに届く
- 同じ原因の失敗をまとめた通知。ネットワークが落ちて 10 個の check が失敗すれば、10 通届く

投稿者の名前とアイコンは Slack App の設定で決まり、mitsume からは変えられない。Slack App の Webhook は payload での上書きを受け付けないためである。

## payload

failure の payload は次のようになる。`--dry-run` を付けると、この JSON が stderr に出る。

```json
{
  "text": "[mitsume] api-health failed (http: status=503, want=200)\nhost: api-prod-01\ntime: 2026-06-30T14:23:15+09:00",
  "attachments": [
    {
      "color": "danger",
      "fields": [
        { "title": "host", "value": "api-prod-01", "short": true },
        { "title": "check", "value": "api-health", "short": true },
        { "title": "type", "value": "http", "short": true },
        { "title": "time", "value": "2026-06-30T14:23:15+09:00", "short": true },
        { "title": "observed", "value": "status=503" },
        { "title": "expected", "value": "status=200" }
      ]
    }
  ]
}
```

Slack では `text` が本文として表示され、その下に `attachments` が赤い帯付きの表として付く。`run` の成功では帯が緑 (`good`) になり、`observed` と `expected` はどちらも `exit=0` になる。メッセージ・停止・panic の通知は `text` だけで、`attachments` を付けない。

`observed` と `expected` の例を示す。

| `type` | `observed` | `expected` |
|---|---|---|
| `http` | `status=503` | `status=200` |
| `deadman` | `last_ping=25h12m ago` | `within=25h` |
| `file` | `exists=true, mtime=26h ago, size=80MB` | `mtime_within=25h, size_min=100MB` |
| `container` | `state=exited` | `running=true` |
| `cmd` / `run` | `exit=1` | `exit=0` |

- 値は最後の評価のものである。`confirm` の再確認の途中で値が変わっても、通知には最後に見た値を載せる
- 時間は `26h`、`25h12m` のように 0 の単位を省いて書く。経過時間は秒まで、`latency` はミリ秒までにする
- 時刻は mitsume を動かす process の timezone の RFC 3339 で書く。container の中では UTC になることが多い
- `cmd` と `run` の失敗では、`text` の末尾に子プロセスの stderr の末尾を付ける。長さは、`cmd` は 20 行か 2KB の短い方、`run` は `--stderr-tail-lines` と `--stderr-tail-bytes` で決まる

## 送信と retry

- 送信に失敗したら、1 秒、2 秒、4 秒と待って最大 3 回やり直す。1 回の送信は 30 秒で打ち切る
- 4xx が返ったときはやり直さない。Webhook URL の誤りや、Webhook が無効になったことが原因で、やり直しても直らないためである
- すべて失敗したら stderr にエラーを出す。`notify` は exit 1 になる。`check` / `watch` / `run` は、通知に失敗しても評価の結果や exit code を変えない

## 届かない通知

監視する道具が、自分が止まったことを必ず知らせることはできない。mitsume は停止と panic の通知を best-effort で送るが、次のときは何も届かない。

- SIGKILL で止められたとき (OOM killer、systemd の stop が `TimeoutStopSec` を過ぎたときを含む)
- host ごと落ちたとき、ネットワークが切れたとき
- 通知の送信がすべて失敗したとき

通知が来ないことは、異常が無いことを意味しない。systemd で動かすなら `OnFailure=` で unit の失敗を別に通知する ([recipes.md](recipes.md#systemd-で常駐監視する))。host ごと落ちることにも備えるなら、別の host の mitsume からこの host を監視する。
