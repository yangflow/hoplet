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
// offset the daemon may commit once the line's events are stored.
//
// That offset is the end of the line's last record, except while a chunked
// line is still incomplete: every Line emitted then carries the offset where
// the earliest incomplete line began, so a committed offset never passes
// records that have not been turned into a line yet. A restart replays the
// interleaved records, so those may be seen twice; that is accepted, as
// losing them would not be.
type ShimStdio struct {
	c     *shim.Client
	recv  func() (shim.Record, int64, error)
	from  int64
	lines chan harness.Line
	done  chan struct{}
	wg    sync.WaitGroup
	once  sync.Once
}

var _ harness.Stdio = (*ShimStdio)(nil)

// NewShimStdio starts reading records from c.
func NewShimStdio(c *shim.Client) *ShimStdio {
	return newShimStdio(c, c.Recv, c.From())
}

// newShimStdio reads records from recv, the first starting at spool offset
// from; c carries the writes and may be nil if there are none.
func newShimStdio(c *shim.Client, recv func() (shim.Record, int64, error), from int64) *ShimStdio {
	s := &ShimStdio{c: c, recv: recv, from: from, lines: make(chan harness.Line, 64), done: make(chan struct{})}
	s.wg.Add(1)
	go s.read()
	return s
}

func (s *ShimStdio) read() {
	defer s.wg.Done()
	defer close(s.lines)
	asm := assembler{prev: s.from}
	for {
		rec, off, err := s.recv()
		var serr *shim.Error
		if errors.As(err, &serr) {
			if !stdinCode(serr.Code) {
				return // the shim cannot serve this connection: the run reports ErrDetached
			}
			// A rejected stdin frame (for example stdin_busy): the connection
			// stays usable. It has no spool position, so Offset is 0.
			if !s.send(harness.Line{Kind: harness.StdinError, Data: []byte(serr.Error())}) {
				return
			}
			continue
		}
		if err != nil {
			return // the connection ended: the run reports ErrDetached
		}
		for _, line := range asm.add(rec, off) {
			if !s.send(line) || line.Kind == harness.Exit {
				return
			}
		}
	}
}

// stdinCode reports whether an error frame code is a rejected stdin write,
// which leaves the connection usable. Every other code means the shim cannot
// serve the connection.
func stdinCode(code string) bool {
	switch code {
	case shim.CodeStdinBusy, shim.CodeTooLarge, shim.CodeBadRequest, shim.CodeExited:
		return true
	}
	return false
}

// assembler rebuilds whole lines from chunked records. It never holds more
// than harness.MaxLine bytes of a line: a longer line comes out as several
// Lines, all but the last with Offset 0.
type assembler struct {
	partial map[shim.RecordKind][]byte
	begin   map[shim.RecordKind]int64 // where each incomplete line began
	order   []shim.RecordKind         // the incomplete lines, oldest first
	prev    int64                     // where the next record starts
}

// cap limits an offset so it does not pass the earliest incomplete line.
func (a *assembler) cap(off int64) int64 {
	if len(a.order) == 0 {
		return off
	}
	return min(off, a.begin[a.order[0]])
}

func (a *assembler) drop(k shim.RecordKind) {
	delete(a.partial, k)
	delete(a.begin, k)
	for i, o := range a.order {
		if o == k {
			a.order = append(a.order[:i], a.order[i+1:]...)
			break
		}
	}
}

// flush returns the incomplete lines as collected, truncated, with no offset.
func (a *assembler) flush() []harness.Line {
	var out []harness.Line
	for _, k := range a.order {
		kind, _ := lineKind(k)
		out = append(out, harness.Line{Kind: kind, Data: a.partial[k]})
	}
	a.partial, a.begin, a.order = nil, nil, nil
	return out
}

// add takes the next record, read at spool offset off (just after it), and
// returns the lines it completes.
func (a *assembler) add(rec shim.Record, off int64) []harness.Line {
	start := a.prev
	a.prev = off
	var out []harness.Line
	if rec.Kind == shim.KindStart || rec.Kind == shim.KindExit {
		// A new process, or the end of this one: what the old one left
		// unfinished must not glue onto what comes next.
		out = a.flush()
	}
	kind, ok := lineKind(rec.Kind)
	if !ok {
		// A start record, or a kind from a newer shim: nothing for the
		// adapter, but the position still counts towards catching up.
		return append(out, harness.Line{Kind: harness.Position, Offset: a.cap(off)})
	}
	data := []byte(rec.Data)
	if len(rec.B) > 0 {
		data = rec.B
	}
	if a.partial == nil {
		a.partial = map[shim.RecordKind][]byte{}
		a.begin = map[shim.RecordKind]int64{}
	}
	buf := append(a.partial[rec.Kind], data...)
	for len(buf) > harness.MaxLine || (rec.More && len(buf) == harness.MaxLine) {
		// Only the record's last piece carries its end offset: committing the
		// offset before the rest of the line is read would lose it on restart.
		out = append(out, harness.Line{Kind: kind, Data: buf[:harness.MaxLine:harness.MaxLine]})
		buf = buf[harness.MaxLine:]
	}
	if rec.More {
		if len(buf) == 0 {
			a.drop(rec.Kind)
			return out
		}
		if _, had := a.partial[rec.Kind]; !had {
			a.begin[rec.Kind] = start
			a.order = append(a.order, rec.Kind)
		}
		a.partial[rec.Kind] = append([]byte(nil), buf...)
		return out
	}
	a.drop(rec.Kind)
	line := harness.Line{Kind: kind, Data: buf, Offset: a.cap(off)}
	if kind == harness.Exit {
		line.Code, line.Signal = rec.Code, rec.Signal
		if rec.Signal != "" && rec.Code == 0 {
			line.Code = -1
		}
	}
	return append(out, line)
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
		if s.c != nil {
			err = s.c.Close()
		}
		s.wg.Wait()
	})
	return err
}
