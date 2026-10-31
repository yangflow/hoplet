package claude

import (
	"context"
	"os/exec"

	"github.com/yangflow/hoplet/internal/harness"
)

// launchCommand builds the process for a launch, for recordings (the
// daemon starts harnesses through a shim instead). It lives alone in this
// file so that the lint exclusion for running a command the adapter
// assembled covers nothing else.
func launchCommand(ctx context.Context, l harness.Launch) *exec.Cmd {
	cmd := exec.CommandContext(ctx, l.Command[0], l.Command[1:]...)
	cmd.Dir, cmd.Env = l.Dir, l.Env
	return cmd
}
