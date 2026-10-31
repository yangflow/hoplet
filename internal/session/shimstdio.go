package session

import (
	"context"
	"errors"
	"sync"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/shim"
)

// ShimStdio is a harness.Stdio over a shim connection. It reassembles
// chunked records into whole lines and reports each line with the spool
// offset just after its last chunk, so the daemon can commit offsets with
// the events they produced.
type ShimStdio struct {
	c     *shim.Client
	lines chan harness.Line
	done  chan struct{}
	wg    sync.WaitGroup
	once  sync.Once
}

var _ harness.Stdio = (*ShimStdio)(nil)

// NewShimStdio starts reading records from c.
func NewShimStdio(c *shim.Client) *ShimStdio {
	s := &ShimStdio{c: c, lines: make(chan harness.Line, 64), done: make(chan struct{})}
	s.wg.Add(1)
	go s.read()
	return s
}

func (s *ShimStdio) read() {
	defer s.wg.Done()
	defer close(s.lines)
	partial := map[shim.RecordKind][]byte{}
	for {
		rec, off, err := s.c.Recv()
		var serr *shim.Error
		if errors.As(err, &serr) {
			// A rejected frame (for example stdin_busy): the connection stays
			// usable. It has no spool position, so Offset is 0.
			if !s.send(harness.Line{Kind: harness.StdinError, Data: []byte(serr.Error())}) {
				return
			}
			continue
		}
		if err != nil {
			return // the connection ended: the run reports ErrDetached
		}
		data := []byte(rec.Data)
		if len(rec.B) > 0 {
			data = rec.B
		}
		kind, ok := lineKind(rec.Kind)
		if !ok {
			continue // a start record carries nothing for the harness adapter
		}
		if rec.More {
			partial[rec.Kind] = append(partial[rec.Kind], data...)
			continue
		}
		if p := partial[rec.Kind]; p != nil {
			data = append(p, data...)
			delete(partial, rec.Kind)
		}
		line := harness.Line{Kind: kind, Data: data, Offset: off}
		if kind == harness.Exit {
			line.Code, line.Signal = rec.Code, rec.Signal
			if rec.Signal != "" && rec.Code == 0 {
				line.Code = -1
			}
		}
		if !s.send(line) || kind == harness.Exit {
			return
		}
	}
}

func lineKind(k shim.RecordKind) (harness.LineKind, bool) {
	switch k {
	case shim.KindStdout:
		return harness.Stdout, true
	case shim.KindStderr:
		return harness.Stderr, true
	case shim.KindStdin:
		return harness.Stdin, true
	case shim.KindStdinError:
		return harness.StdinError, true
	case shim.KindExit:
		return harness.Exit, true
	}
	return 0, false
}

func (s *ShimStdio) send(l harness.Line) bool {
	select {
	case s.lines <- l:
		return true
	case <-s.done:
		return false
	}
}

// WriteLine sends one line to the harness's stdin. A nil error means the
// shim accepted the frame; delivery failures arrive as StdinError lines.
func (s *ShimStdio) WriteLine(ctx context.Context, line []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.c.Stdin(string(line))
}

// Lines delivers lines until the exit record or the end of the connection.
func (s *ShimStdio) Lines() <-chan harness.Line { return s.lines }

// Signal sends INT, TERM, HUP or KILL to the harness's process group.
func (s *ShimStdio) Signal(name string) error { return s.c.Signal(name) }

// Ack tells the shim the daemon has committed everything up to off; acking
// the exit record lets the shim exit.
func (s *ShimStdio) Ack(off int64) error { return s.c.Ack(off) }

// Close detaches from the shim; the harness keeps running.
func (s *ShimStdio) Close() error {
	var err error
	s.once.Do(func() {
		close(s.done)
		err = s.c.Close()
		s.wg.Wait()
	})
	return err
}
