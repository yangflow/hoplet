// Package harness defines how Hoplet drives an agent harness (Claude Code,
// Codex, Hermes, ...) through a structured protocol. Each adapter turns the
// harness's native messages into normalized proto payloads, keeping the raw
// line, and turns Hoplet commands into native messages.
//
// Rules every adapter follows:
//   - A turn ends only on the harness protocol's own signal; a harness that
//     exits mid-turn produces proto.TurnEnded{Reason: proto.TurnAborted}.
//   - The Hoplet session ID is the identity; the harness's own session ID is
//     reported through NativeSession and kept as a pointer.
//   - Approval options are the harness's native options, passed through.
package harness

import (
	"context"
	"errors"

	"github.com/yangflow/hoplet/proto"
)

// ErrUnsupported is returned by operations the harness's Capabilities lack.
var ErrUnsupported = errors.New("harness: operation not supported")

// Info describes an installed harness.
type Info struct {
	Kind         string
	Version      string
	Capabilities proto.Capabilities
}

// Spec is what a session needs from its harness.
type Spec struct {
	Session proto.SessionID
	WorkDir string
	Env     []string // base environment; adapters add only what they need
	Mode    proto.PermissionMode
	Model   string // empty: the harness default
	Resume  string // native session ID to resume; empty starts fresh
}

// Event is one normalized event. Payload is nil for lines that carry
// nothing Hoplet shows (they are still kept raw). Raw is the native line.
type Event struct {
	Payload proto.Payload
	Raw     []byte
}

// LineKind says where a stdio line came from.
type LineKind int

// Line kinds.
const (
	Stdout LineKind = iota
	Stderr
	Exit
)

// Line is one line of a child's output, or its exit.
type Line struct {
	Kind LineKind
	Data []byte
	Code int // exit code when Kind is Exit; -1 if unknown
}

// Stdio is a line-oriented connection to a harness process: in production
// a shim client, in tests a directly executed process.
type Stdio interface {
	WriteLine(line []byte) error
	Lines() <-chan Line // closed after the Exit line or when the connection ends
	Close() error
}

// Launch is how to start a stdio harness.
type Launch struct {
	Command []string
	Env     []string
}

// Harness is one kind of harness.
type Harness interface {
	Kind() string
	Probe(ctx context.Context) (Info, error)
}

// StdioHarness is a harness spoken to over a child process's stdio.
type StdioHarness interface {
	Harness
	Launch(spec Spec) (Launch, error)
	Attach(ctx context.Context, spec Spec, io Stdio) (Run, error)
}

// Run is a live harness session.
type Run interface {
	Send(ctx context.Context, text string) error
	Steer(ctx context.Context, text string) error
	Interrupt(ctx context.Context) error
	Respond(ctx context.Context, approvalID, optionID string) error
	Answer(ctx context.Context, questionID, answer string) error
	// Events delivers normalized events in order and is closed when the run
	// ends; NativeSession and Err are valid after it is closed.
	Events() <-chan Event
	NativeSession() string
	Err() error
	Close() error
}
