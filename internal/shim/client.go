package shim

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// Error is an error frame from the shim. Code is one of the Code constants.
type Error struct {
	Code    string
	Message string
	Version int // the protocol version the shim speaks
}

// Error implements error.
func (e *Error) Error() string { return "shim: " + e.Message }

func errorFrame(m Message) *Error {
	return &Error{Code: m.Code, Message: m.Error, Version: m.Version}
}

// Client is a connection to a shim. Recv must be called from one goroutine;
// the send methods may be called from any.
type Client struct {
	c   *conn
	pid int
}

// Dial connects to the shim in dir and resumes from offset. The whole
// handshake honors ctx.
func Dial(ctx context.Context, dir string, offset int64) (*Client, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "unix", SocketPath(dir))
	if err != nil {
		return nil, fmt.Errorf("shim: dial: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		if err := nc.SetDeadline(dl); err != nil {
			return nil, errors.Join(err, nc.Close())
		}
	}
	stop := context.AfterFunc(ctx, func() { _ = nc.Close() })
	c, err := handshake(nc, offset)
	if !stop() || err != nil {
		if cerr := ctx.Err(); cerr != nil {
			err = errors.Join(cerr, err)
		} else if errors.Is(err, os.ErrDeadlineExceeded) {
			err = errors.Join(context.DeadlineExceeded, err) // the conn deadline beat the context timer
		}
		if err == nil {
			err = errors.New("shim: dial: context ended")
		}
		return nil, errors.Join(err, nc.Close())
	}
	if err := nc.SetDeadline(time.Time{}); err != nil {
		return nil, errors.Join(err, nc.Close())
	}
	return c, nil
}

// handshake sends hello and reads the welcome.
func handshake(nc net.Conn, offset int64) (*Client, error) {
	c := &conn{Conn: nc}
	if err := c.send(Message{Type: MsgHello, Version: ProtocolVersion, Offset: offset}); err != nil {
		return nil, err
	}
	var m Message
	if err := readFrame(c, &m); err != nil {
		return nil, fmt.Errorf("shim: read welcome: %w", err)
	}
	switch m.Type {
	case MsgWelcome:
		if m.Version != ProtocolVersion {
			return nil, &Error{Code: CodeVersion, Message: fmt.Sprintf("shim speaks protocol %d; this client speaks %d", m.Version, ProtocolVersion), Version: m.Version}
		}
		return &Client{c: c, pid: m.Pid}, nil
	case MsgError:
		return nil, errorFrame(m)
	default:
		return nil, fmt.Errorf("shim: unexpected %q frame", m.Type)
	}
}

// Pid is the child's process ID.
func (c *Client) Pid() int { return c.pid }

// Recv returns the next record and the spool offset just after it. An error
// frame from the shim (for example a rejected stdin write) is returned as an
// *Error; the connection stays usable unless the shim closes it.
func (c *Client) Recv() (Record, int64, error) {
	var m Message
	if err := readFrame(c.c, &m); err != nil {
		return Record{}, 0, err
	}
	switch m.Type {
	case MsgRecord:
		if m.Record == nil {
			return Record{}, 0, errors.New("shim: record frame without record")
		}
		return *m.Record, m.Offset, nil
	case MsgError:
		return Record{}, 0, errorFrame(m)
	default:
		return Record{}, 0, fmt.Errorf("shim: unexpected %q frame", m.Type)
	}
}

// Stdin writes one line to the child.
func (c *Client) Stdin(line string) error { return c.c.send(Message{Type: MsgStdin, Data: line}) }

// CloseStdin closes the child's stdin after the lines already queued.
func (c *Client) CloseStdin() error { return c.c.send(Message{Type: MsgCloseStdin}) }

// Signal sends INT, TERM, HUP or KILL to the child's process group.
func (c *Client) Signal(name string) error { return c.c.send(Message{Type: MsgSignal, Signal: name}) }

// Ack tells the shim the client has durably consumed the spool up to off.
// Acknowledging the exit record lets the shim exit.
func (c *Client) Ack(off int64) error { return c.c.send(Message{Type: MsgAck, Offset: off}) }

// Close closes the connection; the shim and its child keep running.
func (c *Client) Close() error { return c.c.Close() }
