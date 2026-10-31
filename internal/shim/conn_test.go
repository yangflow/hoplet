package shim

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func asError(t *testing.T, err error) *Error {
	t.Helper()
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want *Error", err)
	}
	return se
}

func TestSecondShimIsLocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShim(ctx, t, time.Minute, "sleep")
	err := Run(context.Background(), Config{Dir: dir, WorkDir: dir, Command: []string{"true"}})
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Run: %v, want ErrLocked", err)
	}
	if _, err := os.Stat(SocketPath(dir)); err != nil {
		t.Fatalf("the owner's socket was disturbed: %v", err)
	}
	cancel()
	waitDone(t, done)
}

func TestRunValidatesConfig(t *testing.T) {
	dir := shortDir(t)
	for name, cfg := range map[string]Config{
		"no command": {Dir: dir, WorkDir: dir},
		"no workdir": {Dir: dir, Command: []string{"true"}},
		"long path":  {Dir: dir + "/" + strings.Repeat("d", 100), WorkDir: dir, Command: []string{"true"}},
	} {
		if err := Run(context.Background(), cfg); err == nil {
			t.Errorf("%s: Run accepted the config", name)
		}
	}
}

func TestSocketIsPrivate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShim(ctx, t, time.Minute, "sleep")
	st, err := os.Stat(SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode %o, want 600", perm)
	}
	cancel()
	waitDone(t, done)
}

func TestBadHelloKeepsLiveClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShim(ctx, t, time.Minute, "echo")
	live := dial(t, dir, 0)
	for _, hello := range []Message{
		{Type: MsgHello, Version: 99},
		{Type: MsgHello, Version: ProtocolVersion, Offset: 1 << 40},
		{Type: MsgStdin},
	} {
		nc, err := net.Dial("unix", SocketPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if err := writeFrame(nc, hello); err != nil {
			t.Fatal(err)
		}
		var m Message
		if err := readFrame(nc, &m); err != nil || m.Type != MsgError {
			t.Fatalf("probe %+v: %+v, %v", hello, m, err)
		}
		nc.Close()
	}
	if err := live.Stdin("still here"); err != nil {
		t.Fatal(err)
	}
	recvUntil(t, live, func(r Record) bool { return r.Data == "echo:still here" })
	cancel()
	waitDone(t, done)
}

func TestDialHonorsContext(t *testing.T) {
	dir := shortDir(t)
	ln, err := net.Listen("unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			nc, err := ln.Accept() // accept and never answer
			if err != nil {
				return
			}
			defer nc.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	if _, err := Dial(ctx, dir, 0); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial: %v, want deadline exceeded", err)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("Dial took %v", took)
	}
	// Cancellation without a deadline also ends the handshake.
	ctx2, cancel2 := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel2)
	if _, err := Dial(ctx2, dir, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Dial after cancel: %v", err)
	}
}

func TestDialRejectsWelcomeVersion(t *testing.T) {
	dir := shortDir(t)
	ln, err := net.Listen("unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		defer nc.Close()
		var m Message
		if readFrame(nc, &m) == nil {
			_ = writeFrame(nc, Message{Type: MsgWelcome, Version: 7, Pid: 1})
		}
	}()
	if _, err := Dial(context.Background(), dir, 0); asError(t, err).Code != CodeVersion {
		t.Fatalf("Dial: %v", err)
	}
}

func TestStdinBusyThenKill(t *testing.T) {
	dir, done := startShim(context.Background(), t, time.Minute, "sleep") // never reads stdin
	c := dial(t, dir, 0)
	var busy atomic.Bool
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		for {
			rec, _, err := c.Recv()
			var se *Error
			switch {
			case errors.As(err, &se) && se.Code == CodeStdinBusy:
				busy.Store(true)
			case err != nil && !errors.As(err, &se):
				return
			case err == nil && rec.Kind == KindExit:
				return
			}
		}
	}()
	line := strings.Repeat("x", 4096)
	for i := 0; i < 5000 && !busy.Load(); i++ {
		if err := c.Stdin(line); err != nil {
			t.Fatal(err)
		}
		if i%64 == 63 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !busy.Load() {
		t.Fatal("stdin never reported busy")
	}
	if err := c.Signal("KILL"); err != nil { // must not wait behind stdin
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("no exit record after KILL with a full stdin queue")
	}
	waitDoneAfterLinger(t, dir, done)
}

// waitDoneAfterLinger acknowledges the whole spool, which ends the shim once
// it has exited, and waits for Run.
func waitDoneAfterLinger(t *testing.T, dir string, done <-chan error) {
	t.Helper()
	c := dial(t, dir, 0)
	_, final := recvUntil(t, c, isExit)
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}

func TestCloseStdinEndsChild(t *testing.T) {
	dir, done := startShim(context.Background(), t, time.Minute, "echo")
	c := dial(t, dir, 0)
	if err := c.Stdin("one"); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseStdin(); err != nil {
		t.Fatal(err)
	}
	got, final := recvUntil(t, c, isExit)
	var echoed bool
	for _, r := range got {
		echoed = echoed || r.Data == "echo:one"
	}
	if exit := got[len(got)-1]; !echoed || exit.Code != 0 || exit.Signal != "" {
		t.Fatalf("records %+v: want echo:one then a clean exit on EOF", got)
	}
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}

func TestStdinWriteFailureIsRecorded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShim(ctx, t, time.Minute, "sleep")
	c := dial(t, dir, 0)
	if err := c.CloseStdin(); err != nil {
		t.Fatal(err)
	}
	if err := c.Stdin("lost"); err != nil {
		t.Fatal(err)
	}
	got, _ := recvUntil(t, c, func(r Record) bool { return r.Kind == KindStdinError })
	if prev := got[len(got)-2]; prev.Kind != KindStdin || prev.Data != "lost" {
		t.Fatalf("records %+v: the stdin record must precede its error", got)
	}
	cancel()
	waitDone(t, done)
}

func TestStdinValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShim(ctx, t, time.Minute, "sleep")
	c := dial(t, dir, 0)
	for line, code := range map[string]string{
		"two\nlines":                     CodeBadRequest,
		strings.Repeat("y", ChunkSize+1): CodeTooLarge,
	} {
		if err := c.Stdin(line); err != nil {
			t.Fatal(err)
		}
		recvErr := func() error {
			for {
				if _, _, err := c.Recv(); err != nil {
					return err
				}
			}
		}()
		if se := asError(t, recvErr); se.Code != code {
			t.Fatalf("stdin %.10q: code %s, want %s", line, se.Code, code)
		}
	}
	cancel()
	waitDone(t, done)
}

func TestCorruptSpoolIsReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShimCfg(ctx, t, func(cfg *Config) {
		cfg.Command = append(cfg.Command, "sleep")
		if err := os.WriteFile(SpoolPath(cfg.Dir), []byte("not json\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	c := dial(t, dir, 0)
	_, _, err := c.Recv()
	if se := asError(t, err); se.Code != CodeCorrupt {
		t.Fatalf("Recv: %v", err)
	}
	// A client past the damage still works.
	c2 := dial(t, dir, int64(len("not json\n")))
	if rec, _, err := c2.Recv(); err != nil || rec.Kind != KindStart {
		t.Fatalf("after the damage: %+v, %v", rec, err)
	}
	cancel()
	waitDone(t, done)
}
