# CLI

mitsume の subcommand ごとの引数、環境変数、exit code をまとめる。flag の一覧は `mitsume <subcommand> -h` でも出る。

## subcommand

| subcommand | すること | 設定 JSON |
|---|---|---|
| `mitsume ping [<job>]` | heartbeat file に job の時刻を書く | 無くてよい |
| `mitsume notify <msg>` | Slack に 1 通送る | 無くてよい |
| `mitsume run -- <cmd>` | コマンドを実行し、終わったら結果を通知する | 無くてよい |
| `mitsume check` | 全 check を 1 回評価して終わる。cron から呼ぶ | 要る |
| `mitsume watch` | 常駐して、check ごとの `interval` で評価し続ける。systemd などで動かす | 要る |
| `mitsume version` | version を表示する | 読まない |

`mitsume`、`mitsume help`、`mitsume -h` で使い方が出る。

## 共通の flag と環境変数

| flag | 環境変数 | 使える subcommand | 説明 |
|---|---|---|---|
| `--config <path>` | `MITSUME_CONFIG` | `ping` / `notify` / `run` / `check` / `watch` | 設定 JSON の path ([探し方](configuration.md#設定-json-の探し方)) |
| `--heartbeat-file <path>` | `MITSUME_HEARTBEAT_FILE` | `ping` / `check` / `watch` | heartbeat file の path ([決まり方](heartbeat.md#場所の決まり方)) |
| `--slack-webhook-url-env <name>` | | `notify` / `run` | Webhook URL を入れた環境変数の名前 ([渡し方](configuration.md#秘密情報の渡し方)) |
| | `MITSUME_SLACK_WEBHOOK_URL` | `notify` / `run` | Webhook URL を入れる既定の環境変数 |
| `--dry-run` | | `ping` / `notify` / `run` / `check` / `watch` | 下の節 |

`ping` と `notify` は flag を位置引数の後ろにも書ける (`mitsume ping nightly-backup --dry-run`)。`run` の flag は `--` より前に書く。

### --dry-run

Slack に送らず、送るはずだった payload を JSON で stderr に出す。heartbeat file も書き換えない。設定の検査や評価はふだんどおり行うので、設定と通知の中身を本番の前に確かめられる。

| subcommand | `--dry-run` のとき |
|---|---|
| `ping` | heartbeat file を読み、書くはずだった内容を stderr に出す。ファイルは変えない |
| `notify` | payload を stderr に出す |
| `run` | 子プロセスはふだんどおり実行し、通知だけを stderr に出す |
| `check` / `watch` | 評価はふだんどおり行い (HTTP の request も送る)、通知だけを stderr に出す。停止と panic の通知も同じ |

Webhook URL の環境変数は `--dry-run` でも要る。値は使わないので `dummy` でよい。

## mitsume ping

```bash
mitsume ping nightly-backup
```

heartbeat file の、job の `last_ping_at` を今の時刻にする。通知はしない ([heartbeat.md](heartbeat.md))。

`<job>` を省略すると、環境変数 `MITSUME_JOB`、設定 JSON にある唯一の `deadman` の `job` の順で決める。`deadman` が 2 つ以上あるときは決めない。job の名前に使える文字は [checkers.md](checkers.md#deadman) の `job` と同じである。

| exit code | 意味 |
|---|---|
| 0 | 書き込めた |
| 1 | job が決まらない、heartbeat file の path が決まらない、読み書きに失敗した、設定 JSON が読めない |

## mitsume notify

```bash
my-batch || mitsume notify "my-batch failed on $(hostname)"
```

`<msg>` をそのまま Slack に送る。host や時刻は足さないので、要るなら `<msg>` に含める。

| exit code | 意味 |
|---|---|
| 0 | 送れた |
| 1 | `<msg>` が無い、Webhook URL の環境変数が無い、送信に失敗した (やり直しを含めて)、設定 JSON が読めない |

## mitsume run

```bash
mitsume run --name nightly-backup --timeout 2h -- /usr/local/bin/nightly-backup.sh
```

`--` の後ろのコマンドを子プロセスとして実行し、終わったら結果を通知する。子の stdout / stderr はそのまま mitsume の stdout / stderr に出る。

| flag | 既定 | 説明 |
|---|---|---|
| `--name <name>` | コマンドの basename | 通知に載る名前 |
| `--timeout <duration>` | 無し | これを過ぎたら子に SIGTERM を送る |
| `--grace-period <duration>` | `5s` | SIGTERM を送ってから SIGKILL を送るまで待つ時間 |
| `--quiet-on-success` | off | 成功の通知を送らない |
| `--stderr-tail-lines <n>` | `20` | 失敗の通知に付ける stderr の末尾の行数 |
| `--stderr-tail-bytes <n>` | `2048` | 失敗の通知に付ける stderr の末尾の byte 数。行数と比べて短い方を使う |
| `--stderr-buffer-bytes <n>` | `16384` | stderr の末尾を覚えておく buffer の byte 数 |

- mitsume が受けた SIGINT / SIGTERM / SIGHUP / SIGQUIT は子に転送する
- Webhook URL の環境変数が無いときは、子を起動せずに exit 1 で終わる
- heartbeat file には触らない。dead-man's switch と組み合わせるときは `mitsume run -- <cmd> && mitsume ping <job>` とつなぐ

exit code は子の exit code をそのまま返す。次の値は mitsume が決める。

| exit code | 意味 |
|---|---|
| 124 | `--timeout` を過ぎたので止めた |
| 126 | コマンドを実行する権限が無い |
| 127 | コマンドが見つからない |
| 128 + n | 子が signal n で終わった (SIGTERM なら 143) |
| 1 | mitsume 側の問題 (`<cmd>` が無い、Webhook URL の環境変数が無い、設定 JSON が読めない)。子が 1 で終わったときと区別はつかない |

## mitsume check

```bash
mitsume check --config /etc/mitsume/mitsume.json
```

全 check を並行して 1 回ずつ評価し、通知を送り終えたら終わる。`interval` は使わず、cron から呼ぶ間隔がそのまま評価の間隔になる。失敗した check があると、`confirm` の再確認の分だけ時間がかかる (既定なら 1 分ほど)。

| exit code | 意味 |
|---|---|
| 0 | 評価が終わった。失敗した check があっても 0 |
| 1 | 設定 JSON が見つからない、起動時の検査に失敗した、heartbeat file が読めない |
| 2 | 評価の途中で panic した |

check の失敗を exit code に出さないのは、失敗は Slack で知らせるので、cron の失敗通知 (`MAILTO` など) と二重にしないためである。

## mitsume watch

```bash
mitsume watch --config /etc/mitsume/mitsume.json
```

常駐して評価し続ける。systemd で動かす例は [recipes.md](recipes.md#systemd-で常駐監視する) にある。

- 起動すると設定 JSON を検査し、全 check をすぐに 1 回評価する。そのあとは check ごとに、評価が終わってから `interval` だけ待って次を評価する
- SIGINT / SIGTERM を受けると、途中の評価を打ち切り (結果は捨てる)、停止の通知を送って終わる
- 実行中に設定 JSON を読み直さない。設定を変えたら再起動する

| exit code | 意味 |
|---|---|
| 0 | SIGINT / SIGTERM で止まった |
| 1 | 設定 JSON が見つからない、起動時の検査に失敗した、heartbeat file が読めない |
| 2 | 評価の途中で panic した |

## mitsume version

```text
$ mitsume version
mitsume version=1.1.0, commit=<sha>, built=<RFC3339>, go=go1.23.12
```

release の binary には version・commit・build した日時が入る。`go install` で入れたものは `dev` / `none` / `unknown` になる。引数を渡すと exit 1 になる。
