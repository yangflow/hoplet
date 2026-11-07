package noise

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	fnoise "github.com/flynn/noise"
)

// Kind is the handshake a connection opens with.
type Kind byte

// The handshakes a channel can open with.
const (
	KindDaily Kind = 1 // Noise IK: a paired device reaches a known host
	KindPair  Kind = 2 // Noise IKpsk1: a device pairs with a one-time secret
	KindRelay Kind = 3 // Noise XN: a host proves its key to a relay
)

func (k Kind) String() string {
	switch k {
	case KindDaily:
		return "daily"
	case KindPair:
		return "pair"
	case KindRelay:
		return "relay"
	}
	return fmt.Sprintf("kind(%d)", byte(k))
}

const (
	// prologue is bound into every handshake, followed by the kind byte.
	prologue = "hoplet/1"
	// PSKSize is the length of a pairing secret.
	PSKSize = 32
	// MaxNameSize bounds the display name a pairing device sends.
	MaxNameSize = 64
	// maxMessage is the largest Noise message.
	maxMessage = fnoise.MaxMsgLen
	// DefaultMaxFrame is the largest frame a channel assembles by default.
	DefaultMaxFrame = 16 << 20
)

var suite = fnoise.NewCipherSuite(fnoise.DH25519, fnoise.CipherChaChaPoly, fnoise.HashSHA256)

// Errors a handshake or channel fails with; the returned error wraps one of
// them and the underlying cause.
var (
	ErrKind      = errors.New("noise: unknown or unaccepted handshake kind")
	ErrHandshake = errors.New("noise: handshake failed")
	ErrRefused   = errors.New("noise: peer refused")
	ErrDecrypt   = errors.New("noise: message failed to decrypt")
	ErrFrame     = errors.New("noise: bad frame")
	ErrTooLarge  = errors.New("noise: frame too large")
)

// MsgConn carries whole messages in order. Read and write may be called
// from different goroutines; Close unblocks both.
type MsgConn interface {
	ReadMessage(ctx context.Context) ([]byte, error)
	WriteMessage(ctx context.Context, p []byte) error
	Close() error
}

// DialConfig is the initiator's side of a handshake.
type DialConfig struct {
	Identity Identity
	Kind     Kind
	// PeerStatic is the responder's public key; required for KindDaily and
	// KindPair.
	PeerStatic []byte
	// PSK is the pairing secret; KindPair only.
	PSK []byte
	// Payload rides in the first message; KindPair only, the display name.
	Payload []byte
	// MaxFrame bounds frames the channel reads; 0 means DefaultMaxFrame.
	MaxFrame int

	random io.Reader // tests make handshakes repeatable
}

// AcceptConfig is the responder's side of a handshake.
type AcceptConfig struct {
	// Identity is the responder's key; KindRelay responders have none.
	Identity Identity
	// Kinds lists the handshakes accepted; others fail with ErrKind.
	Kinds []Kind
	// PSK returns the live pairing secret for KindPair, nil when no
	// pairing is open.
	PSK func() []byte
	// Allow decides once the initiator's public key is known, before the
	// responder sends anything that completes the handshake; an error
	// refuses the connection.
	Allow func(kind Kind, peer, payload []byte) error
	// MaxFrame bounds frames the channel reads; 0 means DefaultMaxFrame.
	MaxFrame int

	random io.Reader
}

// Dial runs the initiator's handshake over c. On failure c is closed.
func Dial(ctx context.Context, c MsgConn, cfg DialConfig) (*Channel, error) {
	if err := cfg.check(); err != nil {
		_ = c.Close()
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	ch, err := dial(ctx, c, cfg)
	if err != nil {
		_ = c.Close()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", ErrHandshake, context.Cause(ctx))
		}
		return nil, err
	}
	return ch, nil
}

func (cfg DialConfig) check() error {
	if !cfg.Identity.Valid() {
		return errors.New("noise: dial without an identity")
	}
	switch cfg.Kind {
	case KindDaily, KindPair:
		if len(cfg.PeerStatic) != KeySize {
			return fmt.Errorf("noise: %s dial needs the host's %d-byte public key", cfg.Kind, KeySize)
		}
	case KindRelay:
	default:
		return fmt.Errorf("%w: %s", ErrKind, cfg.Kind)
	}
	if cfg.Kind == KindPair {
		if len(cfg.PSK) != PSKSize {
			return fmt.Errorf("noise: pairing secret is %d bytes, want %d", len(cfg.PSK), PSKSize)
		}
		if len(cfg.Payload) > MaxNameSize {
			return fmt.Errorf("noise: display name is %d bytes, at most %d", len(cfg.Payload), MaxNameSize)
		}
	} else if len(cfg.PSK) != 0 || len(cfg.Payload) != 0 {
		return fmt.Errorf("noise: %s dial takes no secret or payload", cfg.Kind)
	}
	return nil
}

