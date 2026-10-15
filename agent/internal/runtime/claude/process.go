package claude

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

const initRequestID = "hoplet-init"

// setModelPrefix starts the request ID of a set_model control request.
const setModelPrefix = "hoplet-model-"

// maxLineBytes bounds one line of Claude Code output; a longer line ends the process as a protocol error.
const maxLineBytes = 64 << 20

type pendingRequest struct {
	input json.RawMessage
}

type process struct {
	cmd    *exec.Cmd
	events chan runtime.ProcessEvent
	stderr *tailBuffer

	writeMu sync.Mutex
	stdin   io.WriteCloser
	closed  bool

	reqSeq atomic.Int64
	// model is the model the driver is configured with; `default` switches to it rather than to Claude Code's
	// own default, as a start without --model does.
	model string

	mu      sync.Mutex
	pending map[string]pendingRequest // permission requests by request id, with the input to echo on allow
	seen    map[string]bool           // tool_use ids already reported in this turn
	initErr error                     // the vendor refused initialization
}

func (p *process) Events() <-chan runtime.ProcessEvent { return p.events }

func (p *process) send(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if p.closed {
		return errors.New("process input is closed")
	}
	_, err = p.stdin.Write(append(line, '\n'))
	return err
}

func controlRequest(id, subtype string) map[string]any {
	return map[string]any{
		"type":       "control_request",
		"request_id": id,
		"request":    map[string]any{"subtype": subtype},
	}
}

// Prompt writes one user message: its images first, as base64 image content, then its text.
func (p *process) Prompt(prompt runtime.Prompt) error {
	p.mu.Lock()
	p.seen = nil
	p.mu.Unlock()
	content := make([]map[string]any, 0, len(prompt.Images)+1)
	for _, img := range prompt.Images {
		content = append(content, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": img.MediaType,
				"data":       base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	content = append(content, map[string]any{"type": "text", "text": prompt.Text})
	return p.send(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": content},
	})
}

func (p *process) Answer(requestID string, allow bool, message string) error {
	p.mu.Lock()
	req, ok := p.pending[requestID]
	delete(p.pending, requestID)
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("no pending permission request %q", requestID)
	}
	resp := map[string]any{"behavior": "deny", "message": message}
	if allow {
		input := req.input
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		resp = map[string]any{"behavior": "allow", "updatedInput": input}
	}
	return p.send(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   resp,
		},
	})
}

// Set switches the model with set_model (Claude Code accepts `default` and returns to its own default model); no other setting can change in a running process. The request goes
// before the prompt that follows it on the same input, so that turn already runs on the new model; a refusal is
// only logged.
func (p *process) Set(id string, value any) error {
	model, ok := value.(string)
	if id != optModel || !ok {
		return fmt.Errorf("claude cannot change %q in a running process", id)
	}
	if model == modelDefault && p.model != "" {
		model = p.model
	}
	req := controlRequest(fmt.Sprintf("%s%d", setModelPrefix, p.reqSeq.Add(1)), "set_model")
	req["request"].(map[string]any)["model"] = model
	return p.send(req)
}

func (p *process) Interrupt() error {
	return p.send(controlRequest(fmt.Sprintf("hoplet-int-%d", p.reqSeq.Add(1)), "interrupt"))
}

func (p *process) Close() error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.stdin.Close()
}

func (p *process) Kill() error {
	_ = p.Close()
	if p.cmd.Process == nil {
		return nil
	}
	// The process runs in its own group, so tools it started end with it.
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return p.cmd.Process.Kill()
	}
	return nil
}

func (p *process) emit(ev runtime.ProcessEvent) { p.events <- ev }

