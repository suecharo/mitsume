# テスト

mitsume のテストの置き場所と、外部とのやりとりをテストでどう再現するかをまとめる。仕様は [../docs/](../docs/) にあり、テストはそこに書いた挙動を確かめる。

## テストの種類と置き場所

| 種類 | 置き場所 | 対象 |
|---|---|---|
| unit | 対象の package の `*_test.go` | 関数や型の入出力 |
| property-based | 対象の package の `*_property_test.go` | parser などの純粋な関数が満たす性質。[rapid](https://github.com/flyingmutant/rapid) を使う |
| integration | `tests/integration/` | build した `mitsume` の binary を実行し、HTTP・ファイル・process をまたぐ経路 |

テストの名前は `Test<対象>_<条件>_<期待する結果>` にする。table-driven test は `t.Run` で subtest に分け、失敗したときにどの入力かが分かるようにする。

## 外部とのやりとりの再現

mock するのは mitsume の外にあるものだけで、内部の関数や interface は mock しない。テストのためだけの interface も作らない。

| 外部 | テストでの再現 |
|---|---|
| HTTP (監視対象、Slack) | `httptest.NewServer` で立てた server |
| ファイル | `t.TempDir()` の中に作る |
| 子プロセス | テストの binary 自身を子として起動し、`TestMain` で引数を見て偽のコマンドとして振る舞わせる |
| Docker Engine API | `net.Listen("unix", ...)` で立てた socket |
| 時刻と待ち時間 | 時刻を返す関数と `runner.Sleeper` を差し替える |

テストどうしで状態を共有しない。環境変数は `t.Setenv`、ファイルは `t.TempDir()` を使い、`-shuffle=on` で順番を変えても通るようにする。

## 実行

```bash
make test                                   # 全テストを実行する
go test ./tests/integration/...             # integration だけを実行する
go test ./internal/... -rapid.checks=1000   # property-based test の試行回数を増やす
make lint                                   # golangci-lint
make fmt                                    # gofumpt で整形する
```

release は tag を push すると GitHub Actions の GoReleaser が作る。手元で試すときは次のようにする。

```bash
goreleaser release --clean --snapshot --skip=publish,sign
```
