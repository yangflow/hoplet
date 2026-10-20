//go:build darwin

package resources

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// NewReader runs one `ps` for the processes and `vm_stat` for the memory. Memory in use is the total
// (`sysctl hw.memsize`) minus the free, inactive and speculative pages, which leaves the active, wired and
// compressed pages: close to what Activity Monitor calls Memory Used. CPU is the sum of the `%cpu` that `ps`
// prints, divided by the number of CPUs; macOS computes it as a decaying average, so it lags a little.
func NewReader() Reader { return &psReader{cpus: runtime.NumCPU()} }

type psReader struct {
	cpus     int
	memTotal uint64
}

func (r *psReader) Read(ctx context.Context) (Reading, error) {
	var out Reading
	if r.memTotal == 0 {
		raw, err := run(ctx, "sysctl", "-n", "hw.memsize")
		if err != nil {
			return out, err
		}
		n, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
		if err != nil || n == 0 {
			return out, errors.New("unreadable hw.memsize")
		}
		r.memTotal = n
	}
	vm, err := run(ctx, "vm_stat")
	if err != nil {
		return out, err
	}
	free, ok := parseVMStat(vm)
	if !ok {
		return out, errors.New("unreadable vm_stat")
	}
	psOut, err := run(ctx, "ps", "-A", "-o", "pid=,ppid=,rss=,pcpu=")
	if err != nil {
		return out, err
	}
	out.MemTotal = r.memTotal
	if free < r.memTotal {
		out.MemUsed = r.memTotal - free
	}
	var sum float64
	for _, p := range parsePS(psOut) {
		cpu := clampPercent(p.PCPU / float64(r.cpus))
		sum += p.PCPU
		out.Procs = append(out.Procs, Proc{PID: p.PID, PPID: p.PPID, RSS: p.RSS, CPU: &cpu})
	}
	machine := clampPercent(sum / float64(r.cpus))
	out.CPU = &machine
	return out, nil
}

// run executes a tool with the C locale so numbers print with a point.
func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	return string(out), err
}
