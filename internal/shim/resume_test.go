package shim

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// TestResumeAfterClientDrop checks the property the daemon relies on: a
// client that disappears without warning reconnects from its last offset
// and receives every later record exactly once, while the child keeps
// running undisturbed.
func TestResumeAfterClientDrop(t *testing.T) {
	const n = 30
	dir, done := startShim(context.Background(), t, time.Minute, "tick", fmt.Sprint(n), "20")
	first := dial(t, dir, 0)
	recs, off := recvUntil(t, first, func(r Record) bool { return r.Data == "tick 4" })
	first.Close() // the daemon vanishes mid-stream

	time.Sleep(150 * time.Millisecond) // the child keeps ticking meanwhile
	second := dial(t, dir, off)
	if second.Pid() != first.Pid() {
		t.Fatalf("child changed: pid %d then %d", first.Pid(), second.Pid())
	}
	more, final := recvUntil(t, second, isExit)
	var ticks []string
	for _, r := range append(recs, more...) {
		if r.Kind == KindStdout {
			ticks = append(ticks, r.Data)
		}
	}
	if len(ticks) != n {
		t.Fatalf("got %d ticks, want %d: %q", len(ticks), n, ticks)
	}
	for i, tk := range ticks {
		if tk != fmt.Sprintf("tick %d", i) {
			t.Fatalf("tick %d is %q: lost, repeated or reordered", i, tk)
		}
	}
	if exit := more[len(more)-1]; exit.Code != 0 {
		t.Fatalf("exit %+v", exit)
	}
	if err := second.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}

func TestNewClientReplacesOld(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShim(ctx, t, time.Minute, "sleep")
	old := dial(t, dir, 0)
	fresh := dial(t, dir, 0)
	for {
		if _, _, err := old.Recv(); err != nil {
			break // replaced: the old connection is closed
		}
	}
	if rec, _, err := fresh.Recv(); err != nil || rec.Kind != KindStart {
		t.Fatalf("new client got %+v, %v; want the start record", rec, err)
	}
	cancel()
	waitDone(t, done)
}

func TestHelloRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel() }()
	dir, done := startShim(ctx, t, time.Minute, "sleep")
	for _, tc := range []struct {
		hello Message
		code  string
	}{
		{Message{Type: MsgHello, Version: 99}, CodeVersion},
		{Message{Type: MsgHello, Version: ProtocolVersion, Offset: 1 << 40}, CodeBadOffset},
		{Message{Type: MsgHello, Version: ProtocolVersion, Offset: 3}, CodeBadOffset}, // inside the start record
	} {
		hello := tc.hello
		nc, err := net.Dial("unix", SocketPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if err := writeFrame(nc, hello); err != nil {
			t.Fatal(err)
		}
		var m Message
		if err := readFrame(nc, &m); err != nil {
			t.Fatal(err)
		}
		nc.Close()
		if m.Type != MsgError || m.Code != tc.code || m.Version != ProtocolVersion {
			t.Fatalf("hello %+v answered with %+v, want error %s at v%d", hello, m, tc.code, ProtocolVersion)
		}
	}
	_, err := Dial(context.Background(), dir, -1)
	var se *Error
	if !errors.As(err, &se) || se.Code != CodeBadOffset {
		t.Fatalf("negative offset: %v", err)
	}
	cancel()
	waitDone(t, done)
}

func TestSignalTerminatesChild(t *testing.T) {
	dir, done := startShim(context.Background(), t, time.Minute, "sleep")
	c := dial(t, dir, 0)
	if err := c.Signal("TERM"); err != nil {
		t.Fatal(err)
	}
	got, final := recvUntil(t, c, isExit)
	if exit := got[len(got)-1]; exit.Signal != "TERM" {
		t.Fatalf("exit %+v, want signal TERM", exit)
	}
	if err := c.Signal("BOGUS"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Recv(); err == nil || !strings.Contains(err.Error(), "unknown signal") {
		t.Fatalf("bogus signal: %v", err)
	}
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}
