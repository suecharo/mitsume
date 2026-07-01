# mitsume

小さな運用の死活監視を、Slack Incoming Webhook 1 本と single static binary 1 個で済ませる CLI ツール。

監視するのは次の 5 種。失敗のとき Slack に投げる。

- HTTP endpoint の応答
- 走るべき cron / batch job が走ったか (dead-man's switch)
- バックアップ file が更新されたか、サイズが妥当か
- container が running か
- 任意コマンドの exit code

依存ゼロで、`go install` するか Docker image を pull するだけで動く。設定ファイル無しで動くモード (`notify` / `run` / `ping` / `version`) を最初に用意していて、既存の script や systemd unit の末尾に 1 行差し込むだけで通知が飛ぶ。

## こういう困りごと向け

- 「動いてるはずの cron が動いていなかった」に気付きたい
- 深夜の batch job がコケたら翌朝までに Slack で見たい
- Docker container が落ちたら知りたい
- Datadog / Grafana / Prometheus を入れるには運用規模が小さい
- 手書きの `curl && slack-cli` の面倒を見たくない

逆に、SLO 計算、時系列のダッシュボード、複数 channel の rich な alert routing が欲しいなら、mitsume では足りない。既存の監視 SaaS を使う。

## モードの使い分け

用途で選ぶ。全モードで Slack Webhook 1 本を共通で使う。

| やりたいこと                                      | サブコマンド                                     | 設定 JSON    |
| ------------------------------------------------- | ------------------------------------------------ | ------------ |
| 既存 script から Slack に 1 通投げる              | `mitsume notify <msg>`                           | 不要         |
| コマンドを wrap して成功 / 失敗を通知             | `mitsume run -- <cmd>`                           | 不要         |
| cron / batch の走り忘れを検知                     | `mitsume ping <job>` + `mitsume check` / `watch` | 評価側は必要 |
| 外部 cron から endpoint / file / container を巡回 | `mitsume check --config <path>`                  | 必要         |
| systemd で常駐して監視                            | `mitsume watch --config <path>`                  | 必要         |

各サブコマンドの引数は [docs/cli.md](docs/cli.md)、設定 JSON の schema は [docs/configuration.md](docs/configuration.md) を参照。

## 5 分で試す

Slack ワークスペースで Incoming Webhook を 1 本発行してから始める。

```bash
# 1. webhook URL を env に置く (CLI 引数で値を直接渡す方式は無い)
export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T.../B.../...'

# 2. 単発通知で疎通確認
mitsume notify "hello from mitsume"

# 3. batch job の成功 / 失敗を丸ごと通知
mitsume run --name daily-report -- /usr/local/bin/daily-report.sh
```

ここまでは設定ファイル無しで動く。cron の走り忘れ検知、HTTP endpoint の巡回、systemd 常駐は [docs/getting-started.md](docs/getting-started.md) に順を追った手順を置いた。

## Install

3 通りの入手方法がある。用途に応じて選ぶ。

### GitHub Releases から binary をダウンロード

Linux / macOS / Windows の pre-built binary を [Releases](https://github.com/suecharo/mitsume/releases) から落とす。`checksums.txt` の sha256 で integrity を確認できる。

```bash
# Linux amd64 の例 (arm64 / darwin / windows は archive 名を差し替え)
curl -fL -o mitsume.tar.gz \
  https://github.com/suecharo/mitsume/releases/download/v0.1.0/mitsume_0.1.0_linux_amd64.tar.gz
tar -xzf mitsume.tar.gz
sudo install -m 0755 mitsume /usr/local/bin/mitsume
mitsume version
```

### `go install`

Go 1.23 以降なら source build できる。version 情報は埋め込まれない (`version=dev` になる)。

```bash
go install github.com/suecharo/mitsume/cmd/mitsume@latest
mitsume version
```

### Docker image

`ghcr.io/suecharo/mitsume` から distroless base の multi-arch (linux/amd64, linux/arm64) image を pull できる。

```bash
docker pull ghcr.io/suecharo/mitsume:v0.1.0
docker run --rm ghcr.io/suecharo/mitsume:v0.1.0 version
```

container の中で運用する形は [docs/recipes.md § mitsume 自身を container 化したい](docs/recipes.md#mitsume-自身を-container-化したい) を参照。

## Documentation

初めて触るとき

- [docs/getting-started.md](docs/getting-started.md) — Slack Webhook 発行から `mitsume watch` 常駐まで、順を追って動かす
- [docs/recipes.md](docs/recipes.md) — 「〜したい」から引く運用パターン集 (systemd / cron / Docker)

リファレンス

- [docs/cli.md](docs/cli.md) — 6 サブコマンドの引数・env・exit code
- [docs/configuration.md](docs/configuration.md) — 設定 JSON schema と探索順
- [docs/checkers.md](docs/checkers.md) — 5 種 checker の判定ロジック
- [docs/notify.md](docs/notify.md) — Slack payload の形式と発火モデル
- [docs/heartbeat.md](docs/heartbeat.md) — heartbeat file の schema と atomic rename

設計思想 (contributor 向け)

- [docs/architecture.md](docs/architecture.md) — なぜ checker / notify / heartbeat の 3 軸に分けたか
- [tests/README.md](tests/README.md) — テスト方針、PBT、mutation testing

## Status

コア実装は完了し、release engineering (GoReleaser + GitHub Actions + Docker image) を整えた段階。v0.1.0 tag が切られるまで `go install` は動くが GitHub Releases / `ghcr.io/suecharo/mitsume` の image は未公開。tag 打刻後にすべての install 手段が有効になる。

## License

[Apache License 2.0](LICENSE)
