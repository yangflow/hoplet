package noise

import (
	"net"
	"sync"

	fnoise "github.com/flynn/noise"
)

// Channel is an encrypted, framed connection after a handshake.
type Channel struct {
	c        MsgConn
	send     *fnoise.CipherState
	recv     *fnoise.CipherState
	peer     []byte
	maxFrame int

	once sync.Once
	done chan struct{}
	err  error // set before done closes
}

func newChannel(c MsgConn, send, recv *fnoise.CipherState, peer []byte, maxFrame int) *Channel {
	if maxFrame <= 0 {
		maxFrame = DefaultMaxFrame
	}
	return &Channel{c: c, send: send, recv: recv, peer: peer, maxFrame: maxFrame, done: make(chan struct{})}
}

// Peer returns the other side's public key; nil for a KindRelay initiator,
// whose responder has none.
func (ch *Channel) Peer() []byte { return ch.peer }

// Close ends the channel and its connection.
func (ch *Channel) Close() error {
	ch.fail(net.ErrClosed)
	return nil
}

func (ch *Channel) fail(err error) {
	ch.once.Do(func() {
		ch.err = err
		close(ch.done)
		_ = ch.c.Close()
	})
}
