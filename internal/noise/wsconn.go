package noise

import (
	"context"
	"fmt"

	"github.com/coder/websocket"
)

// wsReadLimit bounds a WebSocket message: one Noise message fits.
const wsReadLimit = 65536

// WSConn carries Noise messages as WebSocket binary messages, one Noise
// message to one WebSocket message.
type WSConn struct{ ws *websocket.Conn }

// NewWSConn wraps ws and limits the messages it reads.
func NewWSConn(ws *websocket.Conn) *WSConn {
	ws.SetReadLimit(wsReadLimit)
	return &WSConn{ws: ws}
}

// ReadMessage reads one binary message. A text message ends the
// connection.
func (c *WSConn) ReadMessage(ctx context.Context) ([]byte, error) {
	typ, p, err := c.ws.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("noise: websocket read: %w", err)
	}
	if typ != websocket.MessageBinary {
		_ = c.ws.Close(websocket.StatusUnsupportedData, "binary messages only")
		return nil, fmt.Errorf("%w: a websocket text message", ErrFrame)
	}
	return p, nil
}

// WriteMessage writes p as one binary message.
func (c *WSConn) WriteMessage(ctx context.Context, p []byte) error {
	if err := c.ws.Write(ctx, websocket.MessageBinary, p); err != nil {
		return fmt.Errorf("noise: websocket write: %w", err)
	}
	return nil
}

// Close ends the connection at once.
func (c *WSConn) Close() error { return c.ws.CloseNow() }

// Shutdown ends the connection in order: the peer reads what was written
// before it. It waits a few seconds at most for the peer.
func (c *WSConn) Shutdown() error { return c.ws.Close(websocket.StatusNormalClosure, "") }
