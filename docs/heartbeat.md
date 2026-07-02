# Heartbeat file

`mitsume.heartbeat.json` は [dead-man's switch](architecture.md#用語) の唯一の永続状態を保持する JSON text file である。`mitsume ping <job>` が per-job の最終生存時刻を書き込み、`mitsume check` と `mitsume watch` の `deadman` checker が read-only で読む。

保持する情報は per-job の `last_ping_at` のみである。[active checker](architecture.md#用語) の状態、通知履歴、[confirm burst](architecture.md#用語) の途中経過 (連続失敗回数など)、debounce 用の中間状態、いずれも保持しない。この制約の理由は [architecture.md § Design decisions](architecture.md#design-decisions) を、`ping` の CLI は [cli.md § ping](cli.md#mitsume-ping) を参照する。

## File format

heartbeat file の JSON は次の形に固定する。

```json
{
  "jobs": {
    "backup-nightly": { "last_ping_at": "2026-06-30T12:34:56Z" },
    "renew-cert":     { "last_ping_at": "2026-07-01T09:00:00+09:00" }
  }
}
```

- top-level は `jobs` object のみである。
- `jobs.<job>` の key は `mitsume ping <job>` で与えられる `job` 識別子である。命名規則は [configuration.md](configuration.md) を参照する。
- `jobs.<job>.last_ping_at` は RFC3339 timestamp である。`Z` (UTC) と `+09:00` などの offset 表記の両方を受け付ける。
- `version` field は持たない。
- 上記以外の field は書き込まない。特に、active checker の前回評価結果、`consecutive_failures`、通知履歴、前回 alert 時刻、debounce 用状態、起動プロセスの PID、lock 情報は含めない。

`jobs` が空 (`"jobs": {}`) の状態も valid である。file 自体は存在するが `ping` が一度も呼ばれていない状態がこれに当たる。

parse は改行 / インデントの有無を問わない。書き込みは key で sort した pretty print (2 space indent) で出す。`cat` / `jq` / `git diff` での可読性を優先する。

## File location

heartbeat file の path はこの順で解決し、最初に見つかった 1 個を使う。書き手 (`ping`) と読み手 (`check` / `watch`) の両方で同じ順序を適用する。

1. `--heartbeat-file <path>` (CLI で明示指定)
2. `$MITSUME_HEARTBEAT_FILE` (env)
3. 設定 JSON の `heartbeat_file` field
4. 設定 JSON と同じ directory の隣接 file (`/foo/bar/mitsume.json` → `/foo/bar/mitsume.heartbeat.json`。basename の `.json` を `.heartbeat.json` に置換する)

上記のいずれにも該当しない場合は起動時にエラーで exit する。ホーム directory や XDG directory に自動で file を作成しない。

具体例:

| 前提 | 使用する heartbeat file |
|---|---|
| `mitsume ping backup --heartbeat-file /var/lib/mitsume/state.json` | `/var/lib/mitsume/state.json` |
| `MITSUME_HEARTBEAT_FILE=/data/mitsume.json mitsume watch` | `/data/mitsume.json` |
| `mitsume.json` 内に `"heartbeat_file": "/srv/hb.json"`、CLI / env なし | `/srv/hb.json` |
| `mitsume watch --config /opt/app/mitsume.json`、設定内 `heartbeat_file` なし、CLI / env なし | `/opt/app/mitsume.heartbeat.json` |
| `ping` / `watch` ともに設定 JSON なし、CLI / env なし | 起動時にエラーで exit |

`ping` を実行するプロセスと `watch` を動かすプロセスが別ユーザーで動く場合の運用パターンは [recipes.md](recipes.md) を参照する。

## Writing (`ping`)

`mitsume ping <job>` を呼ぶたびに、対応する `jobs.<job>.last_ping_at` を現在時刻で更新する。手順は次の通りである。

1. heartbeat file 全体を読む (存在しなければ `{"jobs": {}}` を出発点とする)。
2. in-memory で `jobs.<job>.last_ping_at` を更新する。該当 `job` の entry がない場合は新規追加する。
3. 同一 directory の tmp file (basename の末尾に一意な suffix を付けたもの) に完全な JSON を書き出す。
4. tmp file を heartbeat file の path へ atomic rename する。

保証と制約:

- **Timestamp の書式.** `last_ping_at` は `ping` を実行するプロセスの local timezone で RFC3339 形式の time stamp を書き出す (実装依存で subsecond 精度を含む場合がある)。read 側は `Z` (UTC) と `+09:00` などの offset 表記の両方を受け付けるため、書き手と読み手の timezone が異なっていても支障はない。
- **Atomic rename.** POSIX 準拠の rename は同一 filesystem 内で atomic である。tmp file を heartbeat file と同じ directory に置くため、`/tmp` が別の mount point であっても rename が失敗しない。
- **並行 `ping` の race.** 同時に複数の `ping` プロセスが起動しても、最後の rename が勝つ。dead-man's switch で監視する job は通常 1h 以上の周期で走るため、数秒以内に同じ job への `ping` が複数届く状況は実運用では想定しない。
- **未知の `job` への `ping`.** heartbeat file に entry がない場合は新規追加する。job の再作成や新規追加後の初回 `ping` に耐えるため、存在確認を行わない。
- **設定 JSON との整合検査は行わない.** `ping` は heartbeat file への書き込みのみを担当する。設定 JSON に定義されていない `job` を `ping` してもエラーにしない (dead-man's switch を含まない config で `ping` だけを使う運用パターンがある。[recipes.md](recipes.md) を参照する)。

最小の状態遷移を示す。初期状態:

```json
{
  "jobs": {
    "backup-nightly": { "last_ping_at": "2026-06-29T03:00:01+09:00" }
  }
}
```

`mitsume ping backup-nightly` を新しい時刻で呼ぶと `last_ping_at` を上書きし、未登録の job (`renew-cert`) に対して `mitsume ping renew-cert` を呼ぶと新規 entry を追加する。両方を適用した後の状態:

```json
{
  "jobs": {
    "backup-nightly": { "last_ping_at": "2026-06-30T03:00:02+09:00" },
    "renew-cert":     { "last_ping_at": "2026-06-30T04:15:00+09:00" }
  }
}
```

heartbeat file を書き換えるのは `ping` のみである。`check` と `watch` は read-only で参照する。

## Reading (`deadman` evaluation)

`deadman` checker の評価は `check` と `watch` の評価サイクル内で次の手順を踏む。

1. heartbeat file を読む。
2. `jobs.<job>.last_ping_at` を取得する。
3. 現在時刻との差分を `expect.within` (duration) と比較する。

判定:

| 状態 | 扱い |
|---|---|
| `jobs.<job>` entry が存在する、かつ 現在時刻 − `last_ping_at` < `expect.within` | success |
| `jobs.<job>` entry が存在する、かつ 現在時刻 − `last_ping_at` ≥ `expect.within` | failure ([notify.md § 通知トリガー](notify.md#通知トリガー) の confirm burst に入る) |
| heartbeat file は存在するが `jobs.<job>` entry がない | failure (`ping` が一度も届いておらず生存未確認) |

heartbeat file の read または parse に失敗した場合 (permission denied、JSON parse error、そして heartbeat file 自体の未存在を含む) の扱いは、起動時と評価サイクル中とで異なる。

- 起動時 pre-flight (評価サイクルに入る前の read / parse 検証、[cli.md § mitsume watch](cli.md#mitsume-watch) を参照) では fail-fast し、`watch` は評価サイクルに入らずに exit する。
- `watch` の 2 回目以降のサイクルで read または parse が失敗した場合は、そのサイクル内の全 `deadman` 評価を failure として扱い、confirm burst と通常の通知パスに乗せる。transient な file 破損や rename 中の read の余地を残すため exit はしない。次サイクルでは通常どおり read を再試行する。
- `check` は 1 回きりの実行のため評価サイクル 2 回目以降は存在しない。read / parse 失敗時は pre-flight と同様に fail-fast する。

`deadman` checker の `interval` は `watch` で次の評価タイミングを決める。判定は heartbeat file の read だけで完結し、HTTP request や container inspect のような外部呼び出しを伴わないため、`interval` を短くしても評価の負荷はほぼ増えない。`check` は 1 回で終了するため `interval` を無視する。詳細は [checkers.md § Deadman checker](checkers.md#deadman-checker) を参照する。

`check` と `watch` は heartbeat file を書き換えない。`deadman` が failure を検知しても heartbeat file には何も書き込まない。前回状態を保持しないため、`watch` プロセスを再起動しても `deadman` の判定結果は heartbeat file の内容から一意に決まる。

heartbeat file を読むのは各評価サイクルの起点で 1 度のみである。同じサイクル内の複数の `deadman` 評価は同じ snapshot を共有する。

## Dry-run

全 subcommand 共通の `--dry-run` flag ([cli.md § 共通 flag](cli.md#共通-flag) を参照) は heartbeat file を一切書き換えない。

| Subcommand | `--dry-run` 時の heartbeat file への挙動 |
|---|---|
| `mitsume ping` | 既存 heartbeat file を読む (in-memory update に必要)。tmp write と rename を skip する。更新予定の内容を stderr に出力する |
| `mitsume check` / `mitsume watch` | heartbeat file を read-only で参照し、`deadman` を通常どおり評価する。Slack への通知 payload は送信せず stderr に出力する |
| `mitsume notify` / `mitsume run` | 通常運用でも heartbeat file を触らない。`--dry-run` の有無で挙動は変わらない |

本番の heartbeat file を指した状態で `mitsume ping --dry-run <job>` を実行しても、`last_ping_at` は書き換えられない。試運転で heartbeat file に副作用を残す事故を防ぐ挙動である (`check` / `watch` は元々 heartbeat file を書き換えないため、`--dry-run` の有無で書き込みの発生有無が変わるのは `ping` のみである)。

## 関連

- [architecture.md](architecture.md) — core components と設計判断の背景
- [cli.md](cli.md) — `ping` / `check` / `watch` の CLI インターフェイスと共通 flag (`--heartbeat-file` / `--dry-run`)
- [configuration.md](configuration.md) — 設定 JSON schema、`heartbeat_file` field、識別子 (`job` / `name`) の規則
- [checkers.md § Deadman checker](checkers.md#deadman-checker) — `deadman` checker の `expect.within` と評価タイミング
- [notify.md § 通知トリガー](notify.md#通知トリガー) — confirm burst と通知トリガー
- [recipes.md](recipes.md) — cron から `ping` を打つ運用パターンと別ユーザー運用の tips
