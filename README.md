# mitsume

mitsume は小規模運用向けの死活監視 CLI である。「動いているはずのものが動いていない」ことに気づくための道具で、通知は Slack Incoming Webhook 1 本に送る。

- **Single binary, no dependencies** — 監視 agent も DB も専用サーバーも不要。Go 製 static binary 1 個を置くだけで動く
- **設定ファイルなしで始められる** — 既存 script への 1 行差し込み (`mitsume notify`) やコマンドの wrap (`mitsume run`) は設定 JSON なしで動く
- **対象は小規模運用** — host 数個・check 数十個の規模。Prometheus / Datadog を組むほどではない homelab・社内 batch サーバーの層を埋める

監視できる対象:

- HTTP endpoint の応答
- cron / batch job の走り忘れ (dead-man's switch)
- backup file の mtime / size
- container の稼働状態
- 任意コマンドの exit code

## Install

入手方法は 3 通り。

### Binary (GitHub Releases)

Linux / macOS / Windows の pre-built binary が [Releases](https://github.com/suecharo/mitsume/releases) にある。`checksums.txt` の sha256 で integrity を検証できる。

```bash
# Linux amd64 の例 (arm64 / darwin / windows は archive 名を差し替える)
curl -fL -o mitsume.tar.gz \
  https://github.com/suecharo/mitsume/releases/download/v<VERSION>/mitsume_<VERSION>_linux_amd64.tar.gz
tar -xzf mitsume.tar.gz
sudo install -m 0755 mitsume /usr/local/bin/mitsume
mitsume version
```

### go install

Go 1.23 以降で source build できる。version 情報は埋め込まれない (`version=dev` になる)。

```bash
go install github.com/suecharo/mitsume/cmd/mitsume@latest
mitsume version
```

### Docker image

`ghcr.io/suecharo/mitsume` に distroless base の multi-arch (linux/amd64, linux/arm64) image がある。

```bash
docker pull ghcr.io/suecharo/mitsume:v<VERSION>
docker run --rm ghcr.io/suecharo/mitsume:v<VERSION> version
```

container 上での運用パターン: [docs/recipes.md § mitsume 自身の container 化](docs/recipes.md#mitsume-自身の-container-化)

## Quickstart

前提: Slack ワークスペースで発行した Incoming Webhook 1 本 (発行手順は [docs/getting-started.md](docs/getting-started.md))。

```bash
# 1. Webhook URL を env に置く
export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T.../B.../...'

# 2. 単発通知で疎通を確認する
mitsume notify "hello from mitsume"

# 3. batch job を wrap して成功 / 失敗を通知する
mitsume run --name daily-report -- /usr/local/bin/daily-report.sh
```

ここまで設定 JSON なしで動く。Webhook URL を CLI 引数で直接渡す方式はない (env 経由のみ。理由は [docs/architecture.md § Security invariants](docs/architecture.md#security-invariants))。

cron の走り忘れ検知から systemd での常駐化までの通し手順: [docs/getting-started.md](docs/getting-started.md)

## 監視を組む

継続的な監視は 5 種の checker (`http` / `deadman` / `file` / `container` / `cmd`) を設定 JSON 1 個に並べて定義する。動く最小形:

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

これを `./mitsume.json` に置けば、path 指定なしで `mitsume watch` が読む。

```bash
mitsume watch    # 常駐して評価し続ける
mitsume check    # cron から 1 回だけ評価する
```

- 設定 JSON の schema 全体: [docs/configuration.md](docs/configuration.md)
- systemd / cron / Docker への組み込み: [docs/recipes.md](docs/recipes.md)

## サブコマンド

設定 JSON なしで動くもの:

| Subcommand                             | 用途                                                     |
| -------------------------------------- | -------------------------------------------------------- |
| `mitsume notify <msg>`                 | Slack に 1 通送信する。script への差し込み用             |
| `mitsume run [--name <name>] -- <cmd>` | コマンドを wrap し、終了時に成功 / 失敗を通知する        |
| `mitsume ping [<job>]`                 | dead-man's switch の heartbeat を記録する (通知はしない) |
| `mitsume version`                      | version / commit / build date を表示する                 |

設定 JSON で監視を定義して使うもの:

| Subcommand      | 用途                                                           |
| --------------- | -------------------------------------------------------------- |
| `mitsume check` | 全 check を 1 回評価して exit する。外部 cron からの呼び出し用 |
| `mitsume watch` | 常駐し、check ごとの interval で評価し続ける。systemd 向け     |

dead-man's switch は 2 つの subcommand の組で動く。job 側の `mitsume ping` が heartbeat file に完了時刻を記録し、監視側の `mitsume check` / `mitsume watch` が「期限内に ping が来たか」を評価する。

```
+----------+  ping   +----------------+  read   +---------------+  notify  +-------+
| cron job | ------> | heartbeat file | <------ | check / watch | -------> | Slack |
+----------+         +----------------+         +---------------+          +-------+
```

各 subcommand の引数と exit code: [docs/cli.md](docs/cli.md)

## やらないこと

機能を足さないことで運用の単純さを保つ設計である。以下は意図的に対象外 (理由も含めた一覧は [docs/architecture.md § Non-goals](docs/architecture.md#non-goals)):

- Slack 以外の通知先、channel 別の routing
- 時系列メトリクス、ダッシュボード、SLO 計算
- debounce / recovery 通知 / リマインド (状態を持たない設計のため構造的に不採用)
- web UI / REST API / 実行時の config reload

## ドキュメント

初めて触るとき:

- [docs/getting-started.md](docs/getting-started.md) — Slack Webhook 発行から `mitsume watch` の常駐化までの通し tutorial
- [docs/recipes.md](docs/recipes.md) — systemd / cron / Docker への組み込みパターン集

リファレンス:

- [docs/cli.md](docs/cli.md) — 6 subcommand の引数、env、exit code
- [docs/configuration.md](docs/configuration.md) — 設定 JSON の schema と探索順
- [docs/checkers.md](docs/checkers.md) — 5 種 checker の判定 logic
- [docs/notify.md](docs/notify.md) — Slack payload の形式と通知トリガー
- [docs/heartbeat.md](docs/heartbeat.md) — heartbeat file の schema と write / read semantics

設計思想 (contributor 向け):

- [docs/architecture.md](docs/architecture.md) — core components、confirm burst、design decisions の背景
- [tests/README.md](tests/README.md) — テスト方針、PBT、mutation testing

## Development

build と test:

```bash
make build     # single static binary をビルドする
make test      # unit / PBT / integration test を実行する
```

test 設計と mock 境界の方針: [tests/README.md](tests/README.md)

## License

[Apache License 2.0](LICENSE)
