# mitsume

小さな運用の死活監視を、Slack Incoming Webhook 1 本と single static binary 1 個で済ませる CLI ツール。

監視するのは次の 5 種。失敗のとき Slack に投げる。

- HTTP endpoint の応答
- 走るべき cron / batch job が走ったか (dead-man's switch)
- バックアップ file が更新されたか、サイズが妥当か
- container が running か
- 任意コマンドの exit code

依存ゼロで、`go install` するか Dockerfile に `ADD` するだけで動く。設定ファイル無しで動くモード (`notify` / `run` / `ping`) を最初に用意していて、既存の script や systemd unit の末尾に 1 行差し込むだけで通知が飛ぶ。

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

> 実装は未着手で release binary もまだ無い ([Status](#status))。下のコマンドは公開後に動く形。今は docs だけ読めば OK。

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

```bash
go install github.com/suecharo/mitsume@latest
```

release binary が配布され次第、GitHub Releases からも取れるようにする。

## Documentation

初めて触るとき

- [docs/getting-started.md](docs/getting-started.md) — Slack Webhook 発行から `mitsume watch` 常駐まで、順を追って動かす
- [docs/recipes.md](docs/recipes.md) — 「〜したい」から引く運用パターン集 (systemd / cron / Docker)

リファレンス

- [docs/cli.md](docs/cli.md) — 5 サブコマンドの引数・env・exit code
- [docs/configuration.md](docs/configuration.md) — 設定 JSON schema と探索順
- [docs/checkers.md](docs/checkers.md) — 5 種 checker の判定ロジック
- [docs/notify.md](docs/notify.md) — Slack payload の形式と発火モデル
- [docs/heartbeat.md](docs/heartbeat.md) — heartbeat file の schema と atomic rename

設計思想 (contributor 向け)

- [docs/architecture.md](docs/architecture.md) — なぜ checker / notify / heartbeat の 3 軸に分けたか
- [tests/README.md](tests/README.md) — テスト方針、PBT、mutation testing

## Status

現在は仕様策定が完了した段階で、Go の実装 (`go.mod` 含む) はまだ入っていない。`docs/` を SSOT として、これから実装を進める。上の Install と 5 分クイックスタートは release binary が配布されるまで動かない。

## License

[Apache License 2.0](LICENSE)
