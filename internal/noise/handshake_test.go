package noise

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"runtime"
	"testing"
)

type side struct {
	ch   *Channel
	kind Kind
	err  error
}

// run dials and accepts over one pipe and returns both results.
func run(t *testing.T, d DialConfig, a AcceptConfig, setup func(dialer, acceptor *pipeEnd)) (dial, acc side) {
	t.Helper()
	dp, ap := newPipe()
	if setup != nil {
		setup(dp, ap)
	}
	ctx := t.Context()
	done := make(chan side, 1)
	go func() {
		ch, kind, err := Accept(ctx, ap, a)
		done <- side{ch, kind, err}
	}()
	ch, err := Dial(ctx, dp, d)
	dial = side{ch: ch, kind: d.Kind, err: err}
	acc = <-done
	return dial, acc
}

func mustIdentity(t *testing.T) Identity {
	t.Helper()
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

var testPSK = bytes.Repeat([]byte{7}, PSKSize)

func allKinds() []Kind { return []Kind{KindDaily, KindPair, KindRelay} }

// configs returns a matching dial and accept configuration for kind.
func configs(t *testing.T, kind Kind) (DialConfig, AcceptConfig, Identity, Identity) {
	t.Helper()
	dev, host := mustIdentity(t), mustIdentity(t)
	d := DialConfig{Identity: dev, Kind: kind}
	a := AcceptConfig{Kinds: allKinds(), PSK: func() []byte { return testPSK }}
	if kind != KindRelay {
		d.PeerStatic = host.Public()
		a.Identity = host
	}
	if kind == KindPair {
		d.PSK = testPSK
		d.Payload = []byte("laptop")
	}
	return d, a, dev, host
}

// checkKeys sends one message each way with the raw cipher states.
func checkKeys(t *testing.T, x, y *Channel) {
	t.Helper()
	for _, pair := range [][2]*Channel{{x, y}, {y, x}} {
		ct, err := pair[0].send.Encrypt(nil, nil, []byte("hi"))
		if err != nil {
			t.Fatal(err)
		}
		pt, err := pair[1].recv.Decrypt(nil, nil, ct)
		if err != nil || string(pt) != "hi" {
			t.Fatalf("keys do not match: %q %v", pt, err)
		}
	}
}

func TestHandshakeRoundTrip(t *testing.T) {
	for _, kind := range allKinds() {
		t.Run(kind.String(), func(t *testing.T) {
			d, a, dev, host := configs(t, kind)
			var gotKind Kind
			var gotPeer, gotPayload []byte
			a.Allow = func(k Kind, peer, payload []byte) error {
				gotKind, gotPeer, gotPayload = k, peer, payload
				return nil
			}
			dial, acc := run(t, d, a, nil)
			if dial.err != nil || acc.err != nil {
				t.Fatalf("dial %v, accept %v", dial.err, acc.err)
			}
			if acc.kind != kind || gotKind != kind {
				t.Fatalf("kind %v / %v", acc.kind, gotKind)
			}
			if !bytes.Equal(gotPeer, dev.Public()) || !bytes.Equal(acc.ch.Peer(), dev.Public()) {
				t.Fatal("responder did not learn the initiator's key")
			}
			if !bytes.Equal(gotPayload, d.Payload) {
				t.Fatalf("payload %q", gotPayload)
			}
			wantPeer := host.Public()
			if kind == KindRelay {
				wantPeer = nil
			}
			if !bytes.Equal(dial.ch.Peer(), wantPeer) {
				t.Fatalf("initiator peer %x", dial.ch.Peer())
			}
			checkKeys(t, dial.ch, acc.ch)
		})
	}
}

func TestHandshakeKind(t *testing.T) {
	d, a, _, _ := configs(t, KindPair)
	a.Kinds = []Kind{KindDaily}
	_, acc := run(t, d, a, nil)
	if !errors.Is(acc.err, ErrKind) || acc.kind != KindPair {
		t.Fatalf("unaccepted kind: %v %v", acc.kind, acc.err)
	}

	for _, first := range [][]byte{{9, 1, 2, 3}, {}} {
		dp, ap := newPipe()
		if err := dp.WriteMessage(t.Context(), first); err != nil {
			t.Fatal(err)
		}
		_, _, err := Accept(t.Context(), ap, AcceptConfig{Identity: mustIdentity(t), Kinds: allKinds()})
		if err == nil {
			t.Fatalf("first message %v accepted", first)
		}
		if len(first) > 0 && !errors.Is(err, ErrKind) {
			t.Fatalf("unknown kind: %v", err)
		}
		if len(dp.in) != 0 || !ap.closed() {
			t.Fatal("responder answered or kept the connection")
		}
	}

	dp, _ := newPipe()
	if _, err := Dial(t.Context(), dp, DialConfig{Identity: mustIdentity(t), Kind: 9}); !errors.Is(err, ErrKind) {
		t.Fatalf("dial unknown kind: %v", err)
	}
}

func TestHandshakeRefusals(t *testing.T) {
	refuse := errors.New("not paired")
	tests := []struct {
		name   string
		kind   Kind
		change func(d *DialConfig, a *AcceptConfig)
		want   error // from Accept
	}{
		{"daily wrong host key", KindDaily, func(d *DialConfig, _ *AcceptConfig) { d.PeerStatic = mustIdentity(t).Public() }, ErrHandshake},
		{"daily refused", KindDaily, func(_ *DialConfig, a *AcceptConfig) {
			a.Allow = func(Kind, []byte, []byte) error { return refuse }
		}, refuse},
		{"pair wrong secret", KindPair, func(d *DialConfig, _ *AcceptConfig) { d.PSK = bytes.Repeat([]byte{8}, PSKSize) }, ErrHandshake},
		{"pair none open", KindPair, func(_ *DialConfig, a *AcceptConfig) { a.PSK = func() []byte { return nil } }, ErrRefused},
		{"pair no PSK func", KindPair, func(_ *DialConfig, a *AcceptConfig) { a.PSK = nil }, ErrRefused},
		{"relay refused", KindRelay, func(_ *DialConfig, a *AcceptConfig) {
			a.Allow = func(Kind, []byte, []byte) error { return refuse }
		}, refuse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, a, _, _ := configs(t, tt.kind)
			allowed := false
			if a.Allow == nil {
				a.Allow = func(Kind, []byte, []byte) error { allowed = true; return nil }
			}
			tt.change(&d, &a)
			dial, acc := run(t, d, a, nil)
			if !errors.Is(acc.err, tt.want) {
				t.Fatalf("accept: %v, want %v", acc.err, tt.want)
			}
			if errors.Is(tt.want, refuse) && !errors.Is(acc.err, ErrRefused) {
				t.Fatalf("refusal not wrapped in ErrRefused: %v", acc.err)
			}
			// An XN initiator is done once it sends its last message; it
			// learns of a refusal only when the connection closes.
			if dial.err == nil && (tt.kind != KindRelay || !dial.ch.c.(*pipeEnd).closed()) {
				t.Fatal("initiator finished a refused handshake")
			}
			if allowed && !errors.Is(tt.want, refuse) {
				t.Fatal("Allow ran before the handshake was verified")
			}
		})
	}
}

