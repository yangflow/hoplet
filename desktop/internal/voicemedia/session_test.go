package voicemedia

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// daemon is a fake host with the checks a session adds to signal.
type daemon struct {
	*signal
	probe proto.VoiceProbeResult
	beats chan error // each heartbeat takes its result from here
}

func (d *daemon) Probe(context.Context) (proto.VoiceProbeResult, error) { return d.probe, nil }

func (d *daemon) Heartbeat(ctx context.Context, _ proto.VoiceCallID) error {
	select {
	case err := <-d.beats:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// watch collects a session's events on channels.
type watch struct {
	connected chan struct{}
	speaking  chan bool
	ended     chan error
}

func newWatch() *watch {
	return &watch{connected: make(chan struct{}, 1), speaking: make(chan bool, 16), ended: make(chan error, 1)}
}

func (w *watch) events() Events {
	return Events{
		Connected: func() { w.connected <- struct{}{} },
		Speaking:  func(b bool) { w.speaking <- b },
		Ended:     func(err error) { w.ended <- err },
	}
}

// never is a period no test waits for.
const never = time.Hour

func TestSessionRefusedByProbe(t *testing.T) {
	path := install(t, scenario{})
	w := newWatch()
	d := &daemon{signal: &signal{path: path}, probe: proto.VoiceProbeResult{Reason: proto.CodeVoiceBusy, BusyDevice: "macbook"}}
	s := run(path, d, w.events(), timing{never, never})
	<-s.Done()
	var api *proto.APIError
	if err := <-w.ended; !errors.As(err, &api) || api.Code != proto.CodeVoiceBusy || api.Message != "macbook" {
		t.Fatalf("ended with %v", err)
	}
	if len(w.connected) != 0 {
		t.Fatal("connected after a refused probe")
	}
}

func TestSessionHangupSilencesFirst(t *testing.T) {
	path := install(t, scenario{})
	w := newWatch()
	d := &daemon{signal: &signal{path: path}, probe: proto.VoiceProbeResult{Available: true}}
	s := run(path, d, w.events(), timing{never, never})
	<-w.connected
	s.Hangup()
	<-s.Done()
	if err := <-w.ended; err != nil {
		t.Fatalf("ended with %v", err)
	}
	want := slices.Concat(connected, []string{"audio mic-muted=true speaker-suppressed=true", "host.stop call-1"})
	if got := readLog(t, path); !slices.Equal(got, want) {
		t.Fatalf("order:\n got %q\nwant %q", got, want)
	}
	assertGone(t, path)
}

func TestSessionHeartbeatFailureEnds(t *testing.T) {
	path := install(t, scenario{})
	w := newWatch()
	d := &daemon{signal: &signal{path: path}, probe: proto.VoiceProbeResult{Available: true}, beats: make(chan error, 1)}
	gone := &proto.APIError{Code: proto.CodeVoiceEnded}
	s := run(path, d, w.events(), timing{time.Millisecond, never})
	<-w.connected
	d.beats <- gone
	<-s.Done()
	if err := <-w.ended; !errors.Is(err, gone) {
		t.Fatalf("ended with %v", err)
	}
	assertGone(t, path)
}

func TestSessionComponentExitEnds(t *testing.T) {
	path := install(t, scenario{DieOn: "inspectAudio"})
	w := newWatch()
	d := &daemon{signal: &signal{path: path}, probe: proto.VoiceProbeResult{Available: true}}
	s := run(path, d, w.events(), timing{never, time.Millisecond})
	<-s.Done()
	if err := <-w.ended; err == nil {
		t.Fatal("ended without a reason")
	}
	assertGone(t, path)
}

func TestSessionReportsSpeakingAndMute(t *testing.T) {
	path := install(t, scenario{SpeakerPeak: speakingPeak + 1})
	w := newWatch()
	d := &daemon{signal: &signal{path: path}, probe: proto.VoiceProbeResult{Available: true}}
	s := run(path, d, w.events(), timing{never, time.Millisecond})
	if !<-w.speaking {
		t.Fatal("first change is not speaking")
	}
	s.SetMuted(true)
	s.Hangup()
	<-s.Done()
	if err := <-w.ended; err != nil {
		t.Fatalf("ended with %v", err)
	}
}

func TestDialDeviceFailureIsMicrophone(t *testing.T) {
	path := install(t, scenario{WrongOn: "openDevices"})
	sig := &signal{path: path}
	if _, err := Dial(t.Context(), path, sig); !errors.Is(err, ErrMicrophone) {
		t.Fatalf("Dial = %v", err)
	}
	if sig.stops != 1 {
		t.Fatalf("%d stops", sig.stops)
	}
	assertGone(t, path)
}

func TestSetMuted(t *testing.T) {
	path := install(t, scenario{})
	c, err := Dial(t.Context(), path, &signal{path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Hangup(context.WithoutCancel(t.Context())) }()
	if err := c.SetMuted(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	got := readLog(t, path)
	if last := got[len(got)-1]; last != "audio mic-muted=true speaker-suppressed=false" {
		t.Fatalf("last = %q", last)
	}
}
