package container

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeUnixServer は tmp dir に unix socket を立てて handler で応答する fake
// Docker Engine を作る。socket path を返す。
func fakeUnixServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir := t.TempDir()
	socket := filepath.Join(dir, "engine.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: handler, ReadTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = l.Close()
	})

	return socket
}

func TestParse_Minimal(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "container", "container": "myapp", "engine": "docker", "interval": "1h", "expect": {"running": true}}`)
	c, err := Parse(raw, Options{SocketPath: "/tmp/fake"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Type() != "container" || c.Container() != "myapp" || c.Name() != "myapp" {
		t.Fatalf("got type=%s container=%s name=%s", c.Type(), c.Container(), c.Name())
	}
	if c.Engine() != "docker" || c.SocketPath() != "/tmp/fake" {
		t.Fatalf("got engine=%s socket=%s", c.Engine(), c.SocketPath())
	}
	if c.expectRunning != true {
		t.Fatalf("expectRunning=%v", c.expectRunning)
	}
}

func TestParse_TypeMismatch(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "http", "container": "x", "interval": "1h", "expect": {"running": true}}`)
	if _, err := Parse(raw, Options{SocketPath: "/tmp/x"}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_MissingContainer(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "container", "engine": "docker", "interval": "1h", "expect": {"running": true}}`)
	if _, err := Parse(raw, Options{SocketPath: "/tmp/x"}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_InvalidEngine(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "container", "container": "x", "engine": "cri-o", "interval": "1h", "expect": {"running": true}}`)
	if _, err := Parse(raw, Options{SocketPath: "/tmp/x"}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_MissingRunning(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "container", "container": "x", "interval": "1h", "expect": {}}`)
	if _, err := Parse(raw, Options{SocketPath: "/tmp/x"}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_UnknownField(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "container", "container": "x", "interval": "1h", "expect": {"running": true}, "wat": 1}`)
	if _, err := Parse(raw, Options{SocketPath: "/tmp/x"}); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParse_SocketNotFoundIsFailfast(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cp := func(engine string) []string {
		switch engine {
		case "docker":
			return []string{filepath.Join(dir, "no-docker.sock")}
		case "podman":
			return []string{filepath.Join(dir, "no-podman.sock")}
		default:
			return []string{filepath.Join(dir, "no-docker.sock"), filepath.Join(dir, "no-podman.sock")}
		}
	}
	raw := json.RawMessage(`{"type": "container", "container": "x", "interval": "1h", "expect": {"running": true}}`)
	if _, err := Parse(raw, Options{CandidatePathsFunc: cp}); err == nil {
		t.Fatalf("expected fail-fast for missing socket")
	}
}

func TestParse_SocketFoundViaCandidatePathsFunc(t *testing.T) {
	t.Parallel()
	sock := fakeUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"State": {"Status": "running"}}`))
	}))
	cp := func(engine string) []string {
		if engine == "docker" {
			return []string{sock}
		}

		return nil
	}
	raw := json.RawMessage(`{"type": "container", "container": "x", "engine": "docker", "interval": "1h", "expect": {"running": true}}`)
	c, err := Parse(raw, Options{CandidatePathsFunc: cp})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.SocketPath() != sock {
		t.Fatalf("SocketPath=%q want %q", c.SocketPath(), sock)
	}
}

func TestCandidatePaths_DockerNoEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	paths := CandidatePaths("docker")
	if len(paths) != 1 || paths[0] != "/var/run/docker.sock" {
		t.Fatalf("got %v", paths)
	}
}

func TestCandidatePaths_DockerHostUnix(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///tmp/docker.sock")
	paths := CandidatePaths("docker")
	if len(paths) != 2 || paths[0] != "/tmp/docker.sock" || paths[1] != "/var/run/docker.sock" {
		t.Fatalf("got %v", paths)
	}
}

func TestCandidatePaths_DockerHostTCPIgnored(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://1.2.3.4:2375")
	paths := CandidatePaths("docker")
	if len(paths) != 1 || paths[0] != "/var/run/docker.sock" {
		t.Fatalf("got %v", paths)
	}
}

func TestCandidatePaths_PodmanNoEnv(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	paths := CandidatePaths("podman")
	if len(paths) != 1 || paths[0] != "/run/podman/podman.sock" {
		t.Fatalf("got %v", paths)
	}
}

func TestCandidatePaths_PodmanWithEnv(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	paths := CandidatePaths("podman")
	if len(paths) != 2 || paths[0] != "/run/user/1000/podman/podman.sock" || paths[1] != "/run/podman/podman.sock" {
		t.Fatalf("got %v", paths)
	}
}

func TestCandidatePaths_AutoDetectDockerBeforePodman(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	paths := CandidatePaths("")
	if len(paths) != 2 || paths[0] != "/var/run/docker.sock" || paths[1] != "/run/podman/podman.sock" {
		t.Fatalf("got %v", paths)
	}
}

func TestResolveSocket_FoundInDockerHost(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatalf("touch: %v", err)
	}
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	got, err := ResolveSocket("docker")
	if err != nil {
		t.Fatalf("ResolveSocket: %v", err)
	}
	if got != sock {
		t.Fatalf("got %s want %s", got, sock)
	}
}

func TestResolveSocketWith_NoneFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cp := func(_ string) []string { return []string{filepath.Join(dir, "no.sock")} }
	if _, err := resolveSocketWith("docker", cp); err == nil {
		t.Fatalf("expected error")
	}
}

func TestEvaluate_RunningMatch(t *testing.T) {
	t.Parallel()
	sock := fakeUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, fmt.Sprintf("/%s/containers/myapp/json", APIVersion)) {
			w.WriteHeader(http.StatusNotFound)

			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"State": {"Status": "running"}, "Id": "abc"}`))
	}))
	raw := json.RawMessage(`{"type": "container", "container": "myapp", "interval": "1h", "expect": {"running": true}}`)
	c, err := Parse(raw, Options{SocketPath: sock})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r := c.Evaluate(context.Background()); !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
}

