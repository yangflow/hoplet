package voicemedia

import (
	"context"
	"errors"
	"time"

	"github.com/yangflow/hoplet/proto"
)

const (
	heartbeatEvery = 5 * time.Second
	levelsEvery    = 250 * time.Millisecond
	// speakingPeak is the speaker peak above which the far side counts as
	// talking.
	speakingPeak = 1000
	// quietPolls is how many quiet level readings in a row end speaking, so
	// the pauses between words do not count.
	quietPolls = 3
)

// Daemon is the host a session runs against.
type Daemon interface {
	Signal
	Probe(ctx context.Context) (proto.VoiceProbeResult, error)
	Heartbeat(ctx context.Context, call proto.VoiceCallID) error
}

// Events are what a session reports, each from its own goroutine.
type Events struct {
	Connected func()
	Speaking  func(bool)
	// Ended follows last, once: nil after Hangup, otherwise why the call
	// could not start or could not go on.
	Ended func(error)
}

// Session is one call from its first check to its end.
type Session struct {
	cancel context.CancelFunc
	muted  chan bool
	done   chan struct{}
}

type timing struct{ heartbeat, levels time.Duration }

// Run starts a call with the component at path. It does not block.
func Run(path string, d Daemon, ev Events) *Session {
	return run(path, d, ev, timing{heartbeatEvery, levelsEvery})
}

func run(path string, d Daemon, ev Events, t timing) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{cancel: cancel, muted: make(chan bool, 1), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		defer cancel()
		err := s.call(ctx, path, d, ev, t)
		if ctx.Err() != nil {
			err = nil
		}
		ev.Ended(err)
	}()
	return s
}

// SetMuted asks for the microphone to be muted or not. It does not block;
// a newer request replaces one not yet applied.
func (s *Session) SetMuted(muted bool) {
	select {
	case <-s.muted:
	default:
	}
	s.muted <- muted
}

// Hangup ends the call, at whatever stage it is. It does not block.
func (s *Session) Hangup() { s.cancel() }

// Done is closed when the call is over and its component gone.
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) call(ctx context.Context, path string, d Daemon, ev Events, t timing) error {
	probe, err := d.Probe(ctx)
	if err != nil {
		return err
	}
	if !probe.Available {
		return &proto.APIError{Code: probe.Reason, Message: probe.BusyDevice}
	}
	c, err := Dial(ctx, path, d)
	if err != nil {
		return err
	}
	defer func() { _ = c.Hangup(context.WithoutCancel(ctx)) }()
	ev.Connected()

	beat, levels := time.NewTicker(t.heartbeat), time.NewTicker(t.levels)
	defer beat.Stop()
	defer levels.Stop()
	speaking, quiet := false, 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.Done():
			return errors.New("voicemedia: the component exited")
		case <-beat.C:
			if err := d.Heartbeat(ctx, c.ID()); err != nil {
				return err
			}
		case m := <-s.muted:
			// A failure here ended the component, which Done reports.
			_ = c.SetMuted(ctx, m)
		case <-levels.C:
			_, peak, err := c.Levels(ctx)
			if err != nil {
				continue // as above
			}
			if peak > speakingPeak {
				quiet = 0
			} else {
				quiet++
			}
			if now := quiet < quietPolls; now != speaking {
				speaking = now
				ev.Speaking(now)
			}
		}
	}
}
