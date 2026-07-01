// Package heartbeat は dead-man's switch 用の永続 state を持つ heartbeat file の
// schema と atomic な R/W を提供する。file 形式とパス解決の仕様は docs/heartbeat.md
// に従う。
package heartbeat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry は 1 job あたりの記録。
type Entry struct {
	LastPingAt time.Time `json:"last_ping_at"`
}

// File は heartbeat file 全体の JSON schema。top-level は jobs object のみ。
type File struct {
	Jobs map[string]Entry `json:"jobs"`
}

// Load は heartbeat file を読み込む。存在しなければ空 File を返す (error ではない)。
// parse 失敗 / permission denied 等の read error は error として返す。
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &File{Jobs: map[string]Entry{}}, nil
		}

		return nil, fmt.Errorf("heartbeat: read %s: %w", path, err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("heartbeat: parse %s: %w", path, err)
	}
	if f.Jobs == nil {
		f.Jobs = map[string]Entry{}
	}

	return &f, nil
}

// Update は job の last_ping_at を書き換える (in-memory)。既存があれば上書き、
// なければ新規追加する。
func (f *File) Update(job string, ts time.Time) {
	if f.Jobs == nil {
		f.Jobs = map[string]Entry{}
	}
	f.Jobs[job] = Entry{LastPingAt: ts}
}

// Marshal は File を pretty JSON (2 space indent、末尾改行 1 個) にする。map の
// key は encoding/json が alphabetical に sort する。
func Marshal(f *File) ([]byte, error) {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("heartbeat: marshal: %w", err)
	}

	return append(data, '\n'), nil
}

// SaveAtomic は File を同一 directory の tmp file に write して os.Rename で
// atomic に置き換える。同 filesystem 内 rename が atomic である POSIX 保証を
// 使う。既存 file がある場合はその permission (mode) を tmp file にコピーして
// から rename する。
func SaveAtomic(path string, f *File) error {
	data, err := Marshal(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, base+".tmp.*")
	if err != nil {
		return fmt.Errorf("heartbeat: create tmp: %w", err)
	}
	tmpName := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("heartbeat: write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("heartbeat: sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("heartbeat: close tmp: %w", err)
	}
	if info, err := os.Stat(path); err == nil {
		if err := os.Chmod(tmpName, info.Mode().Perm()); err != nil {
			return fmt.Errorf("heartbeat: chmod tmp: %w", err)
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("heartbeat: rename: %w", err)
	}
	success = true

	return nil
}
