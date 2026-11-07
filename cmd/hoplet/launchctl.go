package main

import (
	"context"
	"os/exec"
)

// runCommand runs launchctl for package launchd and systemctl for package
// systemd, which build its arguments from their units' names and paths. It lives alone in this file
// so that the lint exclusion for running a command with variable
// arguments covers nothing else.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
