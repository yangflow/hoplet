package noise

import (
	"context"
	"net"
	"sync"
)

// pipeEnd is one side of an in-memory MsgConn pair. Its filter, when set,
// sees every message the side writes and returns what is delivered: tests
// drop, repeat, alter or hold messages with it.
type pipeEnd struct {
	in     chan []byte
	out    chan []byte
	shared *pipeState

	mu     sync.Mutex
	filter func(msg []byte) [][]byte
}

type pipeState struct {
	once sync.Once
	done chan struct{}
}

func newPipe() (*pipeEnd, *pipeEnd) {
	ab, ba := make(chan []byte, 64), make(chan []byte, 64)
	st := &pipeState{done: make(chan struct{})}
	return &pipeEnd{in: ba, out: ab, shared: st}, &pipeEnd{in: ab, out: ba, shared: st}
}

func (p *pipeEnd) setFilter(f func(msg []byte) [][]byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.filter = f
}

func (p *pipeEnd) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case m := <-p.in:
		return m, nil
	case <-p.shared.done:
		// Like a stream, the pipe delivers what was written before the close.
		select {
		case m := <-p.in:
			return m, nil
		default:
			return nil, net.ErrClosed
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *pipeEnd) WriteMessage(ctx context.Context, msg []byte) error {
	msg = append([]byte(nil), msg...)
	p.mu.Lock()
	f := p.filter
	p.mu.Unlock()
	out := [][]byte{msg}
	if f != nil {
		out = f(msg)
	}
	for _, m := range out {
		select {
		case <-p.shared.done:
			return net.ErrClosed
		default:
		}
		select {
		case p.out <- m:
		case <-p.shared.done:
			return net.ErrClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (p *pipeEnd) Close() error {
	p.shared.once.Do(func() { close(p.shared.done) })
	return nil
}

// closed reports whether either side closed the pipe.
func (p *pipeEnd) closed() bool {
	select {
	case <-p.shared.done:
		return true
	default:
		return false
	}
}
