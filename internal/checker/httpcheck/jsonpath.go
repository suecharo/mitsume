package httpcheck

import (
	"fmt"
	"strconv"
	"strings"
)

// jsonPathSegment は path の 1 セグメント。field name (property access) か
// integer index のいずれか一方。
type jsonPathSegment struct {
	field   string
	index   int
	isIndex bool
}

// parseJSONPath は path を segments に変換する。サポートする書式は
// docs/checkers.md § body_jsonpath の path 節に列挙されたもの:
//
//	$                      → root
//	$.field                → property access
//	$.field.subfield       → nested property
//	$.field[N]             → array index
//	$.field[N].subfield    → array element property
//	$[N]                   → root array index
//
// field 名の文字集合は [a-zA-Z0-9_-] (先頭文字を含む全 char に等しく適用する。
// docs には位置的制約は無い)。bracket notation (`['key']`)、再帰探索 (`..`)、
// wildcard (`*`)、filter (`?()`)、slice (`[a:b]`) は非対応。
func parseJSONPath(p string) ([]jsonPathSegment, error) {
	if p == "" {
		return nil, fmt.Errorf("jsonpath: empty path")
	}
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("jsonpath: must start with '$', got %q", p)
	}
	rest := p[1:]
	var segments []jsonPathSegment
	for rest != "" {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			if rest == "" || !isFieldChar(rest[0]) {
				return nil, fmt.Errorf("jsonpath: expected field name after '.' in %q", p)
			}
			end := 0
			for end < len(rest) && isFieldChar(rest[end]) {
				end++
			}
			segments = append(segments, jsonPathSegment{field: rest[:end]})
			rest = rest[end:]
		case '[':
			closeIdx := strings.IndexByte(rest, ']')
			if closeIdx == -1 {
				return nil, fmt.Errorf("jsonpath: unmatched '[' in %q", p)
			}
			numStr := rest[1:closeIdx]
			if numStr == "" {
				return nil, fmt.Errorf("jsonpath: empty '[]' in %q", p)
			}
			n, err := strconv.Atoi(numStr)
			if err != nil {
				return nil, fmt.Errorf("jsonpath: invalid array index %q in %q: %w", numStr, p, err)
			}
			if n < 0 {
				return nil, fmt.Errorf("jsonpath: array index must be >= 0, got %d", n)
			}
			segments = append(segments, jsonPathSegment{index: n, isIndex: true})
			rest = rest[closeIdx+1:]
		default:
			return nil, fmt.Errorf("jsonpath: unexpected character %q in %q", rest[0], p)
		}
	}

	return segments, nil
}

func isFieldChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

// evalJSONPath は segments を root に適用し、(値, 見つかったか) を返す。
// 型不整合 (object 期待だが array 等) と array out-of-range は「見つからない」
// として扱う。exists 演算子の bool 比較に使う。
func evalJSONPath(segments []jsonPathSegment, root interface{}) (interface{}, bool) {
	cur := root
	for _, seg := range segments {
		if seg.isIndex {
			arr, ok := cur.([]interface{})
			if !ok {
				return nil, false
			}
			if seg.index >= len(arr) {
				return nil, false
			}
			cur = arr[seg.index]

			continue
		}
		obj, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		v, exists := obj[seg.field]
		if !exists {
			return nil, false
		}
		cur = v
	}

	return cur, true
}
