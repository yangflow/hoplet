package provider

// Found is a session on this machine. It is a lookup result, not a sync Source.
type Found struct {
	Cwd        string
	NativePath string // the vendor's session file; empty when the vendor has none
}

// Finder looks up one session id without a full Scan. Slice order is locate priority.
type Finder interface {
	Find(sessionID string) (Found, bool)
}
