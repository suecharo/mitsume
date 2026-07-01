package cmdcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// TestMain は cmd checker の test binary を「SIGTERM を無視する fake child
// process」として使う経路を持たせる。env MITSUME_CMDCHECK_TEST_FAKE で分岐。
// これがないと `/bin/sh -c 'trap "" TERM; sleep 30'` の子孫 sleep が stdout
// pipe を保持したまま孤児化し、Go の cmd.Wait が 30 秒返らない (test 環境で
// 実際に踏んだ挙動)。
func TestMain(m *testing.M) {
	switch os.Getenv("MITSUME_CMDCHECK_TEST_FAKE") {
	case "trap_term_sleep":
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	default:
		os.Exit(m.Run())
	}
}

func TestParse_MinimalConfig(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/true"], "interval": "1h", "expect": {"exit_code": 0}}`)
	c, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Type() != "cmd" || c.Name() != "/bin/true" {
		t.Fatalf("got type=%s name=%s", c.Type(), c.Name())
	}
	if c.Timeout() != DefaultTimeout {
		t.Fatalf("Timeout=%s", c.Timeout())
	}
	if c.GracePeriod() != GracePeriod {
		t.Fatalf("GracePeriod=%s", c.GracePeriod())
	}
}

func TestParse_TypeMismatch(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "file", "command": ["/bin/true"], "interval": "1h", "expect": {}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_MissingCommand(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "interval": "1h", "expect": {}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_EmptyCommand(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": [], "interval": "1h", "expect": {}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_EmptyCommandElement(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/sh", "", "echo"], "interval": "1h", "expect": {}}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_ExpectOmitted(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/true"], "interval": "1h", "expect": {}}`)
	c, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.expectExit != 0 {
		t.Fatalf("expectExit=%d, want 0 (default)", c.expectExit)
	}
}

func TestParse_TimeoutResolution(t *testing.T) {
	t.Parallel()
	// explicit
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/true"], "interval": "1h", "timeout": "5s", "expect": {}}`)
	c, _ := Parse(raw, Options{Defaults: DefaultsFallback{Timeout: 20 * time.Second}})
	if c.Timeout() != 5*time.Second {
		t.Fatalf("Timeout=%s", c.Timeout())
	}
	// defaults fallback
	raw2 := json.RawMessage(`{"type": "cmd", "command": ["/bin/true"], "interval": "1h", "expect": {}}`)
	c2, _ := Parse(raw2, Options{Defaults: DefaultsFallback{Timeout: 20 * time.Second}})
	if c2.Timeout() != 20*time.Second {
		t.Fatalf("Timeout=%s", c2.Timeout())
	}
	// implicit default
	c3, _ := Parse(raw2, Options{})
	if c3.Timeout() != DefaultTimeout {
		t.Fatalf("Timeout=%s", c3.Timeout())
	}
}

func TestParse_NameAutoTruncateAt32(t *testing.T) {
	t.Parallel()
	// command joined by space
	raw := json.RawMessage(`{"type": "cmd", "command": ["openssl", "x509", "-checkend", "604800", "-noout", "-in", "/etc/ssl/cert.pem"], "interval": "24h", "expect": {}}`)
	c, _ := Parse(raw, Options{})
	if len(c.Name()) != nameTruncateChars {
		t.Fatalf("Name len=%d, want %d", len(c.Name()), nameTruncateChars)
	}
	if !strings.HasPrefix("openssl x509 -checkend 604800 -noout -in /etc/ssl/cert.pem", c.Name()) {
		t.Fatalf("Name=%s", c.Name())
	}
}

func TestParse_NameAutoTruncateRuneSafe(t *testing.T) {
	t.Parallel()
	// 日本語 command で byte 32 は multi-byte UTF-8 の境界に落ちるため、byte 切り
	// だと invalid UTF-8 になり docs の「先頭 32 文字」の数的解釈からもズレる。
	// 実装は rune 単位で 32 char 切り出すべき (docs/checkers.md § name の自動生成)。
	// joined = "echo <日本語>" で 32 rune を超えるように 2 回並べる
	raw := json.RawMessage(`{"type": "cmd", "command": ["echo", "こんにちは世界です今日はいい天気ですねこんにちは世界です今日はいい天気ですね"], "interval": "1h", "expect": {}}`)
	c, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	name := c.Name()
	if runeCount := utf8.RuneCountInString(name); runeCount != nameTruncateChars {
		t.Fatalf("rune count = %d, want %d (name=%q)", runeCount, nameTruncateChars, name)
	}
	if !utf8.ValidString(name) {
		t.Fatalf("name is invalid UTF-8: %q", name)
	}
}

func TestParse_NameAutoNoTruncateWhenShort(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/true"], "interval": "1h", "expect": {}}`)
	c, _ := Parse(raw, Options{})
	if c.Name() != "/bin/true" {
		t.Fatalf("Name=%s", c.Name())
	}
}

func TestParse_UnknownField(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/true"], "interval": "1h", "expect": {}, "wat": 1}`)
	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestEvaluate_ExitZeroSuccess(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/true"], "interval": "1h", "expect": {"exit_code": 0}}`)
	c, _ := Parse(raw, Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
}

func TestEvaluate_ExitOneFailure(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/false"], "interval": "1h", "expect": {"exit_code": 0}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(r.Observed, "exit=1") {
		t.Errorf("Observed=%s", r.Observed)
	}
	if !strings.Contains(r.Expected, "exit=0") {
		t.Errorf("Expected=%s", r.Expected)
	}
}

func TestEvaluate_ExitCustomMatch(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/sh", "-c", "exit 42"], "interval": "1h", "expect": {"exit_code": 42}}`)
	c, _ := Parse(raw, Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK when exit_code matches 42")
	}
}

func TestEvaluate_GracePeriodExpiredThenSigkill(t *testing.T) {
	t.Parallel()
	// test binary 自身を SIGTERM を無視する fake child として起動する
	// (TestMain の分岐参照)。sh の subshell を使うと sleep が pipe fd を
	// 継承したまま孤児化し Go の cmd.Wait が返らない問題を回避する。
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	raw := json.RawMessage(fmt.Sprintf(
		`{"type": "cmd", "command": [%q], "env": {"MITSUME_CMDCHECK_TEST_FAKE": "trap_term_sleep"}, "interval": "1h", "timeout": "100ms", "expect": {"exit_code": 0}}`,
		self,
	))
	c, err := Parse(raw, Options{GracePeriod: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.GracePeriod() != 200*time.Millisecond {
		t.Fatalf("GracePeriod override = %s, want 200ms", c.GracePeriod())
	}
	start := time.Now()
	r := c.Evaluate(context.Background())
	elapsed := time.Since(start)
	if r.OK {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(r.Observed, fmt.Sprintf("exit=%d", TimeoutExitCode)) {
		t.Errorf("Observed=%s", r.Observed)
	}
	if elapsed < 200*time.Millisecond {
		t.Errorf("elapsed=%s too short; grace period probably skipped", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed=%s too long; SIGKILL likely didn't fire", elapsed)
	}
}

func TestEvaluate_TimeoutKillsChild(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/sleep", "5"], "interval": "1h", "timeout": "100ms", "expect": {"exit_code": 0}}`)
	c, _ := Parse(raw, Options{})
	start := time.Now()
	r := c.Evaluate(context.Background())
	elapsed := time.Since(start)
	if r.OK {
		t.Fatalf("expected failure due to timeout")
	}
	if !strings.Contains(r.Error, "timed out") {
		t.Errorf("Error=%s", r.Error)
	}
	if !strings.Contains(r.Observed, fmt.Sprintf("exit=%d", TimeoutExitCode)) {
		t.Errorf("Observed=%s", r.Observed)
	}
	// SIGTERM で /bin/sleep が即死するので、grace 5s は使わない → 100ms + tiny
	if elapsed > 2*time.Second {
		t.Errorf("evaluate took too long: %s", elapsed)
	}
}

func TestEvaluate_StdoutContainsOK(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/echo", "hello world"], "interval": "1h", "expect": {"stdout_contains": "world"}}`)
	c, _ := Parse(raw, Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
}

func TestEvaluate_StdoutContainsFail(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/echo", "hello"], "interval": "1h", "expect": {"stdout_contains": "goodbye"}}`)
	c, _ := Parse(raw, Options{})
	if c.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure")
	}
}

func TestEvaluate_StderrNotContainsOK(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/true"], "interval": "1h", "expect": {"stderr_not_contains": "panic"}}`)
	c, _ := Parse(raw, Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
}

func TestEvaluate_StderrNotContainsFail(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/sh", "-c", "echo 'error: panic' >&2"], "interval": "1h", "expect": {"stderr_not_contains": "panic"}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(r.Stderr, "panic") {
		t.Errorf("Stderr should include stderr tail, got %q", r.Stderr)
	}
}

func TestEvaluate_EnvOverride(t *testing.T) {
	// t.Setenv は t.Parallel と両立不可
	// parent env FOO=parent, override FOO=child → child wins
	t.Setenv("MITSUME_TEST_FOO", "parent")
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/sh", "-c", "echo $MITSUME_TEST_FOO"], "env": {"MITSUME_TEST_FOO": "child"}, "interval": "1h", "expect": {"stdout_contains": "child"}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func TestEvaluate_EnvUnion(t *testing.T) {
	// parent env と env の両方が入っていることを確認
	t.Setenv("MITSUME_TEST_PARENT", "yes")
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/sh", "-c", "echo p=$MITSUME_TEST_PARENT c=$MITSUME_TEST_CHILD"], "env": {"MITSUME_TEST_CHILD": "yes"}, "interval": "1h", "expect": {"stdout_contains": "p=yes c=yes"}}`)
	c, _ := Parse(raw, Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
}

func TestEvaluate_Cwd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := json.RawMessage(fmt.Sprintf(`{"type": "cmd", "command": ["/bin/sh", "-c", "pwd"], "cwd": %q, "interval": "1h", "expect": {"stdout_contains": %q}}`, dir, dir))
	c, _ := Parse(raw, Options{})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK")
	}
}

func TestEvaluate_StartFailure(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/nonexistent/binary/path"], "interval": "1h", "expect": {}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(r.Error, "start") {
		t.Errorf("Error=%s", r.Error)
	}
}

func TestEvaluate_ContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/sleep", "5"], "interval": "1h", "timeout": "10s", "expect": {}}`)
	c, _ := Parse(raw, Options{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	r := c.Evaluate(ctx)
	if r.OK {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(r.Observed, "canceled") {
		t.Errorf("Observed=%s", r.Observed)
	}
}

func TestEvaluate_ContextAlreadyCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/true"], "interval": "1h", "expect": {}}`)
	c, _ := Parse(raw, Options{})
	if c.Evaluate(ctx).OK {
		t.Fatalf("expected failure for canceled ctx")
	}
}

func TestEvaluate_StderrIncludedInFailure(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "cmd", "command": ["/bin/sh", "-c", "echo 'boom' >&2; exit 1"], "interval": "1h", "expect": {"exit_code": 0}}`)
	c, _ := Parse(raw, Options{})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(r.Stderr, "boom") {
		t.Errorf("Stderr=%q", r.Stderr)
	}
}

func TestTruncateStderr_Empty(t *testing.T) {
	t.Parallel()
	if got := truncateStderr(nil); got != "" {
		t.Errorf("got %q", got)
	}
	if got := truncateStderr([]byte("")); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateStderr_Under20Lines(t *testing.T) {
	t.Parallel()
	b := []byte("line1\nline2\nline3\n")
	if got := truncateStderr(b); got != "line1\nline2\nline3\n" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateStderr_Over20Lines(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "line%03d\n", i)
	}
	got := truncateStderr(b.Bytes())
	if strings.Count(got, "\n") != stderrTailLines {
		t.Fatalf("newline count=%d, want %d", strings.Count(got, "\n"), stderrTailLines)
	}
	if !strings.HasPrefix(got, "line080\n") {
		t.Errorf("got prefix: %q", got[:min(20, len(got))])
	}
	if !strings.HasSuffix(got, "line099\n") {
		t.Errorf("got suffix")
	}
}

func TestTruncateStderr_ExceedsBytes(t *testing.T) {
	t.Parallel()
	// each line ~1000 byte, 5 lines total = ~5005 byte
	// lastNLines(20) → 5 行分 (改行 5 個 <= 20) → 全体
	// byteTail = last 2048 byte
	// 小さい方 = byteTail (2048 byte)
	line := strings.Repeat("A", 1000)
	var b bytes.Buffer
	for i := 0; i < 5; i++ {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	got := truncateStderr(b.Bytes())
	if len(got) != stderrTailBytes {
		t.Fatalf("len=%d, want %d", len(got), stderrTailBytes)
	}
}

func TestMergeEnv_OverridePrevails(t *testing.T) {
	t.Parallel()
	parent := []string{"KEY=parent", "OTHER=x"}
	override := map[string]string{"KEY": "child"}
	got := mergeEnv(parent, override)
	// KEY=child は入っている、KEY=parent は入っていない
	var hasChild, hasParent bool
	for _, e := range got {
		if e == "KEY=child" {
			hasChild = true
		}
		if e == "KEY=parent" {
			hasParent = true
		}
	}
	if !hasChild {
		t.Errorf("KEY=child missing: %v", got)
	}
	if hasParent {
		t.Errorf("KEY=parent leaked: %v", got)
	}
	// OTHER=x は残る
	var hasOther bool
	for _, e := range got {
		if e == "OTHER=x" {
			hasOther = true
		}
	}
	if !hasOther {
		t.Errorf("OTHER=x missing: %v", got)
	}
}

func TestMergeEnv_NilOverrideReturnsParent(t *testing.T) {
	t.Parallel()
	parent := []string{"A=1", "B=2"}
	got := mergeEnv(parent, nil)
	if len(got) != len(parent) {
		t.Fatalf("len=%d, want %d", len(got), len(parent))
	}
}
