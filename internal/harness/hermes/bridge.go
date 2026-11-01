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

// maxHeld bounds the commands the bridge holds while it reconnects.
const maxHeld = 256

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
	Heartbeat time.Duration
}

// Bridge connects to the Hermes gateway and relays: every gateway frame
// becomes a line on Out, every line on In is sent to the gateway. It
// reconnects when the connection drops, writing a "disconnected" and then a
// "connected" line, and holds the lines it reads meanwhile. When ctx ends
// it interrupts a turn it saw start and not end, then returns nil; it also
// returns when In ends.
func Bridge(ctx context.Context, cfg BridgeConfig) error {
	b := &bridge{cfg: cfg, held: make(chan []byte, maxHeld)}
	inDone := make(chan error, 1)
	go func() { inDone <- b.readIn() }()
	backoff := cfg.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	delay := backoff
	reported := false // a "disconnected" line is written once per outage
	for {
		connected, err := b.session(ctx, inDone)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, errInputEnded):
			return nil
		}
		if connected {
			delay, reported = backoff, false
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

type bridge struct {
	cfg  BridgeConfig
	held chan []byte // lines from In

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
		b.held <- append([]byte(nil), line...)
	}
	close(b.held)
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
		return err
	}
	return b.write(line)
}

// session runs one connection until it fails, ctx ends or In ends;
// connected reports it got as far as connecting.
func (b *bridge) session(ctx context.Context, inDone <-chan error) (connected bool, err error) {
	ep, err := Ensure(ctx, b.cfg.Hermes, b.cfg.Env)
	if err != nil {
		return false, err
	}
	conn, err := Dial(ctx, ep)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.CloseNow() }()
	if err := b.status(bridgeLine{Bridge: BridgeConnected, InstanceID: ep.InstanceID, CodeSHA: ep.CodeSHA, Caps: ep.Capabilities}); err != nil {
		return true, err
	}
	return true, b.pump(ctx, conn, inDone)
}

// pump relays in both directions over one connection.
func (b *bridge) pump(ctx context.Context, conn *websocket.Conn, inDone <-chan error) error {
	readErr := make(chan error, 1)
	// Reads end when the connection closes, not when ctx does: stopping
	// must still reach the step that interrupts the running turn.
	go func() { readErr <- b.relay(context.WithoutCancel(ctx), conn) }()
	every := b.cfg.Heartbeat
	if every <= 0 {
		every = heartbeat
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			b.interrupt(conn)
			_ = conn.CloseNow() // the gateway needs no closing handshake
			return ctx.Err()
		case err := <-readErr:
			return err
		case line, ok := <-b.held:
			if !ok {
				_ = conn.CloseNow() // the gateway needs no closing handshake
				return errInputEnded
			}
			if err := conn.Write(ctx, websocket.MessageText, line); err != nil {
				// Not sent: keep it for the next connection.
				b.requeue(line)
				return fmt.Errorf("hermes: send: %w", err)
			}
		case <-tick.C:
			n++
			ping := fmt.Sprintf(`{"jsonrpc":"2.0","id":"%s%d","method":"ping","params":{}}`, heartbeatID, n)
			if err := conn.Write(ctx, websocket.MessageText, []byte(ping)); err != nil {
				return fmt.Errorf("hermes: heartbeat: %w", err)
			}
		case err := <-inDone:
			_ = conn.CloseNow() // the gateway needs no closing handshake
			if err != nil {
				return err
			}
			return errInputEnded
		}
	}
}

// requeue puts a line that was not sent back in front of the queue, as far
// as a channel allows: it goes to the back, which only reorders lines that
// raced a dropped connection.
func (b *bridge) requeue(line []byte) {
	select {
	case b.held <- line:
	default:
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
		var compact bytes.Buffer
		if json.Compact(&compact, frame) != nil {
			continue // not JSON: nothing a client can use
		}
		if b.observe(compact.Bytes()) {
			continue
		}
		if err := b.write(compact.Bytes()); err != nil {
			return err
		}
	}
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
