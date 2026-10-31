package shim

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Client is a connection to a shim. Recv must be called from one goroutine;
// the send methods may be called from any.
type Client struct {
	c   *conn
	pid int
}

// Dial connects to the shim in dir and resumes from offset.
func Dial(ctx context.Context, dir string, offset int64) (*Client, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "unix", SocketPath(dir))
	if err != nil {
		return nil, fmt.Errorf("shim: dial: %w", err)
	}
	c := &conn{Conn: nc}
	if err := c.send(Message{Type: MsgHello, Version: ProtocolVersion, Offset: offset}); err != nil {
		return nil, errors.Join(err, nc.Close())
	}
	var m Message
	if err := readFrame(c, &m); err != nil {
		return nil, errors.Join(fmt.Errorf("shim: read welcome: %w", err), nc.Close())
	}
	switch m.Type {
	case MsgWelcome:
		return &Client{c: c, pid: m.Pid}, nil
	case MsgError:
		return nil, errors.Join(fmt.Errorf("shim: %s", m.Error), nc.Close())
	default:
		return nil, errors.Join(fmt.Errorf("shim: unexpected %q frame", m.Type), nc.Close())
	}
}

// Pid is the child's process ID.
func (c *Client) Pid() int { return c.pid }

// Recv returns the next record and the spool offset just after it. An error
// frame from the shim (for example a rejected stdin write) is returned as an
// error; the connection stays usable.
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
		return Record{}, 0, fmt.Errorf("shim: %s", m.Error)
	default:
		return Record{}, 0, fmt.Errorf("shim: unexpected %q frame", m.Type)
	}
}

// Stdin writes one line to the child.
func (c *Client) Stdin(line string) error { return c.c.send(Message{Type: MsgStdin, Data: line}) }

// CloseStdin closes the child's stdin.
func (c *Client) CloseStdin() error { return c.c.send(Message{Type: MsgCloseStdin}) }

// Signal sends INT, TERM, HUP or KILL to the child's process group.
func (c *Client) Signal(name string) error { return c.c.send(Message{Type: MsgSignal, Signal: name}) }

// Ack tells the shim the client has durably consumed the spool up to off.
// Acknowledging the exit record lets the shim exit.
func (c *Client) Ack(off int64) error { return c.c.send(Message{Type: MsgAck, Offset: off}) }

// Close closes the connection; the shim and its child keep running.
func (c *Client) Close() error { return c.c.Close() }
