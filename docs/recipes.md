# レシピ

目的ごとに、mitsume の組み込み方をまとめる。どれも Slack の Incoming Webhook が 1 つあれば始められる。各 subcommand の細かい挙動は [cli.md](cli.md) にある。

## 準備

### Slack の Webhook を作る

1. [Slack API: Your Apps](https://api.slack.com/apps) で App を作る
2. Incoming Webhooks を有効にし、通知先の channel を選んで Webhook URL を発行する
3. 投稿者の名前とアイコンを変えたいときは、App の Basic Information にある Display Information で変える

### Webhook URL を環境変数に置く

手元で試すときは shell で export し、1 通送って届くかを確かめる。

```bash
export MITSUME_SLACK_WEBHOOK_URL='https://hooks.slack.com/services/T.../B.../...'
mitsume notify "hello from $(hostname)"
```

systemd や cron から使うときは、専用のユーザーを作り、Webhook URL を root とそのユーザーだけが読める env file に書く。以降の例はこの準備ができている前提で書く。

```bash
sudo useradd -r -s /usr/sbin/nologin mitsume
sudo install -d -m 0755 /etc/mitsume
sudo install -d -o mitsume -g mitsume -m 0750 /var/lib/mitsume
sudo install -m 0640 -o root -g mitsume /dev/null /etc/mitsume/webhook.env
sudoedit /etc/mitsume/webhook.env
```

`/etc/mitsume/webhook.env`:

```ini
MITSUME_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T.../B.../...
```

## script の失敗を通知する

### 失敗したときだけ通知する

script の中で、失敗したら `mitsume notify` を呼ぶ。設定 JSON は要らない。

```bash
/usr/local/bin/some-batch.sh || {
  rc=$?
  mitsume notify "some-batch failed on $(hostname): exit $rc"
  exit "$rc"
}
```

`rc=$?` で exit code を先に取っておく。`"exit $?"` と書くと、`$(hostname)` を展開した時点で `$?` が 0 に変わり、いつも `exit 0` と通知される。

### 成功も失敗も通知する

`mitsume run` でコマンドを包むと、終わったときに結果を通知する。失敗の通知には stderr の末尾が付くので、原因の手がかりが Slack に残る。成功を通知しないなら `--quiet-on-success` を付ける。

```bash
mitsume run --name nightly-backup --timeout 2h -- /usr/local/bin/nightly-backup.sh
```

わざと失敗させると、`[mitsume] test-fail failed (run: exit=1)` と `bad thing` が届く。

```bash
mitsume run --name test-fail -- /bin/sh -c 'echo "bad thing" >&2; exit 1'
```

### systemd の unit の失敗を通知する

通知用の template unit を 1 つ作っておき、見張りたい unit に `OnFailure=` を 1 行足す。

`/etc/systemd/system/mitsume-notify@.service`:

```ini
[Unit]
Description=mitsume notify for %i

[Service]
Type=oneshot
EnvironmentFile=/etc/mitsume/webhook.env
ExecStart=/usr/local/bin/mitsume notify "systemd unit %i failed on %H"
```

見張りたい unit (`/etc/systemd/system/some-batch.service`):

```ini
[Unit]
Description=some batch job
OnFailure=mitsume-notify@%n.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/some-batch.sh
```

`%i` には失敗した unit の名前、`%H` には host 名が入る。

### container の主プロセスを包む

`ENTRYPOINT` で `mitsume run` を通すと、主プロセスが終わったときに通知が届く。mitsume が受けた SIGTERM は子に転送されるので、`docker stop` もふだんどおり効く。

```dockerfile
FROM debian:stable-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=ghcr.io/suecharo/mitsume:v<VERSION> /mitsume /usr/local/bin/mitsume
COPY app /app
ENTRYPOINT ["mitsume", "run", "--name", "api-server", "--"]
CMD ["/app/server"]
```

```bash
docker run --hostname api-prod-01 -e MITSUME_SLACK_WEBHOOK_URL my-app
```

Webhook URL は image に入れず、起動するときに渡す。通知の host は container の hostname になるので、`--hostname` (compose なら `hostname:`) で分かる名前を付ける。

## cron job の走り忘れに気づく

`run` や `notify` は、job が起動しなければ何も送れない。cron の設定ミスや host の停止で job が走らなかったことは、dead-man's switch で見つける ([heartbeat.md](heartbeat.md))。job が終わったら `ping` し、別の cron で `check` を呼んで、`ping` が途絶えていないかを評価する。

監視側の設定 JSON (`/etc/mitsume/mitsume.json`):

```json
{
  "heartbeat_file": "/var/lib/mitsume/heartbeat.json",
  "notify": { "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL" },
  "defaults": { "interval": "1h" },
  "checks": [
    { "type": "deadman", "job": "nightly-backup", "expect": { "within": "25h" } },
    { "type": "deadman", "job": "hourly-etl", "expect": { "within": "2h30m" } }
  ]
}
```

`check` を呼ぶ wrapper (`/etc/mitsume/check-cron.sh`、mode 0755):

```sh
#!/bin/sh
set -a
. /etc/mitsume/webhook.env
set +a
exec /usr/local/bin/mitsume check --config /etc/mitsume/mitsume.json
```

`mitsume` ユーザーの crontab (`sudo crontab -u mitsume -e`)。job を別のユーザーで動かすときは [ping と評価を別のユーザーで動かす](#ping-と評価を別のユーザーで動かす) も見る。

```text
0 3 * * *  /usr/local/bin/nightly-backup.sh && /usr/local/bin/mitsume ping --heartbeat-file /var/lib/mitsume/heartbeat.json nightly-backup
15 * * * * /usr/local/bin/hourly-etl.sh && /usr/local/bin/mitsume ping --heartbeat-file /var/lib/mitsume/heartbeat.json hourly-etl
0 * * * *  /etc/mitsume/check-cron.sh
```

- Webhook URL を crontab に直接書かないのは、`crontab -l` や cron の spool のファイルから読めてしまうからである
- `ping` 側は `--heartbeat-file` で path を明示する。`VAR=value cmd1 && cmd2` と書いても `VAR` は `cmd1` にしか渡らないので、env で渡すと `ping` に届かない
- job の失敗そのものも知りたいときは、`mitsume run --name nightly-backup -- /usr/local/bin/nightly-backup.sh && mitsume ping ...` のように `run` で包む
- 常駐させたいときは、`check` の cron の代わりに、次の節の `watch` の設定に `deadman` を足す

動作を確かめるには、`ping` してから `check` を `--dry-run` で呼ぶ。`ping` した `nightly-backup` は期限の中なので何も出ず、まだ `ping` していない `hourly-etl` は 1 分ほどで `never pinged` の failure の payload が stderr に出る。

```bash
sudo -u mitsume /usr/local/bin/mitsume ping --heartbeat-file /var/lib/mitsume/heartbeat.json nightly-backup
sudo -u mitsume env MITSUME_SLACK_WEBHOOK_URL=dummy /usr/local/bin/mitsume check --dry-run --config /etc/mitsume/mitsume.json
```

期限切れを試すときは、`last_ping_at` を古い時刻に書き換えてから同じ `check` を呼ぶ。

```bash
sudo jq '.jobs["nightly-backup"].last_ping_at = "2026-01-01T00:00:00Z"' /var/lib/mitsume/heartbeat.json \
  | sudo -u mitsume tee /var/lib/mitsume/heartbeat.json.new > /dev/null
sudo -u mitsume mv /var/lib/mitsume/heartbeat.json.new /var/lib/mitsume/heartbeat.json
```

### ping と評価を別のユーザーで動かす

job を動かすユーザー (ここでは `app`) と、`check` / `watch` を動かす `mitsume` ユーザーが違うときは、1 つの heartbeat file を両方から読み書きできるようにする。

```bash
sudo usermod -aG mitsume app
sudo chmod 2770 /var/lib/mitsume
echo '{"jobs": {}}' | sudo tee /var/lib/mitsume/heartbeat.json > /dev/null
sudo chown mitsume:mitsume /var/lib/mitsume/heartbeat.json
sudo chmod 0660 /var/lib/mitsume/heartbeat.json
```

- `ping` はディレクトリに一時ファイルを作ってから置き換えるので、ディレクトリにも group の書き込み権限が要る
- ディレクトリに setgid (`2770` の `2`) を付けると、`app` が置き換えたファイルも group が `mitsume` のままになる
- heartbeat file は group で読み書きできる mode で先に作っておく。`ping` は、既にあるファイルなら mode を引き継ぐが、新しく作るときは本人しか読み書きできない mode にする ([heartbeat.md](heartbeat.md#書き込み-ping))

## systemd で常駐監視する

`mitsume watch` を systemd の service として動かす。`check` を cron で呼ぶのと違い、check ごとに `interval` を変えられ、mitsume が落ちても systemd が起動し直す。

`/etc/mitsume/mitsume.json`:

```json
{
  "heartbeat_file": "/var/lib/mitsume/heartbeat.json",
  "notify": { "webhook_url_env": "MITSUME_SLACK_WEBHOOK_URL" },
  "defaults": { "interval": "1h", "timeout": "10s" },
  "checks": [
    {
      "type": "http",
      "name": "api-health",
      "url": "https://api.example.com/health",
      "interval": "10m",
      "expect": { "status": 200, "body_jsonpath": [{ "path": "$.status", "equals": "ok" }] }
    },
    {
      "type": "file",
      "name": "db-backup",
      "path_glob": "/backup/db-*.dump",
      "expect": { "exists": true, "mtime_within": "25h", "size_min": "100MB" }
    },
    { "type": "deadman", "job": "nightly-backup", "expect": { "within": "25h" } }
  ]
}
```

`/etc/systemd/system/mitsume.service`:

```ini
[Unit]
Description=mitsume
After=network-online.target
Wants=network-online.target
OnFailure=mitsume-notify@%n.service
StartLimitIntervalSec=10min
StartLimitBurst=5

[Service]
Type=simple
User=mitsume
Group=mitsume
EnvironmentFile=/etc/mitsume/webhook.env
ExecStart=/usr/local/bin/mitsume watch --config /etc/mitsume/mitsume.json
Restart=on-failure
RestartSec=10s
TimeoutStopSec=15s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

- `Restart=on-failure` で、落ちたら 10 秒後に起動し直す。`StartLimitIntervalSec` と `StartLimitBurst` を書くと、10 分に 5 回を超えたところで起動し直すのをやめ、unit を失敗にする。書かないと systemd の既定 (10 秒に 5 回) はこの間隔では効かず、設定の誤りで起動できないときや panic で落ち続けるとき (再起動のたびに panic の通知が届く) に、いつまでも繰り返す
- unit が失敗すると、`OnFailure=` の [template unit](#systemd-の-unit-の失敗を通知する) から通知が届く
- `TimeoutStopSec` は、止めるときの通知を送り終えるまでの猶予である

起動する前に `check --dry-run` で設定を確かめ、それから有効にする。

```bash
sudo -u mitsume env MITSUME_SLACK_WEBHOOK_URL=dummy /usr/local/bin/mitsume check --dry-run --config /etc/mitsume/mitsume.json
sudo systemctl daemon-reload
sudo systemctl enable --now mitsume.service
journalctl -u mitsume.service -f
```

失敗したときの通知を見たいときは、`expect.status` を実際には返らない値 (`418` など) に変えて `check --dry-run` を呼ぶ。`confirm` の再確認を待つので、payload が出るまで 1 分ほどかかる。すぐに見たいときは、その check に `"confirm": false` を足す。

## container を監視する

`container` checker で、同じ host の Docker / Podman の container が動いているかを見る ([checkers.md](checkers.md#container))。mitsume を動かすユーザーが socket を読めるようにしてから、設定 JSON に足す。

```bash
sudo usermod -aG docker mitsume
sudo systemctl restart mitsume.service
```

```json
{ "type": "container", "container": "jellyfin", "expect": { "running": true } }
```

Docker Compose の container は、`myapp-web-1` のような compose が付けた名前で書く。

container を止めてから `check --dry-run` を呼ぶと、1 分ほどで `[mitsume] jellyfin failed (container: state=exited, want running=true)` の payload が出る。`docker start jellyfin` で戻しても、復旧の通知は届かない。

```bash
docker stop jellyfin
sudo -u mitsume env MITSUME_SLACK_WEBHOOK_URL=dummy /usr/local/bin/mitsume check --dry-run --config /etc/mitsume/mitsume.json
```

## mitsume を container で動かす

host に binary を置かず、`watch` も container で動かす。

```yaml
services:
  mitsume:
    image: ghcr.io/suecharo/mitsume:v<VERSION>
    hostname: container-host-01
    restart: unless-stopped
    command: ["watch", "--config", "/etc/mitsume/mitsume.json"]
    environment:
      MITSUME_SLACK_WEBHOOK_URL: ${MITSUME_SLACK_WEBHOOK_URL}
      MITSUME_HEARTBEAT_FILE: /var/lib/mitsume/heartbeat.json
    volumes:
      - ./mitsume.json:/etc/mitsume/mitsume.json:ro
      - /var/lib/mitsume:/var/lib/mitsume:ro
      - /var/run/docker.sock:/var/run/docker.sock
```

- `MITSUME_SLACK_WEBHOOK_URL` は compose ファイルの隣の `.env` に書き、compose に展開させる。`.env` は git に入れない
- 通知の host は `hostname:` で付けた名前になる
- `/var/lib/mitsume` は host の cron が `ping` する heartbeat file の置き場所で、`watch` は読むだけなので read-only で mount する。`deadman` を使わないなら要らない
- Docker の socket は `container` checker を使うときだけ要る。socket に触れる container は host の Docker を何でも操作できることに気をつける

## うまく動かないとき

- 通知が届かない: `mitsume notify --dry-run "test"` で payload が出るかを見る。送ったときに 4xx のエラーが出るなら Webhook URL の誤りか、Webhook が無効になっている。5xx なら Slack 側の問題で、mitsume がやり直す
- `check` や `watch` がすぐ exit 1 で終わる: stderr (`journalctl -u mitsume.service -n 50`) に `checks[2]: ...` のような形で原因が出る
- `ping` が exit 1 で終わる: heartbeat file の path が決まっていない。`--heartbeat-file` を付ける
- `deadman` が `never pinged` で失敗し続ける: `ping` する側と監視する側で heartbeat file の path が違う
- 止めても停止の通知が来ない: SIGKILL で止められたか、送信に失敗している ([notify.md](notify.md#届かない通知))