func TestEvaluate_RunningExpectedButExited(t *testing.T) {
	t.Parallel()
	sock := fakeUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"State": {"Status": "exited"}}`))
	}))
	raw := json.RawMessage(`{"type": "container", "container": "myapp", "interval": "1h", "expect": {"running": true}}`)
	c, _ := Parse(raw, Options{SocketPath: sock})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(r.Observed, "state=exited") {
		t.Errorf("Observed=%s", r.Observed)
	}
	if !strings.Contains(r.Expected, "running=true") {
		t.Errorf("Expected=%s", r.Expected)
	}
}

func TestEvaluate_RunningFalseExpectedAndExited(t *testing.T) {
	t.Parallel()
	sock := fakeUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"State": {"Status": "exited"}}`))
	}))
	raw := json.RawMessage(`{"type": "container", "container": "myapp", "interval": "1h", "expect": {"running": false}}`)
	c, _ := Parse(raw, Options{SocketPath: sock})
	if !c.Evaluate(context.Background()).OK {
		t.Fatalf("expected OK when running=false matches exited state")
	}
}

func TestEvaluate_ContainerNotFound(t *testing.T) {
	t.Parallel()
	sock := fakeUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	raw := json.RawMessage(`{"type": "container", "container": "missing", "interval": "1h", "expect": {"running": true}}`)
	c, _ := Parse(raw, Options{SocketPath: sock})
	r := c.Evaluate(context.Background())
	if r.OK {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(r.Observed, "not_found") {
		t.Errorf("Observed=%s", r.Observed)
	}
}

func TestEvaluate_EngineReturns5xx(t *testing.T) {
	t.Parallel()
	sock := fakeUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	raw := json.RawMessage(`{"type": "container", "container": "x", "interval": "1h", "expect": {"running": true}}`)
	c, _ := Parse(raw, Options{SocketPath: sock})
	if c.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure for 500")
	}
}

func TestEvaluate_MalformedResponse(t *testing.T) {
	t.Parallel()
	sock := fakeUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	raw := json.RawMessage(`{"type": "container", "container": "x", "interval": "1h", "expect": {"running": true}}`)
	c, _ := Parse(raw, Options{SocketPath: sock})
	if c.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure for malformed body")
	}
}

func TestEvaluate_SocketUnreachable(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type": "container", "container": "x", "interval": "1h", "expect": {"running": true}}`)
	c, err := Parse(raw, Options{SocketPath: "/tmp/nonexistent-socket-path"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Evaluate(context.Background()).OK {
		t.Fatalf("expected failure for unreachable socket")
	}
}

func TestEvaluate_ContextCanceled(t *testing.T) {
	t.Parallel()
	sock := fakeUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	raw := json.RawMessage(`{"type": "container", "container": "x", "interval": "1h", "expect": {"running": true}}`)
	c, _ := Parse(raw, Options{SocketPath: sock})
	if c.Evaluate(ctx).OK {
		t.Fatalf("expected failure for canceled ctx")
	}
}

func TestEvaluate_APIVersionInURLPath(t *testing.T) {
	t.Parallel()
	var seen string
	sock := fakeUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"State": {"Status": "running"}}`))
	}))
	raw := json.RawMessage(`{"type": "container", "container": "abc", "interval": "1h", "expect": {"running": true}}`)
	c, _ := Parse(raw, Options{SocketPath: sock})
	_ = c.Evaluate(context.Background())
	want := fmt.Sprintf("/%s/containers/abc/json", APIVersion)
	if seen != want {
		t.Fatalf("URL path=%q, want %q", seen, want)
	}
}
