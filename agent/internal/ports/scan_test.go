//go:build linux || darwin

package ports

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const helperEnv = "HOPLET_PORTS_TEST_LISTEN"

// TestMain lets the test binary serve as the child process that listens.
func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(2)
		}
		fmt.Println(ln.Addr().(*net.TCPAddr).Port)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestScannerFindsAChildListeningInADirectory runs the real scanner of this platform against a child process
// that listens on loopback from a temporary directory.
func TestScannerFindsAChildListeningInADirectory(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		if _, err := exec.LookPath("lsof"); err != nil {
			t.Skip("neither /proc nor lsof is available")
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.Command(exe)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the child did not report its port: %v", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snap, err := NewScanner().Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var found *Port
	for _, p := range Select(snap, dir, nil) {
		if p.Port == port {
			p := p
			found = &p
		}
	}
	if found == nil {
		t.Fatalf("port %d of the child in %s not found", port, dir)
	}
	if found.Address != Loopback || found.Process == "" {
		t.Fatalf("got %+v, want a loopback port with a process name", *found)
	}
	// Scoped by the process tree alone: this process is the child's parent.
	if got := Select(snap, t.TempDir(), []int{os.Getpid()}); !hasPort(got, port) {
		t.Fatalf("the child was not found as a descendant of this process: %+v", got)
	}
	if got := Select(snap, t.TempDir(), nil); hasPort(got, port) {
		t.Fatalf("a process outside the directory was reported: %+v", got)
	}
}

func hasPort(ps []Port, port int) bool {
	for _, p := range ps {
		if p.Port == port {
			return true
		}
	}
	return false
}
