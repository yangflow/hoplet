package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/proto"
)

var (
	errSlow    = errors.New("daemon: client is too slow")
	errNoHello = errors.New("daemon: no hello in time")
)

// conn is one client connection.
type conn struct {
	s      *server
	ws     *websocket.Conn
	ctx    context.Context
	cancel context.CancelCauseFunc

	authed atomic.Bool
	device string // set by hello, before any request is handled

	outMu    sync.Mutex
	out      [][]byte // frames waiting for the writer
	outBytes int64    // bytes of the frames waiting for the writer
	outWake  chan struct{}

	subMu sync.Mutex
	subs  map[uint64]context.CancelFunc

	work sync.WaitGroup // request handlers, subscriptions included
}

func newConn(s *server, ws *websocket.Conn) *conn {
	ctx, cancel := context.WithCancelCause(s.ctx)
	return &conn{s: s, ws: ws, ctx: ctx, cancel: cancel, outWake: make(chan struct{}, 1), subs: map[uint64]context.CancelFunc{}}
}

// serve runs the connection until the client leaves, the connection fails
// or the daemon stops.
func (c *conn) serve() {
	var bg sync.WaitGroup
	bg.Add(2)
	go func() {
		defer bg.Done()
		c.write()
	}()
	go func() {
		defer bg.Done()
		c.heartbeat()
	}()
	timer := time.AfterFunc(c.s.lim.hello, func() {
		if !c.authed.Load() {
			c.fail(errNoHello)
		}
	})
	c.read()
	timer.Stop()
	c.cancel(context.Canceled)
	c.work.Wait()
	bg.Wait()
	_ = c.ws.CloseNow()
}

// fail ends the connection abruptly; the client reconnects and catches up
// by cursor.
func (c *conn) fail(err error) {
	c.s.log.Info("closing connection", "err", err)
	c.cancel(err)
	_ = c.ws.CloseNow()
}

// read handles incoming frames until the connection ends.
func (c *conn) read() {
	for {
		_, data, err := c.ws.Read(c.ctx)
		if err != nil {
			return
		}
		var f proto.ClientFrame
		if err := json.Unmarshal(data, &f); err != nil {
			c.fail(fmt.Errorf("daemon: bad frame: %w", err))
			return
		}
		if !c.authed.Load() {
			if f.Cancel || f.ID == 0 || f.Method == "" {
				f.Method = "" // refused as a request that is not a hello
				if f.ID == 0 {
					f.ID = 1
				}
			}
			if !c.hello(f) {
				return
			}
			continue
		}
		if f.Cancel {
			c.cancelSub(f.Sub)
			continue
		}
		if f.ID == 0 || f.Method == "" {
			c.fail(errors.New("daemon: a request needs an id and a method"))
			return
		}
		var sub *subscription
		if f.Method == proto.MethodSubscribeCatalog || f.Method == proto.MethodSubscribeSession {
			// Registered here, not in the handler: a cancel that follows
			// this frame must find the subscription.
			if sub = c.register(f.ID); sub == nil {
				c.respond(f.ID, nil, &proto.APIError{Code: proto.CodeBadRequest, Message: "a subscription with this id is open"})
				continue
			}
		}
		c.work.Add(1)
		go func() {
			defer c.work.Done()
			if sub != nil {
				c.subscribe(f, sub)
				return
			}
			c.handle(f)
		}()
	}
}

// hello handles the first request, which must be a hello; it reports
// whether the connection may go on.
func (c *conn) hello(f proto.ClientFrame) bool {
	var p proto.HelloParams
	err := decode(f, &p)
	switch {
	case f.Method != proto.MethodHello:
		err = &proto.APIError{Code: proto.CodeUnauthorized, Message: "the first request must be hello"}
	case err != nil:
	case p.ProtocolMajor != proto.ProtocolMajor:
		err = &proto.APIError{Code: proto.CodeVersion, Message: fmt.Sprintf(
			"the daemon speaks protocol %d, the client %d; upgrade the older one", proto.ProtocolMajor, p.ProtocolMajor)}
	case subtle.ConstantTimeCompare([]byte(p.Token), []byte(c.s.token)) != 1:
		err = &proto.APIError{Code: proto.CodeUnauthorized, Message: "wrong token"}
	case p.DeviceID == "":
		err = &proto.APIError{Code: proto.CodeBadRequest, Message: "hello needs a device id"}
	}
	if err != nil {
		// Written directly: the writer may not run once the connection ends.
		b, _ := json.Marshal(proto.ServerFrame{ID: f.ID, Error: apiError(err)})
		ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		defer cancel()
		_ = c.ws.Write(ctx, websocket.MessageText, b)
		_ = c.ws.Close(websocket.StatusPolicyViolation, "hello refused")
		return false
	}
	c.device = p.DeviceID
	c.authed.Store(true)
	c.respond(f.ID, proto.HelloResult{Host: c.s.host, Version: c.s.version, ProtocolMajor: proto.ProtocolMajor}, nil)
	return true
}

