# mitsume

mitsume は小規模な運用向けの死活監視 CLI である。HTTP endpoint の応答、cron job の走り忘れ、backup ファイルの更新、container の稼働、任意のコマンドの結果を見張り、異常があれば Slack Incoming Webhook に通知する。Go の static binary 1 つで動き、監視用のサーバーや DB は要らない。

host 数台・check 数十個くらいの、Prometheus や Datadog を入れるほどではない homelab や社内の batch サーバーを対象にしている。対象の外にあるもの (Slack 以外の通知先、復旧の通知、メトリクスなど) は [docs/architecture.md](docs/architecture.md#持たない機能) にまとめてある。

## インストール

### Binary (GitHub Releases)

Linux / macOS / Windows 向けの binary を [Releases](https://github.com/suecharo/mitsume/releases) に置いている。`checksums.txt` の sha256 で中身を確かめられる。

```bash
# Linux amd64 の例。arm64 / darwin / windows は archive の名前を変える
curl -fL -o mitsume.tar.gz \
  https://github.com/suecharo/mitsume/releases/download/v<VERSION>/mitsume_<VERSION>_linux_amd64.tar.gz
tar -xzf mitsume.tar.gz
sudo install -m 0755 mitsume /usr/local/bin/mitsume
mitsume version
```

### go install

Go 1.23 以降で build できる。この方法では version の情報が入らない (`version=dev` になる)。

```bash
go install github.com/suecharo/mitsume/cmd/mitsume@latest
```

### Docker image

`ghcr.io/suecharo/mitsume` に linux/amd64 と linux/arm64 の image を置いている。

```bash
docker run --rm ghcr.io/suecharo/mitsume:v<VERSION> version
```

## クイックスタート

Slack で Incoming Webhook を 1 つ作り ([作り方](docs/recipes.md#slack-の-webhook-を作る))、URL を環境変数に入れる。

```bash
export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T.../B.../...'

# 1 通送る
mitsume notify "hello from mitsume"

# コマンドを実行し、終わったら成功か失敗かを通知する
mitsume run --name daily-report -- /usr/local/bin/daily-report.sh
```

ここまでは設定 JSON なしで動く。HTTP endpoint などを続けて見張るときは、設定 JSON に check を並べて、`mitsume watch` で常駐させるか、`mitsume check` を cron から呼ぶ。設定 JSON の書き方は [docs/configuration.md](docs/configuration.md)、systemd や cron への組み込み方は [docs/recipes.md](docs/recipes.md) にある。

## ドキュメント

| ファイル | 内容 |
|---|---|
| [docs/recipes.md](docs/recipes.md) | 目的ごとの組み込み方 (script の失敗の通知、cron の走り忘れ、systemd での常駐、container) |
| [docs/configuration.md](docs/configuration.md) | 設定 JSON の書き方 |
| [docs/checkers.md](docs/checkers.md) | checker の種類ごとの field と判定 |
| [docs/notify.md](docs/notify.md) | 通知の種類と中身、届かない場合 |
| [docs/heartbeat.md](docs/heartbeat.md) | dead-man's switch の仕組みと heartbeat file |
| [docs/cli.md](docs/cli.md) | subcommand ごとの引数・環境変数・exit code |
| [docs/architecture.md](docs/architecture.md) | 設計原則と、持たない機能 |
| [tests/README.md](tests/README.md) | テストの置き場所と書き方 |

## 開発

build・test・lint は `make build` / `make test` / `make lint` で行う。テストの書き方は [tests/README.md](tests/README.md) にある。

## ライセンス

[Apache License 2.0](LICENSE)
