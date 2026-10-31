package shim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

var errDiskFull = errors.New("injected: disk full")

// failingSpool passes the first okWrites writes through, then fails every
// write. Truncate, Sync and Close always reach the real file.
type failingSpool struct {
	spoolFile
	okWrites int64
	writes   atomic.Int64
}

func (f *failingSpool) Write(b []byte) (int, error) {
	if f.writes.Add(1) > f.okWrites {
		return 0, errDiskFull
	}
	return f.spoolFile.Write(b)
}

func TestPumpKeepsDrainingWhenSpoolFails(t *testing.T) {
	ctx := context.Background()
	// Far more output than a pipe buffer holds; appends fail after the start record.
	dir, done := startShimCfg(ctx, t, func(cfg *Config) {
		cfg.Command = []string{"sh", "-c", "yes 0123456789abcdef | head -n 200000"}
		cfg.Linger = 200 * time.Millisecond
		cfg.wrapSpool = func(f spoolFile) spoolFile { return &failingSpool{spoolFile: f, okWrites: 1} }
	})
	waitDone(t, done) // a stalled pump would leave the child blocked on its pipe and Run waiting

	b, err := os.ReadFile(SpoolPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var start Record
	if err := json.Unmarshal(bytes.SplitN(b, []byte("\n"), 2)[0], &start); err != nil || start.Kind != KindStart {
		t.Fatalf("spool %q, %v: want the start record first", b, err)
	}
	if bytes.Contains(b, []byte(`"k":"stdout"`)) {
		t.Fatal("output was recorded despite failing appends")
	}
	if err := syscall.Kill(start.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child %d did not exit: %v", start.Pid, err)
	}
}

func TestStartRecordFailureKillsChild(t *testing.T) {
	dir := shortDir(t)
	var logs bytes.Buffer
	err := Run(context.Background(), Config{
		Dir: dir, WorkDir: dir,
		Command:   []string{"sleep", "30"},
		Env:       os.Environ(),
		Logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
		wrapSpool: func(f spoolFile) spoolFile { return &failingSpool{spoolFile: f} },
	})
	if !errors.Is(err, errDiskFull) {
		t.Fatalf("Run: %v, want the append error", err)
	}
	var started struct {
		Msg string
		Pid int
	}
	for _, line := range bytes.Split(logs.Bytes(), []byte("\n")) {
		if json.Unmarshal(line, &started) == nil && started.Msg == "child started" {
			break
		}
	}
	if started.Pid == 0 {
		t.Fatalf("no pid in logs:\n%s", logs.String())
	}
	if err := syscall.Kill(-started.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process group %d survived a failed start: %v", started.Pid, err)
	}
}
