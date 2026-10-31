package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// collect reads lines until Exit or the channel closes, failing on timeout.
func collect(t *testing.T, e *ExecStdio, d time.Duration) (stdout, stderr []Line, exit *Line) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case l, ok := <-e.Lines():
			if !ok {
				return stdout, stderr, exit
			}
			switch l.Kind {
			case Stdout:
				stdout = append(stdout, l)
			case Stderr:
				stderr = append(stderr, l)
			case Exit:
				exit = &l
			}
		case <-timeout:
			t.Fatal("timed out reading lines")
		}
	}
}

func TestExecStdio(t *testing.T) {
	ctx := context.Background()
	e, err := StartExec(ctx, []string{"/bin/sh", "-c", "echo a; echo b >&2; read x; echo got:$x; exit 4"}, os.Environ(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.WriteLine(ctx, []byte("z")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := collect(t, e, 5*time.Second)
	if len(stdout) != 2 || string(stdout[0].Data) != "a" || string(stdout[1].Data) != "got:z" {
		t.Fatalf("stdout %+v", stdout)
	}
	if len(stderr) != 1 || string(stderr[0].Data) != "b" {
		t.Fatalf("stderr %+v", stderr)
	}
	if exit == nil || exit.Code != 4 || exit.Signal != "" {
		t.Fatalf("exit %+v", exit)
	}
	if _, ok := <-e.Lines(); ok {
		t.Fatal("lines not closed after exit")
	}
	if _, err := StartExec(ctx, nil, nil, ""); err == nil {
		t.Fatal("empty command accepted")
	}
	if err := e.Signal("TERM"); err == nil {
		t.Fatal("Signal after exit succeeded")
	}
}

func TestExecStdioCRLFPreserved(t *testing.T) {
	e, err := StartExec(context.Background(), []string{"/bin/sh", "-c", `printf 'a\r\nb\r\n'`}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	stdout, _, _ := collect(t, e, 5*time.Second)
	if len(stdout) != 2 || string(stdout[0].Data) != "a\r" || string(stdout[1].Data) != "b\r" {
		t.Fatalf("stdout %+v", stdout)
	}
}

func TestExecStdioLongLineIsSplit(t *testing.T) {
	const size = 20 << 20
	e, err := StartExec(context.Background(), []string{"/bin/sh", "-c", `head -c 20971520 /dev/zero | tr '\0' x; echo; echo tail`}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	stdout, _, exit := collect(t, e, 20*time.Second)
	if exit == nil || exit.Code != 0 {
		t.Fatalf("exit %+v", exit)
	}
	if len(stdout) != 3 {
		t.Fatalf("got %d lines", len(stdout))
	}
	var joined []byte
	for _, l := range stdout[:2] {
		if len(l.Data) > MaxLine {
			t.Fatalf("piece of %d bytes", len(l.Data))
		}
		joined = append(joined, l.Data...)
	}
	if len(stdout[0].Data) != MaxLine || !bytes.Equal(joined, bytes.Repeat([]byte("x"), size)) {
		t.Fatalf("pieces %d + %d, joined %d bytes", len(stdout[0].Data), len(stdout[1].Data), len(joined))
	}
	if string(stdout[2].Data) != "tail" {
		t.Fatalf("last line %q", stdout[2].Data)
	}
}

func TestExecStdioSignalReportedInExit(t *testing.T) {
	e, err := StartExec(context.Background(), []string{"/bin/sleep", "60"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Signal("TERM"); err != nil {
		t.Fatal(err)
	}
	_, _, exit := collect(t, e, 5*time.Second)
	if exit == nil || exit.Signal != "TERM" || exit.Code != -1 {
		t.Fatalf("exit %+v", exit)
	}
	if err := e.Signal("BOGUS"); err == nil {
		t.Fatal("unknown signal accepted")
	}
}

func TestExecStdioNilEnvIsEmpty(t *testing.T) {
	e, err := StartExec(context.Background(), []string{"/usr/bin/env"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	stdout, _, exit := collect(t, e, 5*time.Second)
	if len(stdout) != 0 || exit == nil || exit.Code != 0 {
		t.Fatalf("env printed %+v, exit %+v", stdout, exit)
	}
}

// blockedWriter starts a child that never reads stdin and runs WriteLine
// with a payload big enough to fill the pipe; it returns the error channel.
func blockedWriter(ctx context.Context, t *testing.T) (*ExecStdio, chan error) {
	t.Helper()
	e, err := StartExec(context.Background(), []string{"/bin/sleep", "60"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	// Fill the pipe: a write that times out means the child is not reading
	// and the pipe is full, so the next write must block.
	// Two-byte writes are atomic, so a timed-out one writes nothing and does
	// not poison stdin.
	small := []byte("x")
	for {
		fctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := e.WriteLine(fctx, small)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			break
		}
		if err != nil {
			t.Fatalf("filling the pipe: %v", err)
		}
	}
	errc := make(chan error, 1)
	go func() { errc <- e.WriteLine(ctx, bytes.Repeat([]byte("x"), 64<<10)) }()
	return e, errc
}

func TestExecStdioCloseUnblocksWrite(t *testing.T) {
	e, errc := blockedWriter(context.Background(), t)
	begin := time.Now()
	if err := e.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if time.Since(begin) > 3*time.Second {
		t.Fatal("Close was not prompt")
	}
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("blocked write reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WriteLine still blocked after Close")
	}
}

func TestExecStdioWriteHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	e, errc := blockedWriter(ctx, t)
	defer e.Close()
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WriteLine ignored cancellation")
	}
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if err := e.WriteLine(cctx, []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled write: %v", err)
	}
}

func TestExecStdioCloseWithUnreadLines(t *testing.T) {
	// Far more output than the Lines buffer holds, and nobody reads it.
	e, err := StartExec(context.Background(), []string{"/bin/sh", "-c", `yes line | head -n 100000`}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	// Once the first line arrives the child is writing far more than the
	// Lines buffer holds, and nobody reads it.
	select {
	case <-e.Lines():
	case <-time.After(5 * time.Second):
		t.Fatal("no output")
	}
	begin := time.Now()
	if err := e.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if time.Since(begin) > 5*time.Second {
		t.Fatal("Close did not return within its bound")
	}
	select {
	case <-e.finished:
	default:
		t.Fatal("goroutines still running after Close")
	}
}

func TestExecStdioSlowReaderLosesNothingAfterExit(t *testing.T) {
	const n = 8000
	e, err := StartExec(context.Background(), []string{"/bin/sh", "-c", `i=0; while [ $i -lt 8000 ]; do echo line$i; i=$((i+1)); done`}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	first, ok := <-e.Lines()
	if !ok || string(first.Data) != "line0" {
		t.Fatalf("first line %+v", first)
	}
	// Scenario: the reader stalls for longer than the drain bound while the
	// child has already exited with most of its output unread.
	pause := time.NewTimer(drainBound + time.Second)
	defer pause.Stop()
	<-pause.C
	got := 1
	var exit *Line
	for l := range e.Lines() {
		switch l.Kind {
		case Stdout:
			if want := fmt.Sprintf("line%d", got); string(l.Data) != want {
				t.Fatalf("line %d is %q, want %q", got, l.Data, want)
			}
			got++
		case Exit:
			exit = &l
		}
	}
	if got != n {
		t.Fatalf("got %d lines, want %d", got, n)
	}
	if exit == nil || exit.Code != 0 {
		t.Fatalf("exit %+v", exit)
	}
}

func TestExecStdioDescendantHoldingPipeDoesNotStallExit(t *testing.T) {
	e, err := StartExec(context.Background(), []string{"/bin/sh", "-c", `sleep 30 & echo hi; exit 0`}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	stdout, _, exit := collect(t, e, drainBound+3*time.Second)
	if len(stdout) != 1 || string(stdout[0].Data) != "hi" || exit == nil || exit.Code != 0 {
		t.Fatalf("stdout %+v exit %+v", stdout, exit)
	}
	if d := time.Since(begin); d > drainBound+2*time.Second {
		t.Fatalf("exit took %v", d)
	}
	if err := e.Close(); err != nil { // also kills the lingering sleep
		t.Fatal(err)
	}
}

func TestExecStdioCloseKillsDescendantsAfterLeaderExit(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "alive")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	// The read end blocks until the descendant opens the write end.
	opened := make(chan *os.File, 1)
	go func() {
		f, err := os.Open(fifo)
		if err != nil {
			opened <- nil
			return
		}
		opened <- f
	}()
	script := fmt.Sprintf(`sleep 30 >/dev/null 2>&1 3>%q & exit 0`, fifo)
	e, err := StartExec(context.Background(), []string{"/bin/sh", "-c", script}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var f *os.File
	select {
	case f = <-opened:
		if f == nil {
			t.Fatal("open fifo failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("descendant never opened the fifo")
	}
	defer f.Close()
	_, _, exit := collect(t, e, 5*time.Second)
	if exit == nil || exit.Code != 0 {
		t.Fatalf("exit %+v", exit)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	// EOF arrives once the last writer (the descendant) is gone.
	eof := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(f)
		eof <- err
	}()
	select {
	case err := <-eof:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("descendant survived Close")
	}
}

func TestExecStdioPartialWritePoisonsStdin(t *testing.T) {
	e, err := StartExec(context.Background(), []string{"/bin/sleep", "60"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	// 4 MiB cannot fit in the pipe, so the deadline always cuts it short
	// after some bytes went in.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err = e.WriteLine(ctx, bytes.Repeat([]byte("x"), 4<<20))
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrPartialWrite) {
		t.Fatalf("first write: %v", err)
	}
	err = e.WriteLine(context.Background(), []byte("next"))
	if !errors.Is(err, ErrPartialWrite) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("later write: %v", err)
	}
}