func TestHandshakeRefusalReachesInitiator(t *testing.T) {
	refuse := func(err error) func(Kind, []byte, []byte) error {
		return func(Kind, []byte, []byte) error { return err }
	}
	internal := errors.New("store unavailable")
	tests := []struct {
		name   string
		kind   Kind
		change func(d *DialConfig, a *AcceptConfig)
		want   error
	}{
		{"device refused", KindDaily, func(_ *DialConfig, a *AcceptConfig) { a.Allow = refuse(ErrDeviceRefused) }, ErrDeviceRefused},
		{"daily internal error", KindDaily, func(_ *DialConfig, a *AcceptConfig) { a.Allow = refuse(internal) }, ErrHandshake},
		{"no pairing open", KindPair, func(_ *DialConfig, a *AcceptConfig) { a.PSK = func() []byte { return nil } }, ErrPairingRefused},
		{"wrong secret", KindPair, func(d *DialConfig, _ *AcceptConfig) { d.PSK = bytes.Repeat([]byte{8}, PSKSize) }, ErrPairingRefused},
		{"code used", KindPair, func(_ *DialConfig, a *AcceptConfig) { a.Allow = refuse(ErrPairingRefused) }, ErrPairingRefused},
		{"pair internal error", KindPair, func(_ *DialConfig, a *AcceptConfig) { a.Allow = refuse(internal) }, ErrHandshake},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, a, _, _ := configs(t, tt.kind)
			tt.change(&d, &a)
			dial, acc := run(t, d, a, nil)
			if !errors.Is(dial.err, tt.want) {
				t.Fatalf("dial: %v, want %v", dial.err, tt.want)
			}
			if acc.err == nil || acc.ch != nil {
				t.Fatalf("accept served a refused handshake: %v", acc.err)
			}
		})
	}
}

