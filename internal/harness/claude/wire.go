package claude

import "encoding/json"

// line is one stdout line of claude's stream-json output; only the fields
// the adapter reads are declared.
type line struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	// PermissionMode is claude's mode, on system init and status lines.
	PermissionMode string          `json:"permissionMode"`
	Message        *message        `json:"message"`
	Event          *streamEvent    `json:"event"`
	RequestID      string          `json:"request_id"`
	Request        *controlRequest `json:"request"`
	Response       *controlResult  `json:"response"`

	// result fields
	IsError        bool     `json:"is_error"`
	TerminalReason string   `json:"terminal_reason"`
	Result         string   `json:"result"`
	Errors         []string `json:"errors"`
	Usage          *usage   `json:"usage"`
}

type message struct {
	ID      string  `json:"id"`
	Content content `json:"content"`
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
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
	Error     string `json:"error"`
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
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
