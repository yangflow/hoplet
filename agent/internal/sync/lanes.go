package sync

import (
	"errors"
	"time"

	"github.com/gorilla/websocket"
)

// Lane is the class of a wire message (shared/src/wire/, docs/architecture.md#principles): control
// messages are never queued behind bulk ones.
type Lane int

const (
	LaneControl Lane = iota
	LaneBulk
)

// laneOf is the lane of a message the agent sends. The bulk messages of the device-to-core direction are
// the sync stream, the snapshot transfer, the answers that carry file content or a diff and the list of commands;
// scripts/test/contract.test.ts compares this list with the lanes of shared/src/wire/. Everything else is
// control.
func laneOf(msg any) Lane {
	switch msg.(type) {
	case SessionAppend, WorkspaceSnapshot, SessionWorkspaceFile, SessionWorkspaceDiff, RuntimeCommands, HookResult:
		return LaneBulk
	}
	return LaneControl
}

const defaultWriteTimeout = 30 * time.Second

var errConnClosed = errors.New("connection closed")

// outbound is one message waiting for the writer; `done` receives the result of its write.
type outbound struct {
	msg     any
	timeout time.Duration
	done    chan error
}

// newConnState makes the state of one connection and starts its writer. close stops the writer.
func newConnState(conn *websocket.Conn, replies chan reply) *connState {
	cs := &connState{
		conn:    conn,
		replies: replies,
		done:    make(chan struct{}),
		control: make(chan outbound, 64),
		bulk:    make(chan outbound, 4),
		stop:    make(chan struct{}),
	}
	go runWriter(cs.stop, cs.control, cs.bulk, func(o outbound) error {
		cs.conn.SetWriteDeadline(time.Now().Add(o.timeout))
		return cs.conn.WriteJSON(o.msg)
	})
	return cs
}

func (cs *connState) close() {
	cs.stopOnce.Do(func() { close(cs.stop) })
}

// runWriter is the only writer of a connection. Before each message it takes whatever waits on the
// control queue, so a control message waits for at most the one message being written, never for the
// bulk queue.
func runWriter(stop <-chan struct{}, control, bulk <-chan outbound, write func(outbound) error) {
	for {
		var next outbound
		select {
		case next = <-control:
		default:
			select {
			case next = <-control:
			case next = <-bulk:
			case <-stop:
				return
			}
		}
		next.done <- write(next)
	}
}

// writeMsg queues msg on its lane and waits for the result of its write.
func (c *Client) writeMsg(cs *connState, msg any) error {
	timeout := defaultWriteTimeout
	if a, ok := msg.(SessionAppend); ok {
		// A 32 MiB line is a message of about 43 MiB: allow it a link of 256 KiB/s on top of the base.
		timeout += time.Duration(len(a.Lines)/(256<<10)) * time.Second
	}
	return c.writeMsgTimeout(cs, msg, timeout)
}

func (c *Client) writeMsgTimeout(cs *connState, msg any, timeout time.Duration) error {
	queue := cs.control
	if laneOf(msg) == LaneBulk {
		queue = cs.bulk
	}
	o := outbound{msg: msg, timeout: timeout, done: make(chan error, 1)}
	select {
	case queue <- o:
	case <-cs.stop:
		return errConnClosed
	}
	select {
	case err := <-o.done:
		return err
	case <-cs.stop:
		return errConnClosed
	}
}
