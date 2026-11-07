package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/relay"
	"github.com/yangflow/hoplet/proto"
)

// relayControlFrame bounds a frame on a relay's control connection.
const relayControlFrame = 4096

// relayer keeps this host registered with one relay.
type relayer struct {
	s    *server
	url  string // wss://relay[:port], no path
	http *http.Client
}

func newRelayer(s *server, base string, roots *tls.Config) *relayer {
	r := &relayer{s: s, url: strings.TrimSuffix(base, "/")}
	if roots != nil {
		r.http = &http.Client{Transport: &http.Transport{TLSClientConfig: roots.Clone()}}
	}
	return r
}

// run registers with the relay and serves the streams it announces until
// ctx ends, dialing again after a drop: first after relayRetry, then
// twice as long each time up to relayRetryMax; a registration resets it.
func (r *relayer) run(ctx context.Context) {
	log := r.s.log.With("relay", r.url)
	wait := r.s.lim.relayRetry
	for {
		registered, err := r.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if registered {
			wait = r.s.lim.relayRetry
			log.Warn("relay connection lost", "err", err)
		} else {
			log.Warn("relay refused the registration or is unreachable", "err", err)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		wait = min(wait*2, r.s.lim.relayRetryMax)
	}
}

// session runs one control connection and reports whether the relay
// confirmed the registration before it ended.
func (r *relayer) session(ctx context.Context) (registered bool, err error) {
	hctx, cancel := context.WithTimeout(ctx, r.s.lim.hello)
	ch, err := r.register(hctx)
	cancel()
	if err != nil {
		return false, err
	}
	defer ch.Close()
	r.s.log.Info("registered with relay", "relay", r.url)
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go r.heartbeat(ctx, ch)
	for {
		m, err := readRelayMessage(ctx, ch)
		if err != nil {
			return true, err
		}
		if m.Type == relay.TypeStream && m.Stream != "" {
			go r.accept(ctx, m.Stream)
		}
	}
}

// register dials the relay's host path and proves this host's key.
func (r *relayer) register(ctx context.Context) (*noise.Channel, error) {
	ws, _, err := websocket.Dial(ctx, r.url+proto.RelayHostPath, &websocket.DialOptions{HTTPClient: r.http})
	if err != nil {
		return nil, fmt.Errorf("daemon: dial relay: %w", err)
	}
	ch, err := noise.Dial(ctx, noise.NewWSConn(ws), noise.DialConfig{
		Identity: r.s.identity, Kind: noise.KindRelay, MaxFrame: relayControlFrame,
	})
	if err != nil {
		return nil, err
	}
	// The relay refuses a host outside its allowlist by closing: XN's last
	// message is the host's, so a refusal shows only here.
	m, err := readRelayMessage(ctx, ch)
	if err == nil && m.Type != relay.TypeRegistered {
		err = fmt.Errorf("daemon: relay sent %q before registered", m.Type)
	}
	if err != nil {
		_ = ch.Close()
		return nil, err
	}
	return ch, nil
}

// heartbeat pings the relay and ends the control connection when a ping
// goes unanswered.
func (r *relayer) heartbeat(ctx context.Context, ch *noise.Channel) {
	t := time.NewTicker(r.s.lim.ping)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pctx, cancel := context.WithTimeout(ctx, r.s.lim.pingTimeout)
		err := ch.Ping(pctx)
		cancel()
		if err != nil {
			_ = ch.Close()
			return
		}
	}
}

// accept dials back for a stream and serves the client at its other end.
func (r *relayer) accept(ctx context.Context, token string) {
	if !r.s.enter() {
		return
	}
	defer r.s.leave()
	dctx, cancel := context.WithTimeout(ctx, r.s.lim.hello)
	ws, _, err := websocket.Dial(dctx, r.url+proto.RelayAcceptPath+"?stream="+url.QueryEscape(token), &websocket.DialOptions{HTTPClient: r.http})
	cancel()
	if err != nil {
		r.s.log.Info("dial back to relay", "relay", r.url, "err", err)
		return
	}
	r.s.serveRemote(ws)
}

func readRelayMessage(ctx context.Context, ch *noise.Channel) (relay.Message, error) {
	b, err := ch.ReadFrame(ctx)
	if err != nil {
		return relay.Message{}, err
	}
	var m relay.Message
	if err := json.Unmarshal(b, &m); err != nil {
		return relay.Message{}, fmt.Errorf("daemon: relay message: %w", err)
	}
	if m.Type == "" {
		return relay.Message{}, errors.New("daemon: relay message without a type")
	}
	return m, nil
}
