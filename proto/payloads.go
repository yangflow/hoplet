package proto

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
	TurnFailed      TurnEndReason = "error"
)

// TurnEnded marks the end of a turn. Only the harness protocol's own signal
// produces it, or a harness crash (Reason TurnAborted).
type TurnEnded struct {
	Reason TurnEndReason `json:"reason"`
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

// ApprovalOption is one answer the harness offers, passed through as is.
type ApprovalOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Scope string `json:"scope,omitempty"`
}

// ApprovalRequested asks for a decision the harness's permissions require.
type ApprovalRequested struct {
	ID      string           `json:"id"`
	Kind    string           `json:"kind"`
	Title   string           `json:"title"`
	Detail  string           `json:"detail,omitempty"`
	Options []ApprovalOption `json:"options"`
}

// ApprovalResolved records the decision; DecidedBy is a device ID or a
// Hoplet rule ID. Expired is set when the harness withdrew the request.
type ApprovalResolved struct {
	ID        string `json:"id"`
	OptionID  string `json:"option_id,omitempty"`
	DecidedBy string `json:"decided_by,omitempty"`
	Expired   bool   `json:"expired,omitempty"`
}

// Question is a question the harness asks.
type Question struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
}

// Usage reports token use.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Error reports a harness or adapter error that did not end the turn.
type Error struct {
	Message string `json:"message"`
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
}
