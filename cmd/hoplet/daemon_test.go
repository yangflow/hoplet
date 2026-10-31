package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/client"
)

func TestDaemonUsage(t *testing.T) {
	for args, want := range map[string]int{"--help": 0, "extra": 2, "--bogus": 2} {
		var out, errb bytes.Buffer
		if code := run([]string{"daemon", args}, &out, &errb); code != want {
			t.Errorf("daemon %s: exit %d, want %d", args, code, want)
		}
		if out.Len() != 0 {
			t.Errorf("daemon %s: unexpected stdout %q", args, out.String())
		}
	}
}

// TestDaemonServesAndStopsOnSIGTERM runs `hoplet daemon` as a process: it
// serves clients, refuses a second daemon for the same home and exits 0 on
// SIGTERM.
func TestDaemonServesAndStopsOnSIGTERM(t *testing.T) {
	home, err := os.MkdirTemp("", "hd") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	daemon := func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "daemon", "--home", home)
		cmd.Env = append(os.Environ(), "HOPLET_TEST_MAIN=1")
		return cmd
	}
	first := daemon(context.Background())
	logPath := home + "/daemon.log"
	logf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	first.Stderr = logf
	log := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- first.Wait() }()
	reaped := false // set once exited has been received from
	defer func() {
		if !reaped {
			_ = first.Process.Kill()
			t.Logf("daemon stopped: %v", <-exited)
		}
	}()
	var c *client.Client
	deadline := time.Now().Add(15 * time.Second)
	for c == nil {
		if o, err := client.HomeOptions(home, "device:test"); err == nil {
			c, _ = client.Dial(context.Background(), o)
		}
		if c != nil {
			break
		}
		select {
		case err := <-exited:
			reaped = true
			t.Fatalf("the daemon exited before serving: %v; log:\n%s", err, log())
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never served; log:\n%s", log())
		}
	}
	defer c.Close()
	if ss, err := c.Sessions(context.Background()); err != nil || len(ss) != 0 {
		t.Fatalf("sessions %v %v", ss, err)
	}

	// Bounded: a second daemon that wrongly served would never exit.
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	second := daemon(sctx)
	out, err := second.CombinedOutput()
	if code := second.ProcessState.ExitCode(); code != 1 || !bytes.Contains(out, []byte("already running")) {
		t.Fatalf("second daemon: exit %d (%v), output %q", code, err, out)
	}

	if err := first.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err = <-exited
	reaped = true
	if err != nil {
		t.Fatalf("daemon exit: %v; log:\n%s", err, log())
	}
}
