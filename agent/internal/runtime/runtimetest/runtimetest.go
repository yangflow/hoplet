// Package runtimetest builds and configures the scripted fake `claude` executable
// (agent/internal/runtime/fakeclaude) for tests.
package runtimetest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	once   sync.Once
	binDir string
	binErr error
)

// Build compiles the fake executable once per test process and returns its path. Call Cleanup from TestMain.
func Build(t testing.TB) string {
	t.Helper()
	once.Do(func() {
		binDir, binErr = os.MkdirTemp("", "fakeclaude-")
		if binErr != nil {
			return
		}
		_, here, _, _ := runtime.Caller(0)
		src := filepath.Join(filepath.Dir(here), "..", "fakeclaude")
		bin := filepath.Join(binDir, "claude")
		cmd := exec.Command("go", "build", "-o", bin, src)
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = fmt.Errorf("build fakeclaude: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return filepath.Join(binDir, "claude")
}

// Cleanup removes the built executable.
func Cleanup() {
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
}

// Session creates `<projects>/<encoded>/<id>.jsonl` with one line and returns its path. The fake finds it
// through FAKE_CLAUDE_PROJECTS.
func Session(t testing.TB, projects, dir, id string) string {
	t.Helper()
	enc := filepath.Join(projects, "proj")
	if err := os.MkdirAll(enc, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(enc, id+".jsonl")
	line := fmt.Sprintf("{\"type\":\"user\",\"cwd\":%q,\"sessionId\":%q,\"message\":{\"role\":\"user\",\"content\":\"start\"}}\n", dir, id)
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
