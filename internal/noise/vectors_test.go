package noise

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	fnoise "github.com/flynn/noise"
)

// The vectors under testdata/vectors are replayed by the Swift client.
// TestVectors builds them from this package's real handshake and channel:
// with -update it writes them (go generate), otherwise it fails if the
// files differ, so they cannot drift from the implementation.
var updateVectors = flag.Bool("update", false, "rewrite testdata/vectors")

const vectorDir = "../../testdata/vectors"

type partyVector struct {
	StaticPrivate    string `json:"static_private"`
	StaticPublic     string `json:"static_public"`
	EphemeralPrivate string `json:"ephemeral_private"`
}

type stepVector struct {
	From     string   `json:"from"`
	Kind     string   `json:"kind"`
	Size     *int     `json:"size,omitempty"`
	Nonce    string   `json:"nonce,omitempty"`
	Messages []string `json:"messages"`
}

type sessionVector struct {
	Description string       `json:"description"`
	Pattern     string       `json:"pattern"`
	Kind        int          `json:"kind"`
	Prologue    string       `json:"prologue"`
	Initiator   partyVector  `json:"initiator"`
	Responder   partyVector  `json:"responder"`
	PSK         string       `json:"psk,omitempty"`
	Payload     string       `json:"payload,omitempty"`
	Messages    []string     `json:"messages"`
	Steps       []stepVector `json:"steps"`
}

type handshakeNegative struct {
	Name            string `json:"name"`
	Kind            int    `json:"kind"`
	InitiatorStatic string `json:"initiator_static_private"`
	InitiatorEph    string `json:"initiator_ephemeral_private"`
	ResponderStatic string `json:"responder_static_public"`
	PSK             string `json:"psk,omitempty"`
	FirstMessage    string `json:"first_message"`
	Response        string `json:"response"`
	Error           string `json:"error"`
}

type channelNegative struct {
	Name              string   `json:"name"`
	MaxFrame          int      `json:"max_frame,omitempty"`
	Messages          []string `json:"messages"`
	FramesBeforeError int      `json:"frames_before_error"`
	Error             string   `json:"error"`
}

type negativeVectors struct {
	Description string              `json:"description"`
	Handshake   []handshakeNegative `json:"handshake"`
	Channel     []channelNegative   `json:"channel"`
}

// seedReader is a repeatable random source that remembers what it produced.
type seedReader struct {
	label string
	block int
	buf   []byte
	got   []byte
}

func (r *seedReader) Read(p []byte) (int, error) {
	for len(r.buf) < len(p) {
		sum := sha256.Sum256(fmt.Appendf(nil, "hoplet vector random %s %d", r.label, r.block))
		r.block++
		r.buf = append(r.buf, sum[:]...)
	}
	copy(p, r.buf)
	r.buf = r.buf[len(p):]
	r.got = append(r.got, p...)
	return len(p), nil
}

func (r *seedReader) ephemeral() string { return hex.EncodeToString(r.got[:KeySize]) }

func vectorIdentity(t *testing.T, label string) Identity {
	t.Helper()
	sum := sha256.Sum256([]byte("hoplet vector identity " + label))
	id, err := ParseIdentity(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func vectorSecret(label string) []byte {
	sum := sha256.Sum256([]byte("hoplet vector secret " + label))
	return sum[:]
}

func framePattern(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(i % 251)
	}
	return p
}

// recorder captures the messages both sides write, in order.
type recorder struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recorder) capture(msg []byte) [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, hex.EncodeToString(msg))
	return [][]byte{msg}
}

func (r *recorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.msgs
	r.msgs = nil
	return out
}

type sessionSpec struct {
	kind     Kind
	psk      []byte
	payload  []byte
	maxFrame int
}

// session is a handshaken pair of channels over a recording pipe.
type session struct {
	t        *testing.T
	ctx      context.Context
	vector   sessionVector
	rec      *recorder
	ini, res *Channel
	iniEnd   *pipeEnd
	resEnd   *pipeEnd
}

func patternName(k Kind) string {
	if k == KindPair {
		return "Noise_IKpsk1_25519_ChaChaPoly_SHA256"
	}
	return "Noise_IK_25519_ChaChaPoly_SHA256"
}

