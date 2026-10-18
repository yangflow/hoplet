// Package ports finds the listening TCP ports of the processes started from a session's working directory
// (ADR 0026), for the sessions a browser has open. It reads sockets and process attributes only to filter
// them; what leaves the package is the port, the address class and the executable's base name. Command lines,
// arguments, environment, process IDs and the content of connections are never read or reported.
package ports

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
)

// Address classes of a listening socket.
const (
	Loopback = "loopback"
	Any      = "any"
	Other    = "other"
)

const (
	// MaxPorts is the most ports one report holds.
	MaxPorts = 16
	// MaxName is the most characters of an executable name.
	MaxName = 64
)

// Listener is one listening TCP socket and the process that holds it. PID and Cwd only scope the scan.
type Listener struct {
	PID  int
	Port int
	// Class is Loopback, Any or Other.
	Class string
	// Name is the executable's name.
	Name string
	// Cwd is the current directory of the process, "" when it could not be read.
	Cwd string
}

// Snapshot is what one scan sees: the listening sockets of the agent's user and the parent of every process
// it could list, to follow a process up to the session's own processes.
type Snapshot struct {
	Listeners []Listener
	Parents   map[int]int
}

// Scanner reads the machine. The operating system implementations are in scan_linux.go and scan_darwin.go.
type Scanner interface {
	Scan(ctx context.Context) (Snapshot, error)
}

// Port is one reported port.
type Port struct {
	Port    int
	Address string
	Process string
}

// Select keeps the listeners that belong to a session: those whose current directory is inside dir, and those
// that descend from one of roots (the session's Claude Code process or its terminal shell; a root <= 0 is
// ignored). The result has one entry per port, sorted by port and cut at MaxPorts.
func Select(snap Snapshot, dir string, roots []int) []Port {
	dir = resolve(dir)
	rootSet := map[int]bool{}
	for _, r := range roots {
		if r > 0 {
			rootSet[r] = true
		}
	}
	byPort := map[int]Listener{}
	for _, l := range snap.Listeners {
		if l.Port < 1 || l.Port > 65535 {
			continue
		}
		if !inside(l.Cwd, dir) && !descends(l.PID, rootSet, snap.Parents) {
			continue
		}
		cur, ok := byPort[l.Port]
		// A port is listened on both stacks or by several processes; report the widest reach.
		if !ok || rank(l.Class) > rank(cur.Class) || (rank(l.Class) == rank(cur.Class) && l.PID < cur.PID) {
			byPort[l.Port] = l
		}
	}
	out := make([]Port, 0, len(byPort))
	for _, l := range byPort {
		out = append(out, Port{Port: l.Port, Address: l.Class, Process: CleanName(l.Name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	if len(out) > MaxPorts {
		out = out[:MaxPorts]
	}
	return out
}

func rank(class string) int {
	switch class {
	case Any:
		return 2
	case Other:
		return 1
	}
	return 0
}

// resolve returns dir with symlinks followed, as the system reports the directories of processes.
func resolve(dir string) string {
	if dir == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	return filepath.Clean(dir)
}

func inside(cwd, dir string) bool {
	if cwd == "" || dir == "" || dir == string(filepath.Separator) {
		return false
	}
	cwd = filepath.Clean(cwd)
	return cwd == dir || strings.HasPrefix(cwd, dir+string(filepath.Separator))
}

func descends(pid int, roots map[int]bool, parents map[int]int) bool {
	if len(roots) == 0 {
		return false
	}
	for hops := 0; pid > 1 && hops < 256; hops++ {
		if roots[pid] {
			return true
		}
		pid = parents[pid]
	}
	return false
}

// CleanName makes the executable name of a report: the base name, without control characters, cut at MaxName
// characters (and at MaxName UTF-16 units, which is how the wire schema counts).
func CleanName(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ToValidUTF8(name, "")
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	var b strings.Builder
	chars, units := 0, 0
	for _, r := range name {
		n := utf16.RuneLen(r)
		if n < 0 {
			n = 1
		}
		if chars+1 > MaxName || units+n > MaxName {
			break
		}
		chars++
		units += n
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// classify names the reach of a bound address in the text form lsof prints: "*", an IPv4 or an IPv6 address
// with or without brackets.
func classify(host string) string {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	switch host {
	case "*", "0.0.0.0", "::":
		return Any
	case "::1":
		return Loopback
	}
	if strings.HasPrefix(host, "127.") {
		return Loopback
	}
	if rest, ok := strings.CutPrefix(strings.ToLower(host), "::ffff:"); ok {
		return classify(rest)
	}
	return Other
}
