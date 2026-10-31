package proto

import "fmt"

// Event types. Each payload below documents its current version.
const (
	TypeTurnStarted      EventType = "turn.started"
	TypeTurnEnded        EventType = "turn.ended"
	TypeMessageDelta     EventType = "message.delta"
	TypeMessage          EventType = "message"
	TypeThinking         EventType = "thinking"
	TypeToolCall         EventType = "tool.call"
	TypeFileChange       EventType = "file.change"
	TypeApprovalRequest  EventType = "approval.requested"
	TypeApprovalResolved EventType = "approval.resolved"
	TypeQuestion         EventType = "question"
	TypeUsage            EventType = "usage"
	TypeError            EventType = "error"
	TypeModeChanged      EventType = "mode.changed"
)

// TurnStarted marks the start of a turn.
type TurnStarted struct{}

// TurnEndReason says why a turn ended.
type TurnEndReason string

// Turn end reasons.
const (
	TurnCompleted   TurnEndReason = "completed"
	TurnInterrupted TurnEndReason = "interrupted"
	TurnAborted     TurnEndReason = "aborted"
	TurnFailed      TurnEndReason = "failed"
)

// TurnEnded marks the end of a turn. Only the harness protocol's own signal
// produces it, or a harness crash (Reason TurnAborted).
type TurnEnded struct {
	Reason TurnEndReason `json:"reason"`
	// Message says why the turn failed or was aborted.
	Message string `json:"message,omitempty"`
}

// MessageDelta is a streamed fragment of a message.
type MessageDelta struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

// Role is who wrote a message.
type Role string

// Message roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a complete message; it supersedes the deltas with the same ID.
type Message struct {
	ID   string `json:"id"`
	Role Role   `json:"role"`
	Text string `json:"text"`
}

// Thinking is the harness's visible reasoning.
type Thinking struct {
	Text string `json:"text"`
}

// ToolKind groups tool calls for display.
type ToolKind string

// Tool kinds.
const (
	ToolRead  ToolKind = "read"
	ToolEdit  ToolKind = "edit"
	ToolExec  ToolKind = "exec"
	ToolWeb   ToolKind = "web"
	ToolOther ToolKind = "other"
)

// ToolStatus is the state of a tool call.
type ToolStatus string

// Tool call states.
const (
	ToolRunning ToolStatus = "running"
	ToolDone    ToolStatus = "done"
	ToolFailed  ToolStatus = "failed"
)

// ToolCall is a tool invocation. Summary is short; the full output, if any,
// lives in the blob Output points at.
type ToolCall struct {
	ID      string     `json:"id"`
	Kind    ToolKind   `json:"kind"`
	Name    string     `json:"name"`
	Summary string     `json:"summary"`
	Status  ToolStatus `json:"status"`
	Output  *BlobRef   `json:"output,omitempty"`
}

// FileOp is what happened to a file.
type FileOp string

// File operations.
const (
	FileCreated  FileOp = "created"
	FileModified FileOp = "modified"
	FileDeleted  FileOp = "deleted"
)

// FileChange reports a file the harness changed.
type FileChange struct {
	Path string `json:"path"`
	Op   FileOp `json:"op"`
}

// ApprovalKind says what an approval request is about.
type ApprovalKind string

// Approval kinds.
const (
	ApprovalCommand    ApprovalKind = "command"
	ApprovalFileChange ApprovalKind = "file_change"
	ApprovalTool       ApprovalKind = "tool"
	ApprovalHopletTool ApprovalKind = "hoplet_tool"
)

// ApprovalScope is how far an approval option reaches.
type ApprovalScope string

// Approval scopes.
const (
	ScopeOnce    ApprovalScope = "once"
	ScopeSession ApprovalScope = "session"
	ScopeAlways  ApprovalScope = "always"
	ScopeDeny    ApprovalScope = "deny"
)

// ApprovalOption is one answer the harness offers. ID and Label carry the
// harness's native option verbatim; Scope is Hoplet's classification of it.
type ApprovalOption struct {
	ID    string        `json:"id"`
	Label string        `json:"label"`
	Scope ApprovalScope `json:"scope,omitempty"`
}

// ApprovalRequested asks for a decision the harness's permissions require.
type ApprovalRequested struct {
	ID     string       `json:"id"`
	Kind   ApprovalKind `json:"kind"`
	Title  string       `json:"title"`
	Detail string       `json:"detail,omitempty"`
	// ToolCallID is the ID of the ToolCall this approval gates, when the
	// harness reports one.
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Options    []ApprovalOption `json:"options,omitempty"`
}

