package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/internal/frame"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/proto"
)

// routeTimeout bounds one route's dial, handshake and hello, so a dead
// route does not use up the time for the next.
const routeTimeout = 15 * time.Second

// Remote says how to reach another host over Noise.
type Remote struct {
	Routes   []string // tried in order; "ws://host:port" or "wss://host"
	HostKey  []byte   // the host's public key, from pairing
	Identity noise.Identity
}

// DialRemote connects to a paired host and says hello.
func DialRemote(ctx context.Context, r Remote) (*Client, error) {
	return dialRemote(ctx, r, noise.DialConfig{Kind: noise.KindDaily})
}

// Pair joins a host with a pairing code's secret; name is this machine's
// display name on the host. The connection is an ordinary one after.
func Pair(ctx context.Context, r Remote, psk []byte, name string) (*Client, error) {
	return dialRemote(ctx, r, noise.DialConfig{Kind: noise.KindPair, PSK: psk, Payload: []byte(name)})
}

// dialRemote tries the routes in order; the error names every route's
// failure.
func dialRemote(ctx context.Context, r Remote, cfg noise.DialConfig) (*Client, error) {
	want, err := noise.IDOf(r.HostKey)
	if err != nil {
		return nil, fmt.Errorf("client: host key: %w", err)
	}
	if len(r.Routes) == 0 {
		return nil, errors.New("client: the host has no routes")
	}
	cfg.Identity, cfg.PeerStatic, cfg.MaxFrame = r.Identity, r.HostKey, readLimit
	var errs []error
	for _, route := range r.Routes {
		c, err := dialRoute(ctx, route, cfg, want)
		if err == nil {
			return c, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", route, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("client: reach host %s: %w", want, errors.Join(errs...))
}

func dialRoute(ctx context.Context, route string, cfg noise.DialConfig, want noise.HostID) (*Client, error) {
	ctx, cancel := context.WithTimeout(ctx, routeTimeout)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, strings.TrimSuffix(route, "/")+proto.NoisePath, nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	wc := noise.NewWSConn(ws)
	ch, err := noise.Dial(ctx, wc, cfg)
	if err != nil {
		return nil, err
	}
	c, err := start(ctx, frame.Noise(ch, wc), proto.HelloParams{ProtocolMajor: proto.ProtocolMajor})
	if err != nil {
		return nil, err
	}
	if c.hello.HostID != string(want) {
		_ = c.Close()
		return nil, fmt.Errorf("client: the host says it is %q, want %s", c.hello.HostID, want)
	}
	return c, nil
}
