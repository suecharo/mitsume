# checker

checker の種類ごとに、何を見て、どうなったら失敗とするかをまとめる。どの種類にも共通の field (`name` / `interval` / `expect` / `confirm` / `timeout`) は [configuration.md](configuration.md#check-に共通の-field) にある。

| `type` | 見るもの | 失敗の例 |
|---|---|---|
| `http` | HTTP の応答 | status が違う、body に期待する文字列が無い、応答が遅い |
| `deadman` | job から `ping` が届いているか | 最後の `ping` から時間が経ちすぎている |
| `file` | ファイルの有無・更新時刻・サイズ | backup のファイルが 1 日以上更新されていない |
| `container` | container が動いているか | container が止まっている |
| `cmd` | 任意のコマンドの結果 | exit code が 0 でない |

checker は 1 回評価するだけで、やり直しは `confirm` の再確認に任せる。失敗したときに通知へ載る値の例は [notify.md](notify.md#payload) にある。

## http

```json
{
  "type": "http",
  "name": "api-health",
  "url": "https://api.example.com/health",
  "interval": "1h",
  "expect": {
    "status": 200,
    "body_jsonpath": [{ "path": "$.status", "equals": "ok" }],
    "latency_under": "3s"
  }
}
```

| field | 省略したとき | 説明 |
|---|---|---|
| `url` | 省略できない | 呼ぶ URL |
| `method` | `GET` | HTTP method |
| `headers` | 付けない | request header の object |
| `body` | 送らない | request body の文字列 |

`expect` には次の条件を 1 つ以上書く。

| 条件 | 成功とするとき |
|---|---|
| `status` | status code が一致する (100〜599 の整数) |
| `body_contains` | body に文字列が含まれる。`Content-Type` は見ず、byte 列のまま比べる |
| `body_jsonpath` | body を JSON として読み、すべてのルールを満たす (下の例) |
| `latency_under` | request を送ってから body を読み終わるまでの時間が、この値より短い |

`status` を書かなければ status code は見ない。500 が返っても、他の条件を満たせば成功になる。

`body_jsonpath` は、`path` と演算子 1 つを組にしたルールの配列である。

```json
"body_jsonpath": [
  { "path": "$.status", "equals": "ok" },
  { "path": "$.errors", "exists": false },
  { "path": "$.items[0].version", "regex": "^v\\d+" }
]
```

| 演算子 | 成功とするとき |
|---|---|
| `equals` | 値が一致する (文字列・数・真偽値) |
| `contains` | 文字列の値が、指定した文字列を含む |
| `regex` | 文字列の値が正規表現 (RE2) に一致する |
| `exists` | `true` なら path がある、`false` なら path が無い |

`path` は `$` から始め、`.field` と `[N]` だけを組み合わせて書く。field 名に使えるのは英数字と `_` `-` で、`..`、`*`、`['key']`、filter は使えない。

- 接続できない、timeout した、TLS の検証に失敗した、`body_jsonpath` があるのに body が JSON でない、のいずれも失敗になる
- TLS の検証は常に行い、無効にする設定は無い
- redirect は 10 回まで追い、最後の応答で判定する

## deadman

```json
{
  "type": "deadman",
  "job": "nightly-backup",
  "interval": "1h",
  "expect": { "within": "25h" }
}
```

| field | 説明 |
|---|---|
| `job` | 見張る job の名前。`[a-zA-Z0-9_-]{1,64}` で、`checks[]` の中で重複できない |
| `expect.within` | 最後の `ping` から許す時間。省略できない |

評価のたびに heartbeat file を読み、最後の `ping` から `within` 以上経っていれば失敗にする。一度も `ping` が届いていない job も失敗になる。仕組みと heartbeat file の場所は [heartbeat.md](heartbeat.md) にある。

`within` は「job の実行間隔 + 監視側の評価の間隔 + 余裕」を目安にする。毎日走る job を 1 時間ごとに評価するなら `25h`、毎時走る job を 1 時間ごとに評価するなら `2h30m` くらいになる。

判定は heartbeat file を読むだけで済み、外に問い合わせないので、`interval` を短くしても負荷はほとんど増えない。

## file

```json
{
  "type": "file",
  "name": "db-backup",
  "path_glob": "/backup/db-*.dump",
  "interval": "1h",
  "expect": { "exists": true, "mtime_within": "25h", "size_min": "100MB" }
}
```

| field | 説明 |
|---|---|
| `path` | 見るファイルの path |
| `path_glob` | 見るファイルの glob。一致したもののうち、更新時刻が一番新しい 1 つだけを見る |

`path` と `path_glob` はどちらか一方だけを書く。

`expect` には次の条件を 1 つ以上書く。

| 条件 | 成功とするとき |
|---|---|
| `exists` | `true` ならファイルがある、`false` なら無い |
| `mtime_within` | 最後に更新されてから、この時間が経っていない |
| `size_min` / `size_max` | サイズがこの値以上 / 以下 ([size の書き方](configuration.md#size)) |

- ファイルが無いとき (`path_glob` に一致するものが無いときも)、`exists: false` なら成功、それ以外は失敗になる
- ファイルの中身は読まず、属性だけを見る。symlink はたどる。属性を取れなければ (親ディレクトリの権限が無いなど) 失敗になる
- 中身を確かめたいときは `cmd` checker で `grep` などを呼ぶ

## container

```json
{
  "type": "container",
  "container": "myapp-web-1",
  "interval": "1h",
  "expect": { "running": true }
}
```

| field | 説明 |
|---|---|
| `container` | container の名前か id。Docker Compose の container は `<project>-<service>-<N>` の名前で書く |
| `engine` | `docker` か `podman`。省略すると docker、podman の順に socket を探す |
| `expect.running` | `true` なら動いていれば成功、`false` なら止まっていれば成功。省略できない |

Docker Engine API の `/v1.43/containers/<container>/json` を UNIX domain socket 越しに呼び、`State.Status` が `running` かどうかを見る。container が見つからなければ失敗になる。1 回の評価は 30 秒で打ち切り、この時間は変えられない。

socket は次の順に探す。

| `engine` | 探す順 |
|---|---|
| `docker` | `DOCKER_HOST` (`unix://` で始まるときだけ)、`/var/run/docker.sock` |
| `podman` | `$XDG_RUNTIME_DIR/podman/podman.sock`、`/run/podman/podman.sock` |

socket は起動したときに探し、見つからなければ exit 1 になる。mitsume を動かすユーザーが socket を読めるようにしておく ([recipes.md](recipes.md#container-を監視する))。

## cmd

他の checker で見られないもの (ディスクの残量、証明書の期限、systemd の service の状態など) を、任意のコマンドの結果で判定する。

```json
{
  "type": "cmd",
  "name": "tls-cert-expiry",
  "command": ["openssl", "x509", "-checkend", "604800", "-noout", "-in", "/etc/ssl/cert.pem"],
  "interval": "24h",
  "expect": { "exit_code": 0 }
}
```

| field | 省略したとき | 説明 |
|---|---|---|
| `command` | 省略できない | 実行するコマンドの配列。shell を通さずに実行する |
| `env` | 足さない | 追加する環境変数の object。mitsume の環境変数に足し、同じ名前なら上書きする |
| `cwd` | mitsume のカレントディレクトリ | 実行するディレクトリ |

`expect` の条件は次のとおりで、何も書かなければ exit code 0 で成功になる。

| 条件 | 成功とするとき |
|---|---|
| `exit_code` | exit code が一致する (省略すると 0) |
| `stdout_contains` | stdout に文字列が含まれる |
| `stderr_not_contains` | stderr に文字列が含まれない |

- pipe やリダイレクトなど shell の機能が要るときは、`["/bin/sh", "-c", "test $(df --output=pcent /data | tail -1 | tr -dc 0-9) -lt 90"]` のように shell を明示して呼ぶ
- `timeout` を過ぎたら SIGTERM を送り、5 秒経っても終わらなければ SIGKILL を送る。このときの exit code は 124 として判定するので、`exit_code` が 0 なら失敗になる
- `stdout_contains` と `stderr_not_contains` は出力の全体に対して判定する
- 失敗の通知には stderr の末尾を付ける ([notify.md](notify.md#payload))
