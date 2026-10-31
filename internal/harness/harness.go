// Package harness defines how Hoplet drives an agent harness (Claude Code,
// Codex, Hermes, ...) through a structured protocol. Each adapter turns the
// harness's native messages into normalized proto payloads, keeping the raw
// line, and turns Hoplet commands into native messages.
//
// Rules every adapter follows:
//   - A turn ends only on the harness protocol's own signal; a harness that
//     exits mid-turn produces proto.TurnEnded{Reason: proto.TurnAborted}.
//   - The Hoplet session ID is the identity; the harness's own session ID is
//     reported through Event.Native and NativeSession and kept as a pointer.
//   - Approval options are the harness's native options, passed through.
//
// Conventions the daemon follows on top of the adapters:
//   - The daemon numbers turns when it sees TurnStarted.
//   - When a turn ends interrupted or aborted, the store expires the
//     approvals still pending in it.
//   - When Events closes or an Exit event arrives while the store still has
//     an open turn, the daemon itself records TurnEnded{aborted}: the store,
//     not the adapter, is the source of truth for turn state.
package harness

import (
	"context"
	"errors"

	"github.com/yangflow/hoplet/proto"
)

var (
	// ErrUnsupported is returned for operations the harness's Capabilities
	// lack, and by Launch, Attach and Start when Spec.Resume is set but the
	// harness cannot resume.
	ErrUnsupported = errors.New("harness: operation not supported")

	// ErrDetached is Run.Err when the transport ended without the harness
	// exiting: the harness may still be running and the daemon can reattach.
	ErrDetached = errors.New("harness: transport ended without an exit")
)

// Info describes an installed harness.
type Info struct {
	Version      string
	Capabilities proto.Capabilities
	Models       []string
	// Supported is false when Version is outside the verified range. The
	// daemon then keeps existing sessions read-only and starts no new turns.
	Supported bool
	// Notice is something about the installed harness worth showing in
	// doctor (an automatic upgrade, say); Attention marks it as a problem.
	Notice    string
	Attention bool
}

// State is the resume state the daemon reads from its store when it attaches
// to a session that is already under way.
type State struct {
	InTurn bool   // the store has an open turn
	Native string // the harness's own session ID, if already known
}

// MCPServer is an MCP server Hoplet injects into a harness session.
type MCPServer struct {
	Name  string
	URL   string
	Token string
}

// Spec is what a session needs from its harness.
//
// MCP injection lands in a later PR: MCP is reserved and adapters ignore it
// for now. Ephemeral marks an isolated one-shot side task (no tools, no
// persistence).
type Spec struct {
	Session proto.SessionID
	WorkDir string
	// Env is the complete child environment; nil means empty, never
	// "inherit".
	Env  []string
	Mode proto.PermissionMode
	// Plan starts the session in plan mode, with Mode as the permission mode
	// to restore when plan mode ends; Fast in fast mode; Effort is the
	// thinking effort, empty for the harness default. Mode is one of the
	// harness's Capabilities.Modes, or confirm when it declares none.
	Plan      bool
	Fast      bool
	Effort    proto.Effort
	Model     string // empty: the harness default
	Resume    string // native session ID to resume; empty starts fresh
	State     State
	MCP       []MCPServer
	Ephemeral bool
}

// Event is one normalized event.
//
// Every Line a Stdio delivers yields at least one Event (its Payload may be
// nil), carrying that Line's Offset (a Position line yields an Event with
// nothing but the Offset), so the daemon can commit offsets along
// with events and acknowledge the exit record. Raw is the native line; the
// one exception to "every event carries its raw line" is a synthetic event
// (such as an aborted TurnEnded), which carries the triggering line's Offset
// and no Raw.
type Event struct {
	Payload proto.Payload // nil: the line carries nothing Hoplet shows
	Raw     []byte
	Stderr  bool        // Raw came from the harness's stderr
	Offset  int64       // transport position just after the triggering line
	Native  string      // set where the harness announces its own session ID
	Exit    *ExitStatus // set on the final event when the harness process ended
	// StdinError reports that a line written to the harness's stdin failed;
	// the Payload says why.
	StdinError bool
}

