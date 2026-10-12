package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Sentinel errors of snapshot creation, inspection and restoration. Their text is the reason the agent
// reports to the core, so it names a condition and never carries file content.
var (
	ErrNotGitRepo      = errors.New("not-a-git-repository")
	ErrNoCommit        = errors.New("no-commit")
	ErrTooLarge        = errors.New("snapshot-too-large")
	ErrCorrupt         = errors.New("snapshot-corrupt")
	ErrBaseUnavailable = errors.New("base-commit-unavailable")
	ErrTreeMismatch    = errors.New("tree-mismatch")
	ErrWorkspaceMoved  = errors.New("workspace-changed-during-snapshot")
	ErrTargetExists    = errors.New("target-exists")
)

// gitArgs are placed before every subcommand: hooks and file system monitors of the user's repository
// never run for a command Hoplet starts, and git never asks a question on a terminal.
var gitArgs = []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false"}

func gitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append(append([]string(nil), gitArgs...), args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	return cmd
}

// git runs a git command in dir and returns its standard output.
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := gitCommand(ctx, dir, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", subcommand(args), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func gitLine(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := git(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// subcommand is the git subcommand of an argument list that may start with -c options.
func subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		return args[i]
	}
	return ""
}

// wrap makes a failed command's error carry the first line of its output.
func wrap(what string, err error, out []byte) error {
	text := strings.TrimSpace(string(out))
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	if text == "" {
		return fmt.Errorf("git %s: %w", what, err)
	}
	return fmt.Errorf("git %s: %w: %s", what, err, text)
}

// splitZ splits NUL-separated output and drops empty parts.
func splitZ(out []byte) []string {
	var parts []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			parts = append(parts, string(p))
		}
	}
	return parts
}
