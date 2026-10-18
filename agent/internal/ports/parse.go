package ports

import (
	"encoding/hex"
	"strconv"
	"strings"
)

// The parsers below are plain functions over text so that they are tested on every platform.

// parseLsofListeners reads `lsof -nP -iTCP -sTCP:LISTEN -F pcnR`: a `p` line starts a process, `c` is its
// command name, `R` its parent and each `n` line the address of one listening socket.
func parseLsofListeners(out string) ([]Listener, map[int]int) {
	var ls []Listener
	parents := map[int]int{}
	pid, ppid, name := 0, 0, ""
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		val := line[1:]
		switch line[0] {
		case 'p':
			pid, ppid, name = atoi(val), 0, ""
		case 'c':
			name = val
		case 'R':
			ppid = atoi(val)
			if pid > 0 {
				parents[pid] = ppid
			}
		case 'n':
			i := strings.LastIndexByte(val, ':')
			if i < 0 || pid <= 0 {
				continue
			}
			port := atoi(val[i+1:])
			if port <= 0 {
				continue
			}
			ls = append(ls, Listener{PID: pid, Port: port, Class: classify(val[:i]), Name: name})
		}
	}
	return ls, parents
}

// parseLsofCwd reads `lsof -a -p … -d cwd -Fpn` into the current directory of each process.
func parseLsofCwd(out string) map[int]string {
	cwds := map[int]string{}
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid = atoi(line[1:])
		case 'n':
			if pid > 0 {
				cwds[pid] = line[1:]
			}
		}
	}
	return cwds
}

// parsePS reads `ps -A -o pid=,ppid=` into the parent of each process.
func parsePS(out string) map[int]int {
	parents := map[int]int{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, ppid := atoi(f[0]), atoi(f[1])
		if pid > 0 {
			parents[pid] = ppid
		}
	}
	return parents
}

// socket is a listening socket seen in /proc/net/tcp*.
type socket struct {
	port  int
	class string
}

// parseProcNetTCP reads /proc/net/tcp or tcp6 into the listening sockets by inode.
func parseProcNetTCP(text string) map[string]socket {
	out := map[string]socket{}
	for i, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 10 || f[3] != "0A" { // 0A is TCP_LISTEN
			continue
		}
		host, portHex, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil || port == 0 || f[9] == "0" {
			continue
		}
		out[f[9]] = socket{port: int(port), class: classifyHex(host)}
	}
	return out
}

// classifyHex classifies a /proc/net/tcp address: 8 hex digits (an IPv4 address in host byte order, so
// little-endian here) or 32 (four such words for IPv6).
func classifyHex(h string) string {
	raw, err := hex.DecodeString(h)
	if err != nil {
		return Other
	}
	switch len(raw) {
	case 4:
		return classifyIP4(raw[3], raw[2], raw[1], raw[0])
	case 16:
		// Each 4-byte word is stored little-endian.
		b := make([]byte, 16)
		for w := 0; w < 4; w++ {
			for k := 0; k < 4; k++ {
				b[w*4+k] = raw[w*4+3-k]
			}
		}
		zero := func(s []byte) bool {
			for _, c := range s {
				if c != 0 {
					return false
				}
			}
			return true
		}
		if zero(b) {
			return Any
		}
		if zero(b[:15]) && b[15] == 1 {
			return Loopback
		}
		if zero(b[:10]) && b[10] == 0xff && b[11] == 0xff { // ::ffff:a.b.c.d
			return classifyIP4(b[12], b[13], b[14], b[15])
		}
	}
	return Other
}

func classifyIP4(a, b, c, d byte) string {
	switch {
	case a == 0 && b == 0 && c == 0 && d == 0:
		return Any
	case a == 127:
		return Loopback
	}
	return Other
}

// parseStatParent reads the parent from /proc/<pid>/stat: `pid (comm) S ppid …`, where comm may hold spaces
// and parentheses, so the fields start after the last ')'.
func parseStatParent(stat string) (int, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(f[1])
	return n, err == nil
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
