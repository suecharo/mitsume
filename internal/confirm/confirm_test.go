package confirm

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParse_EmptyReturnsDefault(t *testing.T) {
	t.Parallel()
	c, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse(nil): %v", err)
	}
	if c.OneStrike {
		t.Fatalf("OneStrike = true, want false")
	}
	if c.Checks != DefaultChecks || c.Interval != DefaultInterval {
		t.Fatalf("got %+v, want default", c)
	}
}

func TestParse_NullReturnsDefault(t *testing.T) {
	t.Parallel()
	c, err := Parse(json.RawMessage("null"))
	if err != nil {
		t.Fatalf("Parse(null): %v", err)
	}
	if c.Checks != DefaultChecks || c.Interval != DefaultInterval {
		t.Fatalf("got %+v", c)
	}
}

func TestParse_FalseSetsOneStrike(t *testing.T) {
	t.Parallel()
	c, err := Parse(json.RawMessage("false"))
	if err != nil {
		t.Fatalf("Parse(false): %v", err)
	}
	if !c.OneStrike {
		t.Fatalf("OneStrike = false, want true")
	}
}

func TestParse_TrueIsRejected(t *testing.T) {
	t.Parallel()
	if _, err := Parse(json.RawMessage("true")); err == nil {
		t.Fatalf("expected error for confirm=true")
	}
}

func TestParse_ObjectChecksOnly(t *testing.T) {
	t.Parallel()
	c, err := Parse(json.RawMessage(`{"checks": 5}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Checks != 5 || c.Interval != DefaultInterval {
		t.Fatalf("got %+v", c)
	}
}

func TestParse_ObjectBoth(t *testing.T) {
	t.Parallel()
	c, err := Parse(json.RawMessage(`{"checks": 5, "interval": "10s"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Checks != 5 || c.Interval != 10*time.Second {
		t.Fatalf("got %+v", c)
	}
}

func TestParse_ObjectIntervalOnly(t *testing.T) {
	t.Parallel()
	c, err := Parse(json.RawMessage(`{"interval": "10s"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Checks != DefaultChecks || c.Interval != 10*time.Second {
		t.Fatalf("got %+v", c)
	}
}

func TestParse_ChecksBoundaryLower(t *testing.T) {
	t.Parallel()
	c, err := Parse(json.RawMessage(`{"checks": 1}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Checks != 1 {
		t.Fatalf("Checks = %d, want 1", c.Checks)
	}
}

func TestParse_ChecksZeroOrNegativeRejected(t *testing.T) {
	t.Parallel()
	cases := []string{`{"checks": 0}`, `{"checks": -1}`, `{"checks": -1000}`}
	for _, in := range cases {
		if _, err := Parse(json.RawMessage(in)); err == nil {
			t.Errorf("expected error for %s", in)
		}
	}
}

func TestParse_UnknownFieldRejected(t *testing.T) {
	t.Parallel()
	if _, err := Parse(json.RawMessage(`{"checks": 3, "wat": 1}`)); err == nil {
		t.Fatalf("expected error for unknown field")
	}
}

func TestParse_IntervalInvalidRejected(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"interval": "abc"}`,
		`{"interval": "1w"}`,
		`{"interval": ""}`,
		`{"interval": "PT1H"}`,
	}
	for _, in := range cases {
		if _, err := Parse(json.RawMessage(in)); err == nil {
			t.Errorf("expected error for %s", in)
		}
	}
}

func TestParse_IntervalDaySuffix(t *testing.T) {
	t.Parallel()
	c, err := Parse(json.RawMessage(`{"checks": 2, "interval": "1d"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Interval != 24*time.Hour {
		t.Fatalf("Interval = %v, want 24h", c.Interval)
	}
}

func TestParse_MalformedJSONRejected(t *testing.T) {
	t.Parallel()
	if _, err := Parse(json.RawMessage(`{`)); err == nil {
		t.Fatalf("expected error for malformed JSON")
	}
}

func TestParse_StringLiteralRejected(t *testing.T) {
	t.Parallel()
	if _, err := Parse(json.RawMessage(`"foo"`)); err == nil {
		t.Fatalf("expected error for string literal")
	}
}

func TestParse_ErrorMessageMentionsChecks(t *testing.T) {
	t.Parallel()
	_, err := Parse(json.RawMessage(`{"checks": 0}`))
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "checks") {
		t.Errorf("error message should mention checks, got: %v", err)
	}
}

func TestDefault_ValuesMatchDocs(t *testing.T) {
	t.Parallel()
	c := Default()
	if c.OneStrike {
		t.Fatalf("Default.OneStrike = true")
	}
	if c.Checks != 3 {
		t.Fatalf("Default.Checks = %d, want 3", c.Checks)
	}
	if c.Interval != 30*time.Second {
		t.Fatalf("Default.Interval = %v, want 30s", c.Interval)
	}
}
