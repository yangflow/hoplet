package shim

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func isExit(r Record) bool { return r.Kind == KindExit }

func TestEchoStdinStderrAndExit(t *testing.T) {
	dir, done := startShim(context.Background(), t, time.Minute, "echo")
	c := dial(t, dir, 0)
	if c.Pid() == 0 {
		t.Fatal("welcome without pid")
	}
	for _, line := range []string{"hello", "err", "quit"} {
		if err := c.Stdin(line); err != nil {
			t.Fatal(err)
		}
	}
	got, final := recvUntil(t, c, isExit)
	want := map[RecordKind][]string{
		KindStdin:  {"hello", "err", "quit"},
		KindStdout: {"echo:hello"},
		KindStderr: {"oops"},
	}
	for kind, lines := range want {
		var have []string
		for _, r := range got {
			if r.Kind == kind {
				have = append(have, r.Data)
			}
		}
		if !slices.Equal(have, lines) {
			t.Errorf("%s records %q, want %q", kind, have, lines)
		}
	}
	if got[0].Kind != KindStart || got[0].Pid != c.Pid() {
		t.Errorf("first record %+v, want start with pid %d", got[0], c.Pid())
	}
	if exit := got[len(got)-1]; exit.Code != 3 || exit.Signal != "" {
		t.Errorf("exit record %+v, want code 3", exit)
	}
	if err := c.Stdin("late"); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.Recv()
	var se *Error
	if !errors.As(err, &se) || se.Code != CodeExited || !strings.Contains(se.Message, "exited") {
		t.Fatalf("stdin after exit: %v", err)
	}
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	if _, err := os.Stat(SocketPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func TestCancelStopsChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShim(ctx, t, time.Minute, "sleep")
	cancel()
	waitDone(t, done)
	b, err := os.ReadFile(SpoolPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"k":"exit"`)) {
		t.Fatalf("spool has no exit record:\n%s", b)
	}
}

func TestLingerEndsShimWithoutAck(t *testing.T) {
	dir, done := startShim(context.Background(), t, 100*time.Millisecond, "tick", "1", "0")
	waitDone(t, done)
	b, err := os.ReadFile(SpoolPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"k":"exit"`)) {
		t.Fatalf("spool has no exit record:\n%s", b)
	}
}