// ApprovalOutcome is how an approval request ended.
type ApprovalOutcome string

// Approval outcomes.
const (
	OutcomeAllowed ApprovalOutcome = "allowed"
	OutcomeDenied  ApprovalOutcome = "denied"
	OutcomeExpired ApprovalOutcome = "expired"
)

// ApprovalResolved records the decision. OptionID is the harness option
// chosen (empty when expired); DecidedBy is a device ID or a Hoplet rule ID.
type ApprovalResolved struct {
	ID        string          `json:"id"`
	Outcome   ApprovalOutcome `json:"outcome"`
	OptionID  string          `json:"option_id,omitempty"`
	DecidedBy string          `json:"decided_by,omitempty"`
}

// Question is a question the harness asks.
type Question struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
}

// Usage reports token use. The counts are this turn's increment, not session
// totals. Cache tokens may be added later as optional fields.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Error reports a harness or adapter error that did not end the turn.
type Error struct {
	Message string `json:"message"`
}

// ModeChanged records the harness changing the session's permission mode
// or plan mode by itself, as when the user approves a plan and the harness
// leaves plan mode. The store applies it to the session record. Permission
// is the mode the harness actually runs. Changes the user makes through
// Hoplet are not events: they change the session record directly, so the
// event log alone does not hold the history of a session's modes.
type ModeChanged struct {
	Permission PermissionMode `json:"permission"`
	Plan       bool           `json:"plan"`
}

func (m ModeChanged) validate() error {
	if !m.Permission.Valid() {
		return fmt.Errorf("%w: unknown permission mode %q", ErrInvalidPayload, m.Permission)
	}
	return nil
}

func (r ApprovalResolved) validate() error {
	switch r.Outcome {
	case OutcomeAllowed, OutcomeDenied:
	case OutcomeExpired:
		if r.OptionID != "" {
			return fmt.Errorf("%w: expired approval has option %q", ErrInvalidPayload, r.OptionID)
		}
	default:
		return fmt.Errorf("%w: unknown approval outcome %q", ErrInvalidPayload, r.Outcome)
	}
	if r.ID == "" {
		return fmt.Errorf("%w: approval has no ID", ErrInvalidPayload)
	}
	return nil
}

func (TurnStarted) eventType() EventType       { return TypeTurnStarted }
func (TurnEnded) eventType() EventType         { return TypeTurnEnded }
func (MessageDelta) eventType() EventType      { return TypeMessageDelta }
func (Message) eventType() EventType           { return TypeMessage }
func (Thinking) eventType() EventType          { return TypeThinking }
func (ToolCall) eventType() EventType          { return TypeToolCall }
func (FileChange) eventType() EventType        { return TypeFileChange }
func (ApprovalRequested) eventType() EventType { return TypeApprovalRequest }
func (ApprovalResolved) eventType() EventType  { return TypeApprovalResolved }
func (Question) eventType() EventType          { return TypeQuestion }
func (Usage) eventType() EventType             { return TypeUsage }
func (Error) eventType() EventType             { return TypeError }
func (ModeChanged) eventType() EventType       { return TypeModeChanged }

func (TurnStarted) eventVersion() uint16       { return 1 }
func (TurnEnded) eventVersion() uint16         { return 1 }
func (MessageDelta) eventVersion() uint16      { return 1 }
func (Message) eventVersion() uint16           { return 1 }
func (Thinking) eventVersion() uint16          { return 1 }
func (ToolCall) eventVersion() uint16          { return 1 }
func (FileChange) eventVersion() uint16        { return 1 }
func (ApprovalRequested) eventVersion() uint16 { return 1 }
func (ApprovalResolved) eventVersion() uint16  { return 1 }
func (Question) eventVersion() uint16          { return 1 }
func (Usage) eventVersion() uint16             { return 1 }
func (Error) eventVersion() uint16             { return 1 }
func (ModeChanged) eventVersion() uint16       { return 1 }

func init() {
	register[TurnStarted]()
	register[TurnEnded]()
	register[MessageDelta]()
	register[Message]()
	register[Thinking]()
	register[ToolCall]()
	register[FileChange]()
	register[ApprovalRequested]()
	register[ApprovalResolved]()
	register[Question]()
	register[Usage]()
	register[Error]()
	register[ModeChanged]()
}
