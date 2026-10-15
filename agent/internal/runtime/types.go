// Package runtime runs vendor coding-agent processes for remote control (ADR 0008, ADR 0012): one process
// per controlled session, its lifecycle, and the detection of writes Hoplet did not make. A driver
// (runtime/<vendor>) speaks the vendor's streaming protocol; the Manager owns everything vendor independent.
package runtime

import (
	"context"
	"encoding/json"
)

// State is the runtime state of one session (docs/spec.md, runtime states). The core adds `offline`
// when the owner is not connected; the agent never reports it.
type State string

const (
	StateIdle              State = "idle"
	StateStarting          State = "starting"
	StateReady             State = "ready"
	StateRunning           State = "running"
	StateWaitingPermission State = "waiting-permission"
	StateFailed            State = "failed"
	StateExternal          State = "external"
)

// Capability is a control a driver supports (docs/architecture.md#a-runtime-capability).
type Capability string

const (
	CapPermissions Capability = "permissions"
	CapInterrupt   Capability = "interrupt"
	CapThinking    Capability = "thinking"
	CapFast        Capability = "fast"
	CapModels      Capability = "models"
	// CapImages: a prompt may carry images, which the driver passes to the vendor as image content.
	CapImages Capability = "images"
)

// Image is a picture that goes with a prompt: its media type (image/png, image/jpeg, image/gif or
// image/webp) and its bytes, checked before they reach a driver.
type Image struct {
	MediaType string
	Data      []byte
}

// Prompt is one user turn: its text, the images that go with it, and what it changes of the session's
// options from this turn on.
type Prompt struct {
	Text    string
	Images  []Image
	Options Change
}

// Change is what a prompt changes of a session's StartOptions; a zero field keeps the option. A driver applies
// only what it declares (models, thinking, fast).
type Change struct {
	Model    string
	Thinking *bool
	Fast     *bool
}

// StartOptions are what a new session can choose (docs/spec.md#new-session). A driver applies an option only
// when it declares the matching capability (thinking, fast, models); the zero value leaves the vendor's
// defaults.
type StartOptions struct {
	Model    string // one of Driver.Models()
	Thinking *bool  // nil leaves the vendor's default
	Fast     bool
}

// StartSpec says which session a driver starts and where.
type StartSpec struct {
	SessionID string
	Dir       string // the session's working directory
	// New starts the session under SessionID instead of resuming it; the vendor creates its file with the
	// first prompt.
	New     bool
	Options StartOptions
}

// Driver starts processes of one vendor.
type Driver interface {
	Name() string
	Capabilities() []Capability
	// Models lists the model IDs a new session can choose from; empty when the driver cannot choose.
	Models() []string
	// Start launches a process that resumes the session, or creates it when spec.New is set, and begins its
	// initialization; the process reports Ready when the vendor answered. A refused vendor version is an error.
	Start(ctx context.Context, spec StartSpec) (Process, error)
}

// Versioned is implemented by drivers that report the vendor version they found.
type Versioned interface {
	// Version is the installed vendor version and whether the driver was tested against it; an error means
	// the version cannot be read or is refused.
	Version(ctx context.Context) (version string, tested bool, err error)
}

// Process is one running vendor process. Methods are safe for concurrent use.
type Process interface {
	// Events delivers the process's events in order and is closed after the final Exited.
	Events() <-chan ProcessEvent
	Prompt(p Prompt) error
	// SetModel switches the model of the running process from the next turn on; a change of any other option
	// restarts the process.
	SetModel(model string) error
	// Answer responds to a permission request; message explains a denial to the model.
	Answer(requestID string, allow bool, message string) error
	// Interrupt ends the running turn and keeps the process.
	Interrupt() error
	// Close closes the process's input, which makes it exit.
	Close() error
	// Kill ends the process at once.
	Kill() error
}

// ProcessEvent is one of the types below.
type ProcessEvent interface{ processEvent() }

// Ready: the vendor answered the initialization.
type Ready struct{}

// StateChanged carries the vendor's own state, mapped to Running, WaitingPermission or Ready.
type StateChanged struct{ State State }

// OutputKind names what an Output carries.
type OutputKind string

const (
	OutputText       OutputKind = "text"        // Text is a delta
	OutputThinking   OutputKind = "thinking"    // Tokens is the number of tokens thought since the last event
	OutputToolCall   OutputKind = "tool-call"   // Tool, ToolUseID, Input
	OutputToolResult OutputKind = "tool-result" // ToolUseID, IsError, Text
)

// MaxOutputText bounds the Text of a tool result; longer content is cut and Truncated is set.
const MaxOutputText = 64 << 10

// Output is streamed content of the running turn. It is transient (docs/architecture.md#principles).
type Output struct {
	Kind      OutputKind
	Text      string
	Tokens    int
	Tool      string
	ToolUseID string
	Input     json.RawMessage
	IsError   bool
	Truncated bool
}

// PermissionRequest asks to use a tool.
type PermissionRequest struct {
	RequestID string
	Tool      string
	ToolUseID string
	Input     json.RawMessage
}

// PermissionCancelled: the vendor no longer waits for the request (the turn ended).
type PermissionCancelled struct{ RequestID string }

// TurnResult ends a turn. Failed is set when the vendor reports an error; Error then describes it.
type TurnResult struct {
	Failed bool
	Error  string
}

// Exited is the last event of a process. Err is nil for an exit with status 0.
type Exited struct{ Err error }

func (Ready) processEvent()               {}
func (StateChanged) processEvent()        {}
func (Output) processEvent()              {}
func (PermissionRequest) processEvent()   {}
func (PermissionCancelled) processEvent() {}
func (TurnResult) processEvent()          {}
func (Exited) processEvent()              {}
