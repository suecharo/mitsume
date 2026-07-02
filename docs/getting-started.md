# Getting started

mitsume を初めて触るユーザー向けに、Slack Webhook の発行から `mitsume watch` の常駐化までを、動作確認しながら順に進める tutorial である。各 subcommand の flag は [cli.md](cli.md)、運用パターン別の設定例は [recipes.md](recipes.md) を参照する。

## 前提

- `mitsume` の binary が `/usr/local/bin/mitsume` 相当の位置に配置されており、shell から `mitsume` で呼び出せる。
- Slack ワークスペースの管理者権限があり、App を作成して Incoming Webhook を有効化できる。
- Linux (systemd を想定するが、cron 単独でも代替可能である) を対象とする。

binary の入手方法は [README.md](../README.md#install) を参照する。

## Slack Incoming Webhook を発行する

1. Slack ワークスペースで [Your Apps](https://api.slack.com/apps) を開き、新しい App を作成する。
2. Incoming Webhooks を有効化し、通知先の channel を選択して Webhook URL を発行する。
3. 発行された URL (`https://hooks.slack.com/services/T.../B.../...`) を控える。

以降で使う env 変数の default 名は `MITSUME_SLACK_WEBHOOK_URL` である。任意の名前を使う場合は `--slack-webhook-url-env <NAME>` で env 名を渡す (詳細は [notify.md § 秘密情報](notify.md#秘密情報))。

## Webhook URL を環境変数に置く

CLI 引数で URL の値を直接渡す方式は提供しない。理由は [architecture.md § Security invariants](architecture.md#security-invariants) を参照する。値は env に置き、mitsume からは env 変数を経由して参照する。

```bash
export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T.../B.../...'
```

個人開発では shell の `.bashrc` / `.profile` に書く。複数ユーザーで動かす場合は env file (`mode 0640`) に切り出して systemd unit の `EnvironmentFile=` から読み込む。

## 単発通知で疎通を確認する

`mitsume notify` は引数の文字列を Slack に 1 通送信するのみの subcommand である。設定 JSON も heartbeat file も必要としない。

まず `--dry-run` で送信予定の payload を stderr に出して形式を確認する。

```bash
mitsume notify --dry-run "hello from $(hostname)"
```

payload の JSON が stderr に出力されれば動作している。実送信する場合は `--dry-run` を外す。

```bash
mitsume notify "hello from $(hostname)"
```

Slack channel に届いたら本手順は完了である。届かない場合は [Troubleshooting](#troubleshooting) を参照する。

既存の shell script の末尾に 1 行差し込む形は次の通りである。

```bash
#!/bin/bash
set -euo pipefail
/usr/local/bin/some-batch.sh || {
  rc=$?
  mitsume notify "some-batch failed on $(hostname): exit $rc"
  exit "$rc"
}
```

## `run` で子プロセスを wrap する

`mitsume run --name <name> -- <cmd>` は子プロセスを起動し、exit code で成功 / 失敗を判定して内部で notifier を呼び出す。子の stderr 末尾は通知に自動で含まれるため、debug の手がかりが Slack に残る。

意図的に失敗するコマンドで動作を確認する。

```bash
mitsume run --name test-fail -- /bin/sh -c 'echo "bad thing" >&2; exit 1'
```

Slack に `[mitsume] test-fail failed (run: exit=1)` と stderr 末尾 (`bad thing`) が届けば動作している (payload の書式は [notify.md § Payload](notify.md#payload) を参照する)。

成功時の通知を抑止する場合は `--quiet-on-success` を付ける。

```bash
mitsume run --quiet-on-success --name nightly-backup -- /usr/local/bin/nightly-backup.sh
```

`run` の詳細な flag (`--timeout` / `--grace-period` / `--stderr-tail-lines` など) は [cli.md § run](cli.md#mitsume-run) を参照する。

## Dead-man's switch で走り忘れを検知する

「走るべき batch job が走らなかった」ケースは、`mitsume run` の wrap や失敗時通知だけでは取りこぼす。呼び出し側 (cron / systemd timer) が壊れて job そのものが起動しなかった場合は `mitsume run` が呼ばれないため、失敗通知も送信されない。dead-man's switch は「指定期間内に `ping` が届かない」ことを failure として拾い、この隙間を埋める。

mitsume の dead-man's switch は「`ping` を送る側」と「監視する側」の 2 プロセスで組む (概念の定義は [architecture.md § 用語](architecture.md#用語) を参照する)。

1. job の完了時に `mitsume ping <job>` を呼び出し、heartbeat file の `last_ping_at` を更新する。
2. 別プロセスの `mitsume check` または `mitsume watch` が heartbeat file を読み、`expect.within` を超えて古い場合に Slack へ通知する。

まず監視側の設定 JSON を書く。`deadman` checker は「最後の `ping` から `within` 以内に到達していれば OK」を判定する。cron の実行間隔 + 1 サイクル分の buffer を目安に `within` を設定する。

`./mitsume.json`:

```json
{
  "notify": {
    "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"
  },
  "defaults": { "interval": "1h" },
  "checks": [
    {
      "type": "deadman",
      "job": "nightly-backup",
      "expect": { "within": "25h" }
    }
  ]
}
```

heartbeat file の path 解決は [heartbeat.md § File location](heartbeat.md#file-location) を参照する。ここでは何も指定していないため、config と同じ directory の `./mitsume.heartbeat.json` が使われる。

job を実行する側で `ping` を送る。cwd の `mitsume.json` が設定 JSON の探索順で自動的に選ばれる (詳細は [configuration.md § 設定 JSON の場所](configuration.md#設定-json-の場所))。

```bash
mitsume ping nightly-backup
```

heartbeat file が生成 / 更新されたことを確認する。

```bash
cat ./mitsume.heartbeat.json
```

次のような内容が出れば `ping` 側は動作している。

```json
{
  "jobs": {
    "nightly-backup": { "last_ping_at": "2026-07-01T10:00:00+09:00" }
  }
}
```

`mitsume check` は設定 JSON を 1 回だけ評価して exit する外部 cron 向けの subcommand である。

```bash
mitsume check --dry-run
```

`last_ping_at` が 25h 以内であればまだ failure にならない。`jq` で `last_ping_at` を古い時刻に書き換え、failure 検知を確認する。

```bash
jq '.jobs["nightly-backup"].last_ping_at = "2026-06-30T00:00:00+09:00"' \
  ./mitsume.heartbeat.json > ./mitsume.heartbeat.json.tmp \
  && mv ./mitsume.heartbeat.json.tmp ./mitsume.heartbeat.json

mitsume check --dry-run
```

stderr に出力される payload JSON の `text` field に `[mitsume] nightly-backup failed (deadman: ...)` が含まれていれば検知できている (`text` は payload 全体の一部である。詳細は [notify.md § Payload](notify.md#payload) を参照)。`--dry-run` を外すと Slack に送信される。

crontab で運用する場合は、config と heartbeat file を system 側の恒久 path に配置するのが一般的である。以下の例では `/etc/mitsume/mitsume.json` と `/var/lib/mitsume/heartbeat.json` を使う。事前に directory を作成する。

```bash
sudo install -d -m 0755 /etc/mitsume /var/lib/mitsume
# `/etc/mitsume/mitsume.json` を設置する (前段の JSON を配置する)
```

`MITSUME_HEARTBEAT_FILE` env の意味と探索順は [heartbeat.md § File location](heartbeat.md#file-location) を参照する。

`ping` を送る側と監視する側を同じ host に置く場合の crontab は次のようになる。

```text
# job 完了時に ping を送信する (heartbeat file は ping 側の `--heartbeat-file` で明示する)
0 3 * * * /usr/local/bin/nightly-backup.sh && /usr/local/bin/mitsume ping --heartbeat-file /var/lib/mitsume/heartbeat.json nightly-backup

# 監視側は 1 時間ごとに check を実行する
0 * * * * MITSUME_HEARTBEAT_FILE=/var/lib/mitsume/heartbeat.json MITSUME_SLACK_WEBHOOK_URL=https://... /usr/local/bin/mitsume check --config /etc/mitsume/mitsume.json
```

`ping` と `check` は同じ heartbeat file を指す必要がある (`--heartbeat-file` flag、`MITSUME_HEARTBEAT_FILE` env、設定 JSON の `heartbeat_file` field のいずれか)。crontab で `A=B cmd1 && cmd2` の形式にすると、POSIX shell の仕様上 `A=B` は `cmd1` の env にのみ届く。`ping` 側は `--heartbeat-file` flag で明示するのが安全である。

「`ping` する前に job そのものが失敗した」場合の失敗通知は `mitsume run` にまとめる。`mitsume run -- <cmd> && mitsume ping <job>` の形式では、job 失敗時は `run` の通知が送信され、成功時のみ `ping` が送られる。

## HTTP endpoint を監視する

`deadman` と同じ設定 JSON に active checker (`http` / `file` / `container` / `cmd`) を並べる。ここでは HTTP endpoint を 1 個追加する。

`./mitsume.json`:

```json
{
  "host": "api-prod-01",
  "notify": {
    "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"
  },
  "defaults": {
    "interval": "1h",
    "timeout": "10s"
  },
  "checks": [
    {
      "type": "http",
      "name": "api-health",
      "url": "https://api.example.com/health",
      "expect": {
        "status": 200,
        "body_jsonpath": [
          { "path": "$.status", "equals": "ok" }
        ]
      }
    },
    {
      "type": "deadman",
      "job": "nightly-backup",
      "expect": { "within": "25h" }
    }
  ]
}
```

`defaults.interval` は各 check の `interval` の default 値である。`defaults.timeout` は HTTP checker の request timeout と cmd checker の実行 timeout の default 値になる。詳細は [checkers.md](checkers.md) を参照する。

`mitsume check --dry-run` で config が通ることを確認する。schema エラーがあれば起動時に fail-fast で exit 1 とする。

```bash
mitsume check --dry-run
```

HTTP endpoint を意図的に壊して failure 検知を確認する場合は、`expect.status` を存在しない値 (`999` など) に一時的に書き換えて `mitsume check` を実行するのが手軽である。

failure を 1 回検知しても即通知にはならない。default の confirm burst を通過して失敗が確定した後に Slack に通知される。詳細は [checkers.md § confirm](checkers.md#confirm) を参照する。

その他の checker (`file` / `container` / `cmd`) は [checkers.md](checkers.md) を参照する。

## `watch` を systemd で常駐化する

外部 cron で呼び出す `check` は「1h に 1 回 config を全部評価する」動作で軽量である。より高頻度で監視したい場合は `watch` を systemd unit で常駐させる。

system user を作成し、env file と config を配置する。

```bash
sudo useradd -r -s /usr/sbin/nologin mitsume
sudo install -d -o mitsume -g mitsume -m 0750 /var/lib/mitsume /etc/mitsume
```

Webhook 用 env file (`/etc/mitsume/webhook.env`、mode 0640、owner root、group mitsume):

```ini
MITSUME_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T.../B.../...
```

systemd unit (`/etc/systemd/system/mitsume.service`):

```ini
[Unit]
Description=mitsume health monitor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=mitsume
Group=mitsume
EnvironmentFile=/etc/mitsume/webhook.env
ExecStart=/usr/local/bin/mitsume watch --config /etc/mitsume/mitsume.json
Restart=on-failure
RestartSec=10s
KillSignal=SIGTERM
TimeoutStopSec=15s

NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/mitsume
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

有効化する。

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now mitsume.service
sudo journalctl -u mitsume.service -f
```

`Restart=on-failure` により mitsume 自身の異常終了は systemd 側でカバーする。SIGTERM / panic 時は best-effort で 1 通の通知が送信されるが、SIGKILL や電源断の完全カバーは構造的に不可能である ([architecture.md § 自身の死活](architecture.md#自身の死活) を参照)。systemd の `OnFailure=` で二重に受ける ([recipes.md § systemd unit 失敗の捕捉](recipes.md#systemd-unit-失敗の捕捉) の template unit を流用する)。

## Troubleshooting

疎通確認と config 検証で詰まった場合に確認する項目を挙げる。

- **通知が Slack に届かない (単発通知).** `echo $MITSUME_SLACK_WEBHOOK_URL` が空の場合は `export` をやり直す。Slack が HTTP 4xx を返す場合は Webhook URL の typo または Slack 側での無効化を疑う。Slack が HTTP 5xx を返す場合は mitsume が retry する ([notify.md § Retry](notify.md#retry))。retry が全滅した場合は Slack 側の一時障害を疑う。
- **`--dry-run` で payload が出ない.** subcommand が正しく引数を受け取っていない可能性がある。`mitsume notify --dry-run "test"` のように `<msg>` を必ず渡す。
- **`mitsume check` が起動時に exit 1 する.** 設定 JSON の schema 違反である。stderr のエラーメッセージで違反した field を特定する。schema は [configuration.md](configuration.md) を参照する。
- **`mitsume ping <job>` が exit 1 する.** heartbeat file の path が解決できていない。`--heartbeat-file` を明示するか、config 隣接の `./mitsume.heartbeat.json` を使える cwd に移動する ([heartbeat.md § File location](heartbeat.md#file-location) を参照)。
- **`deadman` が failure と判定しない.** heartbeat file 内の `last_ping_at` が新しい可能性がある。`jq` で `last_ping_at` を古い時刻に書き換えて `mitsume check --dry-run` を実行し、payload が stderr に出るか確認する。
- **`watch` が起動後すぐに終了する.** journalctl (`sudo journalctl -u mitsume.service -n 50`) で fatal error を確認する。config validation の失敗、Webhook URL env の未定義、heartbeat file の pre-flight 失敗が主な原因である。

## 関連

- [recipes.md](recipes.md) — Docker container 監視、`mitsume run` の Dockerfile 組み込み、cron dead-man's switch などのパターン集
- [cli.md](cli.md) — 各 subcommand の引数と exit code
- [configuration.md](configuration.md) — 設定 JSON の全 field、`defaults` / `confirm` の書き方
- [checkers.md](checkers.md) — 5 種 checker の判定 logic と固有 field
- [notify.md](notify.md) — Slack payload の形式と通知トリガー
- [heartbeat.md](heartbeat.md) — heartbeat file の schema と write / read semantics
- [architecture.md](architecture.md) — 設計原則、用語集、Design decisions の背景
