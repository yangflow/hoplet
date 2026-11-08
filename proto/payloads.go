package proto

import "fmt"

// Event types. Each payload below documents its current version.
const (
	TypeTurnStarted       EventType = "turn.started"
	TypeTurnEnded         EventType = "turn.ended"
	TypeMessageDelta      EventType = "message.delta"
	TypeMessage           EventType = "message"
	TypeThinking          EventType = "thinking"
	TypeToolCall          EventType = "tool.call"
	TypeFileChange        EventType = "file.change"
	TypeApprovalRequest   EventType = "approval.requested"
	TypeApprovalResolved  EventType = "approval.resolved"
	TypeQuestion          EventType = "question"
	TypeUsage             EventType = "usage"
	TypeError             EventType = "error"
	TypeModeChanged       EventType = "mode.changed"
	TypeModelChanged      EventType = "model.changed"
	TypeContextUsage      EventType = "context.usage"
	TypeTodoList          EventType = "todo.list"
	TypeSubagent          EventType = "subagent"
	TypeArtifactPublished EventType = "artifact.published"
	TypeSessionDelegated  EventType = "session.delegated"
	TypeMemorySaved       EventType = "memory.saved"
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
	// From is the session whose report this user message is; empty for
	// messages the user sent.
	From SessionID `json:"from,omitempty"`
	// Report is how things stood in From when it reported; empty on
	// messages without From and on reports recorded before it existed,
	// which read as ReportDone.
	Report ReportStatus `json:"report,omitempty"`
}

// ReportStatus is how things stood in a session when it reported to its
// parent.
type ReportStatus string

// Report statuses.
const (
	// ReportDone: the session finished what it reported on.
	ReportDone ReportStatus = "done"
	// ReportError: the session's turn ended in an error.
	ReportError ReportStatus = "error"
	// ReportApproval: the session waits for the user to approve something.
	ReportApproval ReportStatus = "approval"
)

func (m Message) validate() error {
	switch m.Report {
	case "":
		return nil
	case ReportDone, ReportError, ReportApproval:
	default:
		return fmt.Errorf("%w: unknown report status %q", ErrInvalidPayload, m.Report)
	}
	if m.From == "" {
		return fmt.Errorf("%w: a report status needs the session that reported", ErrInvalidPayload)
	}
	return nil
}

// DelegationKind says how a session came to be created for its parent.
type DelegationKind string

// Delegation kinds.
const (
	// DelegatedSession: a delegated Personal child session.
	DelegatedSession DelegationKind = "delegate"
	// HandedOff: a Code session handed off to me.
	HandedOff DelegationKind = "handoff"
)

// SessionDelegated records, in the parent's log, a session it created
// through the tool bridge.
type SessionDelegated struct {
	Session SessionID      `json:"session"`
	Kind    DelegationKind `json:"kind"`
	// Title is the new session's title when it was created; it may change
	// later.
	Title string `json:"title,omitempty"`
}

func (d SessionDelegated) validate() error {
	if d.Session == "" {
		return fmt.Errorf("%w: delegation has no session", ErrInvalidPayload)
	}
	if d.Kind != DelegatedSession && d.Kind != HandedOff {
		return fmt.Errorf("%w: unknown delegation kind %q", ErrInvalidPayload, d.Kind)
	}
	return nil
}

// MemorySaveAction says what a save did to a memory entry.
type MemorySaveAction string

// Memory save actions.
const (
	MemoryCreated MemorySaveAction = "created"
	MemoryUpdated MemorySaveAction = "updated"
)

// MemorySaveActor says who saved a memory entry without the user editing it.
type MemorySaveActor string

// Memory save actors.
const (
	MemoryByCompanion  MemorySaveActor = "companion"
	MemoryByExtraction MemorySaveActor = "extraction"
)

// MemorySaved records, in the source session's log, that a memory entry was
// saved without the user editing it. It carries no entry text, so that
// withdrawing the entry leaves nothing of it in the log.
type MemorySaved struct {
	Entry   string           `json:"entry"`
	Version int              `json:"version"`
	Action  MemorySaveAction `json:"action"`
	Actor   MemorySaveActor  `json:"actor"`
}

func (m MemorySaved) validate() error {
	if m.Entry == "" {
		return fmt.Errorf("%w: memory save has no entry", ErrInvalidPayload)
	}
	if m.Version <= 0 {
		return fmt.Errorf("%w: memory save has version %d", ErrInvalidPayload, m.Version)
	}
	if m.Action != MemoryCreated && m.Action != MemoryUpdated {
		return fmt.Errorf("%w: unknown memory save action %q", ErrInvalidPayload, m.Action)
	}
	if m.Actor != MemoryByCompanion && m.Actor != MemoryByExtraction {
		return fmt.Errorf("%w: unknown memory save actor %q", ErrInvalidPayload, m.Actor)
	}
	return nil
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

// ContextUsage reports how full the session's context window is: Used
// tokens the last request held (input, cache and output), and Window, its
// size, once the harness has said it (zero until then). A compaction
// reports the tokens left after it.
type ContextUsage struct {
	Used   int64 `json:"used"`
	Window int64 `json:"window,omitempty"`
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

// ModelChanged records the model the harness reports it runs: at the start
// of a session, and after a model change takes effect. Provider is set for
// harnesses whose models belong to a provider (Hermes). The store applies
// it to the session record.
type ModelChanged struct {
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
}

func (m ModelChanged) validate() error {
	if m.Model == "" {
		return fmt.Errorf("%w: model change without a model", ErrInvalidPayload)
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

// TodoStatus is the state of a task on the harness's task list.
type TodoStatus string

// Task states.
const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
	TodoCancelled  TodoStatus = "cancelled"
)

// TodoItem is one task on the list. Active is how the harness words the
// task while it is in progress ("Running the tests"); empty when it gives
// none.
type TodoItem struct {
	ID     string     `json:"id"`
	Text   string     `json:"text"`
	Active string     `json:"active,omitempty"`
	Status TodoStatus `json:"status"`
}

// TodoList is the whole task list the harness keeps for itself, sent in
// full after every change; only the latest one counts. Nested tasks are
// flattened in order.
type TodoList struct {
	Items []TodoItem `json:"items"`
}

func (l TodoList) validate() error {
	for _, it := range l.Items {
		switch it.Status {
		case TodoPending, TodoInProgress, TodoCompleted, TodoCancelled:
		default:
			return fmt.Errorf("%w: unknown task status %q", ErrInvalidPayload, it.Status)
		}
	}
	return nil
}

// SubagentStatus is the state of a subagent.
type SubagentStatus string

// Subagent states.
const (
	SubagentRunning SubagentStatus = "running"
	SubagentDone    SubagentStatus = "done"
	SubagentFailed  SubagentStatus = "failed"
	SubagentStopped SubagentStatus = "stopped"
)

// Subagent is a subagent the harness sent off within a turn, sent in full
// on every change; only the latest one per ID counts. It replaces the tool
// call that spawned it (CallID). Type is the harness's kind of subagent,
// empty when it gives none; Background means the main agent does not wait
// for it, so it may run past the end of its turn. Summary is the result it
// handed back. The subagent's own events carry its ID in Event.Agent.
type Subagent struct {
	ID         string         `json:"id"`
	CallID     string         `json:"call_id,omitempty"`
	Type       string         `json:"type,omitempty"`
	Title      string         `json:"title"`
	Status     SubagentStatus `json:"status"`
	Background bool           `json:"background,omitempty"`
	Tokens     int64          `json:"tokens,omitempty"`
	ToolUses   int            `json:"tool_uses,omitempty"`
	// DurationMS is how long it ran, in milliseconds, once the harness
	// says.
	DurationMS int64  `json:"duration_ms,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

func (a Subagent) validate() error {
	if a.ID == "" {
		return fmt.Errorf("%w: subagent has no ID", ErrInvalidPayload)
	}
	switch a.Status {
	case SubagentRunning, SubagentDone, SubagentFailed, SubagentStopped:
	default:
		return fmt.Errorf("%w: unknown subagent status %q", ErrInvalidPayload, a.Status)
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
func (ModelChanged) eventType() EventType      { return TypeModelChanged }
func (ContextUsage) eventType() EventType      { return TypeContextUsage }
func (TodoList) eventType() EventType          { return TypeTodoList }
func (Subagent) eventType() EventType          { return TypeSubagent }
func (SessionDelegated) eventType() EventType  { return TypeSessionDelegated }
func (MemorySaved) eventType() EventType       { return TypeMemorySaved }

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
func (ModelChanged) eventVersion() uint16      { return 1 }
func (ContextUsage) eventVersion() uint16      { return 1 }
func (TodoList) eventVersion() uint16          { return 1 }
func (Subagent) eventVersion() uint16          { return 1 }
func (SessionDelegated) eventVersion() uint16  { return 1 }
func (MemorySaved) eventVersion() uint16       { return 1 }

func init() {
	register[MemorySaved]()
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
	register[ModelChanged]()
	register[ContextUsage]()
	register[TodoList]()
	register[Subagent]()
	register[SessionDelegated]()
}
