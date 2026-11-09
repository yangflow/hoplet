package voicemedia

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/yangflow/hoplet/proto"
)

const (
	silenceTimeout = 2 * time.Second
	stopTimeout    = 5 * time.Second
)

// ErrMicrophone says the audio devices would not open, which on this
// platform means the microphone permission is missing.
var ErrMicrophone = errors.New("voicemedia: the audio devices did not open")

// Signal is the host side of a call.
type Signal interface {
	Start(ctx context.Context, offer string) (proto.VoiceStartResult, error)
	Confirm(ctx context.Context, call proto.VoiceCallID) error
	Stop(ctx context.Context, call proto.VoiceCallID) error
}

// Call is a connected call with its component running.
type Call struct {
	id     proto.VoiceCallID
	host   *host
	signal Signal

	once sync.Once
	err  error
}

// Dial runs the component at path and connects a call through signal. The
// microphone opens only after the host has confirmed the call. A missing
// component is a *proto.APIError with [proto.CodeVoiceNoComponent], the host's
// own refusals keep their codes, devices that will not open are
// [ErrMicrophone], and any other error is a media failure. When
// Dial fails, no process is left and no call stays open on the host.
func Dial(ctx context.Context, path string, signal Signal) (*Call, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, &proto.APIError{Code: proto.CodeVoiceNoComponent, Message: "the voice component is not installed"}
	}
	h, err := open(ctx, path)
	if err != nil {
		return nil, err
	}
	offer, err := h.offer(ctx)
	if err != nil {
		h.close()
		return nil, err
	}
	res, err := signal.Start(ctx, offer)
	if err != nil {
		h.close()
		return nil, fmt.Errorf("voicemedia: start call: %w", err)
	}
	c := &Call{id: res.Call, host: h, signal: signal}
	if err := c.connect(ctx, res.Answer); err != nil {
		_ = c.Hangup(context.WithoutCancel(ctx))
		return nil, err
	}
	return c, nil
}

func (c *Call) connect(ctx context.Context, answer string) error {
	if err := c.host.answer(ctx, answer); err != nil {
		return err
	}
	if err := c.signal.Confirm(ctx, c.id); err != nil {
		return fmt.Errorf("voicemedia: confirm call: %w", err)
	}
	return c.host.set(ctx, audio{})
}

// ID names the call at the host.
func (c *Call) ID() proto.VoiceCallID { return c.id }

// SetMuted mutes or unmutes the microphone.
func (c *Call) SetMuted(ctx context.Context, muted bool) error {
	return c.host.set(ctx, audio{MicrophoneMuted: muted})
}

// Done is closed when the component has exited, whether by Hangup or on its
// own. The call's owner still calls Hangup after an exit of its own.
func (c *Call) Done() <-chan struct{} { return c.host.done }

// Levels reads the current microphone and speaker peaks.
func (c *Call) Levels(ctx context.Context) (microphone, speaker uint16, err error) {
	r, err := c.host.expect(ctx, message{Type: "inspectAudio"}, "audioState", localTimeout)
	if err != nil {
		return 0, 0, err
	}
	return r.State.MicrophonePeak, r.State.SpeakerPeak, nil
}

// Hangup silences audio, tells the host, and waits for the component to be
// gone, even if the host cannot be reached. Later calls return the first
// result.
func (c *Call) Hangup(ctx context.Context) error {
	c.once.Do(func() {
		defer c.host.close()
		c.silence(ctx)
		stop, cancel := context.WithTimeout(ctx, stopTimeout)
		defer cancel()
		if err := c.signal.Stop(stop, c.id); err != nil {
			c.err = fmt.Errorf("voicemedia: stop call: %w", err)
		}
	})
	return c.err
}

// silence mutes both directions. A component too busy or too broken to do so
// is ended instead, which stops its audio just the same.
func (c *Call) silence(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, silenceTimeout)
	defer cancel()
	if err := c.host.set(ctx, audio{MicrophoneMuted: true, SpeakerSuppressed: true}); err != nil {
		c.host.kill()
	}
}
