package noise

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"sync"

	fnoise "github.com/flynn/noise"
)

// Flags lead every transport message's plaintext.
const (
	flagLast byte = 0 // the frame's last piece
	flagMore byte = 1 // more pieces follow
	flagPing byte = 2 // 8-byte nonce; answered with flagPong
	flagPong byte = 3 // the ping's nonce
)

const (
	tagSize   = 16
	nonceSize = 8
	// maxPongs is how many unanswered pings a peer may have in flight.
	maxPongs = 16
	// maxPiece is the most frame bytes one transport message carries.
	maxPiece = maxMessage - tagSize - 1
)

// Channel is an encrypted, framed connection after a handshake. One
// goroutine reads; any number write. A failure of either direction ends
// the channel, and every later call returns the same error.
type Channel struct {
	c        MsgConn
	peer     []byte
	maxFrame int

	wmu  sync.Mutex // orders encryption with sending
	send *fnoise.CipherState
	recv *fnoise.CipherState // the reader's alone

	pmu   sync.Mutex
	pings map[[nonceSize]byte]chan struct{}
	pongs chan [nonceSize]byte // owed to the peer; sent by ponger

	once sync.Once
	done chan struct{}
	err  error // set before done closes
}

func newChannel(c MsgConn, send, recv *fnoise.CipherState, peer []byte, maxFrame int) *Channel {
	if maxFrame <= 0 {
		maxFrame = DefaultMaxFrame
	}
	ch := &Channel{
		c: c, send: send, recv: recv, peer: peer, maxFrame: maxFrame,
		pings: map[[nonceSize]byte]chan struct{}{},
		pongs: make(chan [nonceSize]byte, maxPongs),
		done:  make(chan struct{}),
	}
	go ch.ponger()
	return ch
}

// ponger answers pings off the reader's goroutine, so a reader never
// waits on a write while the peer waits on its read.
func (ch *Channel) ponger() {
	for {
		select {
		case nonce := <-ch.pongs:
			if ch.writeControl(context.Background(), flagPong, nonce) != nil {
				return
			}
		case <-ch.done:
			return
		}
	}
}

// Peer returns the other side's public key; nil for a KindRelay initiator,
// whose responder has none.
func (ch *Channel) Peer() []byte { return ch.peer }

// WriteFrame sends p as one frame, split into as many messages as needed.
func (ch *Channel) WriteFrame(ctx context.Context, p []byte) error {
	ch.wmu.Lock()
	defer ch.wmu.Unlock()
	for {
		n := min(len(p), maxPiece)
		flag := flagMore
		if n == len(p) {
			flag = flagLast
		}
		if err := ch.writeLocked(ctx, flag, p[:n]); err != nil {
			return err
		}
		p = p[n:]
		if flag == flagLast {
			return nil
		}
	}
}

func (ch *Channel) writeControl(ctx context.Context, flag byte, nonce [nonceSize]byte) error {
	ch.wmu.Lock()
	defer ch.wmu.Unlock()
	return ch.writeLocked(ctx, flag, nonce[:])
}

func (ch *Channel) writeLocked(ctx context.Context, flag byte, piece []byte) error {
	if err := ch.failed(); err != nil {
		return err
	}
	pt := make([]byte, 0, 1+len(piece))
	pt = append(pt, flag)
	pt = append(pt, piece...)
	ct, err := ch.send.Encrypt(nil, nil, pt)
	if err != nil {
		return ch.fail(fmt.Errorf("noise: encrypt: %w", err))
	}
	if err := ch.c.WriteMessage(ctx, ct); err != nil {
		// A frame cut short leaves the stream unusable.
		return ch.fail(fmt.Errorf("noise: write: %w", err))
	}
	return nil
}

// ReadFrame returns the next frame. Pings and pongs are handled as they
// arrive, so a channel needs a reader for its pings to be answered.
func (ch *Channel) ReadFrame(ctx context.Context) ([]byte, error) {
	var frame []byte
	for {
		if err := ch.failed(); err != nil {
			return nil, err
		}
		msg, err := ch.c.ReadMessage(ctx)
		if err != nil {
			return nil, ch.fail(fmt.Errorf("noise: read: %w", err))
		}
		pt, err := ch.recv.Decrypt(nil, nil, msg)
		if err != nil {
			return nil, ch.fail(fmt.Errorf("%w: %w", ErrDecrypt, err))
		}
		if len(pt) == 0 {
			return nil, ch.fail(fmt.Errorf("%w: empty message", ErrFrame))
		}
		flag, body := pt[0], pt[1:]
		switch flag {
		case flagLast, flagMore:
			if len(frame)+len(body) > ch.maxFrame {
				return nil, ch.fail(fmt.Errorf("%w: over %d bytes", ErrTooLarge, ch.maxFrame))
			}
			if frame == nil {
				frame = []byte{}
			}
			frame = append(frame, body...)
			if flag == flagLast {
				return frame, nil
			}
		case flagPing, flagPong:
			if len(body) != nonceSize {
				return nil, ch.fail(fmt.Errorf("%w: %d-byte nonce", ErrFrame, len(body)))
			}
			nonce := [nonceSize]byte(body)
			if flag == flagPong {
				ch.pong(nonce)
				continue
			}
			select {
			case ch.pongs <- nonce:
			default:
				return nil, ch.fail(fmt.Errorf("%w: over %d pings unanswered", ErrFrame, maxPongs))
			}
		default:
			return nil, ch.fail(fmt.Errorf("%w: flag %d", ErrFrame, flag))
		}
	}
}

// Ping sends a ping and waits for its pong, which the reader delivers.
func (ch *Channel) Ping(ctx context.Context) error {
	var nonce [nonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("noise: ping: %w", err)
	}
	got := make(chan struct{})
	ch.pmu.Lock()
	ch.pings[nonce] = got
	ch.pmu.Unlock()
	defer func() {
		ch.pmu.Lock()
		delete(ch.pings, nonce)
		ch.pmu.Unlock()
	}()
	if err := ch.writeControl(ctx, flagPing, nonce); err != nil {
		return err
	}
	select {
	case <-got:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-ch.done:
		return ch.err
	}
}

func (ch *Channel) pong(nonce [nonceSize]byte) {
	ch.pmu.Lock()
	defer ch.pmu.Unlock()
	if got, ok := ch.pings[nonce]; ok {
		close(got)
		delete(ch.pings, nonce)
	}
}

// Close ends the channel and its connection. Later reads and writes
// return net.ErrClosed.
func (ch *Channel) Close() error {
	_ = ch.fail(net.ErrClosed)
	return nil
}

func (ch *Channel) failed() error {
	select {
	case <-ch.done:
		return ch.err
	default:
		return nil
	}
}

// fail ends the channel with err unless it has already ended, and returns
// the error the channel ended with.
func (ch *Channel) fail(err error) error {
	ch.once.Do(func() {
		ch.err = err
		close(ch.done)
		_ = ch.c.Close()
	})
	return ch.err
}
