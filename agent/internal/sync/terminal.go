package sync

import (
	"encoding/base64"
	"encoding/json"
	"log"
)

// handleTerminal dispatches one terminal.* message of the core to the hooks. It runs on the read loop, so the
// input that follows an open finds its terminal; the hooks must not block for long. Terminal content is never
// logged.
func (c *Client) handleTerminal(cs *connState, typ string, raw json.RawMessage) {
	h := c.hooks.Terminal
	switch typ {
	case TypeTerminalOpen:
		var m TerminalOpen
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		reason := TerminalReasonDisabled
		if h.Enabled && h.Open != nil {
			reason = ""
			if err := h.Open(m); err != nil {
				reason = err.Error()
			}
		}
		if reason == "" {
			return // the hook announced the terminal with terminal.opened
		}
		log.Printf("[agent] terminal %s for %s rejected: %s", shortID(m.TerminalID), shortID(m.SessionID), reason)
		_ = c.writeMsg(cs, TerminalRejected{V: ProtocolVersion, Type: TypeTerminalRejected, TerminalID: m.TerminalID, Reason: reason})
	case TypeTerminalInput:
		var m TerminalInput
		if json.Unmarshal(raw, &m) != nil || !h.Enabled || h.Input == nil {
			return
		}
		data, err := base64.StdEncoding.DecodeString(m.Data)
		if err != nil {
			return
		}
		h.Input(m.TerminalID, data)
	case TypeTerminalResize:
		var m TerminalResize
		if json.Unmarshal(raw, &m) == nil && h.Enabled && h.Resize != nil {
			h.Resize(m.TerminalID, m.Cols, m.Rows)
		}
	case TypeTerminalAck:
		var m TerminalAck
		if json.Unmarshal(raw, &m) == nil && h.Enabled && h.Ack != nil {
			h.Ack(m.TerminalID, m.Bytes)
		}
	case TypeTerminalClose:
		var m TerminalClose
		if json.Unmarshal(raw, &m) == nil && h.Enabled && h.Close != nil {
			h.Close(m.TerminalID, m.Reason)
		}
	}
}

// SendTerminalOpened, SendTerminalOutput and SendTerminalClosed write the agent-to-core terminal messages;
// they return ErrNotConnected when the core is away.
func (c *Client) SendTerminalOpened(terminalID string) error {
	return c.Send(TerminalOpened{V: ProtocolVersion, Type: TypeTerminalOpened, TerminalID: terminalID})
}

func (c *Client) SendTerminalOutput(terminalID string, seq int64, data []byte) error {
	return c.Send(TerminalOutput{
		V: ProtocolVersion, Type: TypeTerminalOutput, TerminalID: terminalID,
		Seq: seq, Data: base64.StdEncoding.EncodeToString(data),
	})
}

func (c *Client) SendTerminalClosed(terminalID, reason string, exitCode *int) error {
	return c.Send(TerminalClosed{V: ProtocolVersion, Type: TypeTerminalClosed, TerminalID: terminalID, Reason: reason, ExitCode: exitCode})
}