func TestDialChecksConfig(t *testing.T) {
	id, host := mustIdentity(t), mustIdentity(t).Public()
	tests := []struct {
		name string
		cfg  DialConfig
	}{
		{"no identity", DialConfig{Kind: KindDaily, PeerStatic: host}},
		{"daily without host key", DialConfig{Identity: id, Kind: KindDaily}},
		{"daily with payload", DialConfig{Identity: id, Kind: KindDaily, PeerStatic: host, Payload: []byte("x")}},
		{"pair short secret", DialConfig{Identity: id, Kind: KindPair, PeerStatic: host, PSK: []byte{1}}},
		{"pair long name", DialConfig{Identity: id, Kind: KindPair, PeerStatic: host, PSK: testPSK, Payload: bytes.Repeat([]byte("n"), MaxNameSize+1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp, _ := newPipe()
			if _, err := Dial(t.Context(), dp, tt.cfg); err == nil {
				t.Fatal("dial accepted the configuration")
			}
			if len(dp.out) != 0 || !dp.closed() {
				t.Fatal("dial sent a message or kept the connection")
			}
		})
	}
}

func TestHandshakeTampering(t *testing.T) {
	alter := map[string]func([]byte) [][]byte{
		"kind daily to pair": func(m []byte) [][]byte {
			if m[0] == byte(KindDaily) {
				m[0] = byte(KindPair)
			}
			return [][]byte{m}
		},
		"flip last byte": func(m []byte) [][]byte { m[len(m)-1] ^= 1; return [][]byte{m} },
		"flip ephemeral": func(m []byte) [][]byte { m[5] ^= 1; return [][]byte{m} },
		"truncate":       func(m []byte) [][]byte { return [][]byte{m[:len(m)-1]} },
		"oversize":       func(m []byte) [][]byte { return [][]byte{append(m, make([]byte, maxMessage)...)} },
	}
	for name, f := range alter {
		for _, kind := range allKinds() {
			if name == "kind daily to pair" && kind != KindDaily {
				continue
			}
			t.Run(name+"/"+kind.String(), func(t *testing.T) {
				d, a, _, _ := configs(t, kind)
				dial, acc := run(t, d, a, func(dp, _ *pipeEnd) {
					first := true
					dp.setFilter(func(m []byte) [][]byte {
						if !first {
							return [][]byte{m}
						}
						first = false
						return f(m)
					})
				})
				// XN's first message has no payload to authenticate: an altered
				// ephemeral key surfaces when the initiator's last message fails.
				if acc.err == nil && dial.err == nil {
					checkKeysFail(t, dial.ch, acc.ch)
					return
				}
				if acc.err == nil {
					t.Fatalf("accepted a tampered handshake (dial: %v)", dial.err)
				}
				if !errors.Is(acc.err, ErrHandshake) && !errors.Is(acc.err, ErrKind) {
					t.Fatalf("accept: %v", acc.err)
				}
			})
		}
	}
}

// checkKeysFail fails unless the two channels' keys disagree.
func checkKeysFail(t *testing.T, x, y *Channel) {
	t.Helper()
	ct, err := x.send.Encrypt(nil, nil, []byte("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.recv.Decrypt(nil, nil, ct); err == nil {
		t.Fatal("a tampered handshake produced matching keys")
	}
}

func TestHandshakeContext(t *testing.T) {
	before := runtime.NumGoroutine()
	for range 20 {
		d, a, _, _ := configs(t, KindDaily)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		dp, ap := newPipe()
		if _, err := Dial(ctx, dp, d); !errors.Is(err, context.Canceled) || !dp.closed() {
			t.Fatalf("dial with a cancelled context: %v", err)
		}
		ap2, _ := newPipe()
		if _, _, err := Accept(ctx, ap2, a); !errors.Is(err, context.Canceled) || !ap2.closed() {
			t.Fatalf("accept with a cancelled context: %v", err)
		}
		_ = ap
	}
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines %d → %d", before, after)
	}
}

// counterReader is a repeatable stand-in for crypto/rand.
type counterReader struct {
	seed byte
	n    uint64
	buf  []byte
}

func (r *counterReader) Read(p []byte) (int, error) {
	for len(r.buf) < len(p) {
		var block [9]byte
		block[0] = r.seed
		binary.BigEndian.PutUint64(block[1:], r.n)
		r.n++
		sum := sha256.Sum256(block[:])
		r.buf = append(r.buf, sum[:]...)
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func TestHandshakeRepeatable(t *testing.T) {
	for _, kind := range allKinds() {
		t.Run(kind.String(), func(t *testing.T) {
			d, a, _, _ := configs(t, kind)
			var runs [2][][]byte
			for i := range runs {
				d.random = &counterReader{seed: 1}
				a.random = &counterReader{seed: 2}
				dial, acc := run(t, d, a, func(dp, ap *pipeEnd) {
					rec := func(m []byte) [][]byte {
						runs[i] = append(runs[i], append([]byte(nil), m...))
						return [][]byte{m}
					}
					dp.setFilter(rec)
					ap.setFilter(rec)
				})
				if dial.err != nil || acc.err != nil {
					t.Fatal(dial.err, acc.err)
				}
			}
			if len(runs[0]) == 0 || len(runs[0]) != len(runs[1]) {
				t.Fatalf("message counts %d, %d", len(runs[0]), len(runs[1]))
			}
			for i := range runs[0] {
				if !bytes.Equal(runs[0][i], runs[1][i]) {
					t.Fatalf("message %d differs", i)
				}
			}
		})
	}
}
