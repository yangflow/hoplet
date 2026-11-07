package claude

import (
	"encoding/json"
	"strings"
)

// line is one stdout line of claude's stream-json output; only the fields
// the adapter reads are declared.
type line struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	// PermissionMode is claude's mode, on system init and status lines.
	PermissionMode string `json:"permissionMode"`
	// Model is the model claude runs, on system init lines.
	Model   string   `json:"model"`
	Message *message `json:"message"`
	// ParentToolUseID marks a subagent's lines; nil for the session's own.
	ParentToolUseID *string `json:"parent_tool_use_id"`
	// ToolUseResult is what claude adds to a tool result line about the
	// call; read for subagents.
	ToolUseResult *toolUseResult `json:"tool_use_result"`
	// CompactMetadata is on compact_boundary system lines.
	CompactMetadata *struct {
		PostTokens int64 `json:"post_tokens"`
	} `json:"compact_metadata"`
	Event     *streamEvent    `json:"event"`
	RequestID string          `json:"request_id"`
	Request   *controlRequest `json:"request"`
	Response  *controlResult  `json:"response"`

	// result fields
	IsError        bool            `json:"is_error"`
	TerminalReason string          `json:"terminal_reason"`
	Result         string          `json:"result"`
	Errors         json.RawMessage `json:"errors"`
	Usage          *usage          `json:"usage"`
	// ModelUsage is per model; its contextWindow is the window's size.
	ModelUsage map[string]struct {
		ContextWindow int64 `json:"contextWindow"`
	} `json:"modelUsage"`
}

// toolUseResult is the part of a subagent call's result details the
// adapter reads; its shape is not under Hoplet's control, so a field that
// does not fit is dropped.
type toolUseResult struct {
	TotalTokens       int64 `json:"totalTokens"`
	TotalToolUseCount int   `json:"totalToolUseCount"`
	TotalDurationMS   int64 `json:"totalDurationMs"`
	// Content is the subagent's report, block by block, without the frame
	// the result text wraps it in.
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// taskNotification is the system line claude sends when a subagent ends,
// foreground or background: the call that sent it off, how it ended, its
// report and what it used.
type taskNotification struct {
	ToolUseID string `json:"tool_use_id"`
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	Usage     struct {
		TotalTokens int64 `json:"total_tokens"`
		ToolUses    int   `json:"tool_uses"`
		DurationMS  int64 `json:"duration_ms"`
	} `json:"usage"`
}

func (t *toolUseResult) UnmarshalJSON(b []byte) error {
	type plain toolUseResult
	var p plain
	if json.Unmarshal(b, &p) == nil {
		*t = toolUseResult(p)
	}
	return nil
}

type message struct {
	ID      string  `json:"id"`
	Content content `json:"content"`
	Usage   *usage  `json:"usage"`
}

// content is a message's content: a list of blocks, or (for user
// messages) a plain string.
type content []block

func (c *content) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*c = content{{Type: "text", Text: s}}
		return nil
	}
	return json.Unmarshal(b, (*[]block)(c))
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type streamEvent struct {
	Type    string   `json:"type"`
	Index   int      `json:"index"`
	Message *message `json:"message"`
	Delta   *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

type controlRequest struct {
	Subtype     string          `json:"subtype"`
	ToolName    string          `json:"tool_name"`
	DisplayName string          `json:"display_name"`
	Input       json.RawMessage `json:"input"`
	Description string          `json:"description"`
	Suggestions json.RawMessage `json:"permission_suggestions"`
	ToolUseID   string          `json:"tool_use_id"`
}

type controlResult struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Error     string          `json:"error"`
	Response  json.RawMessage `json:"response"`
}

// initAnswer is the part of claude's answer to initialize the adapter
// reads: the models it offers.
type initAnswer struct {
	Models []struct {
		Value         string   `json:"value"`
		ResolvedModel string   `json:"resolvedModel"`
		DisplayName   string   `json:"displayName"`
		Description   string   `json:"description"`
		Efforts       []string `json:"supportedEffortLevels"`
		Fast          bool     `json:"supportsFastMode"`
		HasEffort     bool     `json:"supportsEffort"`
	} `json:"models"`
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	// Iterations are the requests of a result's turn, the last one last.
	Iterations []usage `json:"iterations"`
}

// held is the tokens a request held in the context: its input, cached
// or not, and its output.
func (u usage) held() int64 {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens + u.OutputTokens
}

// Outgoing lines.

type userLine struct {
	Type    string      `json:"type"`
	Message userMessage `json:"message"`
}

type userMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type requestLine struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Request   any    `json:"request"`
}

type responseLine struct {
	Type     string        `json:"type"`
	Response responseInner `json:"response"`
}

type responseInner struct {
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
	Response  any    `json:"response,omitempty"`
	Error     string `json:"error,omitempty"`
}

// permission is the answer to a can_use_tool request.
type permission struct {
	Behavior           string          `json:"behavior"`
	UpdatedInput       json.RawMessage `json:"updatedInput,omitempty"`
	UpdatedPermissions json.RawMessage `json:"updatedPermissions,omitempty"`
	Message            string          `json:"message,omitempty"`
}

type suggestion struct {
	Destination string `json:"destination"`
}

// errorsText renders a result's errors: a list of strings joined, anything
// else (the field's shape is not under Hoplet's control) as its JSON text.
func (l *line) errorsText() string {
	var list []string
	if json.Unmarshal(l.Errors, &list) == nil {
		return strings.Join(list, "; ")
	}
	return string(l.Errors)
}
