# Recipes

運用パターン別の設定例をまとめる。Slack Webhook の発行と env の設定は済んでいる前提で書く。ゼロから通しで動かす手順は [getting-started.md](getting-started.md) を参照する。

Webhook URL の受け渡しは env 経由でのみ行う。以下の `export` は対話 shell から動作を確認する場合の例であり、systemd では `EnvironmentFile=` / cron では inline env / Docker では compose の `environment:` と、各 recipe で env の渡し方が異なる。

```bash
export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T.../B.../...'
```

任意の env 名を使う場合は `--slack-webhook-url-env <NAME>` を渡す ([notify.md § 秘密情報](notify.md#秘密情報) を参照)。

## Shell 失敗通知

shell script の末尾で失敗時のみ通知する場合は `mitsume notify` を `||` の後ろに置く。設定 JSON も heartbeat file も必要としない。

```bash
#!/bin/bash
set -euo pipefail

/usr/local/bin/some-batch.sh || {
  rc=$?
  /usr/local/bin/mitsume notify "some-batch failed on $(hostname): exit $rc"
  exit "$rc"
}
```

`||` に入った直後に `rc=$?` で失敗時の exit code を保存する。この保存を挟まず `"exit $?"` を書くと、`$(hostname)` の展開後に `$?` が hostname の exit code (`0`) で上書きされ、常に `exit 0` と通知される点に注意する。

`mitsume notify` は明示的に呼び出したときに 1 通を送信するだけの subcommand であり、成功時に自動で通知を送る仕組みは持たない。成功も通知したい場合は [Batch job の wrap 実行](#batch-job-の-wrap-実行) で `mitsume run` を用いる。

事前に payload を確認する場合は `--dry-run` を挟む。

```bash
mitsume notify --dry-run "test from $(hostname)"
```

## systemd unit 失敗の捕捉

任意の service unit に `OnFailure=` を 1 行足すと、失敗のたびに Slack へ通知する template unit を共通で利用できる。

対象の unit (`/etc/systemd/system/some-batch.service`):

```ini
[Unit]
Description=some batch job
OnFailure=mitsume-notify@%n.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/some-batch.sh

[Install]
WantedBy=multi-user.target
```

Notifier template unit (`/etc/systemd/system/mitsume-notify@.service`):

```ini
[Unit]
Description=mitsume notify for %i

[Service]
Type=oneshot
EnvironmentFile=/etc/mitsume/webhook.env
ExecStart=/usr/local/bin/mitsume notify "systemd unit %i failed on %H"
```

env file (`/etc/mitsume/webhook.env`、mode 0640、group root):

```ini
MITSUME_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T.../B.../...
```

`%i` には失敗した unit 名 (`some-batch.service`) が、`%H` には host 名が入る。template 1 個で任意の unit の失敗を捕捉できる。

動作確認:

```bash
sudo systemctl daemon-reload
sudo systemctl start some-batch.service
sudo journalctl -u mitsume-notify@some-batch.service -n 20
```

## Batch job の wrap 実行

`mitsume run --name <name> -- <cmd>` で子プロセスを起動し、exit code で成否を判定する。子の stderr 末尾は失敗通知に自動で含まれる (default 20 行または 2KB の小さい方)。

```bash
mitsume run --name nightly-backup -- /usr/local/bin/nightly-backup.sh
```

成功時通知を抑止する場合は `--quiet-on-success` を指定する。

```bash
mitsume run --quiet-on-success --name nightly-backup -- /usr/local/bin/nightly-backup.sh
```

timeout を設ける場合は `--timeout` と `--grace-period` を組み合わせる。`mitsume run` は `--timeout` 超過で子を kill した場合、自身の exit code を `124` にする (GNU `timeout(1)` 慣習)。

```bash
mitsume run --name daily-report --timeout 30m --grace-period 5s -- /usr/local/bin/daily-report.sh
```

systemd の timer から `oneshot` として呼び出す例:

`/etc/systemd/system/nightly-backup.service`:

```ini
[Unit]
Description=nightly backup (wrapped by mitsume)
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
EnvironmentFile=/etc/mitsume/webhook.env
Environment=MITSUME_HEARTBEAT_FILE=/var/lib/mitsume/heartbeat.json
ExecStart=/usr/local/bin/mitsume run --name nightly-backup --timeout 2h -- /usr/local/bin/nightly-backup.sh
ExecStartPost=/usr/local/bin/mitsume ping nightly-backup
```

`/etc/systemd/system/nightly-backup.timer`:

```ini
[Unit]
Description=nightly backup timer

[Timer]
OnCalendar=*-*-* 03:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

`ExecStartPost=` は `ExecStart=` が exit 0 で終わった場合のみ実行される (systemd の仕様)。子が失敗した場合は `run` の失敗通知が Slack に送信され、`ping` は実行されない。この場合 [Cron の走り忘れ検知](#cron-の走り忘れ検知) の `deadman` 側が次サイクルで失踪を検知する。

意図的に失敗する例:

```bash
mitsume run --name test-fail -- /bin/sh -c 'echo "bad thing" >&2; exit 1'
```

Slack には `[mitsume] test-fail failed (run: exit=1)` と stderr 末尾の `bad thing` が届く。

## Dockerfile ENTRYPOINT wrap

container の主プロセスを `mitsume run` の子として起動する。子が exit すれば container も exit し、失敗時は Slack に通知が送信される。

```dockerfile
FROM debian:stable-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=ghcr.io/suecharo/mitsume:v<VERSION> /mitsume /usr/local/bin/mitsume
COPY app /app
ENV MITSUME_HOST=api-prod-01
ENTRYPOINT ["mitsume", "run", "--name", "api-server", "--"]
CMD ["/app/server"]
```

`MITSUME_SLACK_WEBHOOK_URL` は `docker run -e`、compose の `environment:`、secret volume 経由で渡す。image に同梱しない。

`MITSUME_HOST` を container ごとに切り替えることで、通知の `host` field で container を識別できる。決定順は [configuration.md § Host identifier](configuration.md#host-identifier) を参照する。

## Cron の走り忘れ検知

`mitsume ping <job>` で「job が完了した」を heartbeat file に記録し、別プロセスの `mitsume check` (または `watch`) の `deadman` checker が「最後の ping から `within` を超えて古い」場合に Slack へ通知する。

webhook URL を crontab に直書きすると `crontab -l` や `/var/spool/cron/crontabs/*` から漏れやすいため、wrapper script 経由で env file から読み込む。監視側 config の `heartbeat_file` field で heartbeat path を SSOT 化し、`ping` 側は `--heartbeat-file` flag で同じ path を明示する。

wrapper script (`/etc/mitsume/check-cron.sh`、mode 0755):

```bash
#!/bin/sh
. /etc/mitsume/webhook.env
exec /usr/local/bin/mitsume check --config /etc/mitsume/mitsume.json
```

env file (`/etc/mitsume/webhook.env`、mode 0640、group root):

```ini
MITSUME_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T.../B.../...
```

同一 host で cron を 2 本立てる crontab:

```text
# job 完了時に ping を送信する (heartbeat file は `--heartbeat-file` で ping 側に渡す)
0 3 * * * /usr/local/bin/nightly-backup.sh && /usr/local/bin/mitsume ping --heartbeat-file /var/lib/mitsume/heartbeat.json nightly-backup
15 * * * * /usr/local/bin/hourly-etl.sh && /usr/local/bin/mitsume ping --heartbeat-file /var/lib/mitsume/heartbeat.json hourly-etl

# 監視側は 1 時間ごとに check を実行する
0 * * * * /etc/mitsume/check-cron.sh
```

監視側の config (`/etc/mitsume/mitsume.json`):

```json
{
  "host": "batch-host-01",
  "heartbeat_file": "/var/lib/mitsume/heartbeat.json",
  "notify": {
    "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL"
  },
  "checks": [
    {
      "type": "deadman",
      "job": "nightly-backup",
      "expect": { "within": "25h" }
    },
    {
      "type": "deadman",
      "job": "hourly-etl",
      "expect": { "within": "90m" }
    }
  ]
}
```

`expect.within` は「job の実行間隔 + 監視側 `check` の cadence + 若干の余裕」を目安に設定する。daily 実行を hourly の `check` で監視するなら `25h`、hourly 実行を hourly の `check` で監視するなら `2h30m` 程度を選ぶ。

### 別ユーザーで ping と評価を分ける場合

`ping` を実行するユーザーと `check` / `watch` を実行するユーザーが異なる (`ping` は app ユーザー、`check` は systemd 専用ユーザーなど) 場合の運用は次の 3 点を揃える。

- 同じ heartbeat file の path を指す `MITSUME_HEARTBEAT_FILE` を両ユーザーに export する。
- 両ユーザーから同じ heartbeat file を read / write できる permission を用意する (共通 group を作り、file の group ownership と mode 0660 を設定するのが素直である)。
- heartbeat file を置く directory の書き込み権限を、両ユーザーが属する group に付与する。atomic rename に必要な tmp file の作成に用いる。

動作確認:

```bash
# ping で heartbeat file が更新されるかを確認する
mitsume ping nightly-backup
cat /var/lib/mitsume/heartbeat.json

# check が失踪を検知するかを確認する (heartbeat file を古い時刻に書き換えるか、within を短く一時変更する)
mitsume check --dry-run --config /etc/mitsume/mitsume.json
```

## 常駐監視

`mitsume watch` を systemd unit で常駐させる。外部 cron で呼び出す `check` より低レイテンシで動作し、`Restart=on-failure` により mitsume 自身の再起動を systemd に任せる。

system user と env file の準備は [getting-started.md](getting-started.md) の該当節を参照する。

config で `http` / `file` / `deadman` を並べる例 (`/etc/mitsume/mitsume.json`):

```json
{
  "host": "app-prod-01",
  "heartbeat_file": "/var/lib/mitsume/heartbeat.json",
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
      "type": "file",
      "name": "db-backup-artifact",
      "path_glob": "/backup/db-*.dump",
      "expect": {
        "exists": true,
        "mtime_within": "25h",
        "size_min": "100MB"
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

systemd unit (`/etc/systemd/system/mitsume.service`):

```ini
[Unit]
Description=mitsume health monitor
After=network-online.target
Wants=network-online.target
OnFailure=mitsume-notify@%n.service

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

`TimeoutStopSec=15s` は SIGTERM 受信時の best-effort 通知に猶予を残すため設定する。`OnFailure=mitsume-notify@%n.service` は mitsume 自身が起動不能な場合 (config 不正、Webhook env 未定義など) に systemd 側から通知を送るためのものである (template unit は [systemd unit 失敗の捕捉](#systemd-unit-失敗の捕捉) を参照)。

有効化:

```bash
sudo useradd -r -s /usr/sbin/nologin mitsume
sudo install -d -o mitsume -g mitsume -m 0750 /var/lib/mitsume
sudo systemctl daemon-reload
sudo systemctl enable --now mitsume.service
sudo journalctl -u mitsume.service -f
```

config の試運転:

```bash
sudo -u mitsume MITSUME_SLACK_WEBHOOK_URL=dummy \
  /usr/local/bin/mitsume watch --dry-run --config /etc/mitsume/mitsume.json
```

意図的に failure を作る方法: `api.example.com/health` を停止する、または `expect.status` を存在しない値 (`999` など) に一時的に変更して `mitsume check --config ...` を実行する。`check` は confirm burst を含めた 1 サイクル分の評価を完走してから exit する。default 設定では 3 回 × 30s = 約 90 秒で burst 全体の動作を観測できる ([architecture.md § Failure confirmation](architecture.md#failure-confirmation) を参照)。

## Docker container の稼働監視

`container` checker は Docker / podman container の稼働状態を確認する。評価 logic の詳細は [checkers.md § Container checker](checkers.md#container-checker) を、Docker SDK を使わない理由は [architecture.md § Design decisions](architecture.md#design-decisions) を参照する。

前提条件:

- Docker または Podman が host で稼働している。
- Docker socket (`/var/run/docker.sock`) または Podman socket (`$XDG_RUNTIME_DIR/podman/podman.sock`) を読める。
- 監視対象 container と `mitsume watch` が同一 host にある。リモート host の container 監視は本ツールの対象外である ([Non-goals](architecture.md#non-goals) を参照)。

mitsume の実行ユーザーが Docker socket を読めるように、group で権限を付与する。

```bash
sudo usermod -aG docker mitsume
sudo systemctl restart mitsume.service
```

[常駐監視](#常駐監視) の config に `container` を並べる。

```json
{
  "checks": [
    {
      "type": "container",
      "name": "jellyfin",
      "container": "jellyfin",
      "engine": "docker",
      "expect": { "running": true }
    },
    {
      "type": "container",
      "name": "postgres-main",
      "container": "postgres-main",
      "engine": "docker",
      "expect": { "running": true }
    }
  ]
}
```

`engine` を省略した場合は Docker socket → Podman socket の順で自動探索する。socket が起動時に見つからない場合は fail-fast で exit 1 とする。

Docker Compose 構成の container は、Compose の自動命名規則 (`{project}-{service}-{N}`) をそのまま `container` field に指定する。

```json
{
  "type": "container",
  "name": "app-web",
  "container": "myapp-web-1",
  "expect": { "running": true }
}
```

動作確認:

```bash
mitsume check --dry-run --config /etc/mitsume/mitsume.json
docker stop jellyfin
mitsume check --config /etc/mitsume/mitsume.json
```

confirm burst (default 3 × 30s) の完了後に `[mitsume] jellyfin failed (container: state=exited, want running=true)` が Slack に届く。復旧通知は仕様として送信しない。詳細は [notify.md § 通知トリガー](notify.md#通知トリガー) を参照する。

```bash
docker start jellyfin
```

## mitsume 自身の container 化

host に mitsume の binary を配置せず、監視も container 化するパターンである。Docker socket を read-only mount することで同一 host の container を監視する。

`docker-compose.yml`:

```yaml
services:
  mitsume:
    image: ghcr.io/suecharo/mitsume:v<VERSION>
    container_name: mitsume-watch
    restart: unless-stopped
    command: ["watch", "--config", "/etc/mitsume/mitsume.json"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./mitsume.json:/etc/mitsume/mitsume.json:ro
      - mitsume-heartbeat:/var/lib/mitsume
    environment:
      MITSUME_HOST: container-host-01
      MITSUME_HEARTBEAT_FILE: /var/lib/mitsume/heartbeat.json
      MITSUME_SLACK_WEBHOOK_URL: ${MITSUME_SLACK_WEBHOOK_URL}

volumes:
  mitsume-heartbeat:
```

`MITSUME_SLACK_WEBHOOK_URL` は host の `.env` から Compose の変数展開で渡す。`.env` は `.gitignore` に追加する。

heartbeat file は named volume (`mitsume-heartbeat`) に配置し、container の再作成でも消えないようにする。

image を自前 registry で管理したい場合は、上記 compose の `image:` を `build: .` に差し替え、以下の Dockerfile を配置する。挙動は `image:` 指定と同等であり、社内 registry への push や CA 証明書の差し替えが必要なとき以外は不要である。

```dockerfile
FROM debian:stable-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=ghcr.io/suecharo/mitsume:v<VERSION> /mitsume /usr/local/bin/mitsume
ENTRYPOINT ["/usr/local/bin/mitsume"]
CMD ["watch", "--config", "/etc/mitsume/mitsume.json"]
```

## 関連

- [getting-started.md](getting-started.md) — 順を追った初回セットアップ
- [cli.md](cli.md) — subcommand の引数と exit code
- [configuration.md](configuration.md) — 設定 JSON の schema
- [checkers.md](checkers.md) — 各 checker の判定 logic
- [notify.md](notify.md) — Slack payload と通知トリガー
- [heartbeat.md](heartbeat.md) — heartbeat file の schema
- [architecture.md](architecture.md) — 設計判断と Non-goals の背景
