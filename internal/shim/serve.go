package shim

import (
	"errors"
	"fmt"
	"net"
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

// serve handles one client: hello, then records from the requested offset
// while client frames are applied to the child.
func (s *shim) serve(nc net.Conn) {
	c := &conn{Conn: nc}
	defer c.Close()
	var hello Message
	if err := c.SetReadDeadline(time.Now().Add(helloTimeout)); err != nil {
		return
	}
	if err := readFrame(c, &hello); err != nil || hello.Type != MsgHello {
		return
	}
	if hello.Version != ProtocolVersion {
		_ = c.send(Message{Type: MsgError, Error: fmt.Sprintf("protocol version %d not supported; shim speaks %d", hello.Version, ProtocolVersion)})
		return
	}
	if size, _ := s.spool.state(); hello.Offset < 0 || hello.Offset > size {
		_ = c.send(Message{Type: MsgError, Error: fmt.Sprintf("offset %d outside spool (size %d)", hello.Offset, size)})
		return
	}
	if err := c.SetReadDeadline(time.Time{}); err != nil {
		return
	}
	if err := c.send(Message{Type: MsgWelcome, Version: ProtocolVersion, Pid: s.cmd.Process.Pid}); err != nil {
		return
	}
	stop := make(chan struct{})
	defer close(stop)
	go s.stream(c, hello.Offset, stop)
	for {
		var m Message
		if err := readFrame(c, &m); err != nil {
			return
		}
		if err := s.apply(m); err != nil {
			if c.send(Message{Type: MsgError, Error: err.Error()}) != nil {
				return
			}
		}
	}
}

// stream sends every record from off onward, then follows new appends.
func (s *shim) stream(c *conn, off int64, stop <-chan struct{}) {
	for {
		size, notify := s.spool.state()
		if size > off {
			err := readRecords(s.spool.path, off, size, func(rec Record, end int64) error {
				off = end
				return c.send(Message{Type: MsgRecord, Offset: end, Record: &rec})
			})
			if err != nil {
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

// apply executes one client frame.
func (s *shim) apply(m Message) error {
	switch m.Type {
	case MsgStdin:
		return s.writeStdin(m.Data)
	case MsgCloseStdin:
		s.stdinMu.Lock()
		defer s.stdinMu.Unlock()
		if err := s.stdin.Close(); err != nil {
			return fmt.Errorf("close stdin: %w", err)
		}
		return nil
	case MsgSignal:
		sig, ok := signals[m.Signal]
		if !ok {
			return fmt.Errorf("unknown signal %q", m.Signal)
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
		return fmt.Errorf("unexpected %q frame", m.Type)
	}
}

// writeStdin writes one line to the child and records it.
func (s *shim) writeStdin(line string) error {
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if exited {
		return errors.New("child has exited")
	}
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	if _, err := s.stdin.Write([]byte(line + "\n")); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	if _, err := s.spool.append(Record{Kind: KindStdin, Data: line}); err != nil {
		return err
	}
	return nil
}
