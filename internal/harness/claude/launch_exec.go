package claude

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/yangflow/hoplet/internal/harness"
)

// launchCommand builds the process for a launch, for recordings (the
// daemon starts harnesses through a shim instead). It and verifySignature
// live alone in this file so that the lint exclusion for running a command
// the adapter assembled covers nothing else.
func launchCommand(ctx context.Context, l harness.Launch) *exec.Cmd {
	cmd := exec.CommandContext(ctx, l.Command[0], l.Command[1:]...)
	cmd.Dir, cmd.Env = l.Dir, l.Env
	return cmd
}

// verifySignature checks the code signature on macOS; other systems have
// no per-binary signature to check.
func verifySignature(ctx context.Context, path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if out, err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", path).CombinedOutput(); err != nil {
		return fmt.Errorf("claude: code signature: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// extractCommand runs the managed binary for one isolated extraction.
func extractCommand(ctx context.Context, bin string, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, args...)
}
