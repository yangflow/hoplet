package ports

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// NewScanner runs lsof for the listening TCP sockets of the agent's user and for the current directory of
// their processes, and ps for the process tree. lsof prints names and directories only; the command line and
// the environment are not asked for.
func NewScanner() Scanner { return lsofScanner{} }

type lsofScanner struct{}

func (lsofScanner) Scan(ctx context.Context) (Snapshot, error) {
	out, err := run(ctx, "lsof", "-nP", "+c", "0", "-iTCP", "-sTCP:LISTEN", "-F", "pcnR")
	if err != nil {
		return Snapshot{}, err
	}
	listeners, lsofParents := parseLsofListeners(out)
	if len(listeners) == 0 {
		return Snapshot{Parents: lsofParents}, nil
	}

	seen := map[int]bool{}
	var pids []string
	for _, l := range listeners {
		if !seen[l.PID] {
			seen[l.PID] = true
			pids = append(pids, strconv.Itoa(l.PID))
		}
	}
	cwdOut, err := run(ctx, "lsof", "-nP", "-a", "-p", strings.Join(pids, ","), "-d", "cwd", "-F", "pn")
	if err != nil {
		return Snapshot{}, err
	}
	cwds := parseLsofCwd(cwdOut)
	for i := range listeners {
		listeners[i].Cwd = cwds[listeners[i].PID]
	}

	parents := lsofParents
	if psOut, err := run(ctx, "ps", "-A", "-o", "pid=,ppid="); err == nil {
		parents = parsePS(psOut)
	}
	return Snapshot{Listeners: listeners, Parents: parents}, nil
}

// run executes a tool with the C locale; lsof exits 1 when it finds nothing, which is an empty answer.
func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	var ee *exec.ExitError
	if err != nil && errors.As(err, &ee) && name == "lsof" && ee.ExitCode() == 1 {
		return string(out), nil
	}
	return string(out), err
}
