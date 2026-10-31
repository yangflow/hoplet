package proto

// PermissionMode is a session's permission level, mapped by each harness
// adapter onto that harness's native permission settings. A harness lacking
// a level uses the nearest stricter one.
type PermissionMode string

// Permission modes, from strictest to most permissive.
const (
	ModeReadOnly PermissionMode = "read_only" // read only; no file changes
	ModeConfirm  PermissionMode = "confirm"   // ask before file changes and commands
	ModeAutoEdit PermissionMode = "auto_edit" // edit inside the workdir freely; ask before commands
	ModeFullAuto PermissionMode = "full_auto" // never ask; the harness's own forced checks still surface
)

// Capabilities says which optional operations a harness supports; clients
// hide controls for the rest. Fork, Rewind and PlanMode have no Run methods
// yet; they arrive with M3.
type Capabilities struct {
	Steer         bool `json:"steer,omitempty"`
	Resume        bool `json:"resume,omitempty"`
	SetMode       bool `json:"set_mode,omitempty"`
	SetModel      bool `json:"set_model,omitempty"`
	Fork          bool `json:"fork,omitempty"`
	Rewind        bool `json:"rewind,omitempty"`
	PlanMode      bool `json:"plan_mode,omitempty"`
	PartialStream bool `json:"partial_stream,omitempty"`
	MCPInject     bool `json:"mcp_inject,omitempty"`
}
