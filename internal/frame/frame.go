// Package frame carries the client protocol's frames over a transport: a
// local WebSocket or, between hosts, a Noise channel.
package frame

import (
	"context"

	"github.com/coder/websocket"
)

// Conn carries whole client-protocol frames. One goroutine reads; any
// number write.
type Conn interface {
	ReadFrame(ctx context.Context) ([]byte, error)
	WriteFrame(ctx context.Context, p []byte) error
	// Ping waits for the peer's answer; it needs a running reader.
	Ping(ctx context.Context) error
	// Close ends the connection at once.
	Close() error
	// Shutdown ends the connection in order: the peer reads what was
	// written before it. It may wait for the peer for a few seconds.
	Shutdown() error
}

// WebSocket carries frames as WebSocket text messages.
func WebSocket(ws *websocket.Conn) Conn { return wsConn{ws} }

type wsConn struct{ ws *websocket.Conn }

func (c wsConn) ReadFrame(ctx context.Context) ([]byte, error) {
	_, p, err := c.ws.Read(ctx)
	return p, err
}

func (c wsConn) WriteFrame(ctx context.Context, p []byte) error {
	return c.ws.Write(ctx, websocket.MessageText, p)
}

func (c wsConn) Ping(ctx context.Context) error { return c.ws.Ping(ctx) }
func (c wsConn) Close() error                   { return c.ws.CloseNow() }
func (c wsConn) Shutdown() error                { return c.ws.Close(websocket.StatusNormalClosure, "") }
