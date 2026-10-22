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
	// CapImages: a prompt may carry images, which the driver passes to the vendor as image content.
	CapImages Capability = "images"
	// CapSteer: a message written during a turn reaches the model at the turn's next tool gap (ADR 0020).
	CapSteer Capability = "steer"
	// CapRulesPromptsOnly: the permission rules act only on the approval requests the vendor sends, because the
	// driver has no hook that checks each tool call (ADR 0029, ADR 0032). Absent, the rules check every call.
	CapRulesPromptsOnly Capability = "rules-prompts-only"
)

// Image is a picture that goes with a prompt: its media type (image/png, image/jpeg, image/gif or
// image/webp) and its bytes, checked before they reach a driver.
type Image struct {
	MediaType string
	Data      []byte
}

// Prompt is one user turn: its text and the images that go with it.
type Prompt struct {
	Text   string
	Images []Image
}

// ConfigType is the kind of a setting (ADR 0018).
type ConfigType string

const (
	ConfigSelect  ConfigType = "select"  // one of Options; the value is a string
	ConfigBoolean ConfigType = "boolean" // the value is a bool
)

// Apply says when a change of a setting takes effect on a session that has a process; a session without one
// starts its next process with the value.
type Apply string

const (
	// ApplyNow: the process switches at once (Process.Set, which returns when the vendor answered).
	ApplyNow Apply = "now"
	// ApplyNextTurn: the process switches before the next prompt is written (Process.Set).
	ApplyNextTurn Apply = "next-turn"
	// ApplyRestart: the process restarts between turns with the values as flags.
	ApplyRestart Apply = "restart"
)

// ConfigValue is one value of a select setting.
type ConfigValue struct {
	Value       string
	Name        string
	Description string
	Risk        string // "high" or empty
	Icon        string // a name of the shared icon vocabulary (shared/src/runtime.ts), or empty
}

// ConfigOption is one setting a driver declares, shaped as an ACP session config option. Current is the
// default of a new session: a string for a select, a bool for a boolean. Apply is the driver's own and is not
// sent on the wire.
type ConfigOption struct {
	ID          string
	Name        string
	Description string
	Category    string
	Type        ConfigType
	Options     []ConfigValue
	Current     any
	Apply       Apply
}

// StartSpec says which session a driver starts and where.
type StartSpec struct {
	SessionID string
	Dir       string // the session's working directory
	// New starts the session under SessionID instead of resuming it; the vendor creates its file with the
	// first prompt.
	New bool
	// Config holds the session's value of every setting of Driver.Config(), by option ID.
	Config map[string]any
}

// Driver starts processes of one vendor.
type Driver interface {
	Name() string
	Capabilities() []Capability
	// Config lists the settings of a session in the driver's order of priority, each with the default of a
	// new session as its Current value.
	Config() []ConfigOption
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

// IDAssigner is implemented by drivers whose vendor assigns a new session's ID and cannot be told one. The
// manager starts such a session with StartNew instead of minting an ID; a driver without it gets the
// manager's ID as StartSpec.SessionID.
type IDAssigner interface {
	// StartNew creates a session in spec.Dir (spec.SessionID is empty, spec.New is set), starts its process
	// as Start does, and returns the ID the vendor gave the session, which is not empty.
	StartNew(ctx context.Context, spec StartSpec) (sessionID string, p Process, err error)
}

// Process is one running vendor process. Methods are safe for concurrent use.
type Process interface {
	// Events delivers the process's events in order and is closed after the final Exited.
	Events() <-chan ProcessEvent
	Prompt(p Prompt) error
	// Steer writes a message into the running turn; the vendor absorbs it at the next tool gap, or runs it as
	// the next turn when the turn ends without one. It does not start a turn and leaves the turn's bookkeeping alone.
	Steer(p Prompt) error
	// Set switches a setting of the running process (an ApplyNow or ApplyNextTurn option); a setting the
	// process cannot switch is an error. An ApplyNow setting returns when the vendor accepted the value, and
	// the vendor's refusal is the error.
	Set(id string, value any) error
	// Answer responds to a permission request (see Verdict).
	Answer(requestID string, v Verdict) error
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

// ConfigChanged: the vendor changed a setting itself (Claude Code leaving plan mode, a terminal switching
// mode); Value is the new value of option ID. A value the driver does not declare is ignored.
type ConfigChanged struct {
	ID    string
	Value any
}

// Command is one slash command or skill of a session. Kind is "command" or "skill"; Source is "builtin",
// "project", "user" or "plugin" (then Plugin names the plugin).
type Command struct {
	Name         string
	Description  string
	ArgumentHint string
	Kind         string
	Source       string
	Plugin       string
}

// Commands: the complete list of commands and skills the vendor offers the process, sorted and bounded; it is
// sent when the process learns it and again whenever the vendor says it changed.
type Commands struct{ List []Command }

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

// PermissionRequest asks to use a tool. PlanExit is set when the request asks to leave a plan (ADR 0019).
type PermissionRequest struct {
	RequestID string
	Tool      string
	ToolUseID string
	Input     json.RawMessage
	PlanExit  *PlanExit
}

// PlanExit is a driver's declaration that a permission request asks to leave a plan: the plan to review and
// the values of one of its select settings (Option, the permission mode) that approval may switch the session
// to. Default is the value a client offers first; it is one of Modes. A request with a PlanExit waits for the
// user as long as it takes. FreshContext says the provider can start a new session from the approved plan
// (ADR 0025).
type PlanExit struct {
	Plan         string
	File         string
	Option       string
	Modes        []string
	Default      string
	FreshContext bool
}

// Verdict answers a permission request. A refusal carries Message, which explains it to the model; the
// manager fills a default text when it is empty. An approval of a plan exit may carry Mode, one of the
// request's PlanExit.Modes, which the session switches to as the plan is approved.
type Verdict struct {
	Allow   bool
	Message string
	Mode    string
}

// PermissionCancelled: the vendor no longer waits for the request (the turn ended).
type PermissionCancelled struct{ RequestID string }

// TurnResult ends a turn. Failed is set when the vendor reports an error; Error then describes it.
type TurnResult struct {
	Failed bool
	Error  string
}

// RuleDecided: a permission rule decided a tool call (ADR 0029), without asking a person. Decision is `allow`,
// `deny` or `ask`; Scope is the rule's scope (`global`, `project`, `session` or `agent`) and Revision the revision
// of the rule set that was in force. ToolUseID is empty when the vendor sent none.
type RuleDecided struct {
	ToolUseID string
	Decision  string
	RuleID    string
	Scope     string
	Revision  int
}

// RulesUnenforced: a Codex command or file change ran without the rule hook being asked, so no rule was checked
// on this process (ADR 0032). ToolUseID is the item that ran, empty when unknown. It is sent once per process.
type RulesUnenforced struct{ ToolUseID string }

// Exited is the last event of a process. Err is nil for an exit with status 0.
type Exited struct{ Err error }

func (Ready) processEvent()               {}
func (StateChanged) processEvent()        {}
func (ConfigChanged) processEvent()       {}
func (Commands) processEvent()            {}
func (RuleDecided) processEvent()         {}
func (RulesUnenforced) processEvent()     {}
func (Output) processEvent()              {}
func (PermissionRequest) processEvent()   {}
func (PermissionCancelled) processEvent() {}
func (TurnResult) processEvent()          {}
func (Exited) processEvent()              {}
