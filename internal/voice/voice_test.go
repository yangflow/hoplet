package voice

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yangflow/hoplet/proto"
)

const (
	offerText  = "v=0 secret-offer"
	answerText = "v=0 secret-answer"
)

type fakeCall struct {
	done    chan struct{}
	endOnce sync.Once
	err     error

	mu    sync.Mutex
	stops int
}

func newFakeCall() *fakeCall { return &fakeCall{done: make(chan struct{})} }

func (c *fakeCall) Answer() string        { return answerText }
func (c *fakeCall) Done() <-chan struct{} { return c.done }
func (c *fakeCall) Err() error            { return c.err }
func (c *fakeCall) Stop(context.Context) error {
	c.mu.Lock()
	c.stops++
	c.mu.Unlock()
	c.end(nil)
	return nil
}
func (c *fakeCall) end(err error) {
	c.endOnce.Do(func() {
		c.err = err
		close(c.done)
	})
}
func (c *fakeCall) stopped() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stops
}

type fakeProvider struct {
	probeErr error
	startErr error
	call     *fakeCall
	req      StartRequest
	block    chan struct{} // when set, Start waits for it
}

func (p *fakeProvider) Name() string                { return "fake" }
func (p *fakeProvider) Probe(context.Context) error { return p.probeErr }
func (p *fakeProvider) Start(_ context.Context, req StartRequest) (Call, error) {
	p.req = req
	if p.probeErr != nil {
		return nil, p.probeErr
	}
	if p.block != nil {
		<-p.block
	}
	if p.startErr != nil {
		return nil, p.startErr
	}
	return p.call, nil
}

func newManager(p Provider) (*Manager, *bytes.Buffer) {
	var logs bytes.Buffer
	return New(p, slog.New(slog.NewTextHandler(&logs, nil))), &logs
}

