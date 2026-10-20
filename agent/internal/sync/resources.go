package sync

// SendDeviceResources reports the machine's load on the control lane; it returns ErrNotConnected when the core
// is away, and the report is dropped, never queued.
func (c *Client) SendDeviceResources(r DeviceResources) error {
	r.V, r.Type = ProtocolVersion, TypeDeviceResources
	if r.Sessions == nil {
		r.Sessions = []SessionResources{}
	}
	return c.Send(r)
}
