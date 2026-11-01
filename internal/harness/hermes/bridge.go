package hermes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// BridgeStatus is the "hoplet_bridge" field of the lines the bridge writes
// about itself, as opposed to the gateway frames it relays.
const (
	BridgeConnected    = "connected"
	BridgeDisconnected = "disconnected"
)

// bridgeLine is a line the bridge writes about itself.
type bridgeLine struct {
	Bridge     string   `json:"hoplet_bridge"`
	InstanceID string   `json:"instance_id,omitempty"`
	CodeSHA    string   `json:"code_sha,omitempty"`
	Caps       []string `json:"capabilities,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// heartbeat is how often the bridge pings the gateway by default, and the
// ID prefix of those pings, whose answers it does not relay.
const (
	heartbeat   = 15 * time.Second
	heartbeatID = "hoplet-hb-"
)

// maxHeld bounds the lines read from In and not yet sent; reading In waits
// when it is full.
const maxHeld = 256

// gatewayWriteTimeout bounds one write to the gateway. Writes do not use the
// stop context: cancelling a write makes the connection close, and the
// interrupt of a running turn must still go out.
const gatewayWriteTimeout = 10 * time.Second

// stableAfter is how long a connection must last before the reconnect delay
// resets: a gateway that accepts and drops at once must not be redialled at
// the minimum delay.
const stableAfter = 30 * time.Second

// BridgeConfig configures a bridge.
type BridgeConfig struct {
	Hermes string   // the hermes executable
	Env    []string // its environment; names the Hermes home
	In     io.Reader
	Out    io.Writer
	// Backoff is the first reconnect delay, doubled up to 30 s; zero means
	// one second.
	Backoff time.Duration
	// Heartbeat is how often the bridge pings the gateway; zero means 15 s.
	// Three missed heartbeats (no frame at all for three intervals) drop the
	// connection; a relay stalled writing to Out does not count as silence.
	Heartbeat time.Duration
}

// Bridge connects to the Hermes gateway and relays: every gateway frame
// becomes a line on Out, every line on In is sent to the gateway. It
// reconnects when the connection drops, writing a "disconnected" and then a
// "connected" line. Lines read while disconnected are discarded when the
// connection returns, and a line whose write fails is dropped: the caller
// resends what it still needs after it has re-attached. When ctx ends it
// interrupts a turn it saw start and not end, then returns nil; it also
// returns when In ends.
func Bridge(ctx context.Context, cfg BridgeConfig) error {
	b := &bridge{cfg: cfg, held: make(chan []byte, maxHeld), done: make(chan struct{})}
	defer close(b.done)
	inDone := make(chan error, 1)
	go func() { inDone <- b.readIn() }()
	backoff := cfg.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	delay := backoff
	reported := false // a "disconnected" line is written once per outage
	for {
		up, connected, err := b.session(ctx, inDone)
		var in *inputError
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, errInputEnded):
			return nil
		case errors.As(err, &in):
			return in.err
		}
		if connected {
			reported = false
			if up >= stableAfter {
				delay = backoff
			}
		}
		if !reported {
			if err := b.status(bridgeLine{Bridge: BridgeDisconnected, Error: err.Error()}); err != nil {
				return err
			}
			reported = true
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-inDone:
			return err
		case <-time.After(delay):
		}
		delay = min(delay*2, 30*time.Second)
	}
}

var errInputEnded = errors.New("hermes: bridge input ended")

// inputError is an input that ended with an error.
type inputError struct{ err error }

func (e *inputError) Error() string { return "hermes: bridge input: " + e.err.Error() }
func (e *inputError) Unwrap() error { return e.err }

type bridge struct {
	cfg  BridgeConfig
	held chan []byte   // lines from In; only readIn sends
	done chan struct{} // closed when Bridge returns

	lastRx  atomic.Pointer[time.Time] // when a frame last arrived (monotonic)
	writing atomic.Bool               // the relay is blocked writing to Out

	outMu sync.Mutex

	// The turn the bridge last saw start and not end.
	turnMu      sync.Mutex
	turnSession string
	gen         json.RawMessage
	running     bool
}

func (b *bridge) readIn() error {
	sc := bufio.NewScanner(b.cfg.In)
	sc.Buffer(nil, 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		select {
		case b.held <- append([]byte(nil), line...):
		case <-b.done:
			return nil
		}
	}
	return sc.Err()
}

func (b *bridge) write(line []byte) error {
	b.outMu.Lock()
	defer b.outMu.Unlock()
	if _, err := b.cfg.Out.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("hermes: bridge output: %w", err)
	}
	return nil
}

func (b *bridge) status(l bridgeLine) error {
	line, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("hermes: bridge status: %w", err)
	}
	return b.write(line)
}

// session runs one connection until it fails, ctx ends or In ends;
// connected reports it got as far as connecting and up how long it lasted.
func (b *bridge) session(ctx context.Context, inDone <-chan error) (up time.Duration, connected bool, err error) {
	ep, err := Ensure(ctx, b.cfg.Hermes, b.cfg.Env)
	if err != nil {
		return 0, false, err
	}
	conn, err := Dial(ctx, ep)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = conn.CloseNow() }()
	// Lines read before this connection were written before the adapter
	// could see it; it resends what it needs after re-attaching, so they are
	// discarded rather than delivered ahead of its session.resume. Best
	// effort: a line still in the stdin pipe can slip through, and the adapter
	// drops answers to requests from an earlier connection.
	for drained := false; !drained; {
		select {
		case <-b.held:
		default:
			drained = true
		}
	}
	start := time.Now()
	if err := b.status(bridgeLine{Bridge: BridgeConnected, InstanceID: ep.InstanceID, CodeSHA: ep.CodeSHA, Caps: ep.Capabilities}); err != nil {
		return time.Since(start), true, err
	}
	err = b.pump(ctx, conn, inDone)
	return time.Since(start), true, err
}

// send writes one frame to the gateway; stopping does not cancel it.
func (b *bridge) send(ctx context.Context, conn *websocket.Conn, frame []byte) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayWriteTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, frame)
}

// pump relays in both directions over one connection. It returns only after
// the relay goroutine has, so no frame reaches Out after pump has.
func (b *bridge) pump(ctx context.Context, conn *websocket.Conn, inDone <-chan error) error {
	b.touch()
	readErr := make(chan error, 1)
	relayDone := make(chan struct{})
	// Reads end when the connection closes, not when ctx does: stopping
	// must still reach the step that interrupts the running turn.
	go func() {
		defer close(relayDone)
		readErr <- b.relay(context.WithoutCancel(ctx), conn)
	}()
	defer func() {
		_ = conn.CloseNow() // the gateway needs no closing handshake
		<-relayDone
	}()
	every := b.cfg.Heartbeat
	if every <= 0 {
		every = heartbeat
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	n := 0
	for {
		if ctx.Err() != nil { // a stop wins over queued lines
			b.interrupt(conn)
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			b.interrupt(conn)
			return ctx.Err()
		case err := <-readErr:
			return err
		case line := <-b.held:
			if err := b.send(ctx, conn, line); err != nil {
				// Dropped: the connection is gone and the adapter resends
				// what it still needs once it has re-attached.
				return fmt.Errorf("hermes: send: %w", err)
			}
		case <-tick.C:
			// A stalled reader of our stdout is not a dead gateway.
			if !b.writing.Load() && time.Since(*b.lastRx.Load()) > 3*every {
				return errors.New("hermes: gateway stopped answering")
			}
			n++
			ping := fmt.Sprintf(`{"jsonrpc":"2.0","id":"%s%d","method":"ping","params":{}}`, heartbeatID, n)
			if err := b.send(ctx, conn, []byte(ping)); err != nil {
				return fmt.Errorf("hermes: heartbeat: %w", err)
			}
		case err := <-inDone:
			// The input is over and every line it read is in held: send
			// them in order. A failed write ends the drain; with the
			// connection gone they have nowhere to go.
			for drained := false; !drained; {
				if ctx.Err() != nil {
					b.interrupt(conn)
					return ctx.Err()
				}
				select {
				case line := <-b.held:
					drained = b.send(ctx, conn, line) != nil
				default:
					drained = true
				}
			}
			if err != nil {
				return &inputError{err}
			}
			return errInputEnded
		}
	}
}

// relay copies gateway frames to Out, one line each, until the connection
// ends.
func (b *bridge) relay(ctx context.Context, conn *websocket.Conn) error {
	for {
		_, frame, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("hermes: gateway connection: %w", err)
		}
		b.touch()
		var compact bytes.Buffer
		if json.Compact(&compact, frame) != nil {
			continue // not JSON: nothing a client can use
		}
		if b.observe(compact.Bytes()) {
			continue
		}
		b.writing.Store(true)
		err = b.write(compact.Bytes())
		b.touch() // the time spent blocked on Out is not silence
		b.writing.Store(false)
		if err != nil {
			return err
		}
	}
}

// touch records that the gateway is alive now.
func (b *bridge) touch() {
	now := time.Now()
	b.lastRx.Store(&now)
}

// observe tracks the running turn from a frame and reports whether the
// frame is a heartbeat answer, which is not relayed.
func (b *bridge) observe(frame []byte) (heartbeatAnswer bool) {
	var f struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Type    string          `json:"type"`
			Session string          `json:"session_id"`
			Gen     json.RawMessage `json:"execution_generation"`
		} `json:"params"`
	}
	if json.Unmarshal(frame, &f) != nil {
		return false
	}
	if f.Method == "" && strings.HasPrefix(strings.Trim(string(f.ID), `"`), heartbeatID) {
		return true
	}
	if f.Method != "event" {
		return false
	}
	b.turnMu.Lock()
	defer b.turnMu.Unlock()
	switch f.Params.Type {
	case "message.start":
		b.turnSession, b.gen, b.running = f.Params.Session, f.Params.Gen, true
	case "message.complete":
		if f.Params.Session == b.turnSession {
			b.running = false
		}
	}
	return false
}

// interrupt asks the gateway to stop the turn the bridge saw start, so a
// stopped session does not keep working.
func (b *bridge) interrupt(conn *websocket.Conn) {
	b.turnMu.Lock()
	running, session, gen := b.running, b.turnSession, b.gen
	b.turnMu.Unlock()
	if !running || len(gen) == 0 {
		return
	}
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":"hoplet-stop","method":"session.interrupt","params":{"session_id":%q,"execution_generation":%s}}`, session, gen)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageText, []byte(req))
}