// LineKind says where a stdio line came from.
type LineKind int

// Line kinds.
const (
	Stdout LineKind = iota
	Stderr
	Stdin      // a line written to the harness's stdin, as recorded by the transport
	StdinError // a stdin write that failed to reach the harness; Data says why
	Exit
	// Position carries only a transport position (Offset), no data: a record
	// the harness adapter has no use for, such as a shim's start record. The
	// daemon still needs its offset to know it has caught up.
	Position
)

// MaxLine is the longest Line.Data a Stdio delivers; longer lines arrive as
// consecutive Lines of at most MaxLine bytes.
const MaxLine = 16 << 20

// Line is one line of a child's output or input, or its exit.
type Line struct {
	Kind LineKind
	Data []byte
	// Offset is the transport position just after this line (a shim's spool
	// byte offset); 0 for transports without one.
	Offset int64
	// Code and Signal describe an Exit. Signal uses the protocol names TERM,
	// KILL, INT and HUP, else "SIG<n>". Code -1 means unknown.
	Code   int
	Signal string
}

// ExitStatus is how a harness process ended.
type ExitStatus struct {
	Code   int
	Signal string
}

// Stdio is a line-oriented connection to a harness process: in production
// a shim client, in tests a directly executed process.
type Stdio interface {
	// WriteLine returning nil means the transport accepted the line, not
	// that the harness received it; delivery failures arrive later as
	// StdinError lines.
	WriteLine(ctx context.Context, line []byte) error
	// Lines is closed after the Exit line or when the connection ends.
	Lines() <-chan Line
	// Signal sends INT, TERM, HUP or KILL to the harness's process group.
	Signal(name string) error
	// Close releases the transport. With a transport that allows it (a shim)
	// the harness keeps running; a directly executed process cannot outlive
	// its owner, so there Close terminates it.
	Close() error
}

// Launch is how to start a stdio harness.
type Launch struct {
	Command []string
	Env     []string
	Dir     string
}

// Harness is one kind of harness. The daemon calls Probe often (every
// session creation and setting change), so adapters cache its result when
// probing is expensive.
type Harness interface {
	Kind() string
	Probe(ctx context.Context) (Info, error)
}

// StdioHarness is a harness spoken to over a child process's stdio. ctx in
// Attach covers only the attaching.
type StdioHarness interface {
	Harness
	Launch(spec Spec) (Launch, error)
	Attach(ctx context.Context, spec Spec, io Stdio) (Run, error)
}

// DirectHarness is a harness with its own server (Hermes). Reconnection
// uses Spec.Resume.
type DirectHarness interface {
	Harness
	Start(ctx context.Context, spec Spec) (Run, error)
}

// Run is a live harness session.
//
// Events must be read continuously: adapters never drop events, so a slow
// reader backpressures the transport. After Events closes, Err is nil when
// the harness ended (see the Exit event), ErrDetached when the transport
// ended without an exit (reattach), and any other error is an adapter
// failure. Close stops translating, releases the transport (see
// Stdio.Close) and returns only after the adapter's goroutine has exited,
// even if nobody reads Events.
type Run interface {
	Send(ctx context.Context, text string) error
	Steer(ctx context.Context, text string) error
	Interrupt(ctx context.Context) error
	Respond(ctx context.Context, approvalID, optionID string) error
	Answer(ctx context.Context, questionID, answer string) error
	// SetMode changes the permission mode. While plan mode is on it only
	// changes the mode the adapter restores when plan mode ends: it never
	// takes the harness out of plan mode. Leaving plan mode is SetPlan(false)
	// or the harness's own decision, reported as proto.ModeChanged.
	SetMode(ctx context.Context, mode proto.PermissionMode) error
	// SetPlan turns plan mode on or off; turning it off restores the
	// session's permission mode, which the adapter remembers from Spec.Mode
	// and SetMode.
	SetPlan(ctx context.Context, on bool) error
	SetEffort(ctx context.Context, effort proto.Effort) error
	SetFast(ctx context.Context, on bool) error
	SetModel(ctx context.Context, model string) error
	Events() <-chan Event
	NativeSession() string
	Err() error
	Close() error
}
