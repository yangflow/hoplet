package main

import (
	"context"
	"os/exec"
)

// runCommand runs launchctl for package launchd, which builds its
// arguments from the agent's label and paths. It lives alone in this file
// so that the lint exclusion for running a command with variable
// arguments covers nothing else.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
