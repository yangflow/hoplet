package shim

import (
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

// stuckShim builds a shim whose child can never be stopped: no process
// group, an exit that never comes, and a pump blocked reading a pipe.
func stuckShim(t *testing.T, waitReturns bool) *shim {
	t.Helper()
	s := &shim{
		cfg:      Config{KillGrace: 50 * time.Millisecond},
		log:      discardLogger(),
		exitCh:   make(chan struct{}),
		waitDone: make(chan struct{}),
	}
	var err error
	if s.outR, _, err = os.Pipe(); err != nil {
		t.Fatal(err)
	}
	if s.errR, _, err = os.Pipe(); err != nil {
		t.Fatal(err)
	}
	if _, s.stdinFile, err = os.Pipe(); err != nil {
		t.Fatal(err)
	}
	s.pumps.Add(1)
	go func() { // a pump stuck reading a child that never writes or closes
		defer s.pumps.Done()
		_, _ = io.Copy(io.Discard, s.outR)
	}()
	if waitReturns {
		close(s.waitDone)
	}
	return s
}

func TestTerminateReportsUnstoppableChild(t *testing.T) {
	s := stuckShim(t, false)
	if err := s.terminate(); err == nil {
		t.Fatal("terminate succeeded for a child that never exits")
	}
}

func TestAbandonFreesPumpsAndReportsStragglers(t *testing.T) {
	// Closing the pipes lets the pump finish; the wait goroutine has returned.
	if s := stuckShim(t, true); !s.abandon() {
		t.Fatal("abandon reported stragglers although everything could finish")
	}
	// A wait goroutine stuck in cmd.Wait cannot be freed: the spool must stay open.
	if s := stuckShim(t, false); s.abandon() {
		t.Fatal("abandon claimed success while the wait goroutine is stuck")
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }
