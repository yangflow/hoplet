package codex

import "encoding/json"

// Only the part of the app-server protocol this adapter uses is typed here;
// the recordings in testdata keep it honest.

// message is one JSON-RPC line, in either direction. A line with Method and
// ID is a request from the server, with Method alone a notification, and
// with ID alone a response.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

type initializeParams struct {
	ClientInfo   clientInfo         `json:"clientInfo"`
	Capabilities clientCapabilities `json:"capabilities"`
}

type clientCapabilities struct {
	ExperimentalAPI bool `json:"experimentalApi"`
}

// threadParams are the settings thread/start and thread/resume share.
type threadParams struct {
	ThreadID          string `json:"threadId,omitempty"`
	Cwd               string `json:"cwd,omitempty"`
	Model             string `json:"model,omitempty"`
	ApprovalPolicy    string `json:"approvalPolicy"`
	Sandbox           string `json:"sandbox"`
	ApprovalsReviewer string `json:"approvalsReviewer"`
	Ephemeral         bool   `json:"ephemeral,omitempty"`
	ExcludeTurns      bool   `json:"excludeTurns,omitempty"`
}

type threadResult struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	Model string `json:"model"`
}

type textInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type turnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []textInput `json:"input"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type turnsListParams struct {
	ThreadID string `json:"threadId"`
	Limit    int    `json:"limit"`
}

type turnsListResult struct {
	Data []turn `json:"data"`
}

type turn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Turn and item states.
const (
	statusInProgress  = "inProgress"
	statusCompleted   = "completed"
	statusInterrupted = "interrupted"
	statusFailed      = "failed"
)

// notification holds the fields of the notifications this adapter reads.
type notification struct {
	ThreadID   string      `json:"threadId"`
	Thread     *thread     `json:"thread"`
	Turn       *turn       `json:"turn"`
	Item       *item       `json:"item"`
	ItemID     string      `json:"itemId"`
	Delta      string      `json:"delta"`
	TokenUsage *tokenUsage `json:"tokenUsage"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error"`
	WillRetry bool `json:"willRetry"`
}

type thread struct {
	ID string `json:"id"`
}

type tokenUsage struct {
	Total              tokenCounts `json:"total"`
	Last               tokenCounts `json:"last"`
	ModelContextWindow int64       `json:"modelContextWindow"`
}

type tokenCounts struct {
	TotalTokens  int64 `json:"totalTokens"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

// item is a thread item; which fields are set depends on Type. Types this
// adapter does not show are kept as raw lines only.
type item struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Status string `json:"status"`

	Text string `json:"text"` // agentMessage

	Summary []string `json:"summary"` // reasoning
	Content []string `json:"content"` // reasoning

	Command          string          `json:"command"` // commandExecution
	CommandActions   []commandAction `json:"commandActions"`
	AggregatedOutput *string         `json:"aggregatedOutput"`
	ExitCode         *int            `json:"exitCode"`

	Changes []fileUpdate `json:"changes"` // fileChange

	Server string     `json:"server"` // mcpToolCall
	Tool   string     `json:"tool"`   // mcpToolCall, dynamicToolCall, collabAgentToolCall
	Result *mcpResult `json:"result"`

	Query string `json:"query"` // webSearch
}

type commandAction struct {
	Command string `json:"command"`
}

type fileUpdate struct {
	Path string `json:"path"`
	Kind struct {
		Type string `json:"type"`
	} `json:"kind"`
}

type mcpResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// Item types.
const (
	itemAgentMessage = "agentMessage"
	itemReasoning    = "reasoning"
	itemCommand      = "commandExecution"
	itemFileChange   = "fileChange"
	itemMCP          = "mcpToolCall"
	itemDynamic      = "dynamicToolCall"
	itemCollab       = "collabAgentToolCall"
	itemWebSearch    = "webSearch"
)
