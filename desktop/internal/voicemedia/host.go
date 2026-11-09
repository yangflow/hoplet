// Package voicemedia runs the official voice component for a call on this
// machine. The component does the WebRTC, echo cancellation and barge-in;
// this package starts it, speaks its framed protocol and keeps the order that
// protects the user: devices open muted, the microphone opens only after the
// host confirms the call, and hanging up silences audio before anything else.
//
// Session descriptions pass through here and are never logged or put in an
// error.
package voicemedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxFrame     = 128 * 1024
	maxCommit    = 128
	protocol     = 1
	startTimeout = 30 * time.Second
	linkTimeout  = 20 * time.Second
	localTimeout = 5 * time.Second
)

// inherited is everything the component gets from this process's
// environment. API keys and loader or plugin overrides never reach it.
var inherited = map[string]bool{
	"HOME": true, "TMPDIR": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	"REQUESTS_CA_BUNDLE": true, "CURL_CA_BUNDLE": true,
}

// hostEnv is the component's environment given this process's.
func hostEnv(environ []string) []string {
	env := []string{
		"GST_PLUGIN_PATH=", "GST_PLUGIN_PATH_1_0=",
		"GST_PLUGIN_SYSTEM_PATH=", "GST_PLUGIN_SYSTEM_PATH_1_0=",
		"GST_REGISTRY=/dev/null", "GST_REGISTRY_UPDATE=no", "GST_REGISTRY_FORK=no",
	}
	for _, kv := range environ {
		if k, _, ok := strings.Cut(kv, "="); ok && inherited[strings.ToUpper(k)] {
			env = append(env, kv)
		}
	}
	return env
}

// command runs the component from its own directory, which it needs to
// initialize.
func command(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // the managed Codex package's own component
	cmd.Dir = filepath.Dir(path)
	cmd.Env = hostEnv(os.Environ())
	return cmd
}

// audio is the component's audio switches.
type audio struct {
	MicrophoneMuted   bool `json:"microphoneMuted"`
	SpeakerSuppressed bool `json:"speakerSuppressed"`
}

// message is a request to the component. Only the fields its type uses are
// set.
type message struct {
	Type        string    `json:"type"`
	Protocol    int       `json:"protocol,omitempty"`
	BuildCommit string    `json:"buildCommit,omitempty"`
	SDP         string    `json:"sdp,omitempty"`
	Controls    *audio    `json:"controls,omitempty"`
	Selection   *struct{} `json:"selection,omitempty"`
}

// reply is a response from the component.
type reply struct {
	Type  string `json:"type"`
	SDP   string `json:"sdp"`
	State struct {
		MicrophonePeak uint16 `json:"microphonePeak"`
		SpeakerPeak    uint16 `json:"speakerPeak"`
	} `json:"state"`
}

// host is a running component. Requests run one at a time; any failed
// request leaves the pipe out of step, so it ends the process.
type host struct {
	cmd  *exec.Cmd
	in   *os.File
	out  *os.File
	turn chan struct{} // held for the length of a request
	once sync.Once
	done chan struct{} // closed once the process has been reaped
}

// open starts the component at path and brings its runtime up.
func open(ctx context.Context, path string) (*host, error) {
	commit, err := buildCommit(ctx, path)
	if err != nil {
		return nil, err
	}
	h, err := spawn(path)
	if err != nil {
		return nil, err
	}
	for _, step := range []struct {
		m    message
		want string
	}{
		{message{Type: "hello", Protocol: protocol, BuildCommit: commit}, "ready"},
		{message{Type: "initializeRuntime"}, "runtimeReady"},
	} {
		if _, err := h.expect(ctx, step.m, step.want, startTimeout); err != nil {
			h.close()
			return nil, err
		}
	}
	return h, nil
}

// buildCommit asks the component which build it is, which the handshake
// repeats back to it.
func buildCommit(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	b, err := command(ctx, path, "--build-commit").Output()
	if err != nil {
		return "", fmt.Errorf("voicemedia: read build: %w", err)
	}
	commit := strings.TrimSpace(string(b))
	if commit == "" || len(commit) > maxCommit {
		return "", errors.New("voicemedia: the component reported no usable build")
	}
	return commit, nil
}