func code(err error) proto.ErrorCode {
	var api *proto.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func TestProbe(t *testing.T) {
	tests := []struct {
		name string
		p    Provider
		want proto.VoiceProbeResult
	}{
		{"no provider", nil, proto.VoiceProbeResult{Reason: proto.CodeVoiceNoHarness}},
		{"ready", &fakeProvider{}, proto.VoiceProbeResult{Provider: "fake", Available: true}},
		{
			"not subscribed", &fakeProvider{probeErr: &proto.APIError{Code: proto.CodeVoiceNotSubscribed}},
			proto.VoiceProbeResult{Provider: "fake", Reason: proto.CodeVoiceNotSubscribed},
		},
		{
			"raw failure", &fakeProvider{probeErr: errors.New("boom")},
			proto.VoiceProbeResult{Provider: "fake", Reason: proto.CodeVoiceMedia},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newManager(tt.p)
			defer m.Close()
			if got := m.Probe(context.Background()); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestStartReportsOnlyFixedCodes(t *testing.T) {
	tests := []struct {
		name  string
		p     *fakeProvider
		want  proto.ErrorCode
		param proto.VoiceStartParams
	}{
		{"probe fails first", &fakeProvider{probeErr: &proto.APIError{Code: proto.CodeVoiceNoComponent}}, proto.CodeVoiceNoComponent, proto.VoiceStartParams{Offer: offerText}},
		{"quota", &fakeProvider{startErr: &proto.APIError{Code: proto.CodeVoiceQuota}}, proto.CodeVoiceQuota, proto.VoiceStartParams{Offer: offerText}},
		{"raw error", &fakeProvider{startErr: errors.New("service said " + offerText)}, proto.CodeVoiceMedia, proto.VoiceStartParams{Offer: offerText}},
		{"foreign code", &fakeProvider{startErr: &proto.APIError{Code: proto.CodeInternal}}, proto.CodeVoiceMedia, proto.VoiceStartParams{Offer: offerText}},
		{"no offer", &fakeProvider{}, proto.CodeInvalid, proto.VoiceStartParams{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, logs := newManager(tt.p)
			defer m.Close()
			_, err := m.Start(context.Background(), "d1", tt.param)
			if code(err) != tt.want {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
			if strings.Contains(err.Error(), offerText) || strings.Contains(logs.String(), offerText) {
				t.Fatalf("the offer leaked: %v / %s", err, logs)
			}
			if got := m.Probe(context.Background()); got.Reason == proto.CodeVoiceBusy {
				t.Fatal("a failed start left the host busy")
			}
		})
	}
}

func TestCallStartsAndStopsWithoutLeakingNegotiation(t *testing.T) {
	p := &fakeProvider{call: newFakeCall()}
	m, logs := newManager(p)
	defer m.Close()
	res, err := m.Start(context.Background(), "d1", proto.VoiceStartParams{Offer: offerText, Voice: "juniper"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != answerText || res.Call == "" || p.req.Offer != offerText || p.req.Voice != "juniper" {
		t.Fatalf("res %+v req %+v", res, p.req)
	}
	if err := m.Confirm("d1", res.Call); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop("d1", res.Call); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop("d1", res.Call); err != nil {
		t.Fatalf("a repeated stop must succeed: %v", err)
	}
	if p.call.stopped() != 1 {
		t.Fatalf("provider stopped %d times", p.call.stopped())
	}
	if err := m.Confirm("d1", res.Call); code(err) != proto.CodeVoiceEnded {
		t.Fatalf("confirm after stop: %v", err)
	}
	if s := logs.String(); strings.Contains(s, offerText) || strings.Contains(s, answerText) {
		t.Fatalf("negotiation in logs: %s", s)
	}
}

func TestOneCallPerHostOwnedByItsDevice(t *testing.T) {
	p := &fakeProvider{call: newFakeCall()}
	m, _ := newManager(p)
	defer m.Close()
	res, err := m.Start(context.Background(), "d1", proto.VoiceStartParams{Offer: offerText})
	if err != nil {
		t.Fatal(err)
	}
	var api *proto.APIError
	_, err = m.Start(context.Background(), "d2", proto.VoiceStartParams{Offer: offerText})
	if !errors.As(err, &api) || api.Code != proto.CodeVoiceBusy || api.Message != "d1" {
		t.Fatalf("second call: %v", err)
	}
	if got := m.Probe(context.Background()); got.Reason != proto.CodeVoiceBusy || got.BusyDevice != "d1" {
		t.Fatalf("probe: %+v", got)
	}
	for name, op := range map[string]func(string, proto.VoiceCallID) error{"confirm": m.Confirm, "heartbeat": m.Heartbeat, "stop": m.Stop} {
		if err := op("d2", res.Call); code(err) != proto.CodeNotFound {
			t.Fatalf("%s from another device: %v", name, err)
		}
	}
	if p.call.stopped() != 0 {
		t.Fatal("another device ended the call")
	}
}

func TestStartingCallKeepsHostBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProvider{call: newFakeCall(), block: make(chan struct{})}
		m, _ := newManager(p)
		defer m.Close()
		go func() { _, _ = m.Start(context.Background(), "d1", proto.VoiceStartParams{Offer: offerText}) }()
		synctest.Wait()
		if _, err := m.Start(context.Background(), "d2", proto.VoiceStartParams{Offer: offerText}); code(err) != proto.CodeVoiceBusy {
			t.Fatalf("got %v", err)
		}
		close(p.block)
		synctest.Wait()
	})
}

func TestHeartbeatKeepsTheCallAndSilenceEndsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProvider{call: newFakeCall()}
		m, _ := newManager(p)
		defer m.Close()
		res, err := m.Start(context.Background(), "d1", proto.VoiceStartParams{Offer: offerText})
		if err != nil {
			t.Fatal(err)
		}
		for range 6 {
			time.Sleep(5 * time.Second)
			if err := m.Heartbeat("d1", res.Call); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
		}
		if p.call.stopped() != 0 {
			t.Fatal("a heartbeating call ended")
		}
		time.Sleep(HeartbeatTimeout + time.Second)
		synctest.Wait()
		if p.call.stopped() != 1 {
			t.Fatal("a silent call kept running")
		}
		if err := m.Heartbeat("d1", res.Call); code(err) != proto.CodeVoiceEnded {
			t.Fatalf("heartbeat after timeout: %v", err)
		}
		if got := m.Probe(context.Background()); !got.Available {
			t.Fatalf("host still busy: %+v", got)
		}
	})
}

func TestProviderEndingTheCallIsReported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProvider{call: newFakeCall()}
		m, _ := newManager(p)
		defer m.Close()
		res, err := m.Start(context.Background(), "d1", proto.VoiceStartParams{Offer: offerText})
		if err != nil {
			t.Fatal(err)
		}
		p.call.end(&proto.APIError{Code: proto.CodeVoiceQuota})
		synctest.Wait()
		if err := m.Heartbeat("d1", res.Call); code(err) != proto.CodeVoiceQuota {
			t.Fatalf("got %v", err)
		}
	})
}

func TestCloseEndsTheCallAndRefusesNewOnes(t *testing.T) {
	p := &fakeProvider{call: newFakeCall()}
	m, _ := newManager(p)
	if _, err := m.Start(context.Background(), "d1", proto.VoiceStartParams{Offer: offerText}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	if p.call.stopped() != 1 {
		t.Fatal("close left the call running")
	}
	if _, err := m.Start(context.Background(), "d1", proto.VoiceStartParams{Offer: offerText}); code(err) != proto.CodeVoiceEnded {
		t.Fatalf("got %v", err)
	}
}

func TestCloseDuringStartStopsTheCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProvider{call: newFakeCall(), block: make(chan struct{})}
		m, _ := newManager(p)
		errc := make(chan error, 1)
		go func() {
			_, err := m.Start(context.Background(), "d1", proto.VoiceStartParams{Offer: offerText})
			errc <- err
		}()
		synctest.Wait()
		m.Close()
		close(p.block)
		if err := <-errc; code(err) != proto.CodeVoiceEnded {
			t.Fatalf("got %v", err)
		}
		if p.call.stopped() != 1 {
			t.Fatal("the late call kept running")
		}
	})
}
