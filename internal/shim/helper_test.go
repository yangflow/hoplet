package shim

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// TestMain lets the test binary act as the shim's child: when
// HOPLET_SHIM_HELPER is set it runs helperMain instead of the tests.
func TestMain(m *testing.M) {
	if os.Getenv("HOPLET_SHIM_HELPER") != "" {
		helperMain(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

// helperMain modes:
//
//	echo        echo stdin lines as "echo:<line>"; "err" writes to stderr; "quit" exits 3
//	tick N MS   print "tick <i>" N times, MS milliseconds apart, then exit 0
//	sleep       block until killed
//	ignoreterm  ignore SIGTERM, print "ready", block until killed
//	bigline N   write N bytes of 'a' without a newline, then exit 0
//	rawbytes    write one line of invalid UTF-8, then the line "ok"
func helperMain(args []string) {
	switch args[0] {
	case "echo":
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			switch line := sc.Text(); line {
			case "quit":
				os.Exit(3)
			case "err":
				fmt.Fprintln(os.Stderr, "oops")
			default:
				fmt.Println("echo:" + line)
			}
		}
		os.Exit(0)
	case "tick":
		n, _ := strconv.Atoi(args[1])
		ms, _ := strconv.Atoi(args[2])
		for i := range n {
			fmt.Printf("tick %d\n", i)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		os.Exit(0)
	case "sleep":
		time.Sleep(time.Hour)
	case "ignoreterm":
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("ready")
		time.Sleep(time.Hour)
	case "bigline":
		n, _ := strconv.Atoi(args[1])
		chunk := make([]byte, 64<<10)
		for i := range chunk {
			chunk[i] = 'a'
		}
		for n > 0 {
			w := min(n, len(chunk))
			if _, err := os.Stdout.Write(chunk[:w]); err != nil {
				os.Exit(1)
			}
			n -= w
		}
		os.Exit(0)
	case "rawbytes":
		os.Stdout.Write([]byte{0xff, 0xfe, '\n'})
		fmt.Println("ok")
		os.Exit(0)
	}
	os.Exit(2)
}

// shortDir returns a temp dir short enough for a Unix socket path.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "shim")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// startShim runs a shim whose child is this test binary in the given mode.
// It returns the dir and a channel carrying Run's result.
func startShim(ctx context.Context, t *testing.T, linger time.Duration, mode ...string) (string, <-chan error) {
	t.Helper()
	return startShimCfg(ctx, t, func(cfg *Config) {
		cfg.Linger = linger
		cfg.Command = append(cfg.Command, mode...)
	})
}

// startShimCfg is startShim with a hook to adjust the Config before Run. The
// hook sees Dir, WorkDir, Env and a Command holding just this test binary.
func startShimCfg(ctx context.Context, t *testing.T, mut func(*Config)) (string, <-chan error) {
	t.Helper()
	dir := shortDir(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Dir:     dir,
		WorkDir: dir,
		Command: []string{exe},
		Env:     append(os.Environ(), "HOPLET_SHIM_HELPER=1"),
		Linger:  time.Minute,
	}
	mut(&cfg)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	return dir, waitSocket(t, dir, done)
}

// waitSocket waits for the shim's socket to appear.
func waitSocket(t *testing.T, dir string, done <-chan error) <-chan error {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(SocketPath(dir)); err == nil {
			return done
		}
		select {
		case err := <-done:
			t.Fatalf("shim exited before serving: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("shim socket never appeared")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func dial(t *testing.T, dir string, off int64) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, dir, off)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// recvUntil reads records until pred matches one, returning all records
// read and the offset after the matching one.
func recvUntil(t *testing.T, c *Client, pred func(Record) bool) ([]Record, int64) {
	t.Helper()
	var got []Record
	for {
		rec, off, err := c.Recv()
		if err != nil {
			t.Fatalf("recv after %v: %v", got, err)
		}
		got = append(got, rec)
		if pred(rec) {
			return got, off
		}
	}
}

func waitDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shim did not exit")
	}
}
