package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/internal/frame"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/pair"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// pairingTTL is how long a pairing code stays valid.
const pairingTTL = 10 * time.Minute

// minDevicePrefix is the shortest device ID prefix devices.revoke takes.
const minDevicePrefix = 4

var (
	errRevoked  = fmt.Errorf("daemon: device revoked: %w", noise.ErrDeviceRefused)
	errUnpaired = fmt.Errorf("daemon: device not paired: %w", noise.ErrDeviceRefused)
	errPSKUsed  = fmt.Errorf("daemon: the pairing code is used, replaced or expired: %w", noise.ErrPairingRefused)
	errBadName  = errors.New("daemon: the display name must be non-empty UTF-8")
)

// pairing holds the open pairing secret, in memory only: a restart or a
// new pairing.start voids it.
type pairing struct {
	mu      sync.Mutex
	psk     []byte
	expires time.Time
}

// live returns the secret while it is unused and unexpired.
func (p *pairing) live(now time.Time) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.psk == nil || !now.Before(p.expires) {
		return nil
	}
	return p.psk
}

// take voids the secret if it is still psk and unexpired, reporting
// whether it was.
func (p *pairing) take(psk []byte, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.psk == nil || !bytes.Equal(p.psk, psk) || !now.Before(p.expires) {
		return false
	}
	p.psk = nil
	return true
}

func (p *pairing) open(psk []byte, expires time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.psk, p.expires = psk, expires
}

// pairingRoutes lists the routes a pairing code carries: the direct
// routes, then each relay's connect URL.
func pairingRoutes(direct, relays []string) []string {
	rs := slices.Clone(direct)
	for _, r := range relays {
		rs = append(rs, strings.TrimSuffix(r, "/")+proto.RelayConnectPath)
	}
	return rs
}

// startPairing opens a new pairing and returns its code.
func (s *server) startPairing() (proto.PairingCode, error) {
	if len(s.routes) == 0 {
		return proto.PairingCode{}, invalid("no routes to put in a pairing code: set network.routes (with network.listen) or network.relays in config.toml, then restart the daemon")
	}
	psk := make([]byte, noise.PSKSize)
	if _, err := rand.Read(psk); err != nil {
		return proto.PairingCode{}, fmt.Errorf("daemon: pairing secret: %w", err)
	}
	exp := s.now().Add(pairingTTL).Truncate(time.Second)
	s.pair.open(psk, exp)
	code := pair.Code{
		ID: s.identity.ID(), Key: s.identity.Public(), Name: pair.DisplayName(s.host),
		Routes: s.routes, PSK: psk, Expires: exp,
	}
	return proto.PairingCode{Code: code.String(), ExpiresAt: exp}, nil
}

// serveNoise is the direct listener's handler: it serves the client
// protocol inside a Noise channel on proto.NoisePath.
func (s *server) serveNoise(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser requests are not accepted", http.StatusForbidden)
		return
	}
	if r.URL.Path != proto.NoisePath {
		http.NotFound(w, r)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Debug("accept", "err", err)
		return
	}
	if !s.enter() {
		_ = ws.CloseNow()
		return
	}
	defer s.leave()
	s.serveRemote(ws)
}

// serveRemote serves the client protocol inside a Noise channel over ws,
// a connection from the direct listener or one dialed back to a relay.
// The caller has entered it.
func (s *server) serveRemote(ws *websocket.Conn) {
	wc := noise.NewWSConn(ws)
	ch, peer, err := s.handshake(wc)
	if err != nil {
		s.log.Info("refused a remote connection", "err", err)
		return
	}
	c := newConn(s, frame.Noise(ch, wc))
	c.remote, c.device = true, string(peer)
	if !s.track(c) {
		c.fail(errRevoked)
		return
	}
	defer s.untrack(c)
	c.serve()
}

// handshake runs the responder's handshake within the hello limit.
func (s *server) handshake(wc *noise.WSConn) (*noise.Channel, noise.HostID, error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.lim.hello)
	defer cancel()
	var used []byte // the secret this handshake was offered
	ch, _, err := noise.Accept(ctx, wc, noise.AcceptConfig{
		Identity: s.identity,
		Kinds:    []noise.Kind{noise.KindDaily, noise.KindPair},
		PSK: func() []byte {
			used = s.pair.live(s.now())
			return used
		},
		Allow: func(kind noise.Kind, peer, payload []byte) error {
			return s.allow(ctx, kind, peer, payload, used)
		},
		MaxFrame: int(s.lim.readLimit),
	})
	if err != nil {
		return nil, "", err
	}
	id, err := noise.IDOf(ch.Peer())
	if err != nil {
		_ = ch.Close()
		return nil, "", err
	}
	return ch, id, nil
}

// allow decides a handshake once the peer's key is known; for a pairing
// the secret has been verified by then.
func (s *server) allow(ctx context.Context, kind noise.Kind, peer, payload, psk []byte) error {
	id, err := noise.IDOf(peer)
	if err != nil {
		return err
	}
	if kind == noise.KindPair {
		name := string(payload)
		if name == "" || !utf8.ValidString(name) {
			return errBadName
		}
		now := s.now()
		if !s.pair.take(psk, now) {
			return errPSKUsed
		}
		return s.store.PairDevice(ctx, store.Device{ID: string(id), PublicKey: peer, Name: name, PairedAt: now})
	}
	d, err := s.store.Device(ctx, string(id))
	if errors.Is(err, store.ErrNotFound) {
		return errUnpaired
	}
	if err != nil {
		return err
	}
	if d.RevokedAt != nil {
		return errRevoked
	}
	return nil
}

// track registers a remote connection under its device so a revocation
// closes it, and reports whether the device may still connect. A
// revocation records itself before closing the tracked connections, so a
// connection tracked after that sweep finds the revocation here.
func (s *server) track(c *conn) bool {
	s.peersMu.Lock()
	if s.peers[c.device] == nil {
		s.peers[c.device] = map[*conn]struct{}{}
	}
	s.peers[c.device][c] = struct{}{}
	s.peersMu.Unlock()
	d, err := s.store.Device(s.ctx, c.device)
	return err == nil && d.RevokedAt == nil
}

func (s *server) untrack(c *conn) {
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	delete(s.peers[c.device], c)
	if len(s.peers[c.device]) == 0 {
		delete(s.peers, c.device)
	}
}

// revoke revokes the device named by id, a full ID or a unique prefix,
// and ends its connections.
func (s *server) revoke(ctx context.Context, id string) error {
	if len(id) < minDevicePrefix {
		return invalid(fmt.Sprintf("give the device ID or at least %d characters of it", minDevicePrefix))
	}
	ds, err := s.store.Devices(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case len(ds) == 0:
		return &proto.APIError{Code: proto.CodeNotFound, Message: "no device " + id}
	case len(ds) > 1:
		ids := make([]string, len(ds))
		for i, d := range ds {
			ids[i] = d.ID
		}
		sort.Strings(ids)
		return invalid(fmt.Sprintf("%s matches several devices: %s", id, strings.Join(ids, ", ")))
	}
	target := ds[0].ID
	if err := s.store.RevokeDevice(ctx, target, s.now()); err != nil {
		return err
	}
	s.peersMu.Lock()
	var gone []*conn
	for c := range s.peers[target] {
		gone = append(gone, c)
	}
	s.peersMu.Unlock()
	for _, c := range gone {
		c.fail(errRevoked)
	}
	return nil
}
