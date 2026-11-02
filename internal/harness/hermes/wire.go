package hermes

import "encoding/json"

// frame is one line from the bridge: a gateway frame (a response or a
// notification) or a line about the bridge itself.
type frame struct {
	Bridge string          `json:"hoplet_bridge"`
	Caps   []string        `json:"capabilities"`
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	// Error is a gateway error object, or the text of a bridge line's
	// error; see rpcErr.
	Error json.RawMessage `json:"error"`
}

// rpcErr reads the error of a response.
func (f frame) rpcErr() *rpcError {
	if len(f.Error) == 0 || string(f.Error) == "null" {
		return nil
	}
	var e rpcError
	if json.Unmarshal(f.Error, &e) != nil {
		return &rpcError{Message: string(f.Error)}
	}
	return &e
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Reason     string `json:"reason"`
		Detail     string `json:"detail"`
		Revision   *int64 `json:"revision"`
		Generation *int64 `json:"execution_generation"`
	} `json:"data"`
}

// event is the params of an "event" notification.
type event struct {
	Type       string          `json:"type"`
	Session    string          `json:"session_id"`
	Payload    json.RawMessage `json:"payload"`
	Generation *int64          `json:"execution_generation"`
	Admission  string          `json:"admission_id"`
}

// snapshot is the result of session.create and session.resume.
type snapshot struct {
	Session    string     `json:"session_id"`
	Stored     string     `json:"stored_session_id"`
	Running    bool       `json:"running"`
	Revision   int64      `json:"revision"`
	Generation int64      `json:"execution_generation"`
	Pending    []admitted `json:"pending"`
	Prompts    []prompt   `json:"prompts"`
}

// admitted is an admission status: prompt.submit's result, a snapshot's
// pending entry and prompt.receipt's result.
type admitted struct {
	Admission  string          `json:"admission_id"`
	Status     string          `json:"status"` // queued, started, unknown, terminal
	Outcome    string          `json:"outcome"`
	Generation *int64          `json:"execution_generation"`
	Result     *turnResult     `json:"result"`
	Usage      *receiptUsage   `json:"usage"`
	Ref        json.RawMessage `json:"ref"`
}

// turnResult is the part of a terminal admission's result Hoplet reads.
type turnResult struct {
	FinalResponse string `json:"final_response"`
	Completed     bool   `json:"completed"`
	Interrupted   bool   `json:"interrupted"`
	Failed        bool   `json:"failed"`
	FailureReason string `json:"failure_reason"`
	Error         string `json:"error"`
}

type receiptUsage struct {
	Input  int64 `json:"input_tokens"`
	Output int64 `json:"output_tokens"`
}

// prompt is an open approval in a snapshot.
type prompt struct {
	Kind        string    `json:"kind"`
	ID          string    `json:"prompt_id"`
	Generation  int64     `json:"execution_generation"`
	Choices     []string  `json:"choices"`
	Command     string    `json:"command"`
	Description string    `json:"description"`
	Edit        *editInfo `json:"edit"`
}

type editInfo struct {
	Tool string `json:"tool_name"`
	Path string `json:"path"`
}

// mutated is the part of a session.mutate result Hoplet reads.
type mutated struct {
	Revision   *int64 `json:"revision"`
	Generation *int64 `json:"execution_generation"`
	Status     string `json:"status"`
	Confirm    string `json:"confirm"`
}

type deltaPayload struct {
	Text *string `json:"text"`
}

type interimPayload struct {
	Text            string `json:"text"`
	AlreadyStreamed bool   `json:"already_streamed"`
}

type toolPayload struct {
	ID      string          `json:"tool_id"`
	Name    string          `json:"name"`
	Context string          `json:"context"`
	Args    json.RawMessage `json:"args"`
	IsError bool            `json:"is_error"`
}

type completePayload struct {
	Text      string `json:"text"`
	Admission string `json:"admission_id"`
	Outcome   string `json:"outcome"`
	Status    string `json:"status"` // complete, interrupted, error
	Failure   string `json:"failure_reason"`
	Surface   *struct {
		Code string `json:"code"`
	} `json:"error_surface"`
}

type messagePayload struct {
	Message string `json:"message"`
}

type settledPayload struct {
	ID string `json:"prompt_id"`
}

// usagePayload is the part of a session.usage event the adapter reads.
type usagePayload struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
}

type infoPayload struct {
	Revision   *int64 `json:"revision"`
	Generation *int64 `json:"execution_generation"`
}