func dial(ctx context.Context, c MsgConn, cfg DialConfig) (*Channel, error) {
	hs, err := fnoise.NewHandshakeState(config(cfg.Kind, true, cfg.Identity, cfg.PeerStatic, cfg.PSK, cfg.random))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshake, err)
	}
	msg, send, recv, err := hs.WriteMessage([]byte{byte(cfg.Kind)}, cfg.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshake, err)
	}
	for {
		if err := c.WriteMessage(ctx, msg); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrHandshake, err)
		}
		if send != nil {
			return newChannel(c, send, recv, hs.PeerStatic(), cfg.MaxFrame), nil
		}
		in, err := readHandshake(ctx, c)
		if err != nil {
			return nil, err
		}
		if _, send, recv, err = hs.ReadMessage(nil, in); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrHandshake, err)
		}
		if send != nil {
			return newChannel(c, send, recv, hs.PeerStatic(), cfg.MaxFrame), nil
		}
		if msg, send, recv, err = hs.WriteMessage(nil, nil); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrHandshake, err)
		}
	}
}

// Accept runs the responder's handshake over c and reports which kind the
// initiator opened with. On failure c is closed.
func Accept(ctx context.Context, c MsgConn, cfg AcceptConfig) (*Channel, Kind, error) {
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	ch, kind, err := accept(ctx, c, cfg)
	if err != nil {
		_ = c.Close()
		if ctx.Err() != nil {
			return nil, kind, fmt.Errorf("%w: %w", ErrHandshake, context.Cause(ctx))
		}
		return nil, kind, err
	}
	return ch, kind, nil
}

func accept(ctx context.Context, c MsgConn, cfg AcceptConfig) (*Channel, Kind, error) {
	first, err := readHandshake(ctx, c)
	if err != nil {
		return nil, 0, err
	}
	if len(first) == 0 {
		return nil, 0, fmt.Errorf("%w: empty first message", ErrHandshake)
	}
	kind := Kind(first[0])
	if !slices.Contains(cfg.Kinds, kind) {
		return nil, kind, fmt.Errorf("%w: %s", ErrKind, kind)
	}
	var psk []byte
	switch kind {
	case KindDaily, KindPair:
		if !cfg.Identity.Valid() {
			return nil, kind, fmt.Errorf("noise: accept %s without an identity", kind)
		}
		if kind == KindPair {
			if cfg.PSK != nil {
				psk = cfg.PSK()
			}
			if len(psk) != PSKSize {
				return nil, kind, fmt.Errorf("%w: no pairing is open", ErrRefused)
			}
		}
	case KindRelay:
	default:
		return nil, kind, fmt.Errorf("%w: %s", ErrKind, kind)
	}
	hs, err := fnoise.NewHandshakeState(config(kind, false, cfg.Identity, nil, psk, cfg.random))
	if err != nil {
		return nil, kind, fmt.Errorf("%w: %w", ErrHandshake, err)
	}
	in := first[1:]
	allowed := false
	for {
		payload, recv, send, err := hs.ReadMessage(nil, in)
		if err != nil {
			return nil, kind, fmt.Errorf("%w: %w", ErrHandshake, err)
		}
		if peer := hs.PeerStatic(); !allowed && len(peer) == KeySize {
			if len(payload) > MaxNameSize {
				return nil, kind, fmt.Errorf("%w: display name is %d bytes", ErrHandshake, len(payload))
			}
			if cfg.Allow != nil {
				if err := cfg.Allow(kind, peer, payload); err != nil {
					return nil, kind, fmt.Errorf("%w: %w", ErrRefused, err)
				}
			}
			allowed = true
		}
		if send != nil {
			return newChannel(c, send, recv, hs.PeerStatic(), cfg.MaxFrame), kind, nil
		}
		msg, recv, send, err := hs.WriteMessage(nil, nil)
		if err != nil {
			return nil, kind, fmt.Errorf("%w: %w", ErrHandshake, err)
		}
		if err := c.WriteMessage(ctx, msg); err != nil {
			return nil, kind, fmt.Errorf("%w: %w", ErrHandshake, err)
		}
		if send != nil {
			return newChannel(c, send, recv, hs.PeerStatic(), cfg.MaxFrame), kind, nil
		}
		if in, err = readHandshake(ctx, c); err != nil {
			return nil, kind, err
		}
	}
}

func readHandshake(ctx context.Context, c MsgConn) ([]byte, error) {
	in, err := c.ReadMessage(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshake, err)
	}
	if len(in) > maxMessage+1 { // the first message carries the kind byte too
		return nil, fmt.Errorf("%w: message of %d bytes", ErrHandshake, len(in))
	}
	return in, nil
}

func config(kind Kind, initiator bool, id Identity, peer, psk []byte, random io.Reader) fnoise.Config {
	cfg := fnoise.Config{
		CipherSuite: suite,
		Random:      random,
		Initiator:   initiator,
		Prologue:    append([]byte(prologue), byte(kind)),
		PeerStatic:  peer,
	}
	if id.Valid() && (initiator || kind != KindRelay) {
		cfg.StaticKeypair = fnoise.DHKey{Private: id.priv.Bytes(), Public: id.Public()}
	}
	switch kind {
	case KindDaily:
		cfg.Pattern = fnoise.HandshakeIK
	case KindPair:
		cfg.Pattern = fnoise.HandshakeIK
		cfg.PresharedKey = psk
		cfg.PresharedKeyPlacement = 1
	case KindRelay:
		cfg.Pattern = fnoise.HandshakeXN
	}
	return cfg
}
