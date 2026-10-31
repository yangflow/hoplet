package proto

// PermissionMode is how much a session may do without asking, mapped by
// each harness adapter onto that harness's native permission settings. It is
// independent of plan mode (Session.Plan), which stops all changes until the
// user approves a plan.
type PermissionMode string

// Permission modes, from strictest to most permissive.
const (
	ModeConfirm    PermissionMode = "confirm"     // ask before file changes and commands
	ModeAutoEdit   PermissionMode = "auto_edit"   // edit inside the workdir freely; ask before commands
	ModeAutoReview PermissionMode = "auto_review" // the harness judges each action's risk and asks only for risky ones
	ModeFullAuto   PermissionMode = "full_auto"   // never ask; the harness's own forced checks still surface
)

// permissionOrder lists the modes from strictest to most permissive.
var permissionOrder = []PermissionMode{ModeConfirm, ModeAutoEdit, ModeAutoReview, ModeFullAuto}

// Valid reports whether m is a known mode.
func (m PermissionMode) Valid() bool {
	for _, k := range permissionOrder {
		if m == k {
			return true
		}
	}
	return false
}

// Effort is how hard a harness thinks, on one scale for all harnesses;
// each adapter maps it onto its harness's own levels. Empty means the
// harness's default.
type Effort string

// Effort levels, from least to most.
const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// Valid reports whether e is a known level.
func (e Effort) Valid() bool {
	switch e {
	case EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax:
		return true
	}
	return false
}

// Capabilities says which optional operations a harness supports; clients
// hide controls for the rest. Fork and Rewind have no Run methods yet; they
// arrive with M3.
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
	// Modes are the permission modes the harness has natively; others fall
	// back to the nearest stricter one (see Fallback).
	Modes []PermissionMode `json:"modes,omitempty"`
	// Efforts are the effort levels the harness accepts; none means effort
	// cannot be set.
	Efforts []Effort `json:"efforts,omitempty"`
}

// Fallback returns the mode a harness with these capabilities runs for m:
// m itself when the harness has it, else the nearest stricter mode it has,
// else ModeConfirm.
func (c Capabilities) Fallback(m PermissionMode) PermissionMode {
	at := -1
	for i, k := range permissionOrder {
		if k == m {
			at = i
		}
	}
	for i := at; i >= 0; i-- {
		for _, have := range c.Modes {
			if have == permissionOrder[i] {
				return have
			}
		}
	}
	return ModeConfirm
}

// HasEffort reports whether the harness accepts effort level e.
func (c Capabilities) HasEffort(e Effort) bool {
	for _, have := range c.Efforts {
		if have == e {
			return true
		}
	}
	return false
}
