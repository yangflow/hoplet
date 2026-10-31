// Package client talks to a Hoplet daemon over its client protocol.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/proto"
)

// ErrClosed is returned once the connection is closed or lost.
var ErrClosed = errors.New("client: connection closed")

const (
	// readLimit is the largest frame accepted from the daemon; a snapshot
	// can be large.
	readLimit = 64 << 20
	ping      = 15 * time.Second
	pingWait  = 10 * time.Second
)

// Options says where and as whom to connect.
type Options struct {
	Socket   string
	Token    string
	DeviceID string
}

// HomeOptions returns the options for the daemon of a home directory.
func HomeOptions(dir, deviceID string) (Options, error) {
	tok, err := home.ReadToken(dir)
	if err != nil {
		return Options{}, err
	}
	return Options{Socket: home.Socket(dir), Token: tok, DeviceID: deviceID}, nil
}

// Client is a connection to a daemon. Its methods are safe for concurrent
// use. A lost connection is not reestablished: dial again and resume
// subscriptions from their cursors.
type Client struct {
	ws    *websocket.Conn
	hello proto.HelloResult

	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}  // closed when the reader stops
	wg     sync.WaitGroup // reader and heartbeat

	mu        sync.Mutex
	closing   bool          // Close has begun; later losses are not reported
	closeDone chan struct{} // closed when the first Close has finished
	next      uint64
	pending   map[uint64]chan proto.ServerFrame
	subs      map[uint64]*Subscription
}

// Dial connects and says hello.
func Dial(ctx context.Context, o Options) (*Client, error) {
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", o.Socket)
		},
	}}
	ws, resp, err := websocket.Dial(ctx, "ws://hoplet"+proto.APIPath, &websocket.DialOptions{HTTPClient: hc})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("client: dial %s: %w", o.Socket, err)
	}
	ws.SetReadLimit(readLimit)
	cctx, cancel := context.WithCancelCause(context.Background())
	c := &Client{
		ws: ws, ctx: cctx, cancel: cancel, done: make(chan struct{}),
		closeDone: make(chan struct{}), pending: map[uint64]chan proto.ServerFrame{}, subs: map[uint64]*Subscription{},
	}
	c.wg.Add(2)
	go c.read()
	go c.heartbeat()
	err = c.Call(ctx, proto.MethodHello, proto.HelloParams{
		ProtocolMajor: proto.ProtocolMajor, DeviceID: o.DeviceID, Token: o.Token,
	}, &c.hello)
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Hello is what the daemon said about itself.
func (c *Client) Hello() proto.HelloResult { return c.hello }

// Done is closed when the connection is lost or closed.
func (c *Client) Done() <-chan struct{} { return c.done }

// Close closes the connection; it reports an error only when the closing
// handshake fails on a connection that was still up.
// It is idempotent: a later or concurrent call waits for the first to
// finish and returns nil.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		<-c.closeDone
		return nil
	}
	c.closing = true
	defer close(c.closeDone)
	lost := c.ctx.Err() != nil
	c.mu.Unlock()
	err := c.ws.Close(websocket.StatusNormalClosure, "")
	c.cancel(ErrClosed)
	c.wg.Wait()
	if lost {
		return nil
	}
	return err
}

// lose ends the connection with cause unless Close has already begun.
func (c *Client) lose(cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closing {
		c.cancel(cause)
	}
}

func (c *Client) read() {
	defer c.wg.Done()
	defer close(c.done)
	defer c.drop()
	for {
		_, data, err := c.ws.Read(c.ctx)
		if err != nil {
			c.lose(fmt.Errorf("%w: %w", ErrClosed, err))
			return
		}
		var f proto.ServerFrame
		if err := json.Unmarshal(data, &f); err != nil {
			c.lose(fmt.Errorf("%w: bad frame: %w", ErrClosed, err))
			_ = c.ws.CloseNow()
			return
		}
		c.mu.Lock()
		if f.Sub != 0 {
			s := c.subs[f.Sub]
			if f.End {
				delete(c.subs, f.Sub)
			}
			c.mu.Unlock()
			if s != nil {
				s.push(f)
			}
			continue
		}
		ch := c.pending[f.ID]
		delete(c.pending, f.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- f
		}
	}
}

// drop fails every waiting call and open subscription.
func (c *Client) drop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	for id, s := range c.subs {
		s.end(c.err())
		delete(c.subs, id)
	}
}

func (c *Client) heartbeat() {
	defer c.wg.Done()
	t := time.NewTicker(ping)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(c.ctx, pingWait)
		err := c.ws.Ping(ctx)
		cancel()
		if err != nil {
			c.lose(fmt.Errorf("%w: heartbeat: %w", ErrClosed, err))
			_ = c.ws.CloseNow()
			return
		}
	}
}

// gone reports whether the connection is closing or lost.
func (c *Client) gone() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closing || c.ctx.Err() != nil
}

// err is why the connection ended.
func (c *Client) err() error {
	if err := context.Cause(c.ctx); err != nil {
		return err
	}
	return ErrClosed
}

