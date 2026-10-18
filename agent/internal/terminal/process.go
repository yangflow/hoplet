package terminal

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// proc is a process seen by listProcs.
type proc struct{ pid, ppid int }

// listProcs lists every process of the machine with its parent.
func listProcs() ([]proc, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}
	var procs []proc
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		procs = append(procs, proc{pid, ppid})
	}
	return procs, nil
}

// identity names one process for good: a recycled PID has another start time.
type identity struct {
	pid   int
	start string
}

// descendants lists the processes below root (not root itself) with their start times.
func descendants(root int) []identity {
	procs, err := listProcs()
	if err != nil {
		return nil
	}
	children := map[int][]int{}
	for _, p := range procs {
		children[p.ppid] = append(children[p.ppid], p.pid)
	}
	var out []identity
	queue := []int{root}
	seen := map[int]bool{root: true}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range children[pid] {
			if seen[c] {
				continue
			}
			seen[c] = true
			queue = append(queue, c)
			if start, ok := startTime(c); ok {
				out = append(out, identity{c, start})
			}
		}
	}
	return out
}

// merge adds the identities of b that a lacks.
func merge(a, b []identity) []identity {
	have := map[int]bool{}
	for _, i := range a {
		have[i.pid] = true
	}
	for _, i := range b {
		if !have[i.pid] {
			a = append(a, i)
		}
	}
	return a
}

// killIdentities kills the processes that still have the start time they were seen with.
func killIdentities(ids []identity) {
	for _, id := range ids {
		if start, ok := startTime(id.pid); ok && start == id.start {
			_ = syscall.Kill(id.pid, syscall.SIGKILL)
		}
	}
}

// killGroup sends sig to the process group pgid.
func killGroup(pgid int, sig syscall.Signal) {
	if pgid > 1 {
		_ = syscall.Kill(-pgid, sig)
	}
}
