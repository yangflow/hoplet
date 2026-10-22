package sync

import (
	"encoding/json"
	"log"
)

// handleHookEvent hands one hook.event to the runner, which queues it and returns at once (ADR 0031).
func (c *Client) handleHookEvent(raw []byte) {
	var msg HookEvent
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("[agent] hook.event unreadable: %v", err)
		return
	}
	if c.hooks.HookEvent == nil {
		return
	}
	c.hooks.HookEvent(msg.HookID, msg.Event)
}

// SendHookResult reports the outcome of one hook.event on the bulk lane. It returns ErrNotConnected when the
// link is down; the core settles such a delivery when the link drops.
func (c *Client) SendHookResult(r HookResult) error {
	r.V, r.Type = ProtocolVersion, TypeHookResult
	return c.Send(r)
}
