# Getting Started

mitsume を初めて触る人向けに、Slack Webhook 発行から `mitsume watch` の常駐化までを、動作確認しながら順に進める。個別の細かい引数や運用パターン別の設定例は [cli.md](cli.md) / [recipes.md](recipes.md) を参照。

## 前提

- `mitsume` binary が `/usr/local/bin/mitsume` 相当の位置に置いてあり、shell から `mitsume` で呼べる
- Slack ワークスペースの管理者権限があり、App を作って Incoming Webhook を有効化できる
- Linux (systemd + Docker がある想定、cron でも代替可) を想定する

Go binary の入手は [README.md](../README.md#install) を参照。

## Step 1. Slack Incoming Webhook を発行する

1. Slack ワークスペースで [Your Apps](https://api.slack.com/apps) を開いて新しい App を作る
2. 「Incoming Webhooks」を有効化し、通知先 channel を選んで webhook URL を発行する
3. 発行された URL (`https://hooks.slack.com/services/T.../B.../...`) を控える

以降で使う env 変数は `MITSUME_SLACK_WEBHOOK_URL`。任意の名前を使う場合は `--slack-webhook-url-env <NAME>` で env 名を渡す (詳細は [notify.md](notify.md#秘密情報の扱い))。

## Step 2. webhook URL を env に置く

CLI 引数で URL の値を直接渡す方式は用意していない (`ps aux` 経由で漏れるため)。env に置いて `mitsume` から参照する。

```bash
export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T.../B.../...'
```

`.bashrc` / `.profile` に書くか、後で紹介する systemd unit の `EnvironmentFile=` に切り出す。個人開発なら shell の export、複数ユーザーが動かすなら env file に切り出して mode 0640 で置くのが無難。

## Step 3. 単発通知が飛ぶことを確認する

`mitsume notify` は「引数の文字列を Slack に 1 通投げる」だけのサブコマンド。設定 JSON も heartbeat file も要らない。

まず `--dry-run` で送るはずの payload を stderr に出して形を確認する。

```bash
mitsume notify --dry-run "hello from $(hostname)"
```

payload の JSON が stderr に出れば動いている。実送するときは `--dry-run` を外す。

```bash
mitsume notify "hello from $(hostname)"
```

Slack channel に届いたら Step 3 完了。届かないときは:

- `echo $MITSUME_SLACK_WEBHOOK_URL` が空 → export をやり直す
- 4xx が返る → webhook URL の typo か、Slack 側で無効化されている
- 5xx が返る → mitsume は 3 回 retry する (1s → 2s → 4s、[notify.md](notify.md#通知失敗時の-retry))。retry 全滅なら Slack 側の一時障害を疑う

既存の shell script の末尾に 1 行差し込むならこの形。

```bash
#!/bin/bash
set -euo pipefail
/usr/local/bin/some-batch.sh || {
  mitsume notify "some-batch failed on $(hostname): exit $?"
  exit 1
}
```

## Step 4. コマンドを wrap して成功 / 失敗を通知する

`mitsume run --name <name> -- <cmd>` は cmd を fork で起動し、exit code で成功 / 失敗を判定して、内部で `notify` を呼ぶ。子の stderr 末尾を通知にそのまま乗せてくれるので、debug のとっかかりが Slack 上に残る。

わざと失敗するコマンドで挙動を見る。

```bash
mitsume run --name test-fail -- /bin/sh -c 'echo "bad thing" >&2; exit 1'
```

Slack に「`[mitsume] test-fail failed (run: exit 1)`」と stderr 末尾 (`bad thing`) が届けば OK。

成功時の通知を抑えたければ `--quiet-on-success` を付ける。

```bash
mitsume run --quiet-on-success --name nightly-backup -- /usr/local/bin/nightly-backup.sh
```

`run` の詳細な flag (`--timeout` / `--grace-period` / `--stderr-tail-lines` など) は [cli.md](cli.md#mitsume-run) を参照。

## Step 5. cron の走り忘れを検知する (dead-man's switch)

「走るべき batch job が走らなかった」ケースは、job が失敗しても止まっても、そもそも呼び出し側 (cron / systemd timer) が壊れても、単に何も起きない。従来の「成功したら通知」形では取りこぼす。

mitsume の dead-man's switch は反転監視で、次の 2 段構えで組む。

1. job 完了時に `mitsume ping <job>` で「生きてる」を打刻する → heartbeat file の `last_ping_at` を更新
2. 別プロセスの `mitsume check` (または `watch`) が heartbeat file を読み、`expect.within` を超えて古ければ Slack に投げる

まず評価側の設定 JSON を書く。deadman は「最後の ping から `within` 以内に到達していれば OK」を判定する。cron の実行間隔 + 1 サイクル分の buffer を目安に `within` を置く。

`./mitsume.json`:

```json
{
  "notify": {
    "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"
  },
  "checks": [
    {
      "type": "deadman",
      "job": "nightly-backup",
      "expect": { "within": "25h" }
    }
  ]
}
```

heartbeat file のパスは、`--heartbeat-file` / `$MITSUME_HEARTBEAT_FILE` / config JSON の `heartbeat_file` フィールド / config 隣接ファイルの順で探す (詳細は [heartbeat.md](heartbeat.md#場所))。ここでは何も指定していないので、config 隣接の `./mitsume.heartbeat.json` が使われる。

job 側で ping を打つ。cwd の `mitsume.json` が config 探索順で自動的に拾われる (詳細は [configuration.md](configuration.md#設定-json-の場所))。

```bash
mitsume ping nightly-backup
```

heartbeat file が生成 / 更新されたことを確認する。

```bash
cat ./mitsume.heartbeat.json
```

次のような内容が出れば ping 側は動いている。

```json
{
  "jobs": {
    "nightly-backup": { "last_ping_at": "2026-07-01T10:00:00+09:00" }
  }
}
```

`mitsume check` は設定 JSON を 1 回だけ評価して exit する外部 cron 向けのモード (`--config` 省略で cwd の `mitsume.json` を拾う)。

```bash
mitsume check --dry-run
```

`last_ping_at` が 25h 以内ならまだ失敗にならない。`jq` で `last_ping_at` を古い時刻に書き換えて、失敗検知を確認する。

```bash
jq '.jobs["nightly-backup"].last_ping_at = "2026-06-30T00:00:00+09:00"' \
  ./mitsume.heartbeat.json > ./mitsume.heartbeat.json.tmp \
  && mv ./mitsume.heartbeat.json.tmp ./mitsume.heartbeat.json

mitsume check --dry-run
```

stderr に「`[mitsume] nightly-backup failed (deadman: ...)`」の payload が出れば検知できている。`--dry-run` を外すと Slack に飛ぶ。

crontab の 1 行例 (job 側と評価側を同じホストに置く場合):

```text
# job 完了時に ping
0 3 * * * MITSUME_HEARTBEAT_FILE=/var/lib/mitsume/heartbeat.json /usr/local/bin/nightly-backup.sh && /usr/local/bin/mitsume ping nightly-backup

# 評価側は 1 時間ごと
0 * * * * MITSUME_HEARTBEAT_FILE=/var/lib/mitsume/heartbeat.json MITSUME_SLACK_WEBHOOK_URL=https://... /usr/local/bin/mitsume check --config /etc/mitsume/mitsume.json
```

`ping` と `check` が同じ heartbeat file を指す必要がある (`MITSUME_HEARTBEAT_FILE` で明示するか、設定 JSON の `heartbeat_file` フィールドで指定する)。

「`ping` する前に job そのものが失敗したとき」の失敗通知は `mitsume run` に寄せる。`mitsume run -- <cmd> && mitsume ping <job>` の形なら、job 失敗時は `run` の通知が飛び、成功時のみ `ping` が打たれる。

## Step 6. HTTP endpoint を巡回する

deadman と同じ config に、能動 checker (`http` / `file` / `container` / `cmd`) を並べる。ここでは HTTP endpoint を 1 個追加する。

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

`defaults.interval` を各 check の interval のデフォルトにする。`defaults.timeout` は http / cmd の request timeout に効く。

`mitsume check --dry-run` で config が通ることを確認する。schema エラーがあれば起動時に fail-fast で exit 1 する。

```bash
mitsume check --dry-run
```

HTTP endpoint を意図的に壊して失敗検知を確かめるなら、`expect.status` を存在しない値 (`999` など) に一時的に書き換えて `mitsume check` を回すのが手軽。

失敗を 1 回検知しても即通知にはしない。デフォルトでは `confirm.checks: 3` × `confirm.interval: 30s` の短 retry burst を回して、3 回連続で失敗したら Slack に投げる。詳細は [checkers.md](checkers.md#confirm)。

その他の checker (`file` / `container` / `cmd`) は [checkers.md](checkers.md) を参照。

## Step 7. `watch` で常駐監視する (systemd)

外部 cron で叩く `check` は「1h に 1 回、config を全部評価する」動きで軽量。もう少しレスポンス良く、あるいは interval を短めに刻みたい場合は `watch` を systemd unit で常駐させる。

system user を作り、env file と config を配置する。

```bash
sudo useradd -r -s /usr/sbin/nologin mitsume
sudo install -d -o mitsume -g mitsume -m 0750 /var/lib/mitsume /etc/mitsume
```

webhook 用 env file (`/etc/mitsume/webhook.env`、mode 0640、owner root、group mitsume):

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

`Restart=on-failure` で mitsume 自身の死は systemd 側でカバーする。SIGTERM / panic 時は best-effort で 1 発通知が飛ぶが、SIGKILL や電源断の完全カバーは構造上不可能なので、systemd の `OnFailure=` で二重に受ける ([recipes.md](recipes.md#systemd-unit-の失敗を丸ごと拾いたい) の template unit を流用する)。

## 次に読む

- [recipes.md](recipes.md) — Docker container 監視、`mitsume run` の Dockerfile 組み込み、cron dead-man's switch の詳細
- [cli.md](cli.md) — 各サブコマンドの引数と exit code
- [configuration.md](configuration.md) — 設定 JSON の全フィールド、`defaults` / `confirm` の書き方
- [checkers.md](checkers.md) — 5 種 checker の判定ロジックと固有フィールド
- [notify.md](notify.md) — Slack payload の形式と発火モデル
- [heartbeat.md](heartbeat.md) — heartbeat file の schema と atomic rename
