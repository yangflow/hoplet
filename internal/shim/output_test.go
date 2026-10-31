package shim

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// gone polls until no process has the given pid.
func gone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d still alive", pid)
}

// pidAfter parses the pid a script printed as "<prefix><pid>".
func pidAfter(t *testing.T, recs []Record, prefix string) int {
	t.Helper()
	for _, r := range recs {
		if r.Kind == KindStdout && strings.HasPrefix(r.Data, prefix) {
			pid, err := strconv.Atoi(strings.TrimPrefix(r.Data, prefix))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
	}
	t.Fatalf("no %q line in %+v", prefix, recs)
	return 0
}

func TestChunkFitsFrame(t *testing.T) {
	// JSON may expand a byte to six (\u00XX), plus the record's other fields.
	if 6*ChunkSize+4096 >= MaxFrame {
		t.Fatalf("a worst-case record (%d) does not fit MaxFrame (%d)", 6*ChunkSize+4096, MaxFrame)
	}
}

func TestLongLineIsChunked(t *testing.T) {
	for _, size := range []int{20 << 20, 20<<20 + 100} { // exact multiple, and not
		dir, done := startShim(context.Background(), t, time.Minute, "bigline", strconv.Itoa(size))
		c := dial(t, dir, 0)
		got, final := recvUntil(t, c, isExit)
		var total int
		var lines []Record
		var rebuilt bytes.Buffer
		for _, r := range got {
			if r.Kind != KindStdout {
				continue
			}
			lines = append(lines, r)
			total += len(r.Data)
			rebuilt.WriteString(r.Data)
			if len(r.Data) > ChunkSize {
				t.Fatalf("record of %d bytes exceeds ChunkSize", len(r.Data))
			}
		}
		for i, r := range lines {
			if last := i == len(lines)-1; r.More == last {
				t.Fatalf("record %d of %d has More=%v", i, len(lines), r.More)
			}
		}
		if total != size || !bytes.Equal(rebuilt.Bytes(), bytes.Repeat([]byte{'a'}, size)) {
			t.Fatalf("reassembled %d bytes, want exactly %d 'a' bytes", total, size)
		}
		if err := c.Ack(final); err != nil {
			t.Fatal(err)
		}
		waitDone(t, done)
	}
}

func TestInvalidUTF8GoesToB(t *testing.T) {
	dir, done := startShim(context.Background(), t, time.Minute, "rawbytes")
	c := dial(t, dir, 0)
	got, final := recvUntil(t, c, isExit)
	var raw, ok *Record
	for i := range got {
		switch {
		case got[i].Kind == KindStdout && got[i].Data == "ok":
			ok = &got[i]
		case got[i].Kind == KindStdout:
			raw = &got[i]
		}
	}
	if raw == nil || ok == nil || !bytes.Equal(raw.B, []byte{0xff, 0xfe}) || raw.Data != "" {
		t.Fatalf("records %+v", got)
	}
	for _, r := range got {
		if r.T == 0 {
			t.Fatalf("record without timestamp: %+v", r)
		}
	}
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}

func TestExitNotHeldByDescendant(t *testing.T) {
	dir, done := startShimCfg(context.Background(), t, func(cfg *Config) {
		cfg.Command = []string{"sh", "-c", "sleep 30 & echo hi $!; exit 0"}
	})
	c := dial(t, dir, 0)
	began := time.Now()
	hi, _ := recvUntil(t, c, func(r Record) bool { return strings.HasPrefix(r.Data, "hi ") })
	sleeper := pidAfter(t, hi, "hi ")
	if syscall.Kill(sleeper, 0) != nil {
		t.Fatal("the descendant should still be running when its parent's output arrives")
	}
	got, final := recvUntil(t, c, isExit)
	if took := time.Since(began); took > 4*time.Second {
		t.Fatalf("exit record took %v with a descendant holding the pipes", took)
	}
	if exit := got[len(got)-1]; exit.Code != 0 || exit.Signal != "" {
		t.Fatalf("exit %+v", exit)
	}
	gone(t, sleeper) // reaped right after the exit record, long before the shim exits
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}

func TestCancelEscalatesToKill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShimCfg(ctx, t, func(cfg *Config) {
		cfg.Command = append(cfg.Command, "ignoreterm")
		cfg.KillGrace = 200 * time.Millisecond
	})
	c := dial(t, dir, 0)
	recvUntil(t, c, func(r Record) bool { return r.Data == "ready" }) // SIGTERM is ignored from here on
	cancel()
	waitDone(t, done)
	b, err := os.ReadFile(SpoolPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"k":"exit"`)) || !bytes.Contains(b, []byte(`"signal":"KILL"`)) {
		t.Fatalf("spool lacks a KILL exit record:\n%s", b)
	}
}

func TestSignalReachesGrandchild(t *testing.T) {
	dir, done := startShimCfg(context.Background(), t, func(cfg *Config) {
		cfg.Command = []string{"sh", "-c", "sleep 30 & echo $!; wait"}
	})
	c := dial(t, dir, 0)
	var sleeper int
	recvUntil(t, c, func(r Record) bool {
		if r.Kind != KindStdout {
			return false
		}
		pid, err := strconv.Atoi(r.Data)
		sleeper = pid
		return err == nil
	})
	if err := c.Signal("TERM"); err != nil {
		t.Fatal(err)
	}
	_, final := recvUntil(t, c, isExit)
	gone(t, sleeper) // before the shim exits, so the group signal did it
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}

func TestSignalAfterExitRejected(t *testing.T) {
	dir, done := startShim(context.Background(), t, time.Minute, "tick", "1", "0")
	c := dial(t, dir, 0)
	_, final := recvUntil(t, c, isExit)
	if err := c.Signal("KILL"); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.Recv()
	if se := asError(t, err); se.Code != CodeExited {
		t.Fatalf("signal after exit: %v", err)
	}
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}

func TestExitRecordIsLast(t *testing.T) {
	// Slow appends keep the stdin writer busy while the child exits.
	dir, done := startShimCfg(context.Background(), t, func(cfg *Config) {
		cfg.Command = append(cfg.Command, "echo")
		cfg.wrapSpool = func(f spoolFile) spoolFile { return slowSpool{f} }
	})
	c := dial(t, dir, 0)
	for i := 0; i < 20; i++ {
		if err := c.Stdin(fmt.Sprint("before ", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Stdin("quit"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ { // queued behind quit; the child will never read them
		if err := c.Stdin(fmt.Sprint("after ", i)); err != nil {
			t.Fatal(err)
		}
	}
	var final int64
	for { // error frames for the late lines are expected
		rec, off, err := c.Recv()
		var se *Error
		if errors.As(err, &se) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if rec.Kind == KindExit {
			final = off
			break
		}
	}
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done) // Run returns only after the stdin writer is done
	b, err := os.ReadFile(SpoolPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(b)) != final {
		t.Fatalf("spool is %d bytes but the exit record ends at %d: records follow the exit record", len(b), final)
	}
	lines := bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n"))
	if last := lines[len(lines)-1]; !bytes.Contains(last, []byte(`"k":"exit"`)) {
		t.Fatalf("last record is %s", last)
	}
}

// slowSpool delays every write.
type slowSpool struct{ spoolFile }

func (s slowSpool) Write(b []byte) (int, error) {
	time.Sleep(3 * time.Millisecond)
	return s.spoolFile.Write(b)
}
