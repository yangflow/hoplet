// Package voice runs the lifecycle of voice calls on a host: one call at a
// time, owned by the device that started it and ended when that device
// stops sending heartbeats. Media flows between the client and the voice
// service; only the negotiation passes through here, and it is never logged
// or stored.
package voice

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/yangflow/hoplet/proto"
)

const (
	// HeartbeatTimeout is how long a call lives without a heartbeat.
	HeartbeatTimeout = 15 * time.Second

	probeTimeout = 15 * time.Second
	startTimeout = 30 * time.Second
	stopTimeout  = 5 * time.Second
)

// StartRequest is what a provider needs to start a call.
type StartRequest struct {
	Offer string
	Voice string
}

// Provider is a voice service a host can use. Probe and Start report a
// failure the user can act on as a *proto.APIError with a voice error code;
// any other error counts as a media failure.
type Provider interface {
	// Name names the service for clients.
	Name() string
	Probe(ctx context.Context) error
	Start(ctx context.Context, req StartRequest) (Call, error)
}

// Call is a started call at a provider.
type Call interface {
	// Answer is the media answer for the client.
	Answer() string
	// Done is closed when the call ends on the provider's side.
	Done() <-chan struct{}
	// Err says why the call ended; valid once Done is closed.
	Err() error
	// Stop ends the call and releases what it holds.
	Stop(ctx context.Context) error
}

// active is the call a manager holds. Its call field is nil while the
// provider is starting it.
type active struct {
	id     proto.VoiceCallID
	device string
	call   Call
	beat   chan struct{} // wakes the watcher to extend the deadline
	gone   chan struct{} // closed when the call is over
}

// ended remembers why the latest call ended, for its owner's next request.
type ended struct {
	id     proto.VoiceCallID
	device string
	reason proto.ErrorCode
}

// Manager holds the host's voice call.
type Manager struct {
	provider Provider
	log      *slog.Logger

	mu     sync.Mutex
	cur    *active
	last   ended
	closed bool
	wg     sync.WaitGroup
}

// New returns a manager using provider; nil means the host has none.
func New(provider Provider, log *slog.Logger) *Manager {
	return &Manager{provider: provider, log: log}
}

// Probe says whether the host can take a call now. A failed check is an
// answer, not an error.
func (m *Manager) Probe(ctx context.Context) proto.VoiceProbeResult {
	if m.provider == nil {
		return proto.VoiceProbeResult{Reason: proto.CodeVoiceNoHarness}
	}
	res := proto.VoiceProbeResult{Provider: m.provider.Name()}
	m.mu.Lock()
	cur := m.cur
	m.mu.Unlock()
	if cur != nil {
		res.Reason, res.BusyDevice = proto.CodeVoiceBusy, cur.device
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if err := m.provider.Probe(ctx); err != nil {
		res.Reason = m.reason(err)
		return res
	}
	res.Available = true
	return res
}

// Start starts a call for device. All checks come before the call begins,
// and the host stays busy for other devices while it does.
func (m *Manager) Start(ctx context.Context, device string, p proto.VoiceStartParams) (proto.VoiceStartResult, error) {
	if p.Offer == "" {
		return proto.VoiceStartResult{}, &proto.APIError{Code: proto.CodeInvalid, Message: "a call needs a media offer"}
	}
	if m.provider == nil {
		return proto.VoiceStartResult{}, publicError(proto.CodeVoiceNoHarness)
	}
	c, err := m.reserve(device)
	if err != nil {
		return proto.VoiceStartResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	call, err := m.begin(ctx, p)
	if err != nil {
		m.release(c)
		code := m.reason(err)
		m.log.Info("voice call not started", "reason", code)
		return proto.VoiceStartResult{}, publicError(code)
	}
	if !m.attach(c, call) {
		m.stop(call)
		return proto.VoiceStartResult{}, publicError(proto.CodeVoiceEnded)
	}
	m.log.Info("voice call started", "call", c.id)
	return proto.VoiceStartResult{Call: c.id, Answer: call.Answer()}, nil
}

// Confirm tells the client it may open the microphone: the call is still
// alive on the host and belongs to device.
func (m *Manager) Confirm(device string, id proto.VoiceCallID) error {
	_, err := m.find(device, id)
	return err
}

// Heartbeat keeps the call alive.
func (m *Manager) Heartbeat(device string, id proto.VoiceCallID) error {
	c, err := m.find(device, id)
	if err != nil {
		return err
	}
	select {
	case c.beat <- struct{}{}:
	default: // a wake is already pending
	}
	return nil
}

// Stop ends the call. Stopping a call that already ended succeeds.
func (m *Manager) Stop(device string, id proto.VoiceCallID) error {
	c, err := m.find(device, id)
	if err != nil {
		var api *proto.APIError
		if errors.As(err, &api) && api.Code != proto.CodeNotFound {
			return nil
		}
		return err
	}
	m.finish(c, proto.CodeVoiceEnded)
	return nil
}

// Close ends the current call and refuses new ones. Calls do not outlive
// the daemon.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	c := m.cur
	m.mu.Unlock()
	if c != nil {
		m.finish(c, proto.CodeVoiceEnded)
	}
	m.wg.Wait()
}

func (m *Manager) reserve(device string) (*active, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, publicError(proto.CodeVoiceEnded)
	}
	if m.cur != nil {
		return nil, &proto.APIError{Code: proto.CodeVoiceBusy, Message: m.cur.device}
	}
	m.cur = &active{
		id: proto.VoiceCallID(rand.Text()), device: device,
		beat: make(chan struct{}, 1), gone: make(chan struct{}),
	}
	return m.cur, nil
}

