package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/yangflow/hoplet/desktop/internal/voicemedia"
	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/harness/codex"
	"github.com/yangflow/hoplet/proto"
)

const (
	voiceRPCTimeout = 30 * time.Second
	beatTimeout     = 5 * time.Second
	// consentFile marks that this device has agreed to voice calls.
	consentFile = "voice-consent"
)

// voiceDaemon carries a call's signalling to the daemon.
type voiceDaemon struct {
	c *client.Client
	// before is the previous call winding down, if any; the daemon would refuse
	// this one as busy until it has.
	before <-chan struct{}
}

func (v voiceDaemon) Probe(ctx context.Context) (proto.VoiceProbeResult, error) {
	var out proto.VoiceProbeResult
	if v.before != nil {
		select {
		case <-v.before:
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(ctx, voiceRPCTimeout)
	defer cancel()
	err := v.c.Call(ctx, proto.MethodVoiceProbe, struct{}{}, &out)
	return out, err
}

func (v voiceDaemon) Start(ctx context.Context, offer string) (proto.VoiceStartResult, error) {
	ctx, cancel := context.WithTimeout(ctx, voiceRPCTimeout)
	defer cancel()
	var out proto.VoiceStartResult
	err := v.c.Call(ctx, proto.MethodVoiceStart, proto.VoiceStartParams{Offer: offer}, &out)
	return out, err
}

func (v voiceDaemon) ref(ctx context.Context, method string, call proto.VoiceCallID, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return v.c.Call(ctx, method, proto.VoiceCallRef{Call: call}, nil)
}

func (v voiceDaemon) Confirm(ctx context.Context, call proto.VoiceCallID) error {
	return v.ref(ctx, proto.MethodVoiceConfirm, call, voiceRPCTimeout)
}

func (v voiceDaemon) Heartbeat(ctx context.Context, call proto.VoiceCallID) error {
	return v.ref(ctx, proto.MethodVoiceHeartbeat, call, beatTimeout)
}

func (v voiceDaemon) Stop(ctx context.Context, call proto.VoiceCallID) error {
	return v.ref(ctx, proto.MethodVoiceStop, call, voiceRPCTimeout)
}

// VoiceConsented implements shell.Host.
func (d *desktop) VoiceConsented() bool { return d.consented }

// ConsentVoice implements shell.Host.
func (d *desktop) ConsentVoice() {
	d.consented = true
	go func() {
		if err := os.MkdirAll(d.home, 0o700); err != nil {
			slog.Warn("voice consent", "err", err)
			return
		}
		if err := os.WriteFile(filepath.Join(d.home, consentFile), nil, 0o600); err != nil {
			slog.Warn("voice consent", "err", err)
		}
	}()
}

// readConsent reports whether this device agreed on an earlier run.
func readConsent(home string) bool {
	_, err := os.Stat(filepath.Join(home, consentFile))
	return err == nil
}

// StartCall implements shell.Host.
func (d *desktop) StartCall(ev voicemedia.Events) {
	c := d.client()
	if c == nil {
		ev.Ended(errNoDaemon)
		return
	}
	var before <-chan struct{}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.voice != nil {
		before = d.voice.Done()
	}
	d.voice = voicemedia.Run(codex.VoiceHostPath(d.home), voiceDaemon{c: c, before: before}, voicemedia.Events{
		Connected: func() { d.ui(ev.Connected) },
		Speaking:  func(b bool) { d.ui(func() { ev.Speaking(b) }) },
		Ended:     func(err error) { d.ui(func() { ev.Ended(err) }) },
	})
}

// MuteCall implements shell.Host.
func (d *desktop) MuteCall(muted bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.voice != nil {
		d.voice.SetMuted(muted)
	}
}

// HangUp implements shell.Host.
func (d *desktop) HangUp() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.voice != nil {
		d.voice.Hangup()
	}
}

// endCall hangs up and waits until no audio or component is left.
func (d *desktop) endCall() {
	d.mu.Lock()
	s := d.voice
	d.mu.Unlock()
	if s != nil {
		s.Hangup()
		<-s.Done()
	}
}
