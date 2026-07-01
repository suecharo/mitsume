// Package tailio は byte slice の末尾を「行数上限 or byte 数上限の小さい方」で
// 切り出す共通ヘルパを提供する。docs/notify.md § payload 形式 の cmd checker /
// run サブコマンドの stderr 尾切りロジックを 1 実装に集約する。
package tailio

// Truncate は b の末尾から maxLines 行分と maxBytes byte 分の tail を計算し、
// byte 数が小さい方を返す。maxLines <= 0 なら行数制約を無視、maxBytes <= 0 なら
// byte 数制約を無視。両方 <= 0 なら b をそのまま返す。
//
// 挙動例 (docs/checkers.md § cmd の 20 行 / 2KB 規則との対応):
//
//   - 出力が 5 行 / 500B の短い場合: 全体を返す (どちらの制約にも掛からない)
//   - 出力が 100 行だが 500B 以下の場合: 20 行を返す (行制約が bytes を上回る前に効く)
//   - 出力が 100 行 / 10KB の場合: 2KB byte tail を返す (bytes 制約が勝つ)
func Truncate(b []byte, maxLines, maxBytes int) []byte {
	lineTail := b
	if maxLines > 0 {
		lineTail = lastNLines(b, maxLines)
	}
	byteTail := b
	if maxBytes > 0 && len(byteTail) > maxBytes {
		byteTail = byteTail[len(byteTail)-maxBytes:]
	}
	if len(lineTail) < len(byteTail) {
		return lineTail
	}

	return byteTail
}

// lastNLines は 末尾から n 行分の byte slice を返す。末尾に trailing newline が
// 無い場合はその trailing 部分自体を 1 行として数える (これを数えないと "a\nb\nc"
// の tail 1 行が "b\nc" の 2 行分になって上限を超える)。改行が n 未満なら
// b をそのまま返す。
func lastNLines(b []byte, n int) []byte {
	if n <= 0 || len(b) == 0 {
		return b
	}
	count := 0
	if b[len(b)-1] != '\n' {
		count = 1
	}
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			count++
			if count > n {
				return b[i+1:]
			}
		}
	}

	return b
}