func (m *Manager) begin(ctx context.Context, p proto.VoiceStartParams) (Call, error) {
	if err := m.provider.Probe(ctx); err != nil {
		return nil, err
	}
	return m.provider.Start(ctx, StartRequest{Offer: p.Offer, Voice: p.Voice})
}

// release gives up a reservation whose call never began.
func (m *Manager) release(c *active) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == c {
		m.cur = nil
		close(c.gone)
	}
}

// attach makes a begun call current and starts watching it; it reports false
// when the daemon closed meanwhile.
func (m *Manager) attach(c *active, call Call) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur != c {
		return false // Close already took the reservation
	}
	c.call = call
	m.wg.Go(func() { m.watch(c) })
	return true
}

// watch ends the call when its heartbeats stop or the provider ends it.
func (m *Manager) watch(c *active) {
	deadline := time.NewTimer(HeartbeatTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-c.beat:
			deadline.Reset(HeartbeatTimeout)
		case <-deadline.C:
			m.log.Info("voice call lost its client", "call", c.id)
			m.finish(c, proto.CodeVoiceEnded)
			return
		case <-c.call.Done():
			m.finish(c, m.endReason(c.call))
			return
		case <-c.gone:
			return
		}
	}
}

func (m *Manager) endReason(c Call) proto.ErrorCode {
	if err := c.Err(); err != nil {
		return m.reason(err)
	}
	return proto.CodeVoiceEnded
}

// find returns device's current call, or the error its next request gets.
func (m *Manager) find(device string, id proto.VoiceCallID) (*active, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur != nil && m.cur.id == id && m.cur.device == device && m.cur.call != nil {
		return m.cur, nil
	}
	if m.last.id == id && m.last.device == device && id != "" {
		return nil, publicError(m.last.reason)
	}
	return nil, &proto.APIError{Code: proto.CodeNotFound, Message: "no such voice call"}
}

// finish ends c once; later calls do nothing.
func (m *Manager) finish(c *active, reason proto.ErrorCode) {
	m.mu.Lock()
	if m.cur != c {
		m.mu.Unlock()
		return
	}
	m.cur = nil
	m.last = ended{id: c.id, device: c.device, reason: reason}
	call := c.call
	close(c.gone)
	m.mu.Unlock()
	m.log.Info("voice call ended", "call", c.id, "reason", reason)
	if call != nil {
		m.stop(call)
	}
}

func (m *Manager) stop(call Call) {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if err := call.Stop(ctx); err != nil {
		// The provider's text may quote the negotiation; it is not logged.
		m.log.Warn("voice call did not stop cleanly")
	}
}

// reason reduces err to a fixed code; the provider's own text never reaches
// clients or logs.
func (m *Manager) reason(err error) proto.ErrorCode {
	var api *proto.APIError
	if errors.As(err, &api) && api.Code.IsVoice() {
		return api.Code
	}
	return proto.CodeVoiceMedia
}

func publicError(code proto.ErrorCode) *proto.APIError {
	return &proto.APIError{Code: code, Message: "the voice call is unavailable"}
}
