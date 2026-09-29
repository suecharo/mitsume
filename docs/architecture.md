# 設計

mitsume の設計原則と、それを守るための制約をまとめる。機能を足したり構造を変えたりする前に読む。

## 対象とする規模

mitsume は「動いているはずのものが動いていない」ことに気づくための道具で、次の条件がそろう運用を対象にする。

- 監視対象は host 数台、check 数十個まで
- 通知先は Slack の channel 1 つで足りる
- 時系列のメトリクスやダッシュボードは要らない
- 監視のためにサーバーや DB を別に立てたくない

これより大きな運用には Prometheus や Datadog などを使う。

## 設計原則

原則どうしがぶつかったときは、上にあるものを優先する。

1. binary 1 つで動く。監視 agent・DB・専用サーバーを要求しない。`CGO_ENABLED=0` で build し、依存するライブラリも増やさない。`container` checker が Docker SDK を使わず、Docker Engine API を socket 越しに直接呼ぶのもこのためである
2. 設定 JSON なしで使い始められる。`notify` / `run` / `ping` は設定 JSON が無くても動き、既存の script・crontab・Dockerfile に 1 行足すだけで通知が届く。機能を足すときもこの形を壊さない
3. 失敗を取りこぼさず、通知を増やさない。失敗は `confirm` の再確認を通ってから通知し、同じ失敗を思い出させるためだけの通知は送らない
4. 必要なものだけを持つ。「入れても害はない」は採用の理由にしない

## 状態を持たない設計

mitsume が process をまたいで保存する状態は、heartbeat file に書く job ごとの最後の `ping` の時刻だけである。check の前回の結果、失敗が続いた回数、最後に通知した時刻は、どこにも保存しない。

このため次のことが成り立つ。

- `watch` を再起動しても判定が変わらない。判定はその時点の監視対象と heartbeat file の中身だけで決まるので、再起動によって通知が二重になったり抜けたりすることはない
- 設定 JSON を変えたら process を再起動すればよい。実行中に設定を読み直す仕組み (SIGHUP など) は要らない
- 読み書きがぶつかりうる場所は heartbeat file の 1 か所だけで、同じ filesystem の中の atomic rename で守れる ([heartbeat.md](heartbeat.md#書き込み-ping))
- heartbeat file を `cat` や `jq` で読める JSON のまま保てる。DB を持ち込まないので schema の移行も起きない

代わりに、前回の状態が要る機能は持てない。

- 復旧の通知。「前回は失敗していた」を覚えていないので送れない
- 評価をまたいだ debounce (「N 回続けて失敗したら通知する」を `interval` ごとの評価で数えるもの)。1 回の評価の中で済む `confirm` の再確認で代える ([configuration.md](configuration.md#confirm))
- リマインド。前回いつ通知したかを覚えていないので持てない。代わりに、失敗している間は評価のたびに通知する

通知の側から見た挙動は [notify.md](notify.md#通知の種類) にある。

## 処理の流れと責務の境界

```text
  job side                                         monitor side
  --------                                         ------------
  mitsume ping ----- write -----> heartbeat file <----- read ---- deadman checker --+
                                                                                    |
                                                  http / file / container / cmd ----+
                                                  checker                           |
                                                                                    |
                                                  mitsume check / watch  <----------+
                                                  (internal/runner)     results
                                                           |
                                                           | failure
                                                           v
  mitsume notify / run -------------------------------> notifier ------> Slack
```

各部分の責務は次のように分かれていて、境界を越えた処理は持たない。

- checker (`internal/checker/<type>/`) は 1 回の評価をして、成功か失敗かを返すだけである。やり直しも通知もしない
- `confirm` の再確認と通知するかどうかの判断は、`check` / `watch` の評価ループ (`internal/runner/`) が持つ。checker を繰り返し呼び、失敗が続いたときだけ notifier に渡す
- heartbeat file を書くのは `ping` だけで、`check` / `watch` は読むだけである
- `run` (`internal/supervisor/`) は notifier だけを使い、heartbeat file には触らない。dead-man's switch と組み合わせるときは shell で `mitsume run -- <cmd> && mitsume ping <job>` とつなぐ
- 設定 JSON の読み込み (`internal/config/`) は checker の種類を知らず、checker ごとの field は各 checker が検査する。両者をつなぐのが `internal/loader/` である

## セキュリティ上の制約

どの処理でも次のことを守る。

- 秘密情報 (Slack の Webhook URL) を CLI の引数で受け取らない。引数は `ps` や `/proc/<pid>/cmdline` から同じ host の他のユーザーに見えるためである。値は環境変数で受け取り、引数や設定 JSON には環境変数の名前だけを書く ([configuration.md](configuration.md#秘密情報の渡し方))
- 秘密情報をログ・エラーメッセージ・通知・heartbeat file に書かない。HTTP クライアントのエラーは URL を含むので、URL を取り除いてから出す
- heartbeat file は、他のユーザーが書き換えられない mode で作る。書き換えられると、走っていない job を走ったことにできてしまうためである

## 持たない機能

「小規模な運用の死活監視」から外れるものは持たない。必要になったら別の道具を使う。

| 機能 | 持たない理由 |
|---|---|
| Slack 以外の通知先、check ごとの通知先の振り分け | 通知先ごとの retry や書式を設定に出すことになり、設定が一気に複雑になる。channel を分けたいときは Webhook と設定 JSON を分けて、process を別に動かす |
| severity、tag、Block Kit | 通知は「何が、どう失敗したか」が読めれば足りる |
| メトリクス、ダッシュボード、SLO の計算 | 対象とする規模の外 |
| 実行中の設定の読み直し | 再起動すれば同じ結果になる |
| 別の host の container の監視 | 監視対象と同じ host に mitsume を置けば足りる。TCP 越しに Docker Engine API へ繋ぐことはしない |
| Docker の `HEALTHCHECK` との連動 | `container` checker は動いているかだけを見る。中身の確認は `http` か `cmd` で行う |
| mitsume 自身の生存を外へ知らせる機能 (自分で ping を送る、外部の dead-man's switch サービスとの連携) | 生存は systemd の `Restart=` や Docker の restart policy に任せる。止まるときの通知は best-effort で送る ([notify.md](notify.md#届かない通知)) |
| web UI、REST API | CLI と設定 JSON で足りる |
