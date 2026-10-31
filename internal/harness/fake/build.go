package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// BuildBinary compiles the fakeharness binary into dir and returns its
// path. It needs the Go toolchain and is meant for tests.
func BuildBinary(ctx context.Context, dir string) (string, error) {
	out := filepath.Join(dir, "fakeharness")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "github.com/yangflow/hoplet/internal/harness/fake/fakeharness")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("fake: build fakeharness: %w: %s", err, b)
	}
	return out, nil
}

// WriteScript writes s as JSON into dir and returns the file's path.
func WriteScript(dir string, s Script) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("fake: encode script: %w", err)
	}
	path := filepath.Join(dir, "script.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", fmt.Errorf("fake: write script: %w", err)
	}
	return path, nil
}
