package resources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// procReader reads /proc. CPU figures are differences between two readings, so the first reading has none.
// It reads only the counters of /proc/stat, /proc/meminfo and /proc/<pid>/stat; never cmdline, environ or exe.
type procReader struct {
	root     string
	pageSize uint64

	have      bool
	prevBusy  uint64
	prevTotal uint64
	prevTicks map[int]uint64
}

func newProcReader(root string) *procReader {
	return &procReader{root: root, pageSize: uint64(os.Getpagesize())}
}

func (r *procReader) Read(ctx context.Context) (Reading, error) {
	var out Reading
	statRaw, err := os.ReadFile(filepath.Join(r.root, "stat"))
	if err != nil {
		return out, err
	}
	busy, total, ok := parseCPUStat(string(statRaw))
	if !ok {
		return out, errors.New("unreadable /proc/stat")
	}
	memRaw, err := os.ReadFile(filepath.Join(r.root, "meminfo"))
	if err != nil {
		return out, err
	}
	memTotal, avail, ok := parseMeminfo(string(memRaw))
	if !ok {
		return out, errors.New("unreadable /proc/meminfo")
	}
	out.MemTotal = memTotal
	if avail < memTotal {
		out.MemUsed = memTotal - avail
	}

	var dTotal uint64
	known := r.have && total > r.prevTotal
	if known {
		dTotal = total - r.prevTotal
		if busy >= r.prevBusy {
			cpu := clampPercent(100 * float64(busy-r.prevBusy) / float64(dTotal))
			out.CPU = &cpu
		}
	}

	entries, err := os.ReadDir(r.root)
	if err != nil {
		return out, err
	}
	ticksNow := make(map[int]uint64, len(entries))
	for _, e := range entries {
		if ctx.Err() != nil {
			return Reading{}, ctx.Err()
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(r.root, e.Name(), "stat"))
		if err != nil {
			continue // gone since the listing
		}
		ppid, ticks, rss, ok := parsePidStat(string(raw))
		if !ok {
			continue
		}
		ticksNow[pid] = ticks
		p := Proc{PID: pid, PPID: ppid, RSS: rss * r.pageSize}
		if known {
			// A process that is new, or whose ID was reused, has used at most what it shows now.
			delta := ticks
			if prev, seen := r.prevTicks[pid]; seen && ticks >= prev {
				delta = ticks - prev
			}
			cpu := clampPercent(100 * float64(delta) / float64(dTotal))
			p.CPU = &cpu
		}
		out.Procs = append(out.Procs, p)
	}
	r.have, r.prevBusy, r.prevTotal, r.prevTicks = true, busy, total, ticksNow
	return out, nil
}
