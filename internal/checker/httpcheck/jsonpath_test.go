package httpcheck

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseJSONPath_ValidForms(t *testing.T) {
	t.Parallel()
	cases := map[string][]jsonPathSegment{
		"$":             nil,
		"$.status":      {{field: "status"}},
		"$.data.value":  {{field: "data"}, {field: "value"}},
		"$.items[0]":    {{field: "items"}, {index: 0, isIndex: true}},
		"$.items[0].id": {{field: "items"}, {index: 0, isIndex: true}, {field: "id"}},
		"$[0]":          {{index: 0, isIndex: true}},
		"$[0].name":     {{index: 0, isIndex: true}, {field: "name"}},
		"$.a_b-c":       {{field: "a_b-c"}},
		"$.a.b.c.d.e.f": {{field: "a"}, {field: "b"}, {field: "c"}, {field: "d"}, {field: "e"}, {field: "f"}},
		// docs/checkers.md § body_jsonpath は field 名の文字集合を英数字 + _ + - と規定し
		// 先頭文字への位置的制約は無い。digit / - 始まりも valid として扱う。
		"$.1st_call":  {{field: "1st_call"}},
		"$.-meta":     {{field: "-meta"}},
		"$.9":         {{field: "9"}},
		"$.data.-key": {{field: "data"}, {field: "-key"}},
	}
	for input, want := range cases {
		got, err := parseJSONPath(input)
		if err != nil {
			t.Errorf("parseJSONPath(%q): %v", input, err)

			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parseJSONPath(%q) = %+v, want %+v", input, got, want)
		}
	}
}

func TestParseJSONPath_InvalidForms(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"status",      // must start with $
		"$.",          // trailing dot
		"$..field",    // recursive descent
		"$.*",         // wildcard
		"$['key']",    // bracket notation
		"$.field[",    // unmatched
		"$.field[]",   // empty index
		"$.field[a]",  // non-numeric index
		"$.field[-1]", // negative index
		"$[",          // unmatched
		"$.a?",        // invalid char
		"$.a b",       // space in field
	}
	for _, in := range cases {
		if _, err := parseJSONPath(in); err == nil {
			t.Errorf("parseJSONPath(%q) should error", in)
		}
	}
}

func TestEvalJSONPath_ObjectField(t *testing.T) {
	t.Parallel()
	var root interface{}
	_ = json.Unmarshal([]byte(`{"status": "ok", "code": 200}`), &root)
	segs, _ := parseJSONPath("$.status")
	v, ok := evalJSONPath(segs, root)
	if !ok || v != "ok" {
		t.Fatalf("got %v ok=%v", v, ok)
	}
}

func TestEvalJSONPath_MissingField(t *testing.T) {
	t.Parallel()
	var root interface{}
	_ = json.Unmarshal([]byte(`{"status": "ok"}`), &root)
	segs, _ := parseJSONPath("$.missing")
	_, ok := evalJSONPath(segs, root)
	if ok {
		t.Fatalf("expected not found")
	}
}

func TestEvalJSONPath_NestedField(t *testing.T) {
	t.Parallel()
	var root interface{}
	_ = json.Unmarshal([]byte(`{"a": {"b": {"c": 42}}}`), &root)
	segs, _ := parseJSONPath("$.a.b.c")
	v, ok := evalJSONPath(segs, root)
	if !ok || v.(float64) != 42 {
		t.Fatalf("got %v ok=%v", v, ok)
	}
}

func TestEvalJSONPath_ArrayIndex(t *testing.T) {
	t.Parallel()
	var root interface{}
	_ = json.Unmarshal([]byte(`{"items": [{"id": 1}, {"id": 2}, {"id": 3}]}`), &root)
	segs, _ := parseJSONPath("$.items[1].id")
	v, ok := evalJSONPath(segs, root)
	if !ok || v.(float64) != 2 {
		t.Fatalf("got %v ok=%v", v, ok)
	}
}

func TestEvalJSONPath_ArrayOutOfRange(t *testing.T) {
	t.Parallel()
	var root interface{}
	_ = json.Unmarshal([]byte(`{"items": [1, 2]}`), &root)
	segs, _ := parseJSONPath("$.items[5]")
	_, ok := evalJSONPath(segs, root)
	if ok {
		t.Fatalf("expected not found for out-of-range")
	}
}

func TestEvalJSONPath_TypeMismatchTreatedAsMissing(t *testing.T) {
	t.Parallel()
	var root interface{}
	_ = json.Unmarshal([]byte(`{"status": "ok"}`), &root)
	segs, _ := parseJSONPath("$.status[0]")
	if _, ok := evalJSONPath(segs, root); ok {
		t.Fatalf("expected not found for string treated as array")
	}
	segs2, _ := parseJSONPath("$.status.x")
	if _, ok := evalJSONPath(segs2, root); ok {
		t.Fatalf("expected not found for string treated as object")
	}
}

func TestEvalJSONPath_RootDollar(t *testing.T) {
	t.Parallel()
	var root interface{}
	_ = json.Unmarshal([]byte(`"hello"`), &root)
	segs, _ := parseJSONPath("$")
	v, ok := evalJSONPath(segs, root)
	if !ok || v != "hello" {
		t.Fatalf("got %v ok=%v", v, ok)
	}
}

func TestEvalJSONPath_BoolValue(t *testing.T) {
	t.Parallel()
	var root interface{}
	_ = json.Unmarshal([]byte(`{"ok": true}`), &root)
	segs, _ := parseJSONPath("$.ok")
	v, ok := evalJSONPath(segs, root)
	if !ok || v.(bool) != true {
		t.Fatalf("got %v ok=%v", v, ok)
	}
}
