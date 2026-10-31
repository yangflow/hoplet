package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/launchd"
	"github.com/yangflow/hoplet/internal/shim"
)

// fakeAgents is the LaunchAgent manager of every test: its user home is
// $HOPLET_TEST_USERHOME and its launchctl is fakeLaunchctl, so no test
// reaches the real launchctl or ~/Library/LaunchAgents.
func fakeAgents() (launchd.Manager, error) {
	userHome := os.Getenv("HOPLET_TEST_USERHOME")
	if userHome == "" {
		return launchd.Manager{}, errors.New("HOPLET_TEST_USERHOME is not set")
	}
	return launchd.Manager{UserHome: userHome, UID: os.Getuid(), Run: fakeLaunchctl(userHome)}, nil
}

// fakeLaunchctl records each call in launchctl.log under userHome and
// answers `print` as a running agent only while its plist exists.
func fakeLaunchctl(userHome string) launchd.Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		line := name + " " + strings.Join(args, " ") + "\n"
		f, err := os.OpenFile(filepath.Join(userHome, "launchctl.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		_, werr := f.WriteString(line)
		if err := errors.Join(werr, f.Close()); err != nil {
			return nil, err
		}
		if len(args) > 0 && args[0] == "print" {
			if _, err := os.Stat(launchd.Manager{UserHome: userHome}.Path()); err != nil {
				// A real *exec.ExitError, as launchctl's would be.
				return []byte("Could not find service"), exec.Command("sh", "-c", "exit 113").Run()
			}
			return []byte("state = running\n\tpid = " + strconv.Itoa(os.Getpid()) + "\n"), nil
		}
		return nil, nil
	}
}

// TestMain lets the test binary stand in for the hoplet binary: with
// HOPLET_TEST_MAIN set it runs the command line it was given. Its daemon
// starts harnesses with a fixed environment that keeps HOPLET_TEST_MAIN,
// so the shims it starts (this binary again) run as shims too, and serves
// the fake harness when HOPLET_TEST_FAKE names its binary.
// probeURL is what the test daemon's doctor reaches: HOPLET_TEST_PROBE, or
// an address nothing listens on, so that a test that forgets to set one
// never reaches the network.
func probeURL() string {
	if u := os.Getenv("HOPLET_TEST_PROBE"); u != "" {
		return u
	}
	return "http://127.0.0.1:1"
}

func TestMain(m *testing.M) {
	if os.Getenv("HOPLET_TEST_MAIN") != "" {
		a := app{
			stdout: os.Stdout, stderr: os.Stderr,
			env:      env.Static{"PATH=" + os.Getenv("PATH"), "LANG=en_US.UTF-8", "HOPLET_TEST_MAIN=1"},
			probeURL: probeURL(),
			agents:   fakeAgents, allowTempBinary: true,
		}
		hs := map[string]harness.Harness{}
		if bin := os.Getenv("HOPLET_TEST_FAKE"); bin != "" {
			hs["fake"] = &fake.Harness{Binary: bin, Script: os.Getenv("HOPLET_TEST_SCRIPT")}
		}
		a.harnesses = func(string) map[string]harness.Harness { return hs }
		os.Exit(a.run(os.Args[1:]))
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
