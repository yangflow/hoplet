package shim

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
)

// signals maps protocol signal names to signals sent to the child's group.
var signals = map[string]syscall.Signal{
	"INT":  syscall.SIGINT,
	"TERM": syscall.SIGTERM,
	"HUP":  syscall.SIGHUP,
	"KILL": syscall.SIGKILL,
}

// conn serializes frame writes to one client.
type conn struct {
	net.Conn
	wmu sync.Mutex
}

func (c *conn) send(m Message) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeFrame(c.Conn, m)
}

// sendError sends an error frame.
func (c *conn) sendError(code, text string, off int64) error {
	return c.send(Message{Type: MsgError, Version: ProtocolVersion, Code: code, Error: text, Offset: off})
}

// codedError is a request failure with a protocol code.
type codedError struct{ code, msg string }

func (e *codedError) Error() string { return e.msg }

func coded(code, format string, args ...any) error {
	return &codedError{code: code, msg: fmt.Sprintf(format, args...)}
}

// serve handles one client: hello, then records from the requested offset
// while client frames are applied to the child. A client replaces the
// previous one only once its hello is accepted.
func (s *shim) serve(nc net.Conn) {
	c := &conn{Conn: nc}
	defer s.wg.Done()
	defer s.release(nc)
	defer c.Close()
	var hello Message
	if err := c.SetReadDeadline(time.Now().Add(helloTimeout)); err != nil {
		return
	}
	if err := readFrame(c, &hello); err != nil {
		return
	}
	if hello.Type != MsgHello {
		_ = c.sendError(CodeBadRequest, fmt.Sprintf("expected hello, got %q", hello.Type), 0)
		return
	}
	if hello.Version != ProtocolVersion {
		_ = c.sendError(CodeVersion, fmt.Sprintf("protocol version %d not supported; shim speaks %d", hello.Version, ProtocolVersion), 0)
		return
	}
	size, _ := s.spool.state()
	if hello.Offset < 0 || hello.Offset > size || !s.spool.boundary(hello.Offset) {
		_ = c.sendError(CodeBadOffset, fmt.Sprintf("offset %d is not a record boundary inside the spool (size %d)", hello.Offset, size), 0)
		return
	}
	if err := c.SetReadDeadline(time.Time{}); err != nil {
		return
	}
	if !s.adopt(c) {
		return
	}
	s.log.Info("client connected", "offset", hello.Offset)
	if err := c.send(Message{Type: MsgWelcome, Version: ProtocolVersion, Pid: s.pgid}); err != nil {
		return
	}
	stop := make(chan struct{})
	defer close(stop)
	s.wg.Add(1)
	go s.stream(c, hello.Offset, stop)
	for {
		var m Message
		if err := readFrame(c, &m); err != nil {
			return
		}
		if err := s.apply(m); err != nil {
			code := CodeBadRequest
			var ce *codedError
			if errors.As(err, &ce) {
				code = ce.code
			}
			if c.sendError(code, err.Error(), 0) != nil {
				return
			}
		}
	}
}

// adopt makes nc the live client, closing the one it replaces. It reports
// false when the shim is shutting down.
func (s *shim) adopt(c *conn) bool {
	nc := c.Conn
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	if s.current != nil && s.current != nc {
		s.log.Info("client replaced")
		_ = s.current.Close() // its serve goroutine notices and exits
	}
	s.current, s.live = nc, c
	return true
}

// release forgets nc.
func (s *shim) release(nc net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, nc)
	if s.current == nc {
		s.current, s.live = nil, nil
	}
}

// stream sends every record from off onward, then follows new appends.
func (s *shim) stream(c *conn, off int64, stop <-chan struct{}) {
	defer s.wg.Done()
	for {
		size, notify := s.spool.state()
		if size > off {
			err := readRecords(s.spool.path, off, size, func(rec Record, end int64) error {
				if err := c.send(Message{Type: MsgRecord, Offset: end, Record: &rec}); err != nil {
					return err
				}
				off = end
				return nil
			})
			if err != nil {
				switch {
				case errors.Is(err, ErrFrameTooLarge):
					_ = c.sendError(CodeRecordTooLarge, fmt.Sprintf("record at %d does not fit in a frame", off), off)
				case errors.Is(err, errCorrupt):
					_ = c.sendError(CodeCorrupt, err.Error(), off)
				}
				c.Close()
				return
			}
			continue
		}
		select {
		case <-notify:
		case <-stop:
			return
		}
	}
}

// apply executes one client frame without ever waiting on the child.
func (s *shim) apply(m Message) error {
	switch m.Type {
	case MsgStdin:
		if strings.ContainsRune(m.Data, '\n') {
			return coded(CodeBadRequest, "stdin line contains a newline")
		}
		if len(m.Data) > ChunkSize {
			return coded(CodeTooLarge, "stdin line is %d bytes; the limit is %d", len(m.Data), ChunkSize)
		}
		return s.enqueueStdin(stdinReq{line: m.Data})
	case MsgCloseStdin:
		return s.enqueueStdin(stdinReq{close: true})
	case MsgSignal:
		sig, ok := signals[m.Signal]
		if !ok {
			return coded(CodeBadRequest, "unknown signal %q", m.Signal)
		}
		if s.hasExited() {
			return coded(CodeExited, "child has exited")
		}
		s.signal(sig)
		return nil
	case MsgAck:
		s.mu.Lock()
		finished := s.exited && s.final > 0 && m.Offset >= s.final
		s.mu.Unlock()
		if finished {
			s.finish()
		}
		return nil
	default:
		return coded(CodeBadRequest, "unexpected %q frame", m.Type)
	}
}

// enqueueStdin hands a request to the writer goroutine.
func (s *shim) enqueueStdin(req stdinReq) error {
	if s.hasExited() {
		return coded(CodeExited, "child has exited")
	}
	select {
	case s.stdinQ <- req:
		return nil
	default:
		return coded(CodeStdinBusy, "stdin queue is full")
	}
}

// appendUnlessExited appends rec unless the child has exited. It takes the
// lock the exit path holds while it marks the child exited, so no record can
// follow the exit record.
func (s *shim) appendUnlessExited(rec Record) (written bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited {
		return false, nil
	}
	_, err = s.spool.append(rec)
	return err == nil, err
}

// rejectExited tells the live client that a request arrived too late.
func (s *shim) rejectExited() {
	s.mu.Lock()
	live := s.live
	s.mu.Unlock()
	if live != nil {
		_ = live.sendError(CodeExited, "child has exited", 0)
	}
}

// stdinLoop writes queued stdin to the child. It records each line before
// writing it, so the spool shows what was attempted even if the child dies
// mid-write. Once the child has exited it records nothing more.
func (s *shim) stdinLoop() {
	defer s.wg.Done()
	for {
		select {
		case req := <-s.stdinQ:
			if req.close {
				_ = s.stdinFile.Close() // already closed when the child is gone
				continue
			}
			written, err := s.appendUnlessExited(Record{Kind: KindStdin, Data: req.line})
			if err != nil {
				s.log.Error("stdin record not written", "error", err)
			}
			if !written && err == nil {
				s.rejectExited()
				continue
			}
			if _, err := s.stdinFile.Write([]byte(req.line + "\n")); err != nil {
				if _, aerr := s.appendUnlessExited(Record{Kind: KindStdinError, Data: err.Error()}); aerr != nil {
					s.log.Error("stdin_error record not written", "error", aerr)
				}
			}
		case <-s.exitCh:
			return
		case <-s.quit:
			return
		}
	}
}
