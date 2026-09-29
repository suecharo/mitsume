# heartbeat file

dead-man's switch は「決まった時間のうちに job から `ping` が届かなかったら通知する」監視である。job が失敗したときだけでなく、cron の設定ミスや host の停止で job がそもそも起動しなかったときにも気づける。この doc では、その仕組みと、`ping` と `deadman` checker が共有する heartbeat file をまとめる。組み方の例は [recipes.md](recipes.md#cron-job-の走り忘れに気づく) にある。

## 仕組み

```text
  cron job --(done)--> mitsume ping <job> --write--> heartbeat file
                                                          ^
                                                          | read
                                   mitsume check / watch (deadman checker) --> Slack
```

`mitsume ping <job>` は heartbeat file の、その job の時刻を今の時刻に書き換えるだけで、通知はしない。`check` / `watch` の `deadman` checker がそれを読み、`expect.within` を過ぎていれば通知する。heartbeat file を書くのは `ping` だけで、`check` / `watch` は読むだけである。

## ファイルの形式

```json
{
  "jobs": {
    "nightly-backup": {
      "last_ping_at": "2026-06-30T03:00:02.418265123+09:00"
    },
    "renew-cert": {
      "last_ping_at": "2026-06-30T04:15:00.5+09:00"
    }
  }
}
```

- `jobs` の key が job の名前、`last_ping_at` が最後に `ping` した時刻 (RFC 3339) である
- 時刻は `ping` を実行した process の timezone で書く。読むときはどの offset でも受け付けるので、書く側と読む側の timezone が違ってもよい
- key の順に並べ、2 space で字下げして書く。`cat` や `jq` でそのまま読める
- これ以外の情報は持たない ([architecture.md](architecture.md#状態を持たない設計))

## 場所の決まり方

`ping` と `check` / `watch` は同じ順で path を決め、最初に決まったものを使う。

1. `--heartbeat-file <path>`
2. 環境変数 `MITSUME_HEARTBEAT_FILE`
3. 設定 JSON の `heartbeat_file`
4. 設定 JSON と同じディレクトリの、設定 JSON の名前の `.json` を `.heartbeat.json` に変えたファイル (`/etc/mitsume/mitsume.json` なら `/etc/mitsume/mitsume.heartbeat.json`)

`ping` で設定 JSON が無く、1〜3 も無いときは exit 1 になる。home ディレクトリなどに勝手に作ることはしない。

`ping` する側と監視する側で path がずれると、`ping` しているのに `never pinged` で失敗し続ける。cron から `ping` するときは `--heartbeat-file` で path を明示するのが確実である。

## 書き込み (`ping`)

`ping` は次の順で書く。

1. heartbeat file を読む。無ければ空として扱う
2. job の `last_ping_at` を今の時刻にする。無い job なら足す
3. 同じディレクトリに一時ファイルを作って全体を書き、heartbeat file の path へ rename する

- 同じ filesystem の中の rename は atomic なので、読む側が書きかけの JSON を見ることはない。一時ファイルを同じディレクトリに作るのはこのためで、ディレクトリにも書き込み権限が要る
- 新しく作るファイルの mode は 0600 にする。すでにあるファイルは、そのファイルの mode を引き継ぐ
- 設定 JSON に無い job でも書く。`ping` は設定 JSON の `deadman` と突き合わせない
- 2 つの `ping` が、読んでから rename するまでの間に重なると、後から rename したほうの内容が残る。別の job への `ping` でも、先に書いた job の更新が消えることがある

## 読み込み (`deadman`)

`deadman` checker は評価の最初に heartbeat file を読み、その内容で判定する。`confirm` の再確認でも同じ内容で判定するので、やり直している間に `ping` が届いても、その評価の通知は止まらない。

| 状態 | 結果 |
|---|---|
| 最後の `ping` から `within` 経っていない | 成功 |
| 最後の `ping` から `within` 以上経っている | 失敗 (`observed` は `last_ping=25h12m ago`) |
| job の記録が無い、または heartbeat file が無い | 失敗 (`observed` は `never pinged`) |
| heartbeat file が読めない、または JSON として壊れている | 起動時なら exit 1、評価中なら失敗 |

設定 JSON に `deadman` があるとき、`check` / `watch` は評価を始める前に一度 heartbeat file を読み、読めない・壊れているときは exit 1 にする。path や権限の誤りに起動したときに気づけるようにするためである。評価を始めたあとに読めなくなったときは、その評価を失敗として扱い、process は止めずに次の評価でまた読む。
