package sync

import (
	"encoding/json"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// maxPortsWatch is the most sessions one ports.watch names (ADR 0026).
const maxPortsWatch = 64

// handlePortsWatch passes the set of sessions to scan for dev server ports to the hook. It runs on the read
// loop and the hook only stores the set.
func (c *Client) handlePortsWatch(raw json.RawMessage) {
	var m PortsWatch
	if json.Unmarshal(raw, &m) != nil || c.hooks.Ports.Watch == nil {
		return
	}
	ids := make([]string, 0, len(m.SessionIDs))
	for _, id := range m.SessionIDs {
		if len(ids) == maxPortsWatch {
			break
		}
		if provider.ValidSessionID(id) {
			ids = append(ids, id)
		}
	}
	c.hooks.Ports.Watch(ids)
}

// SendSessionPorts reports the dev server ports of one session on the control lane; it returns ErrNotConnected
// when the core is away.
func (c *Client) SendSessionPorts(sessionID string, ports []SessionPort) error {
	if ports == nil {
		ports = []SessionPort{}
	}
	return c.Send(SessionPorts{V: ProtocolVersion, Type: TypeSessionPorts, SessionID: sessionID, Ports: ports})
}
