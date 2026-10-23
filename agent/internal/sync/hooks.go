package sync

import (
	"context"
	"encoding/json"

	"github.com/yangflow/hoplet/agent/internal/rules"
)

// RuntimeHooks handle the remote control messages of the core (docs/spec.md#remote-control). The vendor
// process, not Hoplet, writes the session file.
type RuntimeHooks struct {
	// Prompt accepts a prompt and the uploaded images it names or refuses it; the error text is the reason
	// sent as runtime.rejected.
	Prompt func(sessionID, text string, attachments []string) error
	// Steer writes a message into the running turn or refuses it; the error text is the reason sent as
	// runtime.rejected.
	Steer func(sessionID, text string, attachments []string) error
	// Answer answers a permission request: message explains a refusal, mode is the mode an approved plan
	// switches to.
	Answer    func(sessionID, requestID string, allow bool, message, mode string) error
	Interrupt func(sessionID string) error
	// Config sets one setting of a session (ADR 0018); value is a string or a bool. The agent reports the
	// session's settings in its next runtime.state.
	Config func(sessionID, configID string, value any) error
	Stop   func(sessionID string)
	// Start creates a session as RuntimeStart asks and returns its ID once the session file exists and is
	// imported, or the reason it could not, and the worktree it made when req asked for one. The error text is
	// sent as runtime.start.rejected.
	Start func(ctx context.Context, req RuntimeStart) (RuntimeStartResult, error)
	// Started is called after the answer to a successful Start was written; the agent reports the state of
	// the new session then, which the core accepts only once it has the answer.
	Started func(sessionID string)
	// Connected is called in its own goroutine after every connection to the core; the agent reports the
	// state of the processes it still runs through Client.Send.
	Connected func()
}

// RuntimeStartResult is what a successful Start returns.
type RuntimeStartResult struct {
	SessionID string
	Worktree  *StartedWorktree
}

// TerminalHooks serve the terminal.* messages of the core (ADR 0026). Enabled is the agent's terminal setting:
// it is announced in device.hello, and while it is false terminal.open is answered with reason disabled.
type TerminalHooks struct {
	Enabled bool
	// Open starts a shell or refuses; the error text is the reason sent as terminal.rejected.
	Open   func(open TerminalOpen) error
	Input  func(terminalID string, data []byte)
	Resize func(terminalID string, cols, rows int)
	Ack    func(terminalID string, bytes int64)
	Close  func(terminalID, reason string)
	// Disconnected is called when the connection to the core is gone, before the next attempt; the terminals
	// have no reader any more and close.
	Disconnected func()
}

// PortsHooks serve ports.watch (ADR 0026).
type PortsHooks struct {
	// Watch replaces the set of sessions whose dev server ports the agent scans for; empty stops the scan.
	// It must not block: it runs on the read loop.
	Watch func(sessionIDs []string)
}

// WorkspaceListing is the content of session.workspace.
type WorkspaceListing struct {
	Git       bool
	Commit    string
	Origin    string
	Paths     []string
	Truncated bool
	Changes   []WorkspaceChange
}

// WorkspaceFileContent is the content of session.workspace.file; Data is sent as base64.
type WorkspaceFileContent struct {
	Kind      string
	Size      int64
	Text      string
	Truncated bool
	MediaType string
	Data      []byte
}

// WorkspaceDiffContent is the content of session.workspace.diff.
type WorkspaceDiffContent struct {
	Status    string
	Kind      string
	Diff      string
	Truncated bool
	Next      *int64
	Size      int64
	OldSize   *int64
	NewSize   *int64
}

// WorkspaceFunc lists the working tree of a session on this device.
type WorkspaceFunc func(ctx context.Context, sessionID string) (WorkspaceListing, error)

// WorkspaceFileFunc reads one file of a session's working tree; at is "worktree", "head" or empty.
type WorkspaceFileFunc func(ctx context.Context, sessionID, path, at string) (WorkspaceFileContent, error)

// WorkspaceDiffPage selects the page of a diff: see SessionWorkspaceDiffRead.
type WorkspaceDiffPage struct {
	Base   bool
	Cursor int64
	Limit  int
}

// WorkspaceDiffFunc reads one page of the diff of one changed path of a session's working tree.
type WorkspaceDiffFunc func(ctx context.Context, sessionID, path string, page WorkspaceDiffPage) (WorkspaceDiffContent, error)

// WorkspaceChangesContent is the content of session.workspace.changes.
type WorkspaceChangesContent struct {
	Branch string
	Base   string
	Shared bool
	Total  int
	Files  []WorkspaceChange
}

// WorkspaceChangesFunc lists what a session changed against the commit it started from.
type WorkspaceChangesFunc func(ctx context.Context, sessionID string) (WorkspaceChangesContent, error)

// DirListing is the content of device.dirs.
type DirListing struct {
	Path      string
	Parent    *string
	Root      string
	Entries   []DirEntry
	Truncated bool
}

