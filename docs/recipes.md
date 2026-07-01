# Recipes

「〜したい」から引く運用パターン集。Slack Webhook の発行や env の置き方は済んでいる前提で書く。ゼロから通しで動かす手順は [getting-started.md](getting-started.md) を参照。

> ここで参照している `v0.1.0` の release archive URL や `ghcr.io/suecharo/mitsume:v0.1.0` image は tag 打刻後に有効になる。tag が未打刻の間はローカル build (`make build`) の binary で試すか、Status に載っている手順で入手する。

- [shell script 末尾で失敗時だけ通知したい](#shell-script-末尾で失敗時だけ通知したい)
- [systemd unit の失敗を丸ごと拾いたい](#systemd-unit-の失敗を丸ごと拾いたい)
- [batch job を wrap して成功 / 失敗どちらも通知したい](#batch-job-を-wrap-して成功--失敗どちらも通知したい)
- [Dockerfile の ENTRYPOINT で run に wrap したい](#dockerfile-の-entrypoint-で-run-に-wrap-したい)
- [cron の走り忘れを検知したい](#cron-の走り忘れを検知したい)
- [HTTP endpoint / file / container を常駐で見張りたい](#http-endpoint--file--container-を常駐で見張りたい)
- [Docker container の稼働を監視したい](#docker-container-の稼働を監視したい)
- [mitsume 自身を container 化したい](#mitsume-自身を-container-化したい)

前提となる env は全 recipe 共通で以下。

```bash
export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T.../B.../...'
```

任意の env 名を使うなら `--slack-webhook-url-env <NAME>` を渡す ([notify.md](notify.md#秘密情報の扱い) を参照)。

## shell script 末尾で失敗時だけ通知したい

`mitsume notify` を `||` の後ろに置く。設定 JSON も heartbeat file も要らない。

```bash
#!/bin/bash
set -euo pipefail

/usr/local/bin/some-batch.sh || {
  /usr/local/bin/mitsume notify "some-batch failed on $(hostname): exit $?"
  exit 1
}
```

「成功も失敗も両方通知したい」なら [batch job を wrap して...](#batch-job-を-wrap-して成功--失敗どちらも通知したい) に寄せる。`notify` は明示的に呼んだときに 1 通投げるサブコマンドなので、成功系のロジックは持たない。

事前に payload を確認するには `--dry-run` を挟む。

```bash
mitsume notify --dry-run "test from $(hostname)"
```

## systemd unit の失敗を丸ごと拾いたい

任意の service unit に `OnFailure=` を 1 行足せば、失敗ごとに Slack に投げる template unit を共通で使える。

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

notifier template unit (`/etc/systemd/system/mitsume-notify@.service`):

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

`%i` に失敗した unit 名 (`some-batch.service`) が入り、`%H` に host 名が入る。template 1 個で任意の unit の失敗を拾える。

失敗テスト。

```bash
sudo systemctl daemon-reload
sudo systemctl start some-batch.service
sudo journalctl -u mitsume-notify@some-batch.service -n 20
```

## batch job を wrap して成功 / 失敗どちらも通知したい

`mitsume run --name <name> -- <cmd>` で子プロセスを起動して、exit code で成否を判定する。子の stderr 末尾は失敗通知に自動で乗る (デフォルト 20 行 or 2KB の小さい方)。

```bash
mitsume run --name nightly-backup -- /usr/local/bin/nightly-backup.sh
```

成功時通知を消すなら `--quiet-on-success`。

```bash
mitsume run --quiet-on-success --name nightly-backup -- /usr/local/bin/nightly-backup.sh
```

timeout を設けるなら `--timeout` + `--grace-period`。timeout kill で exit code は `124` (GNU `timeout(1)` 慣習)。

```bash
mitsume run --name daily-report --timeout 30m --grace-period 5s -- /usr/local/bin/daily-report.sh
```

systemd の timer から `oneshot` として叩く例。

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

`ExecStartPost=` は `ExecStart=` の exit 0 のときだけ走る (systemd の仕様)。子が失敗すれば `run` の失敗通知が Slack に飛び、`ping` は打たれない。この場合 [cron の走り忘れを検知したい](#cron-の走り忘れを検知したい) の deadman 側が次サイクルで失踪を検知する。

意図的に失敗するコマンドで挙動を見るには:

```bash
mitsume run --name test-fail -- /bin/sh -c 'echo "bad thing" >&2; exit 1'
```

Slack に「`[mitsume] test-fail failed (run: exit 1)`」と、stderr 末尾の `bad thing` が届く。

## Dockerfile の ENTRYPOINT で run に wrap したい

container の主プロセスを `mitsume run` の子にする。子が exit すれば container も exit し、失敗時は Slack に通知が飛ぶ。

```dockerfile
FROM debian:slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=ghcr.io/suecharo/mitsume:v0.1.0 /mitsume /usr/local/bin/mitsume
COPY app /app
ENV MITSUME_HOST=api-prod-01
ENTRYPOINT ["mitsume", "run", "--name", "api-server", "--"]
CMD ["/app/server"]
```

`MITSUME_SLACK_WEBHOOK_URL` は `docker run -e` / compose の `environment:` / secret volume で渡す。image に焼き込まない。

`MITSUME_HOST` を container ごとに切ることで、通知の `host` フィールドで区別できる。決定順は [notify.md](notify.md#payload-形式) を参照。

## cron の走り忘れを検知したい

`mitsume ping <job>` で「job が完了した」を heartbeat file に打刻し、別プロセスの `mitsume check` (または `watch`) の `deadman` checker が「最後の ping から `within` を超えて古い」を Slack に投げる。

同一 host で cron を 2 本立てる例:

```text
# job 完了時に ping を打つ
0 3 * * * MITSUME_HEARTBEAT_FILE=/var/lib/mitsume/heartbeat.json /usr/local/bin/nightly-backup.sh && /usr/local/bin/mitsume ping nightly-backup
15 * * * * MITSUME_HEARTBEAT_FILE=/var/lib/mitsume/heartbeat.json /usr/local/bin/hourly-etl.sh && /usr/local/bin/mitsume ping hourly-etl

# 評価側は 1 時間に 1 回
0 * * * * MITSUME_HEARTBEAT_FILE=/var/lib/mitsume/heartbeat.json MITSUME_SLACK_WEBHOOK_URL=https://... /usr/local/bin/mitsume check --config /etc/mitsume/mitsume.json
```

評価側の config (`/etc/mitsume/mitsume.json`):

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

`expect.within` は「cron の実行間隔 + 1 サイクル分の buffer」を目安に置く。1 日 1 回の cron なら `25h`、1 時間ごとなら `90m` のように。

ping 側と評価側でユーザーが違う場合 (`ping` は app ユーザー、`check` は systemd 専用ユーザー、など) は、両者から同じ heartbeat file を read / write できる permission と、同じ path を指す `MITSUME_HEARTBEAT_FILE` を用意する。

動作確認:

```bash
# ping で heartbeat file が更新されるか
mitsume ping nightly-backup
cat /var/lib/mitsume/heartbeat.json

# check が失踪を検知するか (heartbeat file を古い時刻に書き換えるか、within を短く一時変更)
mitsume check --dry-run --config /etc/mitsume/mitsume.json
```

## HTTP endpoint / file / container を常駐で見張りたい

`mitsume watch` を systemd unit で常駐させる。外部 cron で叩く `check` より低レイテンシで、`Restart=on-failure` で mitsume 自身の再起動を systemd に任せる。

system user と env file を用意する ([getting-started.md](getting-started.md#step-7-watch-で常駐監視する-systemd) の前段と同じ)。

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

`TimeoutStopSec=15s` は SIGTERM 時の best-effort 通知に猶予を残すため。`OnFailure=mitsume-notify@%n.service` で mitsume 自身が起動不能なとき (config 不正 / webhook env 未定義) の通知を、systemd 側から拾う (template unit は [systemd unit の失敗を丸ごと拾いたい](#systemd-unit-の失敗を丸ごと拾いたい) を参照)。

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

意図的な failure の作り方: `api.example.com/health` を止める、あるいは `expect.status` を存在しない値 (`999` など) に一時的に書き換えて `mitsume check --config ...` を回す (`check` は 1 回で終わるので confirm burst のテストに向く)。

## Docker container の稼働を監視したい

`container` checker は Docker Engine API の `/containers/<container>/json` を unix socket 経由で直接叩き、`.State.Status == "running"` を評価する。Docker SDK には依存しない。

前提として:

- Docker (or Podman) が host で稼働している
- Docker socket (`/var/run/docker.sock`) または Podman socket (`$XDG_RUNTIME_DIR/podman/podman.sock`) が読める
- 監視対象 container と `mitsume watch` は同一 host にある (リモート host の監視は scope 外)

`mitsume` 実行 user が docker socket を読めるように、group で権限を付与する。

```bash
sudo usermod -aG docker mitsume
sudo systemctl restart mitsume.service
```

「HTTP endpoint / file / container を...」の config に `container` を並べる。

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

`engine` を省略すると docker socket → podman socket の順で自動探索する。socket が起動時に見つからなければ fail-fast で exit 1 する。

compose 構成の container は、compose の自動命名規則 (`{project}-{service}-{N}`) をそのまま `container` フィールドに書く。

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

`confirm.checks` × `confirm.interval` (default 3 × 30s) の burst を通ってから「`[mitsume] jellyfin failed (container: not running)`」が届く。復旧通知は仕様として出さない (詳細は [notify.md](notify.md#発火モデル))。

```bash
docker start jellyfin
```

## mitsume 自身を container 化したい

host に mitsume binary を置かず、監視まで container 化するパターン。docker socket を read-only mount して同一 host の container を見る (リモートは変わらず scope 外)。

`docker-compose.yml`:

```yaml
services:
  mitsume:
    image: ghcr.io/suecharo/mitsume:v0.1.0
    container_name: mitsume-watch
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./mitsume.json:/etc/mitsume/mitsume.json:ro
      - mitsume-heartbeat:/var/lib/mitsume
    environment:
      MITSUME_HOST: container-host-01
      MITSUME_SLACK_WEBHOOK_URL: ${MITSUME_SLACK_WEBHOOK_URL}

volumes:
  mitsume-heartbeat:
```

mitsume 焼き込み image の Dockerfile:

```dockerfile
FROM debian:slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=ghcr.io/suecharo/mitsume:v0.1.0 /mitsume /usr/local/bin/mitsume
ENTRYPOINT ["/usr/local/bin/mitsume"]
CMD ["watch", "--config", "/etc/mitsume/mitsume.json"]
```

`MITSUME_SLACK_WEBHOOK_URL` は host の `.env` から compose interpolation で渡す。`.env` は `.gitignore` に入れる。

heartbeat file は named volume (`mitsume-heartbeat`) に置いて container 再作成でも消えないようにする。

## 関連

- [getting-started.md](getting-started.md) — 順を追った初回セットアップ
- [cli.md](cli.md) — サブコマンドの引数と exit code
- [configuration.md](configuration.md) — 設定 JSON schema
- [checkers.md](checkers.md) — 各 checker の判定ロジック
- [notify.md](notify.md) — Slack payload と発火モデル
- [heartbeat.md](heartbeat.md) — heartbeat file の schema