// read translates Claude Code's stdout into events until it ends, then reports the exit.
func (p *process) read(stdout io.Reader) {
	defer close(p.events)
	r := bufio.NewReaderSize(stdout, 64<<10)
	var readErr error
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > maxLineBytes {
			readErr = errors.New("output line too long")
			_ = p.Kill()
			break
		}
		if len(bytes.TrimSpace(line)) > 0 {
			p.handleLine(line)
		}
		if err != nil {
			break
		}
	}
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := p.cmd.Wait()
	err := readErr
	p.mu.Lock()
	if err == nil {
		err = p.initErr
	}
	p.mu.Unlock()
	if err == nil && waitErr != nil {
		err = waitErr
	}
	if err != nil {
		if tail := p.stderr.String(); tail != "" {
			err = fmt.Errorf("%w: %s", err, lastLine(tail))
		}
	}
	p.emit(runtime.Exited{Err: err})
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// message is the part of a Claude Code output line the driver reads; unknown fields are ignored.
type message struct {
	Type      string  `json:"type"`
	Subtype   string  `json:"subtype"`
	State     string  `json:"state"`
	RequestID string  `json:"request_id"`
	ParentID  *string `json:"parent_tool_use_id"`

	EstimatedTokens      int `json:"estimated_tokens"`
	EstimatedTokensDelta int `json:"estimated_tokens_delta"`

	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`

	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`

	Request struct {
		Subtype   string          `json:"subtype"`
		ToolName  string          `json:"tool_name"`
		ToolUseID string          `json:"tool_use_id"`
		Input     json.RawMessage `json:"input"`
	} `json:"request"`

	Response struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Error     string `json:"error"`
	} `json:"response"`

	IsError bool            `json:"is_error"`
	Result  json.RawMessage `json:"result"`
	Errors  []string        `json:"errors"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
	Text      string          `json:"text"`
}

func (p *process) handleLine(line []byte) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		return
	}
	switch m.Type {
	case "system":
		p.handleSystem(&m)
	case "stream_event":
		// Only text deltas are relayed. thinking_delta carries no text, signature_delta is large and opaque,
		// input_json_delta is partial; the full tool call arrives with the assistant message.
		if m.ParentID == nil && m.Event.Type == "content_block_delta" && m.Event.Delta.Type == "text_delta" && m.Event.Delta.Text != "" {
			p.emit(runtime.Output{Kind: runtime.OutputText, Text: m.Event.Delta.Text})
		}
	case "assistant":
		if m.ParentID == nil {
			p.handleAssistant(&m)
		}
	case "user":
		if m.ParentID == nil {
			p.handleUser(&m)
		}
	case "result":
		res := runtime.TurnResult{Failed: m.IsError}
		if m.IsError {
			res.Error = describeFailure(&m)
		}
		p.emit(res)
	case "control_request":
		p.handleControlRequest(&m)
	case "control_cancel_request":
		p.mu.Lock()
		delete(p.pending, m.RequestID)
		p.mu.Unlock()
		p.emit(runtime.PermissionCancelled{RequestID: m.RequestID})
	case "control_response":
		if strings.HasPrefix(m.Response.RequestID, setModelPrefix) && m.Response.Subtype != "success" {
			log.Printf("[agent] claude refused a model switch: %s", m.Response.Error)
			return
		}
		if m.Response.RequestID != initRequestID {
			return
		}
		if m.Response.Subtype == "success" {
			p.emit(runtime.Ready{})
			return
		}
		msg := m.Response.Error
		if msg == "" {
			msg = "initialize was refused"
		}
		p.mu.Lock()
		p.initErr = errors.New(msg)
		p.mu.Unlock()
		_ = p.Kill()
	}
}

func (p *process) handleSystem(m *message) {
	switch m.Subtype {
	case "session_state_changed":
		switch m.State {
		case "running":
			p.emit(runtime.StateChanged{State: runtime.StateRunning})
		case "requires_action":
			p.emit(runtime.StateChanged{State: runtime.StateWaitingPermission})
		case "idle":
			p.emit(runtime.StateChanged{State: runtime.StateReady})
		}
	case "thinking_tokens":
		n := m.EstimatedTokensDelta
		if n == 0 {
			n = m.EstimatedTokens
		}
		if n > 0 {
			p.emit(runtime.Output{Kind: runtime.OutputThinking, Tokens: n})
		}
	}
}

func (p *process) handleAssistant(m *message) {
	for _, b := range blocks(m.Message.Content) {
		if b.Type != "tool_use" || b.ID == "" {
			continue
		}
		p.mu.Lock()
		dup := p.seen[b.ID]
		if p.seen == nil {
			p.seen = map[string]bool{}
		}
		p.seen[b.ID] = true
		p.mu.Unlock()
		if dup {
			continue
		}
		p.emit(runtime.Output{Kind: runtime.OutputToolCall, Tool: b.Name, ToolUseID: b.ID, Input: b.Input})
	}
}

func (p *process) handleUser(m *message) {
	for _, b := range blocks(m.Message.Content) {
		if b.Type != "tool_result" {
			continue
		}
		text, truncated := resultText(b.Content)
		p.emit(runtime.Output{
			Kind: runtime.OutputToolResult, ToolUseID: b.ToolUseID, IsError: b.IsError,
			Text: text, Truncated: truncated,
		})
	}
}

func (p *process) handleControlRequest(m *message) {
	if m.Request.Subtype == "can_use_tool" {
		p.mu.Lock()
		p.pending[m.RequestID] = pendingRequest{input: m.Request.Input}
		p.mu.Unlock()
		p.emit(runtime.PermissionRequest{
			RequestID: m.RequestID, Tool: m.Request.ToolName, ToolUseID: m.Request.ToolUseID, Input: m.Request.Input,
		})
		return
	}
	// Claude Code waits for an answer to every request it sends; a kind Hoplet does not handle gets an error.
	_ = p.send(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "error",
			"request_id": m.RequestID,
			"error":      "unsupported control request: " + m.Request.Subtype,
		},
	})
}

// blocks decodes message content, which is a list of blocks or a plain string.
func blocks(raw json.RawMessage) []contentBlock {
	var out []contentBlock
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// resultText flattens the content of a tool result and bounds it.
func resultText(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		var parts []contentBlock
		if json.Unmarshal(raw, &parts) != nil {
			return "", false
		}
		var sb strings.Builder
		for _, part := range parts {
			if part.Type == "text" {
				sb.WriteString(part.Text)
			}
		}
		s = sb.String()
	}
	if len(s) > runtime.MaxOutputText {
		cut := runtime.MaxOutputText
		for cut > 0 && s[cut]&0xC0 == 0x80 { // keep the cut on a rune boundary
			cut--
		}
		return s[:cut], true
	}
	return s, false
}

func describeFailure(m *message) string {
	var parts []string
	if m.Subtype != "" {
		parts = append(parts, m.Subtype)
	}
	var text string
	if json.Unmarshal(m.Result, &text) == nil && text != "" {
		parts = append(parts, text)
	}
	parts = append(parts, m.Errors...)
	if len(parts) == 0 {
		return "turn failed"
	}
	return strings.Join(parts, ": ")
}
