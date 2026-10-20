package resources

import (
	"bufio"
	"strconv"
	"strings"
)

// The parsers take the text a platform tool or /proc file prints. They live here, free of build tags, so every
// platform's fixtures run everywhere.

// parseCPUStat reads the first line of /proc/stat: the jiffies the machine spent busy and in total, all CPUs
// together. Idle and iowait count as idle.
func parseCPUStat(text string) (busy, total uint64, ok bool) {
	line, _, _ := strings.Cut(text, "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	var idle uint64
	for i, s := range f[1:] {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		if i >= 8 {
			break // guest and guest_nice are already part of user and nice
		}
		total += n
		if i == 3 || i == 4 {
			idle += n
		}
	}
	return total - idle, total, true
}

// parseMeminfo reads /proc/meminfo: the total memory and what is available without swapping, in bytes. Used
// memory is the difference.
func parseMeminfo(text string) (total, available uint64, ok bool) {
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		name, rest, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "MemTotal":
			total, haveTotal = n*1024, true
		case "MemAvailable":
			available, haveAvail = n*1024, true
		}
	}
	return total, available, haveTotal && haveAvail
}

// parsePidStat reads /proc/<pid>/stat: the parent, the CPU time (user plus system, in jiffies) and the resident
// pages. The command name sits in parentheses and may hold spaces and parentheses, so fields count from the
// last closing one.
func parsePidStat(text string) (ppid int, ticks uint64, rssPages uint64, ok bool) {
	i := strings.LastIndexByte(text, ')')
	if i < 0 {
		return 0, 0, 0, false
	}
	f := strings.Fields(text[i+1:])
	// f[0] is field 3 (state); ppid is field 4, utime 14, stime 15, rss 24.
	if len(f) < 22 {
		return 0, 0, 0, false
	}
	pp, err1 := strconv.Atoi(f[1])
	ut, err2 := strconv.ParseUint(f[11], 10, 64)
	st, err3 := strconv.ParseUint(f[12], 10, 64)
	rss, err4 := strconv.ParseInt(f[21], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || rss < 0 {
		return 0, 0, 0, false
	}
	return pp, ut + st, uint64(rss), true
}

// psProc is one line of `ps -A -o pid=,ppid=,rss=,pcpu=`: rss in bytes, pcpu in percent of one core.
type psProc struct {
	PID, PPID int
	RSS       uint64
	PCPU      float64
}

func parsePS(text string) []psProc {
	var out []psProc
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		kb, err3 := strconv.ParseUint(f[2], 10, 64)
		cpu, err4 := strconv.ParseFloat(f[3], 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || pid <= 0 {
			continue
		}
		out = append(out, psProc{PID: pid, PPID: ppid, RSS: kb * 1024, PCPU: cpu})
	}
	return out
}

// parseVMStat reads `vm_stat` of macOS and returns the bytes that count as not used: free, inactive and
// speculative pages. Wired, active and compressed pages are used.
func parseVMStat(text string) (reclaimable uint64, ok bool) {
	head, _, _ := strings.Cut(text, "\n")
	// "Mach Virtual Memory Statistics: (page size of 16384 bytes)"
	_, after, found := strings.Cut(head, "page size of ")
	if !found {
		return 0, false
	}
	sizeText, _, _ := strings.Cut(after, " ")
	pageSize, err := strconv.ParseUint(sizeText, 10, 64)
	if err != nil || pageSize == 0 {
		return 0, false
	}
	var pages uint64
	seen := 0
	for _, line := range strings.Split(text, "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(name) {
		case "Pages free", "Pages inactive", "Pages speculative":
			n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(rest), "."), 10, 64)
			if err != nil {
				return 0, false
			}
			pages += n
			seen++
		}
	}
	return pages * pageSize, seen == 3
}