func newSession(t *testing.T, spec sessionSpec) *session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	iniID, resID := vectorIdentity(t, "initiator"), vectorIdentity(t, "responder")
	iniRand, resRand := &seedReader{label: "initiator"}, &seedReader{label: "responder"}
	iniEnd, resEnd := newPipe()
	rec := &recorder{}
	iniEnd.setFilter(rec.capture)
	resEnd.setFilter(rec.capture)
	t.Cleanup(func() { _ = iniEnd.Close() })

	type dialed struct {
		ch  *Channel
		err error
	}
	done := make(chan dialed, 1)
	go func() {
		ch, err := Dial(ctx, iniEnd, DialConfig{
			Identity: iniID, Kind: spec.kind, PeerStatic: resID.Public(),
			PSK: spec.psk, Payload: spec.payload, MaxFrame: spec.maxFrame, random: iniRand,
		})
		done <- dialed{ch, err}
	}()
	res, _, err := Accept(ctx, resEnd, AcceptConfig{
		Identity: resID, Kinds: []Kind{spec.kind}, random: resRand,
		PSK: func() []byte { return spec.psk },
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	d := <-done
	if d.err != nil {
		t.Fatalf("dial: %v", d.err)
	}

	party := func(id Identity, r *seedReader) partyVector {
		return partyVector{
			StaticPrivate:    hex.EncodeToString(id.priv.Bytes()),
			StaticPublic:     hex.EncodeToString(id.Public()),
			EphemeralPrivate: r.ephemeral(),
		}
	}
	return &session{
		t: t, ctx: ctx, rec: rec, ini: d.ch, res: res, iniEnd: iniEnd, resEnd: resEnd,
		vector: sessionVector{
			Pattern:   patternName(spec.kind),
			Kind:      int(spec.kind),
			Prologue:  hex.EncodeToString(append([]byte(prologue), byte(spec.kind))),
			Initiator: party(iniID, iniRand),
			Responder: party(resID, resRand),
			PSK:       hex.EncodeToString(spec.psk),
			Payload:   hex.EncodeToString(spec.payload),
			Messages:  rec.take(),
		},
	}
}

// readAll delivers ch's frames until the channel ends.
func readAll(ctx context.Context, ch *Channel) <-chan []byte {
	out := make(chan []byte, 8)
	go func() {
		defer close(out)
		for {
			f, err := ch.ReadFrame(ctx)
			if err != nil {
				return
			}
			out <- f
		}
	}()
	return out
}

type stepRunner struct {
	s        *session
	iniFrame <-chan []byte
	resFrame <-chan []byte
}

func (s *session) runner() *stepRunner {
	return &stepRunner{s: s, iniFrame: readAll(s.ctx, s.ini), resFrame: readAll(s.ctx, s.res)}
}

func sides(r *stepRunner, from string) (sender *Channel, received <-chan []byte, other string) {
	if from == "initiator" {
		return r.s.ini, r.resFrame, "responder"
	}
	return r.s.res, r.iniFrame, "initiator"
}

func (r *stepRunner) frame(from string, size int) {
	t := r.s.t
	t.Helper()
	sender, received, _ := sides(r, from)
	want := framePattern(size)
	if err := sender.WriteFrame(r.s.ctx, want); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	select {
	case got := <-received:
		if !bytes.Equal(got, want) {
			t.Fatalf("frame of %d bytes came back different", size)
		}
	case <-r.s.ctx.Done():
		t.Fatal("frame never arrived")
	}
	n := size
	r.s.vector.Steps = append(r.s.vector.Steps, stepVector{From: from, Kind: "frame", Size: &n, Messages: r.s.rec.take()})
}

// ping sends a ping with a fixed nonce and waits for its pong.
func (r *stepRunner) ping(from string, nonce [nonceSize]byte) {
	t := r.s.t
	t.Helper()
	sender, _, other := sides(r, from)
	got := make(chan struct{})
	sender.pmu.Lock()
	sender.pings[nonce] = got
	sender.pmu.Unlock()
	if err := sender.writeControl(r.s.ctx, flagPing, nonce); err != nil {
		t.Fatalf("ping: %v", err)
	}
	select {
	case <-got:
	case <-r.s.ctx.Done():
		t.Fatal("pong never arrived")
	}
	msgs := r.s.rec.take()
	if len(msgs) != 2 {
		t.Fatalf("ping exchange wrote %d messages, want 2", len(msgs))
	}
	text := hex.EncodeToString(nonce[:])
	r.s.vector.Steps = append(r.s.vector.Steps,
		stepVector{From: from, Kind: "ping", Nonce: text, Messages: msgs[:1]},
		stepVector{From: other, Kind: "pong", Nonce: text, Messages: msgs[1:]},
	)
}

func buildSession(t *testing.T, spec sessionSpec, description string, script func(*stepRunner)) sessionVector {
	t.Helper()
	s := newSession(t, spec)
	r := s.runner()
	script(r)
	s.vector.Description = description
	return s.vector
}

func dailySpec() sessionSpec { return sessionSpec{kind: KindDaily} }

func pairSpec() sessionSpec {
	return sessionSpec{kind: KindPair, psk: vectorSecret("pairing"), payload: []byte("测试 iPhone")}
}

func writeOrCompare(t *testing.T, name string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join(vectorDir, name)
	if *updateVectors {
		if err := os.MkdirAll(vectorDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	have, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run make gen", err)
	}
	if !bytes.Equal(have, data) {
		t.Fatalf("%s differs from the implementation; run make gen", name)
	}
}

// rawHandshake runs the two handshake messages outside Dial and Accept, so
// that a response can be made from a deliberately wrong transcript.
func rawHandshake(t *testing.T, spec sessionSpec, iniLabel, suffix string) (first, second []byte) {
	t.Helper()
	iniID, resID := vectorIdentity(t, "initiator"), vectorIdentity(t, "responder")
	iniCfg := config(spec.kind, true, iniID, resID.Public(), spec.psk, &seedReader{label: iniLabel})
	resCfg := config(spec.kind, false, resID, nil, spec.psk, &seedReader{label: "responder"})
	iniCfg.Prologue = append(iniCfg.Prologue, suffix...)
	resCfg.Prologue = append(resCfg.Prologue, suffix...)
	ini, err := fnoise.NewHandshakeState(iniCfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := fnoise.NewHandshakeState(resCfg)
	if err != nil {
		t.Fatal(err)
	}
	first, _, _, err = ini.WriteMessage([]byte{byte(spec.kind)}, spec.payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = res.ReadMessage(nil, first[1:]); err != nil {
		t.Fatal(err)
	}
	second, _, _, err = res.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return first, second
}

// checkHandshakeNegative dials with the real implementation and answers
// with response, which must make the initiator fail.
func checkHandshakeNegative(t *testing.T, spec sessionSpec, response []byte) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ini, peer := newPipe()
	defer func() { _ = ini.Close() }()
	done := make(chan error, 1)
	go func() {
		_, err := Dial(ctx, ini, DialConfig{
			Identity: vectorIdentity(t, "initiator"), Kind: spec.kind,
			PeerStatic: vectorIdentity(t, "responder").Public(),
			PSK:        spec.psk, Payload: spec.payload, random: &seedReader{label: "initiator"},
		})
		done <- err
	}()
	first, err := peer.ReadMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.WriteMessage(ctx, response); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrHandshake) {
		t.Fatalf("dial = %v, want ErrHandshake", err)
	}
	return first
}

func buildHandshakeNegatives(t *testing.T) []handshakeNegative {
	t.Helper()
	wrongPSK := pairSpec()
	wrongPSK.psk = vectorSecret("another pairing")

	flipLast := func(m []byte) []byte {
		out := bytes.Clone(m)
		out[len(out)-1] ^= 1
		return out
	}
	_, good := rawHandshake(t, dailySpec(), "initiator", "")
	_, goodPair := rawHandshake(t, pairSpec(), "initiator", "")
	_, badPrologue := rawHandshake(t, dailySpec(), "initiator", "x")
	_, badPSK := rawHandshake(t, wrongPSK, "initiator", "")
	_, otherSession := rawHandshake(t, dailySpec(), "another initiator", "")

	cases := []struct {
		name     string
		spec     sessionSpec
		response []byte
	}{
		{"tampered_response", dailySpec(), flipLast(good)},
		{"truncated_response", dailySpec(), good[:40]},
		{"wrong_prologue", dailySpec(), badPrologue},
		{"response_from_another_session", dailySpec(), otherSession},
		{"wrong_psk", pairSpec(), badPSK},
		{"tampered_pair_response", pairSpec(), flipLast(goodPair)},
	}

	iniID, resID := vectorIdentity(t, "initiator"), vectorIdentity(t, "responder")
	eph := &seedReader{label: "initiator"}
	if _, err := eph.Read(make([]byte, KeySize)); err != nil {
		t.Fatal(err)
	}
	out := make([]handshakeNegative, 0, len(cases))
	for _, c := range cases {
		first := checkHandshakeNegative(t, c.spec, c.response)
		out = append(out, handshakeNegative{
			Name:            c.name,
			Kind:            int(c.spec.kind),
			InitiatorStatic: hex.EncodeToString(iniID.priv.Bytes()),
			InitiatorEph:    eph.ephemeral(),
			ResponderStatic: hex.EncodeToString(resID.Public()),
			PSK:             hex.EncodeToString(c.spec.psk),
			FirstMessage:    hex.EncodeToString(first),
			Response:        hex.EncodeToString(c.response),
			Error:           "handshake",
		})
	}
	return out
}

func buildChannelNegatives(t *testing.T) []channelNegative {
	t.Helper()
	type script struct {
		name     string
		maxFrame int
		plain    [][]byte // encrypted in order by the responder
		deliver  func(msgs [][]byte) [][]byte
		before   int
		want     error
		label    string
	}
	same := func(m [][]byte) [][]byte { return m }
	lastFrame := []byte{flagLast, 'h', 'i'}
	cases := []script{
		{name: "replay", plain: [][]byte{lastFrame}, deliver: func(m [][]byte) [][]byte { return [][]byte{m[0], m[0]} }, before: 1, want: ErrDecrypt, label: "decrypt"},
		{name: "reorder", plain: [][]byte{lastFrame, lastFrame}, deliver: func(m [][]byte) [][]byte { return [][]byte{m[1], m[0]} }, want: ErrDecrypt, label: "decrypt"},
		{name: "tamper", plain: [][]byte{lastFrame}, deliver: func(m [][]byte) [][]byte {
			out := bytes.Clone(m[0])
			out[0] ^= 1
			return [][]byte{out}
		}, want: ErrDecrypt, label: "decrypt"},
		{name: "unknown_flag", plain: [][]byte{{9, 'x'}}, deliver: same, want: ErrFrame, label: "frame"},
		{name: "empty_plaintext", plain: [][]byte{{}}, deliver: same, want: ErrFrame, label: "frame"},
		{name: "short_ping_nonce", plain: [][]byte{{flagPing, 1, 2, 3}}, deliver: same, want: ErrFrame, label: "frame"},
		{name: "over_frame_limit", maxFrame: 16, plain: [][]byte{append([]byte{flagMore}, framePattern(10)...), append([]byte{flagLast}, framePattern(10)...)}, deliver: same, want: ErrTooLarge, label: "too_large"},
	}

	out := make([]channelNegative, 0, len(cases))
	for _, c := range cases {
		spec := dailySpec()
		spec.maxFrame = c.maxFrame
		s := newSession(t, spec)
		msgs := make([][]byte, 0, len(c.plain))
		for _, p := range c.plain {
			ct, err := s.res.send.Encrypt(nil, nil, p)
			if err != nil {
				t.Fatal(err)
			}
			msgs = append(msgs, ct)
		}
		delivered := c.deliver(msgs)
		text := make([]string, 0, len(delivered))
		for _, m := range delivered {
			if err := s.resEnd.WriteMessage(s.ctx, m); err != nil {
				t.Fatal(err)
			}
			text = append(text, hex.EncodeToString(m))
		}
		frames := 0
		var err error
		for {
			if _, err = s.ini.ReadFrame(s.ctx); err != nil {
				break
			}
			frames++
		}
		if !errors.Is(err, c.want) || frames != c.before {
			t.Fatalf("%s: %d frames then %v, want %d then %v", c.name, frames, err, c.before, c.want)
		}
		out = append(out, channelNegative{Name: c.name, MaxFrame: c.maxFrame, Messages: text, FramesBeforeError: frames, Error: c.label})
	}
	return out
}

func TestVectors(t *testing.T) {
	smallExchange := func(r *stepRunner) {
		r.frame("initiator", 5)
		r.frame("responder", 5)
	}
	writeOrCompare(t, "noise-ik.json", buildSession(t, dailySpec(), "Daily handshake, then one frame each way.", smallExchange))
	writeOrCompare(t, "noise-ikpsk1.json", buildSession(t, pairSpec(), "Pairing handshake, then one frame each way.", smallExchange))
	writeOrCompare(t, "channel.json", buildSession(t, dailySpec(), "Channel framing after a daily handshake, in order.", func(r *stepRunner) {
		r.frame("initiator", 0)
		r.frame("initiator", 5)
		r.frame("initiator", maxPiece)
		r.frame("initiator", maxPiece+1)
		r.frame("responder", 5)
		r.frame("responder", maxPiece+1)
		r.ping("responder", [nonceSize]byte{1, 2, 3, 4, 5, 6, 7, 8})
		r.ping("initiator", [nonceSize]byte{8, 7, 6, 5, 4, 3, 2, 1})
		r.frame("initiator", 5)
		r.frame("responder", 5)
	}))
	writeOrCompare(t, "noise-negative.json", negativeVectors{
		Description: "Every case must fail. Channel cases read as the initiator after the handshake in channel.json.",
		Handshake:   buildHandshakeNegatives(t),
		Channel:     buildChannelNegatives(t),
	})
}