func decode(f proto.ClientFrame, v any) error {
	if len(f.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(f.Params, v); err != nil {
		return &proto.APIError{Code: proto.CodeBadRequest, Message: "params: " + err.Error()}
	}
	return nil
}

// handle answers one request. Commands run under the server's context, not
// the connection's: a client that drops after its message is recorded must
// not cancel the message's delivery to the harness, and since commands are
// keyed and idempotent the client's retry after reconnecting finds the
// result. Subscriptions, which serve only this connection, keep its context.
func (c *conn) handle(f proto.ClientFrame) {
	res, err := c.call(c.s.ctx, f)
	c.respond(f.ID, res, err)
}

// respond queues the response to request id.
func (c *conn) respond(id uint64, res any, err error) {
	f := proto.ServerFrame{ID: id}
	if err != nil {
		f.Error = apiError(err)
	} else if f.Result, err = json.Marshal(res); err != nil {
		f.Result, f.Error = nil, apiError(fmt.Errorf("daemon: encode result: %w", err))
	}
	c.send(f)
}

// item queues an item of subscription sub.
func (c *conn) item(sub uint64, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("daemon: encode item: %w", err)
	}
	c.send(proto.ServerFrame{Sub: sub, Item: b})
	return nil
}

func (c *conn) send(f proto.ServerFrame) {
	if c.ctx.Err() != nil {
		return // the writer is gone
	}
	b, err := json.Marshal(f)
	if err != nil {
		c.fail(fmt.Errorf("daemon: encode frame: %w", err))
		return
	}
	c.outMu.Lock()
	// A frame alone always goes out, however large; frames waiting for the
	// writer beyond the limit mean the client does not keep up.
	if c.outBytes > 0 && c.outBytes+int64(len(b)) > c.s.lim.outbound {
		c.outMu.Unlock()
		c.fail(errSlow)
		return
	}
	c.out = append(c.out, b)
	c.outBytes += int64(len(b))
	c.outMu.Unlock()
	select {
	case c.outWake <- struct{}{}:
	default:
	}
}

// write writes queued frames in order until the connection ends.
func (c *conn) write() {
	for {
		c.outMu.Lock()
		if len(c.out) == 0 {
			c.outMu.Unlock()
			select {
			case <-c.ctx.Done():
				return
			case <-c.outWake:
			}
			continue
		}
		b := c.out[0]
		c.out[0] = nil
		c.out = c.out[1:]
		c.outBytes -= int64(len(b))
		c.outMu.Unlock()
		if err := c.ws.Write(c.ctx, websocket.MessageText, b); err != nil {
			c.cancel(err)
			return
		}
	}
}

// heartbeat pings the client; one that does not answer in time is gone.
func (c *conn) heartbeat() {
	t := time.NewTicker(c.s.lim.ping)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(c.ctx, c.s.lim.pingTimeout)
		err := c.ws.Ping(ctx)
		cancel()
		if err != nil {
			if c.ctx.Err() == nil {
				c.fail(fmt.Errorf("daemon: heartbeat: %w", err))
			}
			return
		}
	}
}

// subscription is an open subscription's context.
type subscription struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// register opens the subscription with the given id; it returns nil when the
// id is in use.
func (c *conn) register(id uint64) *subscription {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	if _, dup := c.subs[id]; dup {
		return nil
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.subs[id] = cancel
	return &subscription{ctx: ctx, cancel: cancel}
}

// subscribe runs a registered subscription until it ends, the client cancels
// it or the connection ends. The response to the request comes first; items
// follow.
func (c *conn) subscribe(f proto.ClientFrame, sub *subscription) {
	ctx := sub.ctx
	defer sub.cancel()
	defer func() {
		c.subMu.Lock()
		delete(c.subs, f.ID)
		c.subMu.Unlock()
	}()

	var run func(context.Context) error
	switch f.Method {
	case proto.MethodSubscribeCatalog:
		var p proto.CatalogParams
		if err := decode(f, &p); err != nil {
			c.respond(f.ID, nil, err)
			return
		}
		run = func(ctx context.Context) error { return c.catalog(ctx, f.ID, p) }
	case proto.MethodSubscribeSession:
		var p proto.SessionParams
		if err := decode(f, &p); err != nil {
			c.respond(f.ID, nil, err)
			return
		}
		// The connection's context, not the subscription's: a subscription
		// canceled before it starts still gets its response and end.
		if _, err := c.s.store.Session(c.ctx, p.Session); err != nil {
			c.respond(f.ID, nil, err)
			return
		}
		run = func(ctx context.Context) error { return c.session(ctx, f.ID, p) }
	}
	c.respond(f.ID, struct{}{}, nil)
	err := run(ctx)
	if c.ctx.Err() != nil {
		return // the connection is gone
	}
	end := proto.ServerFrame{Sub: f.ID, End: true}
	if err != nil && ctx.Err() == nil {
		end.Error = apiError(err)
	}
	c.send(end)
}

func (c *conn) cancelSub(id uint64) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	if cancel, ok := c.subs[id]; ok {
		cancel()
	}
}