func spawn(path string) (*host, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("voicemedia: pipe: %w", err)
	}
	defer func() { _ = inR.Close() }()
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inW.Close()
		return nil, fmt.Errorf("voicemedia: pipe: %w", err)
	}
	defer func() { _ = outW.Close() }()

	// The process must outlive whichever context opened it; close ends it.
	cmd := command(context.Background(), path)
	cmd.Stdin, cmd.Stdout = inR, outW
	if err := cmd.Start(); err != nil {
		_ = inW.Close()
		_ = outR.Close()
		return nil, fmt.Errorf("voicemedia: start: %w", err)
	}
	h := &host{cmd: cmd, in: inW, out: outR, turn: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		h.kill() // closes the pipes when the process ended on its own
		close(h.done)
	}()
	return h, nil
}

// kill ends the process and unblocks any request in flight. It does not wait.
func (h *host) kill() {
	h.once.Do(func() {
		_ = h.cmd.Process.Kill()
		_ = h.in.Close()
		_ = h.out.Close()
	})
}

// close ends the process and waits until it is gone.
func (h *host) close() {
	h.kill()
	<-h.done
}

// exchange sends one request and reads its reply within timeout.
func (h *host) exchange(ctx context.Context, m message, timeout time.Duration) (reply, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case h.turn <- struct{}{}:
		defer func() { <-h.turn }()
	case <-ctx.Done():
		return reply{}, fmt.Errorf("voicemedia: %s: %w", m.Type, ctx.Err())
	}

	// A request cut short leaves part of a frame in the pipe.
	stop := context.AfterFunc(ctx, h.kill)
	defer stop()
	r, err := h.roundTrip(m)
	if err != nil {
		h.kill()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return reply{}, fmt.Errorf("voicemedia: %s: %w", m.Type, err)
	}
	return r, nil
}

func (h *host) roundTrip(m message) (reply, error) {
	payload, err := json.Marshal(m)
	if err != nil {
		return reply{}, fmt.Errorf("encode: %w", err)
	}
	if err := writeFrame(h.in, payload); err != nil {
		return reply{}, err
	}
	b, err := readFrame(h.out)
	if err != nil {
		return reply{}, err
	}
	var r reply
	if err := json.Unmarshal(b, &r); err != nil {
		return reply{}, errors.New("malformed reply")
	}
	return r, nil
}

// expect is exchange for a request with one acceptable reply type.
func (h *host) expect(ctx context.Context, m message, want string, timeout time.Duration) (reply, error) {
	r, err := h.exchange(ctx, m, timeout)
	if err != nil {
		return reply{}, err
	}
	if r.Type != want {
		h.kill()
		return reply{}, fmt.Errorf("voicemedia: %s: expected %s", m.Type, want)
	}
	return r, nil
}

// offer starts the transport and returns its session description.
func (h *host) offer(ctx context.Context) (string, error) {
	r, err := h.expect(ctx, message{Type: "startTransport"}, "offer", linkTimeout)
	if err != nil {
		return "", err
	}
	if r.SDP == "" {
		h.kill()
		return "", errors.New("voicemedia: startTransport: empty offer")
	}
	return r.SDP, nil
}

// answer completes the transport and opens the devices silenced.
func (h *host) answer(ctx context.Context, sdp string) error {
	if _, err := h.expect(ctx, message{Type: "applyAnswer", SDP: sdp}, "transportReady", linkTimeout); err != nil {
		return err
	}
	if _, err := h.expect(ctx, message{Type: "openDevices", Selection: &struct{}{}}, "devicesOpened", localTimeout); err != nil {
		return fmt.Errorf("%w: %w", ErrMicrophone, err)
	}
	return h.set(ctx, audio{MicrophoneMuted: true, SpeakerSuppressed: true})
}

func (h *host) set(ctx context.Context, a audio) error {
	_, err := h.expect(ctx, message{Type: "setAudioControls", Controls: &a}, "audioControlsApplied", localTimeout)
	return err
}

// writeFrame writes one length-prefixed frame.
func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) > maxFrame {
		return errors.New("frame too large")
	}
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(payload))) //nolint:gosec // bounded above
	b.Write(payload)
	_, err := w.Write(b.Bytes())
	return err
}

// readFrame reads one length-prefixed frame.
func readFrame(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	if n == 0 || n > maxFrame {
		return nil, errors.New("bad frame length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}
