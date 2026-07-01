# heartbeat file

`mitsume.heartbeat.json` は dead-man's switch の唯一の永続状態を持つ JSON text file。`mitsume ping <job>` が最終生存時刻を書き込み、`mitsume check` / `mitsume watch` の `deadman` checker が read only で読む。中身は per-`job` の `last_ping_at` だけで、能動 checker の状態や通知履歴は入らない。`cat` / `jq` で人間が読める形を保ち、SQLite などの DB は使わない。

deadman checker と `ping` の全体像は [architecture.md](architecture.md)、`ping` の呼び方は [cli.md](cli.md#mitsume-ping) を参照。

## 目的

- `job` から最後に生存信号が届いた時刻を、`ping` と評価側 (`check` / `watch`) の間で受け渡す。
- 能動 checker (`http` / `file` / `cmd` / `container`) の状態は持たない。能動 checker は毎 `interval` で外部世界を再評価する。
- 通知履歴・前回 alert 時刻・`consecutive_failures` などの debounce 用状態も持たない。通知は失敗のたびに 1 通で debounce しないため、保持する必要がない。

## 場所

heartbeat file のパスはこの順で探し、最初に見つかった 1 個を使う。書き手 (`ping`) と読み手 (`check` / `watch`) の両方に共通する。

1. `--heartbeat-file <path>` (CLI で明示指定)
2. `$MITSUME_HEARTBEAT_FILE` (env)
3. 設定 JSON の `heartbeat_file` フィールド
4. 設定 JSON と同じディレクトリの隣接ファイル (`/foo/bar/mitsume.json` → `/foo/bar/mitsume.heartbeat.json`。basename の `.json` を `.heartbeat.json` に置換)
5. いずれにも該当しない → 起動時にエラーで exit する。ホームディレクトリや XDG ディレクトリに勝手にファイルを作らない。

具体例:

| 前提 | 結果として使う heartbeat file |
|---|---|
| `mitsume ping backup --heartbeat-file /var/lib/mitsume/state.json` | `/var/lib/mitsume/state.json` |
| `MITSUME_HEARTBEAT_FILE=/data/mitsume.json mitsume watch` | `/data/mitsume.json` |
| `mitsume.json` 内に `"heartbeat_file": "/srv/hb.json"`、CLI / env なし | `/srv/hb.json` |
| `mitsume watch --config /opt/app/mitsume.json`、設定内 `heartbeat_file` なし、CLI / env なし | `/opt/app/mitsume.heartbeat.json` |
| `ping` / `watch` 共に設定 JSON なし、CLI / env なし | 起動時にエラーで exit |

`ping` を打つプロセスと `watch` を動かすプロセスが別ユーザー (例: `watch` は systemd 専用ユーザー、`ping` は app ユーザー) の場合、両者で `$MITSUME_HEARTBEAT_FILE` を export して同じパスを指す。ファイルの owner / mode は運用側で調整する。

## JSON schema

heartbeat file の中身はこの形に固定する。

```json
{
  "jobs": {
    "backup-nightly": { "last_ping_at": "2026-06-30T12:34:56Z" },
    "renew-cert":     { "last_ping_at": "2026-07-01T09:00:00+09:00" }
  }
}
```

- top-level は `jobs` object のみ。
- `jobs.<job>` の key は `mitsume ping <job>` で与えられる `job` 識別子 (命名規則は [configuration.md](configuration.md) を参照)。
- `jobs.<job>.last_ping_at` は RFC3339 timestamp。`Z` (UTC) と `+09:00` などの offset 表記の両方を受け付ける。
- `version` フィールドは持たない。
- これら以外のフィールドは書かない。特に、能動 checker の前回評価結果・`consecutive_failures`、通知履歴、前回 alert 時刻、debounce 用状態、起動プロセスの PID や lock 情報は入らない。

`jobs` が空 (`"jobs": {}`) の状態も valid。ファイル自体はあるが `ping` がまだ 1 度も呼ばれていない状態がこれに当たる。

parse は改行 / インデントの有無を問わない。書き込みは key で sort した pretty print (2 space indent) で出す。`cat` / `jq` / `git diff` での読みやすさを優先する。

## 書き込みモデル

`mitsume ping <job>` を呼ぶたびに、対応する `jobs.<job>.last_ping_at` を現在時刻で更新する。手順:

1. heartbeat file 全体を read (存在しなければ `{"jobs": {}}` を出発点とする)
2. in-memory で `jobs.<job>.last_ping_at` を更新 (該当 `job` の entry が無ければ新規追加する)
3. 同一ディレクトリの tmp file (例: `mitsume.heartbeat.json.tmp.<pid>`) に完全な JSON を write
4. `os.Rename` で tmp file を heartbeat file に atomic rename

保証と制約:

- atomic rename: POSIX rename は同一 filesystem 内で atomic。tmp file を heartbeat file と同じディレクトリに置くことで、`/tmp` が別 mount point でも rename が失敗しない。
- 並行 `ping` の race: 同時に複数の `ping` プロセスが起動しても、最後の rename が勝つ。数秒以内に複数の `ping` が同じ job に届くケースは実運用では起きない想定 (deadman の `interval` は 1h 以上が現実的)。
- 未知の `job` への `ping`: heartbeat file に entry が無ければ新規追加する。job の再作成や新規追加後の初回 `ping` に耐えるため、存在確認はしない。
- 設定 JSON との整合検査は `ping` 側でしない。`ping` は heartbeat file への write のみを担当する。設定 JSON に定義されていない `job` を `ping` してもエラーにしない (deadman を含まない config で `ping` だけ使う運用パターンがある。[recipes.md](recipes.md) 参照)。

最小の状態遷移を示す。初期状態:

```json
{
  "jobs": {
    "backup-nightly": { "last_ping_at": "2026-06-29T03:00:01+09:00" }
  }
}
```

`mitsume ping backup-nightly` を 2026-06-30T03:00:02+09:00 に呼ぶと、既存 entry の `last_ping_at` を上書きする。

```json
{
  "jobs": {
    "backup-nightly": { "last_ping_at": "2026-06-30T03:00:02+09:00" }
  }
}
```

続けて未登録の job に対して `mitsume ping renew-cert` を呼ぶと、新規 entry を追加する。

```json
{
  "jobs": {
    "backup-nightly": { "last_ping_at": "2026-06-30T03:00:02+09:00" },
    "renew-cert":     { "last_ping_at": "2026-06-30T04:15:00+09:00" }
  }
}
```

書き込むのは `ping` だけ。`check` / `watch` は heartbeat file を read only で参照する。

## 読み込みモデル

`deadman` checker の評価は `check` / `watch` の評価サイクル内でこの手順を踏む。

1. heartbeat file を read
2. `jobs.<job>.last_ping_at` を取得
3. 現在時刻との差分を `expect.within` (duration) と比較

判定:

| 状態 | 扱い |
|---|---|
| `jobs.<job>` entry が存在する、かつ現在時刻 - `last_ping_at` <= `expect.within` | success |
| `jobs.<job>` entry が存在する、かつ現在時刻 - `last_ping_at` > `expect.within` | failure ([notify.md](notify.md#発火モデル) の `confirm` burst に入る) |
| `jobs.<job>` entry が存在しない (heartbeat file 自体が未存在の場合を含む) | failure (`ping` が一度も届いておらず生存未確認) |
| heartbeat file の read / parse に失敗する (permission denied / JSON parse error など) | 起動時に fail-fast (evaluation loop に入る前) |

deadman checker の `interval` は `watch` で次の評価タイミングを決める。判定は heartbeat file の read だけで完結し実測コストがゼロなので、`interval` を短くしてもコストは増えない。`check` は 1 回で終わるので `interval` を無視する。詳細は [checkers.md](checkers.md#deadman-checker) を参照。

`check` / `watch` は heartbeat file を書き換えない。deadman が failure を検知しても heartbeat file には何も書かない。前回の状態を持たないため、`watch` プロセスを再起動しても deadman の判定結果は heartbeat file の内容から一意に決まる。

heartbeat file を読むのは各評価サイクルの起点で 1 度だけ。同じサイクル内の複数 deadman 評価は同じ snapshot を共有する。

## `--dry-run` 時の挙動

`--dry-run` は全サブコマンドで共通のフラグで、heartbeat file を一切触らない。

| サブコマンド | `--dry-run` 時の heartbeat file への挙動 |
|---|---|
| `mitsume ping` | 既存 heartbeat file を read (in-memory update に必要) し、tmp write と atomic rename を skip する。更新予定の内容は stderr に出力する |
| `mitsume check` / `mitsume watch` | heartbeat file を read only で参照して deadman を通常どおり評価する。Slack への通知 payload は送信せず stderr に出力する |
| `mitsume notify` / `mitsume run` | 通常運用でも触らない (`--dry-run` の有無で挙動は変わらない) |

本番の heartbeat file を指した状態で `mitsume check --dry-run` を叩いても、deadman evaluation が本番の `last_ping_at` を書換えることはない。試運転で heartbeat file に副作用を残す事故を防ぐための挙動。

## 関連

- [architecture.md](architecture.md) — checker / notify / heartbeat file の 3 直交軸と全体像
- [cli.md](cli.md#mitsume-ping) — `ping` / `check` / `watch` の CLI インターフェイスと共通フラグ (`--heartbeat-file` / `--dry-run`)
- [configuration.md](configuration.md) — 設定 JSON schema、`heartbeat_file` フィールド、識別子 (`job` / `name`) の規則
- [checkers.md](checkers.md#deadman-checker) — `deadman` checker の `expect.within` と評価タイミング
- [notify.md](notify.md#発火モデル) — 失敗確信モデル (`confirm`) と通知トリガー
- [recipes.md](recipes.md) — cron から `ping` を打つ運用パターン
