package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	// Routes are tried in order: a direct route ("ws://host:port") or a
	// relay's connect URL ("wss://relay/v1/connect").
	Routes   []string
	HostKey  []byte // the host's public key, from pairing
	Identity noise.Identity
	// TLS, when set, is the TLS configuration wss:// routes are dialed
	// with (tests trust a test certificate with it); nil means the
	// system's.
	TLS *tls.Config
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
// failure. A host that refuses this device ends the search: its answer is
// authenticated and holds on every route.
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
		c, err := dialRoute(ctx, route, r.TLS, cfg, want)
		if err == nil {
			return c, nil
		}
		if errors.Is(err, noise.ErrDeviceRefused) {
			return nil, fmt.Errorf("client: reach host %s: %w", want, err)
		}
		errs = append(errs, fmt.Errorf("%s: %w", route, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("client: reach host %s: %w", want, errors.Join(errs...))
}

// routeURL is where a route is dialed for the host with ID host: a relay
// route asks the relay for the host, a direct route is the host's own
// Noise path.
func routeURL(route string, host noise.HostID) (string, error) {
	u, err := url.Parse(route)
	if err != nil {
		return "", fmt.Errorf("client: route: %w", err)
	}
	if u.Path == proto.RelayConnectPath {
		u.RawQuery = url.Values{"host": {string(host)}}.Encode()
		return u.String(), nil
	}
	return strings.TrimSuffix(route, "/") + proto.NoisePath, nil
}

func dialRoute(ctx context.Context, route string, tlsConf *tls.Config, cfg noise.DialConfig, want noise.HostID) (*Client, error) {
	ctx, cancel := context.WithTimeout(ctx, routeTimeout)
	defer cancel()
	u, err := routeURL(route, want)
	if err != nil {
		return nil, err
	}
	var opts *websocket.DialOptions
	if tlsConf != nil {
		opts = &websocket.DialOptions{HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConf.Clone()}}}
	}
	ws, resp, err := websocket.Dial(ctx, u, opts)
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
	c.route = route
	return c, nil
}
