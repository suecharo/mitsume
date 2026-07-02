# mitsume

mitsume は小規模運用の死活監視を Slack Incoming Webhook 1 本と single static binary 1 個で完結させる Go 製 CLI である。

監視できる対象:

- HTTP endpoint の応答
- cron / batch job の走り忘れ (dead-man's switch)
- backup file の mtime、size
- container の稼働状態
- 任意コマンドの exit code

script に 1 行差し込む単発通知から、systemd で常駐する複数対象監視まで、同じ binary で組める。

## Install

3 通りの入手方法がある。用途に応じて選ぶ。

### Binary (GitHub Releases)

Linux / macOS / Windows の pre-built binary を [Releases](https://github.com/suecharo/mitsume/releases) からダウンロードする。`checksums.txt` の sha256 で integrity を検証できる。

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

`ghcr.io/suecharo/mitsume` から distroless base の multi-arch (linux/amd64、linux/arm64) image を pull する。

```bash
docker pull ghcr.io/suecharo/mitsume:v<VERSION>
docker run --rm ghcr.io/suecharo/mitsume:v<VERSION> version
```

container 上での運用パターンは [docs/recipes.md § mitsume 自身の container 化](docs/recipes.md#mitsume-自身の-container-化) を参照する。

## Quickstart

Slack ワークスペースで Incoming Webhook を 1 本発行してから開始する。

```bash
# 1. Webhook URL を env に置く (CLI 引数で値を直接渡す方式は提供しない)
export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T.../B.../...'

# 2. 単発通知で疎通を確認する
mitsume notify "hello from mitsume"

# 3. batch job を wrap して成功 / 失敗を通知する
mitsume run --name daily-report -- /usr/local/bin/daily-report.sh
```

ここまでは設定 JSON なしで動作する。cron の走り忘れ検知、HTTP endpoint の巡回、systemd での常駐化までの通し手順は [docs/getting-started.md](docs/getting-started.md) を参照する。

## サブコマンドの使い分け

全 subcommand で共通の Slack Webhook 1 本を使う。

| やりたいこと | Subcommand | 設定 JSON |
|---|---|---|
| 既存 script から Slack に 1 通送信する | `mitsume notify <msg>` | 不要 |
| コマンドを wrap して成功 / 失敗を通知する | `mitsume run -- <cmd>` | 不要 |
| cron / batch job 側で完了を記録する (dead-man's switch) | `mitsume ping <job>` | 不要 |
| 監視側で job の失踪を検知する (dead-man's switch の評価) | `mitsume check --config <path>` または `mitsume watch --config <path>` | 必要 |
| 外部 cron から endpoint / file / container を巡回する | `mitsume check --config <path>` | 必要 |
| systemd で常駐して監視する | `mitsume watch --config <path>` | 必要 |
| binary の version / commit / build date を表示する | `mitsume version` | 不要 |

dead-man's switch は「job 側で `mitsume ping` を送る」と「監視側で `mitsume check` または `mitsume watch` が評価する」の 2 command の組で構成する。`mitsume check` は外部 cron 用途 (走り忘れの評価と endpoint 巡回) で共通に使う subcommand であり、上表の 3 行目と 5 行目は同じ subcommand の異なる利用パターンを示す。

各 subcommand の引数と exit code は [docs/cli.md](docs/cli.md) を、設定 JSON の schema は [docs/configuration.md](docs/configuration.md) を参照する。

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

test 設計と mock 境界の方針は [tests/README.md](tests/README.md) を参照する。

## License

[Apache License 2.0](LICENSE)
