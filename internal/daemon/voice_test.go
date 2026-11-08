package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/internal/voice"
	"github.com/yangflow/hoplet/proto"
)

type testVoiceCall struct {
	once sync.Once
	done chan struct{}
}

func (c *testVoiceCall) Answer() string        { return "secret-answer" }
func (c *testVoiceCall) Done() <-chan struct{} { return c.done }
func (c *testVoiceCall) Err() error            { return nil }
func (c *testVoiceCall) Stop(context.Context) error {
	c.once.Do(func() { close(c.done) })
	return nil
}

type testVoice struct{ call *testVoiceCall }

func (*testVoice) Name() string                { return "test" }
func (*testVoice) Probe(context.Context) error { return nil }
func (v *testVoice) Start(context.Context, voice.StartRequest) (voice.Call, error) {
	return v.call, nil
}

// syncBuffer is a log sink the daemon's goroutines can share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestVoiceCallOverTheProtocol(t *testing.T) {
	h, script := newHome(t, fake.Script{})
	call := &testVoiceCall{done: make(chan struct{})}
	logs := &syncBuffer{}
	cfg := testConfig(h, script)
	cfg.Voice = &testVoice{call: call}
	cfg.Logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	start(t, cfg)
	c := dial(t, h, "desk")

	var probe proto.VoiceProbeResult
	if err := c.Call(timeout(t), proto.MethodVoiceProbe, struct{}{}, &probe); err != nil {
		t.Fatal(err)
	}
	if !probe.Available || probe.Provider != "test" {
		t.Fatalf("probe: %+v", probe)
	}
	var res proto.VoiceStartResult
	if err := c.Call(timeout(t), proto.MethodVoiceStart, proto.VoiceStartParams{Offer: "secret-offer"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Answer != "secret-answer" {
		t.Fatalf("start: %+v", res)
	}
	ref := proto.VoiceCallRef{Call: res.Call}
	for _, method := range []string{proto.MethodVoiceConfirm, proto.MethodVoiceHeartbeat, proto.MethodVoiceStop} {
		if err := c.Call(timeout(t), method, ref, nil); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	select {
	case <-call.done:
	case <-timeout(t).Done():
		t.Fatal("the call was not stopped")
	}
	if err := c.Call(timeout(t), proto.MethodVoiceHeartbeat, ref, nil); apiCode(err) != proto.CodeVoiceEnded {
		t.Fatalf("heartbeat after stop: %v", err)
	}
	if s := logs.String(); strings.Contains(s, "secret-offer") || strings.Contains(s, "secret-answer") {
		t.Fatalf("negotiation in logs: %s", s)
	}
}

func TestVoiceWithoutProviderIsAnAnswer(t *testing.T) {
	h, script := newHome(t, fake.Script{})
	start(t, testConfig(h, script))
	c := dial(t, h, "desk")
	var probe proto.VoiceProbeResult
	if err := c.Call(timeout(t), proto.MethodVoiceProbe, struct{}{}, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Available || probe.Reason != proto.CodeVoiceNoHarness {
		t.Fatalf("probe: %+v", probe)
	}
	err := c.Call(timeout(t), proto.MethodVoiceStart, proto.VoiceStartParams{Offer: "o"}, nil)
	if apiCode(err) != proto.CodeVoiceNoHarness {
		t.Fatalf("start: %v", err)
	}
}

func TestDaemonCloseEndsTheCall(t *testing.T) {
	h, script := newHome(t, fake.Script{})
	call := &testVoiceCall{done: make(chan struct{})}
	cfg := testConfig(h, script)
	cfg.Voice = &testVoice{call: call}
	d, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	o, err := client.HomeOptions(h, "desk")
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.Dial(timeout(t), o)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call(timeout(t), proto.MethodVoiceStart, proto.VoiceStartParams{Offer: "o"}, &proto.VoiceStartResult{}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-call.done:
	default:
		t.Fatal("the call outlived the daemon")
	}
}

func TestVoiceControllerRecordsTheCall(t *testing.T) {
	ctx := timeout(t)
	h, script := newHome(t, fake.Script{})
	d := start(t, testConfig(h, script))
	c := &voiceController{store: d.store, command: []string{"/bin/hoplet", "mcp"}, log: slog.New(slog.DiscardHandler)}

	begin := func() (*voiceControl, string) {
		ctl, err := c.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		v := ctl.(*voiceControl)
		_, token, _ := strings.Cut(v.tools[0].Env[0], "=")
		return v, token
	}

	silent, token := begin()
	if _, err := d.store.SessionByToken(ctx, store.TokenHash(token)); err != nil {
		t.Fatal(err)
	}
	silent.End()
	if s, err := d.store.Session(ctx, silent.id); err != nil || s.ArchivedAt == nil {
		t.Fatalf("a silent call stayed: %+v %v", s, err)
	}

	v, token := begin()
	v.Transcript(proto.RoleUser, "seg1", "hello")
	v.End()
	if _, err := d.store.SessionByToken(ctx, store.TokenHash(token)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the token outlived the call: %v", err)
	}
	s, err := d.store.Session(ctx, v.id)
	if err != nil || s.ArchivedAt != nil {
		t.Fatalf("session: %+v %v", s, err)
	}
	evs, err := d.store.Events(ctx, v.id, 0, 10)
	if err != nil || len(evs) != 1 {
		t.Fatalf("events: %+v %v", evs, err)
	}
	if ss, err := d.store.Sessions(ctx); err != nil || len(ss) != 0 {
		t.Fatalf("listed: %+v %v", ss, err)
	}
}

func TestVoiceSessionTakesNoMessages(t *testing.T) {
	ctx := timeout(t)
	h, script := newHome(t, fake.Script{})
	d := start(t, testConfig(h, script))
	c := &voiceController{store: d.store, command: []string{"x"}, log: slog.New(slog.DiscardHandler)}
	ctl, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.End()
	id := ctl.(*voiceControl).id
	cl := dial(t, h, "desk")
	var reply proto.SendReply
	err = cl.Call(ctx, proto.MethodSessionsSend, proto.SendParams{Session: id, Text: "hi", Key: "k"}, &reply)
	var api *proto.APIError
	if !errors.As(err, &api) || api.Code != proto.CodeInvalid {
		t.Fatalf("send: %v", err)
	}
}
