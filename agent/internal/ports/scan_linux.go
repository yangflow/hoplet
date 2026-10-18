package ports

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// NewScanner reads /proc: the listening sockets of /proc/net/tcp and tcp6, matched to the processes of the
// agent's user by the socket inodes in /proc/<pid>/fd. Only the executable name, the current directory and the
// parent of a process are read; never cmdline, environ or the content of a connection.
func NewScanner() Scanner { return procScanner{root: "/proc"} }

type procScanner struct{ root string }

func (s procScanner) Scan(ctx context.Context) (Snapshot, error) {
	sockets := map[string]socket{}
	for _, name := range []string{"net/tcp", "net/tcp6"} {
		raw, err := os.ReadFile(filepath.Join(s.root, name))
		if err != nil {
			if name == "net/tcp" {
				return Snapshot{}, err
			}
			continue // no IPv6
		}
		for inode, sk := range parseProcNetTCP(string(raw)) {
			sockets[inode] = sk
		}
	}
	snap := Snapshot{Parents: map[int]int{}}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return Snapshot{}, err
	}
	for _, e := range entries {
		if ctx.Err() != nil {
			return Snapshot{}, ctx.Err()
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join(s.root, e.Name())
		if raw, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
			if ppid, ok := parseStatParent(string(raw)); ok {
				snap.Parents[pid] = ppid
			}
		}
		if len(sockets) == 0 {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue // another user's process
		}
		var held []socket
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			if sk, ok := sockets[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")]; ok {
				held = append(held, sk)
			}
		}
		if len(held) == 0 {
			continue
		}
		name, cwd := procName(dir), ""
		if c, err := os.Readlink(filepath.Join(dir, "cwd")); err == nil {
			cwd = strings.TrimSuffix(c, " (deleted)")
		}
		for _, sk := range held {
			snap.Listeners = append(snap.Listeners, Listener{PID: pid, Port: sk.port, Class: sk.class, Name: name, Cwd: cwd})
		}
	}
	return snap, nil
}

// procName is the base name of the executable, else the kernel's short name (comm) of the process.
func procName(dir string) string {
	if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
		if name := filepath.Base(strings.TrimSuffix(exe, " (deleted)")); name != "" && name != "." && name != "/" {
			return name
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "comm"))
	return strings.TrimSpace(string(raw))
}