// request sends a request and returns the channel its response arrives on;
// sub, when set, is registered before the request goes out so no item is
// missed.
func (c *Client) request(method string, params any, sub *Subscription) (uint64, chan proto.ServerFrame, error) {
	b, err := json.Marshal(params)
	if err != nil {
		return 0, nil, fmt.Errorf("client: encode params: %w", err)
	}
	ch := make(chan proto.ServerFrame, 1)
	c.mu.Lock()
	if c.closing || c.ctx.Err() != nil {
		c.mu.Unlock()
		return 0, nil, c.err()
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	if sub != nil {
		sub.id = id
		c.subs[id] = sub
	}
	c.mu.Unlock()
	frame, err := json.Marshal(proto.ClientFrame{ID: id, Method: method, Params: b})
	if err != nil {
		return 0, nil, fmt.Errorf("client: encode request: %w", err)
	}
	// The frame is written under the connection's lifetime: a caller's
	// context firing mid-write would otherwise close the shared connection.
	if err := c.ws.Write(c.ctx, websocket.MessageText, frame); err != nil {
		c.forget(id)
		if c.gone() {
			return 0, nil, c.err()
		}
		return 0, nil, fmt.Errorf("client: send %s: %w", method, err)
	}
	return id, ch, nil
}

func (c *Client) forget(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
	delete(c.subs, id)
}

// await waits for a response; an error from the daemon is a *proto.APIError.
func (c *Client) await(ctx context.Context, id uint64, ch chan proto.ServerFrame) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	case f, ok := <-ch:
		if !ok {
			return nil, c.err()
		}
		if f.Error != nil {
			return nil, f.Error
		}
		return f.Result, nil
	}
}

// Call sends a request and decodes its result into result, which may be nil.
// ctx bounds the wait for the response; the request frame itself is written
// under the connection's lifetime.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	id, ch, err := c.request(method, params, nil)
	if err != nil {
		return err
	}
	raw, err := c.await(ctx, id, ch)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("client: decode %s result: %w", method, err)
	}
	return nil
}

// Subscribe opens a subscription. ctx bounds the wait for the daemon's
// acceptance; if it ends after the request was sent, the daemon is asked to
// drop the subscription.
func (c *Client) Subscribe(ctx context.Context, method string, params any) (*Subscription, error) {
	s := &Subscription{c: c, wake: make(chan struct{}, 1)}
	id, ch, err := c.request(method, params, s)
	if err != nil {
		return nil, err
	}
	if _, err := c.await(ctx, id, ch); err != nil {
		c.forget(id)
		if ctx.Err() != nil {
			c.sendCancel(id)
		}
		return nil, err
	}
	return s, nil
}

// sendCancel asks the daemon to end subscription id, best effort.
func (c *Client) sendCancel(id uint64) {
	b, err := json.Marshal(proto.ClientFrame{Sub: id, Cancel: true})
	if err != nil {
		return
	}
	_ = c.ws.Write(c.ctx, websocket.MessageText, b)
}

// Subscription is an open subscription. Received items wait in memory,
// without bound, until Next takes them: consumers must keep reading or
// Cancel.
type Subscription struct {
	c    *Client
	id   uint64
	wake chan struct{}

	mu    sync.Mutex
	items []json.RawMessage
	ended bool
	err   error
}

func (s *Subscription) push(f proto.ServerFrame) {
	s.mu.Lock()
	switch {
	case s.ended:
	case f.End:
		s.ended = true
		if f.Error != nil {
			s.err = f.Error
		}
	default:
		s.items = append(s.items, f.Item)
	}
	s.mu.Unlock()
	s.signal()
}

// end ends the subscription with err, which is the connection's.
func (s *Subscription) end(err error) {
	s.mu.Lock()
	if !s.ended {
		s.ended = true
		s.err = err
	}
	s.mu.Unlock()
	s.signal()
}

// signal wakes a waiting Next without blocking.
func (s *Subscription) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// ErrEnded is returned by Next once a subscription that ended without an
// error (canceled by the client, or ended cleanly by the daemon) has
// delivered its last item.
var ErrEnded = errors.New("client: subscription ended")

// Next decodes the next item into v, waiting for one. After the last item
// it returns the subscription's error: the daemon's, ErrEnded when it ended
// without one, or an error wrapping ErrClosed when the connection ended
// first. Items are delivered in order to whichever caller takes them.
func (s *Subscription) Next(ctx context.Context, v any) error {
	for {
		s.mu.Lock()
		if len(s.items) > 0 {
			item := s.items[0]
			s.items[0] = nil
			s.items = s.items[1:]
			more := len(s.items) > 0 || s.ended
			s.mu.Unlock()
			if more {
				s.signal()
			}
			if err := json.Unmarshal(item, v); err != nil {
				return fmt.Errorf("client: decode item: %w", err)
			}
			return nil
		}
		if s.ended {
			err := s.err
			s.mu.Unlock()
			s.signal()
			if err == nil {
				return ErrEnded
			}
			return err
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.wake:
		}
	}
}

// Cancel asks the daemon to end the subscription; Next returns the items
// already received, then ErrEnded.
// It does nothing if the subscription already ended. The frame is written
// under the connection's lifetime; ctx is not used.
func (s *Subscription) Cancel(_ context.Context) error {
	s.mu.Lock()
	ended := s.ended
	s.mu.Unlock()
	if ended {
		return nil
	}
	b, err := json.Marshal(proto.ClientFrame{Sub: s.id, Cancel: true})
	if err != nil {
		return err
	}
	if err := s.c.ws.Write(s.c.ctx, websocket.MessageText, b); err != nil {
		if s.c.gone() {
			return s.c.err()
		}
		return fmt.Errorf("client: cancel: %w", err)
	}
	return nil
}