// Hooks are the device-side handlers for control messages from core.
type Hooks struct {
	// Connected is called in its own goroutine after every connection to the core; the agent reports its
	// inventory then.
	Connected func()
	// Providers lists the agent's provider configurations as they are now, with the capabilities of their
	// runtime drivers; device.hello announces them on each connection.
	Providers func() []ProviderInfo
	// ProvidersUpdate applies a device.providers.update and returns the list afterwards; an error becomes
	// device.providers.rejected with its text as the reason.
	ProvidersUpdate func(DeviceProvidersUpdate) ([]ProviderInfo, error)
	// Rules applies a device.rules, given as the JSON of its fields without v and type, and returns the revision
	// it applied; an error means the previous set stays in force and becomes device.rules.applied with the error.
	Rules func(set []byte) (int, error)
	// SessionConfiguration is the ID of the provider configuration the session belongs to, sent with its
	// appends; empty for the vendor's own.
	SessionConfiguration func(sessionID string) string

	Runtime   RuntimeHooks
	Workspace WorkspaceFunc
	File      WorkspaceFileFunc
	Diff      WorkspaceDiffFunc
	Changes   WorkspaceChangesFunc
	Handoff   HandoffHooks
	Terminal  TerminalHooks
	Ports     PortsHooks
	// PreviewHost is announced in device.hello: the host the web UI links dev servers with.
	PreviewHost string
	// PermissionDeny is announced in device.hello: the operator's deny rules, as rules.Announced makes them.
	PermissionDeny []rules.Rule
	// ScriptHooks lists the script hooks of the operator's config.json with their counters as they are now;
	// device.hello announces them on each connection (ADR 0031).
	ScriptHooks func() []HookAnnounce
	// SessionMCP is announced in device.hello: the owner enabled session_mcp (ADR 0034).
	SessionMCP bool
	// McpToken takes one runtime.mcp.token, the core's answer to a request sent with Client.Send. It must not
	// block: it runs on the read loop.
	McpToken func(RuntimeMcpToken)
	// HookEvent takes one hook.event. It must not block: it runs on the read loop.
	HookEvent func(hookID string, event json.RawMessage)

	// ProjectsList / ProjectsAdd / ProjectsRemove match hoplet-agent projects list|add|remove.
	ProjectsList   func(ctx context.Context) (candidates []string, projects []DeviceProject, err error)
	ProjectsAdd    func(ctx context.Context, path string) (candidates []string, projects []DeviceProject, err error)
	ProjectsRemove func(ctx context.Context, path string) (candidates []string, projects []DeviceProject, err error)

	// DirsList lists the directories of path under the browse root; an error becomes device.dirs.rejected
	// with its text as the reason.
	DirsList func(ctx context.Context, path string) (DirListing, error)

	// AttachmentPut takes one chunk of an image for a coming prompt and reports whether the image is complete
	// and stored; an error becomes attachment.rejected with its text.
	AttachmentPut func(put AttachmentPut) (done bool, err error)

	// CloneFind names the clone a handoff of a repository with this origin would use, "" when there is none.
	CloneFind func(ctx context.Context, origin string) (string, error)

	// CloneCheck answers whether origin can be cloned into cloneInto ("": a suggested destination).
	CloneCheck func(ctx context.Context, origin, cloneInto string) CloneChecked
	// CloneStart clones origin into cloneInto for a handoff and returns the path of the clone, or an error
	// that is a *CloneError. It ends early when ctx is cancelled, as a CloneError with reason "cancelled".
	CloneStart func(ctx context.Context, handoffID, origin, cloneInto string, progress func(received int64, total *int64)) (string, error)

	// SessionsImport / SessionsRemove match hoplet-agent sessions import|remove.
	SessionsImport func(ctx context.Context, path string, sessionIDs []string) (candidates []string, projects []DeviceProject, err error)
	SessionsRemove func(ctx context.Context, path string, sessionIDs []string) (candidates []string, projects []DeviceProject, err error)

	// SessionInspect finds what deleting the session would remove on this device (ADR 0023); an error becomes
	// device.session.rejected with its text as the reason.
	SessionInspect func(ctx context.Context, sessionID string) (SessionInspection, error)
	// SessionDelete runs the steps of a deletion in order and reports each; the client stops syncing the
	// session itself before it calls this. worktreeDirty says the user ticked a worktree that had uncommitted
	// changes; without it the worktree step fails with reason dirty when the worktree is dirty.
	SessionDelete func(ctx context.Context, sessionID string, vendorFile, worktree, worktreeDirty bool) []DeletionStep
	// SessionForget removes the session from every project's imports.
	SessionForget func(ctx context.Context, sessionID string) error
}

// SessionInspection is the content of device.session.inspected.
type SessionInspection struct {
	VendorFile *DeletionFile
	Worktree   *DeletionWorktree
}

// CloneChecked is the answer of Hooks.CloneCheck.
type CloneChecked struct {
	Reason    string
	Detail    string
	CloneInto string
}

// CloneError is the failure of Hooks.CloneStart.
type CloneError struct {
	Reason string
	Detail string
}

func (e *CloneError) Error() string { return e.Reason }
