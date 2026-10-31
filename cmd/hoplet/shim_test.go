package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/shim"
)

// TestMain lets the test binary stand in for the hoplet binary: with
// HOPLET_TEST_MAIN set it runs the command line it was given.
func TestMain(m *testing.M) {
	if os.Getenv("HOPLET_TEST_MAIN") != "" {
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func TestShimUsage(t *testing.T) {
	for _, args := range [][]string{
		{"shim"},
		{"shim", "--dir", "/tmp/x"},
		{"shim", "--dir", "/tmp/x", "--", "true"}, // no --cwd
		{"shim", "--cwd", "/tmp/x", "--", "true"}, // no --dir
		{"shim", "--", "true"},
		{"shim", "--bogus"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%q: unexpected stdout %q", args, out.String())
		}
		if !bytes.Contains(errb.Bytes(), []byte("usage")) && !bytes.Contains(errb.Bytes(), []byte("flag provided but not defined")) {
			t.Errorf("%q: stderr %q", args, errb.String())
		}
	}
}

func TestShimHelpExitsZero(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"shim", "--help"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !bytes.Contains(errb.Bytes(), []byte("setsid")) {
		t.Fatalf("help should tell the daemon how to start the shim: %q", errb.String())
	}
}

func TestShimSurvivesParentSignals(t *testing.T) {
	dir, err := os.MkdirTemp("", "shim")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command(os.Args[0], "shim", "--dir", dir, "--cwd", dir, "--", "sleep", "60")
	cmd.Env = append(os.Environ(), "HOPLET_TEST_MAIN=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }() // no-op once the shim has exited

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(shim.SocketPath(dir)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shim socket never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGPIPE} {
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := shim.Dial(ctx, dir, 0)
	if err != nil {
		t.Fatalf("shim did not survive HUP, INT and PIPE: %v", err)
	}
	c.Close()

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("shim after SIGTERM: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shim ignored SIGTERM")
	}
}
