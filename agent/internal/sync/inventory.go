package sync

// SendDeviceInventory reports what the machine offers on the control lane; it returns ErrNotConnected when the
// core is away, and the report is dropped, never queued.
func (c *Client) SendDeviceInventory(r DeviceInventory) error {
	r.V, r.Type = ProtocolVersion, TypeDeviceInventory
	if r.Toolchains == nil {
		r.Toolchains = []InventoryToolchain{}
	}
	if r.Gpus == nil {
		r.Gpus = []InventoryGpu{}
	}
	if r.Repositories == nil {
		r.Repositories = []InventoryRepository{}
	}
	return c.Send(r)
}
