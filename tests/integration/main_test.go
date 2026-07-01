// Package integration は build した mitsume バイナリを subprocess として叩く
// end-to-end テスト群。TestMain で 1 回 go build を実行し、bin を全 test で共有する。
package integration

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var mitsumeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mitsume-integration-*")
	if err != nil {
		log.Fatalf("mkdir tmp: %v", err)
	}
	mitsumeBin = filepath.Join(dir, "mitsume")
	cmd := exec.Command("go", "build", "-o", mitsumeBin, "../../cmd/mitsume")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dir)
		log.Fatalf("build mitsume bin: %v", err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
