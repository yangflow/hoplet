package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/voice"
	"github.com/yangflow/hoplet/proto"
)

const (
	voiceOffer  = "v=0 offer-body"
	voiceAnswer = "v=0 answer-body"
)

// voiceServer scripts an app-server. The default answers describe a healthy
// ChatGPT login; on replaces one method.
type voiceServer struct {
	t      *testing.T
	in     *bufio.Reader
	out    io.Writer
	on     map[string]func(id json.RawMessage, params json.RawMessage)
	mu     sync.Mutex
	params map[string]json.RawMessage
	writeM sync.Mutex
}

func (s *voiceServer) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		s.t.Error(err)
		return
	}
	s.writeM.Lock()
	defer s.writeM.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
}

func (s *voiceServer) reply(id json.RawMessage, result any) {
	s.send(map[string]any{"id": id, "result": result})
}

func (s *voiceServer) refuse(id json.RawMessage, text string) {
	s.send(map[string]any{"id": id, "error": map[string]any{"code": -1, "message": text}})
}

func (s *voiceServer) note(method string, params any) {
	s.send(map[string]any{"method": method, "params": params})
}

func (s *voiceServer) saw(method string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.params[method]
}

func (s *voiceServer) serve() {
	for {
		line, err := s.in.ReadBytes('\n')
		if err != nil {
			return
		}
		var m message
		if json.Unmarshal(line, &m) != nil || len(m.ID) == 0 {
			continue
		}
		s.mu.Lock()
		s.params[m.Method] = m.Params
		s.mu.Unlock()
		if f := s.on[m.Method]; f != nil {
			f(m.ID, m.Params)
			continue
		}
		switch m.Method {
		case "account/read":
			s.reply(m.ID, map[string]any{"account": map[string]any{"type": "chatgpt"}})
		case "config/read":
			s.reply(m.ID, map[string]any{"config": map[string]any{}})
		case "thread/start":
			s.reply(m.ID, map[string]any{"thread": map[string]any{"id": "t1"}, "modelProvider": "openai"})
		case "thread/realtime/start":
			var p struct {
				RealtimeSessionID string `json:"realtimeSessionId"`
			}
			_ = json.Unmarshal(m.Params, &p)
			s.reply(m.ID, map[string]any{})
			// The answer arrives before the start confirmation on purpose.
			s.note("thread/realtime/sdp", map[string]any{"threadId": "t1", "sdp": voiceAnswer})
			s.note("thread/realtime/started", map[string]any{"threadId": "t1", "version": "v3", "realtimeSessionId": p.RealtimeSessionID})
		default:
			s.reply(m.ID, map[string]any{})
		}
	}
}

func newVoiceProvider(t *testing.T, on map[string]func(*voiceServer, json.RawMessage, json.RawMessage)) (*VoiceProvider, *voiceServer) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &voiceServer{t: t, in: bufio.NewReader(inR), out: outW, on: map[string]func(json.RawMessage, json.RawMessage){}, params: map[string]json.RawMessage{}}
	for k, f := range on {
		s.on[k] = func(id, p json.RawMessage) { f(s, id, p) }
	}
	go s.serve()
	stop := func() error {
		_ = outW.Close()
		_ = inR.Close()
		return nil
	}
	t.Cleanup(func() { _ = stop() })
	p := &VoiceProvider{open: func(context.Context) (*voiceProcess, error) {
		return &voiceProcess{in: inW, out: outR, stop: stop}, nil
	}}
	return p, s
}

func voiceCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func voiceCode(err error) proto.ErrorCode {
	var api *proto.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func TestVoiceProbe(t *testing.T) {
	account := func(v any) map[string]func(*voiceServer, json.RawMessage, json.RawMessage) {
		return map[string]func(*voiceServer, json.RawMessage, json.RawMessage){
			"account/read": func(s *voiceServer, id, _ json.RawMessage) { s.reply(id, map[string]any{"account": v}) },
		}
	}
	config := func(v map[string]any) map[string]func(*voiceServer, json.RawMessage, json.RawMessage) {
		return map[string]func(*voiceServer, json.RawMessage, json.RawMessage){
			"config/read": func(s *voiceServer, id, _ json.RawMessage) { s.reply(id, map[string]any{"config": v}) },
		}
	}
	tests := []struct {
		name string
		on   map[string]func(*voiceServer, json.RawMessage, json.RawMessage)
		want proto.ErrorCode
	}{
		{"chatgpt", nil, ""},
		{"api key", account(map[string]any{"type": "apikey"}), proto.CodeVoiceNotSubscribed},
		{"logged out", account(nil), proto.CodeVoiceNotSubscribed},
		{"other provider", config(map[string]any{"model_provider": "local"}), proto.CodeVoiceNotSubscribed},
		{"openai overridden", config(map[string]any{"model_providers": map[string]any{"openai": map[string]any{}}}), proto.CodeVoiceNotSubscribed},
		{"custom voice url", config(map[string]any{"experimental_realtime_webrtc_call_base_url": "x"}), proto.CodeVoiceNotSubscribed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newVoiceProvider(t, tt.on)
			if got := voiceCode(p.Probe(voiceCtx(t))); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVoiceProbeFindsWhatIsMissing(t *testing.T) {
	dir := t.TempDir()
	p := &VoiceProvider{Binary: filepath.Join(dir, "bin", "codex"), Env: env.Static{}}
	if got := voiceCode(p.Probe(voiceCtx(t))); got != proto.CodeVoiceNoHarness {
		t.Fatalf("no binary: %q", got)
	}
	if err := os.MkdirAll(filepath.Dir(p.Binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Binary, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := voiceCode(p.Probe(voiceCtx(t))); got != proto.CodeVoiceNoComponent {
		t.Fatalf("no component: %q", got)
	}
}

func TestVoiceStart(t *testing.T) {
	p, s := newVoiceProvider(t, nil)
	call, err := p.Start(voiceCtx(t), voice.StartRequest{Offer: voiceOffer, Voice: "juniper"})
	if err != nil {
		t.Fatal(err)
	}
	if call.Answer() != voiceAnswer {
		t.Fatalf("answer %q", call.Answer())
	}
	var got struct {
		ThreadID  string `json:"threadId"`
		Transport struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		} `json:"transport"`
		Version  string `json:"version"`
		Output   string `json:"outputModality"`
		Voice    string `json:"voice"`
		Handoffs *bool  `json:"clientManagedHandoffs"`
	}
	if err := json.Unmarshal(s.saw("thread/realtime/start"), &got); err != nil {
		t.Fatal(err)
	}
	if got.ThreadID != "t1" || got.Transport.Type != "webrtc" || got.Transport.SDP != voiceOffer || got.Version != "v3" ||
		got.Output != "audio" || got.Voice != "juniper" || got.Handoffs == nil || *got.Handoffs {
		t.Fatalf("start params: %+v", got)
	}
	var th threadParams
	if err := json.Unmarshal(s.saw("thread/start"), &th); err != nil {
		t.Fatal(err)
	}
	if th.Sandbox != "read-only" || th.ApprovalPolicy != "never" || !th.Ephemeral {
		t.Fatalf("thread params: %+v", th)
	}
	if entries, err := os.ReadDir(th.Cwd); err != nil || len(entries) != 0 {
		t.Fatalf("working directory: %v %v", entries, err)
	}
	if err := call.Stop(voiceCtx(t)); err != nil {
		t.Fatal(err)
	}
	if s.saw("thread/realtime/stop") == nil {
		t.Fatal("the call was not stopped at the service")
	}
	if _, err := os.Stat(th.Cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("working directory kept: %v", err)
	}
	if err := call.Err(); err != nil {
		t.Fatalf("a stopped call has no failure: %v", err)
	}
}

func TestVoiceStartFailures(t *testing.T) {
	notify := func(text string) map[string]func(*voiceServer, json.RawMessage, json.RawMessage) {
		return map[string]func(*voiceServer, json.RawMessage, json.RawMessage){
			"thread/realtime/start": func(s *voiceServer, id, _ json.RawMessage) {
				s.reply(id, map[string]any{})
				s.note("thread/realtime/error", map[string]any{"message": text})
			},
		}
	}
	tests := []struct {
		name string
		on   map[string]func(*voiceServer, json.RawMessage, json.RawMessage)
		want proto.ErrorCode
	}{
		{"not enabled", notify(`unexpected status 404 Not Found: {"detail":"Not Found"}, url: https://example.test/calls`), proto.CodeVoiceNotEnabled},
		{"rate limited", notify("unexpected status 429 Too Many Requests"), proto.CodeVoiceQuota},
		{"quota text", notify("You reached your usage limit"), proto.CodeVoiceQuota},
		{"other", notify("something with " + voiceOffer), proto.CodeVoiceMedia},
		{"thread on another service", map[string]func(*voiceServer, json.RawMessage, json.RawMessage){
			"thread/start": func(s *voiceServer, id, _ json.RawMessage) {
				s.reply(id, map[string]any{"thread": map[string]any{"id": "t1"}, "modelProvider": "local"})
			},
		}, proto.CodeVoiceNotSubscribed},
		{"refused request", map[string]func(*voiceServer, json.RawMessage, json.RawMessage){
			"thread/realtime/start": func(s *voiceServer, id, _ json.RawMessage) { s.refuse(id, "bad "+voiceOffer) },
		}, proto.CodeVoiceMedia},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newVoiceProvider(t, tt.on)
			_, err := p.Start(voiceCtx(t), voice.StartRequest{Offer: voiceOffer})
			if voiceCode(err) != tt.want {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
			if strings.Contains(err.Error(), voiceOffer) {
				t.Fatalf("the offer leaked: %v", err)
			}
		})
	}
}

func TestVoiceCallEndedByTheService(t *testing.T) {
	tests := []struct {
		name string
		end  func(*voiceServer)
		want proto.ErrorCode
	}{
		{"closed", func(s *voiceServer) { s.note("thread/realtime/closed", map[string]any{"threadId": "t1"}) }, ""},
		{"error", func(s *voiceServer) { s.note("thread/realtime/error", map[string]any{"message": "usage limit"}) }, proto.CodeVoiceQuota},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, s := newVoiceProvider(t, nil)
			call, err := p.Start(voiceCtx(t), voice.StartRequest{Offer: voiceOffer})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = call.Stop(voiceCtx(t)) }()
			tt.end(s)
			select {
			case <-call.Done():
			case <-voiceCtx(t).Done():
				t.Fatal("the call did not end")
			}
			if voiceCode(call.Err()) != tt.want {
				t.Fatalf("got %v", call.Err())
			}
		})
	}
}
